package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

// TabInfo summarises one tab.
type TabInfo struct {
	Number    int    `json:"number"`
	ID        string `json:"id,omitempty"`
	Title     string `json:"title"`
	Nesting   int    `json:"nesting,omitempty"`
	Headings  int    `json:"headings"`
	Blocks    int    `json:"blocks"`
	Headers   int    `json:"headers,omitempty"`
	Footers   int    `json:"footers,omitempty"`
	Footnotes int    `json:"footnotes,omitempty"`
}

// Capabilities tell the model what this server instance can do.
type Capabilities struct {
	Preview          bool     `json:"preview"`
	WriteModes       []string `json:"write_modes"`
	DefaultWriteMode string   `json:"default_write_mode"`
	ReadOnly         bool     `json:"read_only"`
}

// Info is the get_document result.
type Info struct {
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	URL            string       `json:"url"`
	RevisionID     string       `json:"revision_id"`
	Tabs           []TabInfo    `json:"tabs"`
	Owner          string       `json:"owner,omitempty"`
	Created        string       `json:"created,omitempty"`
	LastModified   string       `json:"last_modified,omitempty"`
	LastModifiedBy string       `json:"last_modified_by,omitempty"`
	CanEdit        *bool        `json:"can_edit,omitempty"`
	CanComment     *bool        `json:"can_comment,omitempty"`
	Stats          doc.Stats    `json:"stats"`
	Capabilities   Capabilities `json:"capabilities"`
	Warnings       []string     `json:"warnings,omitempty"`
}

func (s *Service) capabilities() Capabilities {
	c := Capabilities{Preview: s.opts.Preview, DefaultWriteMode: string(s.opts.DefaultWriteMode), ReadOnly: s.opts.ReadOnly, WriteModes: []string{}}
	for _, m := range (config.Config{Preview: s.opts.Preview}).AvailableWriteModes() {
		c.WriteModes = append(c.WriteModes, string(m))
	}
	return c
}

// Info describes a document: structure counts plus Drive metadata. The
// two Google calls need only the id, so they run concurrently.
func (s *Service) Info(ctx context.Context, ref string) (*Info, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	type fileResult struct {
		file *gapi.File
		err  error
	}
	fileCh := make(chan fileResult, 1)
	go func() {
		file, err := s.api.GetFile(ctx, id)
		fileCh <- fileResult{file, err}
	}()
	f, err := s.Fetch(ctx, id)
	if err != nil {
		<-fileCh
		return nil, err
	}
	d := f.Doc
	info := &Info{ID: d.ID, Title: d.Title, URL: doc.DocumentURL(d.ID), RevisionID: d.RevisionID, Stats: d.Stats(), Capabilities: s.capabilities()}
	for _, t := range d.Tabs {
		ti := TabInfo{Number: t.Number, ID: t.ID, Title: t.Title, Nesting: t.Nesting, Blocks: len(t.Body.Blocks),
			Headers: len(t.Headers), Footers: len(t.Footers), Footnotes: len(t.Footnotes)}
		for _, b := range t.Body.Blocks {
			if b.IsHeading() {
				ti.Headings++
			}
		}
		info.Tabs = append(info.Tabs, ti)
	}
	fr := <-fileCh
	if fr.err != nil {
		info.Warnings = append(info.Warnings, "Drive metadata unavailable: "+wrapAPI(fr.err, "file").Error())
		return info, nil //nolint:nilerr // the document itself was read; Drive metadata is optional
	}
	file := fr.file
	if len(file.Owners) > 0 {
		info.Owner = userLabel(file.Owners[0])
	}
	info.Created = file.CreatedTime
	info.LastModified = file.ModifiedTime
	if file.LastModifyingUser != nil {
		info.LastModifiedBy = userLabel(file.LastModifyingUser)
	}
	if file.WebViewLink != "" {
		info.URL = file.WebViewLink
	}
	if c := file.Capabilities; c != nil {
		info.CanEdit, info.CanComment = &c.CanEdit, &c.CanComment
	}
	if file.Trashed {
		info.Warnings = append(info.Warnings, "this document is in the trash")
	}
	return info, nil
}

func userLabel(u *gapi.User) string {
	switch {
	case u == nil:
		return ""
	case u.DisplayName != "" && u.EmailAddress != "":
		return u.DisplayName + " <" + u.EmailAddress + ">"
	case u.EmailAddress != "":
		return u.EmailAddress
	}
	return u.DisplayName
}

