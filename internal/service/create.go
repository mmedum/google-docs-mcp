package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/mdfile"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
)

// CreateRequest makes a new document.
type CreateRequest struct {
	Title         string
	Content       string
	ContentFormat string
	// Kind is document (the default) or markdown_file.
	Kind string
	// FromFile makes a working copy of a Markdown file: a Google Doc
	// holding its text, which WriteBack writes back into it.
	FromFile string
}

// CreateResult describes the new document.
type CreateResult struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	URL        string   `json:"url"`
	RevisionID string   `json:"revision_id"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Create makes a document and, when content is given, writes it directly
// (a new document has nothing to suggest against).
func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if err := s.requireWritable(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.FromFile) != "" {
		if k := strings.ToLower(strings.TrimSpace(req.Kind)); k != "" && k != KindDocument {
			return nil, Errorf("invalid", "a working copy is a Google Doc; leave kind out")
		}
		return s.createWorkingCopy(ctx, req)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, Errorf("invalid", "title is empty")
	}
	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case "", KindDocument:
	case KindMarkdownFile:
		return s.createMarkdown(ctx, title, req.Content)
	default:
		return nil, Errorf("invalid", "kind %q; use document or markdown_file", req.Kind)
	}
	d, err := s.api.CreateDocument(ctx, title)
	if err != nil {
		return nil, wrapWriteError(err)
	}
	res := &CreateResult{ID: d.DocumentID, Title: d.Title, Kind: KindDocument, URL: doc.DocumentURL(d.DocumentID), RevisionID: d.RevisionID}
	if strings.TrimSpace(req.Content) == "" {
		return res, nil
	}
	// The create reply is the whole (empty) document, so the edit plans
	// against it without another read. It says nothing about the comments
	// view, so it is not cached for other calls to read.
	f, err := s.parse(d, false)
	if err != nil {
		return nil, err
	}
	edit, _, err := s.editFetched(ctx, f, EditRequest{Document: d.DocumentID, Mode: string(plan.ModeDirect), Ops: []EditOp{{Kind: plan.OpAppend, Content: req.Content, ContentFormat: req.ContentFormat}}})
	if err != nil {
		res.Warnings = append(res.Warnings, "the document was created empty; writing the content failed: "+err.Error())
		return res, nil //nolint:nilerr // the document exists; the caller gets it with a warning
	}
	res.RevisionID = edit.RevisionID
	res.Warnings = append(res.Warnings, edit.Warnings...)
	return res, nil
}

// createMarkdown makes a Markdown file in My Drive holding content as
// given, which Docs opens in place. The name gets .md unless it ends in
// .md or .markdown, which is what Docs and Drive recognize the file by.
func (s *Service) createMarkdown(ctx context.Context, title, content string) (*CreateResult, error) {
	if mdfile.ContainsMarkup(content) {
		return nil, Errorf("invalid", "%s", markupRefused)
	}
	if len(content) > maxUpload {
		return nil, tooLarge(len(content))
	}
	name := title
	if lower := strings.ToLower(name); !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
		name += ".md"
	}
	f, err := s.api.CreateFile(ctx, gapi.NewFile{Name: name, MimeType: gapi.MarkdownMimeType}, gapi.MarkdownMimeType, []byte(content))
	if err != nil {
		return nil, wrapAPI(err, "file")
	}
	s.rememberKind(f.ID, gapi.MarkdownMimeType)
	res := &CreateResult{ID: f.ID, Title: f.Name, Kind: KindMarkdownFile, URL: f.WebViewLink, RevisionID: f.HeadRevisionID}
	if res.URL == "" {
		res.URL = doc.FileURL(f.ID)
	}
	if f.MimeType != "" && f.MimeType != gapi.MarkdownMimeType {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Drive typed the new file %s, not %s", f.MimeType, gapi.MarkdownMimeType))
	}
	return res, nil
}
