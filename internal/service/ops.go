package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
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

// ReadResult is the read_document result. Revision is set instead of
// RevisionID when an old revision was read through the export path.
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
	var threads []CommentThread
	if req.IncludeComments && format != FormatRaw {
		if threads, err = s.comments(ctx, f); err != nil {
			return nil, err
		}
		req.Options.Marks = commentMarks(threads)
	}
	var r render.Result
	switch format {
	case FormatMarkdown:
		r = render.Markdown(rs.Segment, rs.From, rs.To, req.Options)
	case FormatText:
		r = render.Plain(rs.Segment, rs.From, rs.To, req.Options)
	case FormatRaw:
		r, err = rawJSON(f.Wire, rs, req.Options.MaxChars)
		if err != nil {
			return nil, err
		}
	}
	if req.IncludeComments && format != FormatRaw {
		r.Text += commentFooter(rs, r, threads)
	}
	return &ReadResult{
		Text: r.Text, RevisionID: f.Doc.RevisionID,
		TabNumber: rs.Tab.Number, TabID: rs.Tab.ID, TabTitle: rs.Tab.Title,
		Segment: rs.Segment.Label(), Scope: rs.Description,
		Blocks: r.Blocks, Chars: r.Chars, Truncated: r.Truncated, ContinueFrom: r.ContinueFrom,
	}, nil
}

// commentMarks turns located, live threads into renderer marks.
func commentMarks(threads []CommentThread) []render.Mark {
	var marks []render.Mark
	for _, t := range threads {
		if t.Handle == "" || t.Deleted {
			continue
		}
		marks = append(marks, render.Mark{TabID: t.Tab, SegmentID: t.Segment, Start: t.Start, End: t.End, ID: t.ID})
	}
	return marks
}

// commentFooter lists the threads marked in the rendered range and
// counts the ones outside it.
func commentFooter(rs Resolved, r render.Result, threads []CommentThread) string {
	if len(threads) == 0 {
		return "\n\n<!-- no comments -->"
	}
	var start, end int64
	if r.To > rs.From && r.To <= len(rs.Segment.Blocks) {
		start, end = rs.Segment.Blocks[rs.From].Start, rs.Segment.Blocks[r.To-1].End
	}
	var sb strings.Builder
	other := 0
	for _, t := range threads {
		if t.Deleted {
			continue
		}
		inRange := t.Handle != "" && t.Tab == rs.Tab.ID && t.Segment == rs.Segment.ID && t.End > start && t.Start < end
		if !inRange {
			other++
			continue
		}
		fmt.Fprintf(&sb, "\n- c:%s [%s]", t.ID, t.Handle)
		if t.Author != "" {
			sb.WriteString(" by " + t.Author)
		}
		if t.Resolved {
			sb.WriteString(" [resolved]")
		}
		sb.WriteString(": " + oneLine(t.Content))
		if n := len(t.Replies); n > 0 {
			label := "replies"
			if n == 1 {
				label = "reply"
			}
			fmt.Fprintf(&sb, " (%d %s)", n, label)
		}
	}
	out := "\n\ncomments:" + sb.String()
	if sb.Len() == 0 {
		out = "\n\ncomments: none in this range"
	}
	if other > 0 {
		out += fmt.Sprintf("\n(%d more elsewhere or unlocated; use list_comments)", other)
	}
	return out
}

// rawJSON returns the API's structural elements for the range, one JSON
// array, cut at element boundaries when over budget.
func rawJSON(w *gdocs.Document, rs Resolved, maxChars int) (render.Result, error) {
	elems := wireElements(w, rs.Tab, rs.Segment)
	if elems == nil {
		return render.Result{}, Errorf("unexpected", "raw content not found for this segment")
	}
	var res render.Result
	var sb strings.Builder
	sb.WriteString("[")
	for i := rs.From; i < rs.To && i < len(elems); i++ {
		data, err := json.Marshal(elems[i])
		if err != nil {
			return render.Result{}, Errorf("unexpected", "encode element: %v", err)
		}
		if res.Blocks > 0 && maxChars > 0 && sb.Len()+len(data)+2 > maxChars {
			res.Truncated = true
			if i < len(rs.Segment.Blocks) {
				res.ContinueFrom = rs.Segment.Blocks[i].Handle
			}
			break
		}
		if res.Blocks > 0 {
			sb.WriteString(",\n")
		}
		sb.Write(data)
		res.Blocks++
		res.To = i + 1
	}
	sb.WriteString("]")
	res.Text = sb.String()
	res.Chars = len(res.Text)
	return res, nil
}

func wireElements(w *gdocs.Document, tab *doc.Tab, seg *doc.Segment) []*gdocs.StructuralElement {
	var dt *gdocs.DocumentTab
	if len(w.Tabs) == 0 {
		dt = &gdocs.DocumentTab{Body: w.Body, Headers: w.Headers, Footers: w.Footers, Footnotes: w.Footnotes}
	} else {
		gdocs.WalkTabs(w.Tabs, func(t *gdocs.Tab) bool {
			if t.TabProperties != nil && t.TabProperties.TabID == tab.ID {
				dt = t.DocumentTab
				return false
			}
			return true
		})
	}
	if dt == nil {
		return nil
	}
	switch seg.Kind {
	case doc.SegmentBody:
		if dt.Body != nil {
			return dt.Body.Content
		}
	case doc.SegmentHeader:
		if h, ok := dt.Headers[seg.ID]; ok {
			return h.Content
		}
	case doc.SegmentFooter:
		if f, ok := dt.Footers[seg.ID]; ok {
			return f.Content
		}
	case doc.SegmentFootnote:
		if fn, ok := dt.Footnotes[seg.ID]; ok {
			return fn.Content
		}
	}
	return nil
}