// OutlineResult is the get_outline result.
type OutlineResult struct {
	Text       string
	RevisionID string
	Tabs       []render.OutlineTab
}

// Outline renders the heading tree of one tab or all tabs.
func (s *Service) Outline(ctx context.Context, ref, tab string) (*OutlineResult, error) {
	f, err := s.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	var only *doc.Tab
	if tab != "" {
		if only, err = tabOf(f.Doc, tab); err != nil {
			return nil, err
		}
	}
	tabs := render.OutlineData(f.Doc, only)
	return &OutlineResult{Text: render.Outline(f.Doc, tabs), RevisionID: f.Doc.RevisionID, Tabs: tabs}, nil
}

// Read formats.
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
	FormatRaw      = "raw"
)

// ReadRequest is the read_document input after validation.
type ReadRequest struct {
	Document        string
	Scope           ReadScope
	Format          string
	Options         render.Options
	IncludeComments bool
}

// ReadResult is the read_document result. Text carries the header
// comment naming the scope, the revision and the continuation, because
// a client may show the model only a result's text. Revision is set
// instead of RevisionID when an old revision was read through the
// export path.
type ReadResult struct {
	Text         string
	RevisionID   string
	Revision     string
	TabNumber    int
	TabID        string
	TabTitle     string
	Segment      string
	Scope        string
	Blocks       int
	Chars        int
	Truncated    bool
	ContinueFrom string
}

// Read renders a scoped view of the document.
func (s *Service) Read(ctx context.Context, req ReadRequest) (*ReadResult, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = FormatMarkdown
	}
	if format != FormatMarkdown && format != FormatText && format != FormatRaw {
		return nil, Errorf("invalid", "format %q; use markdown, text or raw", req.Format)
	}
	f, err := s.Fetch(ctx, req.Document)
	if err != nil {
		return nil, err
	}
	rs, err := s.ResolveScope(f, req.Scope)
	if err != nil {
		return nil, err
	}
	if req.IncludeComments && format != FormatRaw {
		threads, err := s.comments(ctx, f)
		if err != nil {
			return nil, err
		}
		req.Options.Marks, req.Options.CommentFooter = commentMarks(threads), true
	}
	var r render.Result
	switch format {
	case FormatMarkdown:
		r = render.Markdown(rs.Segment, rs.From, rs.To, req.Options)
	case FormatText:
		r = render.Plain(rs.Segment, rs.From, rs.To, req.Options)
	case FormatRaw:
		if r, err = render.Raw(rs.Segment, rs.From, rs.To, req.Options.MaxChars); err != nil {
			return nil, &Error{Class: "unexpected", Message: err.Error(), Err: err}
		}
	}
	res := &ReadResult{
		Text: r.Text, RevisionID: f.Doc.RevisionID,
		TabNumber: rs.Tab.Number, TabID: rs.Tab.ID, TabTitle: rs.Tab.Title,
		Segment: rs.Segment.Label(), Scope: rs.Description,
		Blocks: r.Blocks, Chars: r.Chars, Truncated: r.Truncated, ContinueFrom: r.ContinueFrom,
	}
	if format != FormatRaw {
		res.Text = res.header() + res.Text
	}
	return res, nil
}

// header is the comment above a read: what was read, at which revision,
// and where to continue.
func (r *ReadResult) header() string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s", r.Scope)
	if r.RevisionID != "" {
		fmt.Fprintf(&b, " · revision %s · %d block(s)", r.RevisionID, r.Blocks)
	}
	if r.Truncated {
		b.WriteString(" · truncated")
		if r.ContinueFrom != "" {
			fmt.Fprintf(&b, ", continue_from %s", r.ContinueFrom)
		}
	}
	b.WriteString(" -->\n")
	return b.String()
}

// commentMarks turns the live threads into renderer marks: located
// ones get markers, every one is counted in the footer.
func commentMarks(threads []CommentThread) []render.Mark {
	var marks []render.Mark
	for _, t := range threads {
		if t.Deleted {
			continue
		}
		marks = append(marks, render.Mark{TabID: t.Tab, SegmentID: t.Segment, Start: t.Start, End: t.End, ID: t.ID,
			Handle: t.Handle, Author: t.Author, Content: t.Content, Resolved: t.Resolved, Replies: len(t.Replies)})
	}
	return marks
}
