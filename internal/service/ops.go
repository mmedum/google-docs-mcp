package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	c := Capabilities{Preview: s.opts.Preview, DefaultWriteMode: string(s.opts.DefaultWriteMode), ReadOnly: s.opts.ReadOnly}
	for _, m := range s.opts.WriteModes {
		c.WriteModes = append(c.WriteModes, string(m))
	}
	if c.WriteModes == nil {
		c.WriteModes = []string{}
	}
	return c
}

// Info describes a document: structure counts plus Drive metadata.
func (s *Service) Info(ctx context.Context, ref string) (*Info, error) {
	f, err := s.Fetch(ctx, ref)
	if err != nil {
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
	file, err := s.api.GetFile(ctx, d.ID)
	if err != nil {
		info.Warnings = append(info.Warnings, "Drive metadata unavailable: "+wrapAPI(err, "file").Error())
		return info, nil
	}
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
		t, ok := f.Doc.Tab(tab)
		if !ok {
			return nil, Errorf("not_found", "no tab %q; tabs: %s", tab, tabList(f.Doc))
		}
		only = t
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
	Document string
	Scope    ReadScope
	Format   string
	Options  render.Options
}

// ReadResult is the read_document result.
type ReadResult struct {
	Text         string
	RevisionID   string
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
	rs, err := ResolveScope(f.Doc, req.Scope)
	if err != nil {
		return nil, err
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
	return &ReadResult{
		Text: r.Text, RevisionID: f.Doc.RevisionID,
		TabNumber: rs.Tab.Number, TabID: rs.Tab.ID, TabTitle: rs.Tab.Title,
		Segment: rs.Segment.Label(), Scope: rs.Description,
		Blocks: r.Blocks, Chars: r.Chars, Truncated: r.Truncated, ContinueFrom: r.ContinueFrom,
	}, nil
}

// rawJSON returns the API's structural elements for the range, one JSON
// array, cut at element boundaries when over budget.
func rawJSON(w *gdocs.Document, rs Resolved, maxChars int) (render.Result, error) {
	elems := wireElements(w, rs.Tab, rs.Segment)
	if elems == nil {
		return render.Result{}, errors.New("[unexpected] raw content not found for this segment")
	}
	var res render.Result
	var sb strings.Builder
	sb.WriteString("[")
	for i := rs.From; i < rs.To && i < len(elems); i++ {
		data, err := json.Marshal(elems[i])
		if err != nil {
			return render.Result{}, fmt.Errorf("[unexpected] encode element: %w", err)
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
		var walk func([]*gdocs.Tab)
		walk = func(tabs []*gdocs.Tab) {
			for _, t := range tabs {
				if t.TabProperties != nil && t.TabProperties.TabID == tab.ID {
					dt = t.DocumentTab
					return
				}
				walk(t.ChildTabs)
			}
		}
		walk(w.Tabs)
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
