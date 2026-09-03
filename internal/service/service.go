// Package service orchestrates reads: fetch a document, parse it, keep
// the handle memory, resolve a read scope, and render. Tools stay thin
// and every rule that needs testing lives here.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// API is the subset of the Google client the service uses.
type API interface {
	GetDocument(ctx context.Context, id string, o gapi.GetOptions) (*gapi.DocumentResult, error)
	GetFile(ctx context.Context, id string) (*gapi.File, error)
	BatchUpdate(ctx context.Context, id string, req *gapi.BatchUpdateRequest) (*gapi.BatchUpdateResponse, error)
	CreateDocument(ctx context.Context, title string) (*gdocs.Document, error)
	SearchFiles(ctx context.Context, q string, limit int, pageToken string) (*gapi.FileList, error)
	Export(ctx context.Context, id, mimeType string) ([]byte, error)
	CreateComment(ctx context.Context, fileID, content, quote string) (*gapi.DriveComment, error)
	ListComments(ctx context.Context, fileID string, includeDeleted bool) ([]*gapi.DriveComment, error)
}

// Options configure the service.
type Options struct {
	Preview          bool
	ReadOnly         bool
	DefaultWriteMode config.WriteMode
	Logger           *slog.Logger
	// ExportDir is where binary exports may be written; empty disables them.
	ExportDir string
	// CacheTTL coalesces repeated fetches of one document. Default 5s.
	CacheTTL time.Duration
	Now      func() time.Time
}

// Service is the read/write orchestrator for one authenticated user.
type Service struct {
	api  API
	opts Options
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	cache   map[string]*Fetched
	handles map[string]HandleMemory
}

// New builds a service.
func New(api API, o Options) *Service {
	s := &Service{api: api, opts: o, log: o.Logger, now: o.Now, cache: map[string]*Fetched{}, handles: map[string]HandleMemory{}}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.opts.CacheTTL == 0 {
		s.opts.CacheTTL = 5 * time.Second
	}
	return s
}

// Error is an LLM-facing failure with a class the model can branch on.
type Error struct {
	Class   string
	Message string
	Err     error
}

func (e *Error) Error() string { return "[" + e.Class + "] " + e.Message }

// Unwrap exposes the underlying error for errors.Is.
func (e *Error) Unwrap() error { return e.Err }

// Errorf builds an Error.
func Errorf(class, format string, args ...any) *Error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// Fetched is a parsed document plus the wire form it came from.
type Fetched struct {
	Doc       *doc.Document
	Wire      *gdocs.Document
	FetchedAt time.Time
}

// HandleMemory records what each handle pointed at in the last read so a
// later write can check that it still does.
type HandleMemory struct {
	RevisionID string
	Text       map[string]string
	At         time.Time
}

// Fetch loads a document by id or URL, through the coalescing cache.
func (s *Service) Fetch(ctx context.Context, ref string) (*Fetched, error) {
	return s.fetch(ctx, ref, false)
}

// FetchFresh bypasses the cache and leaves the handle memory alone:
// writes plan against a fresh read but check handles against the read
// the caller actually saw.
func (s *Service) FetchFresh(ctx context.Context, ref string) (*Fetched, error) {
	return s.fetch(ctx, ref, true)
}

func (s *Service) fetch(ctx context.Context, ref string, fresh bool) (*Fetched, error) {
	id, err := doc.ParseID(ref)
	if err != nil {
		return nil, Errorf("invalid", "%q is not a Google Docs document id or URL", ref)
	}
	if !fresh {
		s.mu.Lock()
		if f := s.cache[id]; f != nil && s.now().Sub(f.FetchedAt) < s.opts.CacheTTL {
			s.mu.Unlock()
			return f, nil
		}
		s.mu.Unlock()
	}

	opts := gapi.GetOptions{SuggestionsViewMode: gapi.SuggestionsInline}
	if s.opts.Preview {
		opts.CommentsViewMode = gapi.CommentsIncluded
	}
	res, err := s.api.GetDocument(ctx, id, opts)
	if err != nil {
		return nil, wrapAPI(err, "document")
	}
	f, err := s.adopt(res.Document)
	if err != nil {
		return nil, err
	}
	if !fresh {
		s.Remember(f)
	}
	s.log.DebugContext(ctx, "document fetched", "doc", gapi.ShortID(id), "revision", f.Doc.RevisionID, "tabs", len(f.Doc.Tabs))
	return f, nil
}

// adopt parses a wire document and caches it, evicting stale entries so
// a long session does not keep every document it ever touched.
func (s *Service) adopt(w *gdocs.Document) (*Fetched, error) {
	parsed, err := doc.Parse(w)
	if err != nil {
		return nil, &Error{Class: "unexpected", Message: err.Error(), Err: err}
	}
	f := &Fetched{Doc: parsed, Wire: w, FetchedAt: s.now()}
	s.mu.Lock()
	for id, c := range s.cache {
		if s.now().Sub(c.FetchedAt) >= s.opts.CacheTTL {
			delete(s.cache, id)
		}
	}
	s.cache[parsed.ID] = f
	s.mu.Unlock()
	return f, nil
}

// Remember records what every handle points at in a document the caller
// has seen, so later writes can detect handles that went stale.
func (s *Service) Remember(f *Fetched) {
	m := memory(f.Doc, f.FetchedAt)
	s.mu.Lock()
	s.handles[f.Doc.ID] = m
	s.mu.Unlock()
}

// Invalidate drops the cached copy of a document (after a write).
func (s *Service) Invalidate(id string) {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
}

// Handles returns the handle memory for a document, if any.
func (s *Service) Handles(id string) (HandleMemory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.handles[id]
	return m, ok
}

func memory(d *doc.Document, at time.Time) HandleMemory {
	m := HandleMemory{RevisionID: d.RevisionID, Text: map[string]string{}, At: at}
	for _, b := range d.AllBlocks() {
		m.Text[b.Handle] = doc.Normalize(b.Text(doc.ViewInline))
	}
	return m
}

// requireWritable refuses writes on a read-only server. Tools are not
// registered in that mode; this guards direct callers as well.
func (s *Service) requireWritable() error {
	if s.opts.ReadOnly {
		return Errorf("forbidden", "this server runs read-only (GDOCS_READ_ONLY=true)")
	}
	return nil
}

// tabOf finds a tab or explains which exist.
func tabOf(d *doc.Document, ref string) (*doc.Tab, error) {
	tab, ok := d.Tab(ref)
	if !ok {
		return nil, Errorf("not_found", "no tab %q; tabs: %s", ref, tabList(d))
	}
	return tab, nil
}

// tabSegment resolves a tab and one of its segments.
func tabSegment(d *doc.Document, tabRef, segRef string) (*doc.Tab, *doc.Segment, error) {
	tab, err := tabOf(d, tabRef)
	if err != nil {
		return nil, nil, err
	}
	seg, err := selectSegment(tab, segRef)
	if err != nil {
		return nil, nil, err
	}
	return tab, seg, nil
}

// DefaultMaxChars is the output budget when a caller gives none
// (about 5k tokens).
const DefaultMaxChars = 20000

// wrapAPI turns a Google API failure into an actionable Error.
func wrapAPI(err error, what string) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	class := gapi.Class(err)
	msg := ""
	switch {
	case errors.Is(err, gapi.ErrMissingScope):
		msg = "the stored credentials lack a required OAuth scope; re-run `google-docs-mcp login`"
	case errors.Is(err, gapi.ErrUnauthorized):
		msg = "Google rejected the stored credentials; run `google-docs-mcp login` and try again"
	case errors.Is(err, gapi.ErrForbidden):
		msg = "this account cannot access the " + what + " (not shared with it, or the API is disabled in the Cloud project)"
	case errors.Is(err, gapi.ErrNotFound):
		msg = "the " + what + " was not found; check the id or URL and that it is shared with this account"
	case errors.Is(err, gapi.ErrRateLimited):
		msg = "Google's rate limit was hit; wait a moment and retry"
	case errors.Is(err, gapi.ErrServer):
		msg = "Google returned a server error after retries; retry shortly"
	case errors.Is(err, gapi.ErrNetwork):
		msg = "could not reach Google: " + err.Error()
	default:
		msg = err.Error()
	}
	return &Error{Class: class, Message: msg, Err: err}
}
