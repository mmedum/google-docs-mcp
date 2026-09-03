package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
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
	// Page is the tab's page setup, and NamedRanges and FloatingObjects
	// are what it holds that no read of the text shows.
	Page            *doc.PageSetup   `json:"page,omitempty"`
	NamedRanges     []NamedRangeInfo `json:"named_ranges,omitempty"`
	FloatingObjects []ObjectInfo     `json:"floating_objects,omitempty"`
	// NamedStyles are the definitions the tab's paragraphs inherit from,
	// which layout_document's named_style op redefines.
	NamedStyles []NamedStyleInfo `json:"named_styles,omitempty"`
}

// NamedStyleInfo is one named style definition with how many of the
// tab's paragraphs carry it, counted over every segment because
// redefining a style changes the whole tab — a wider count than
// Stats.Paragraphs, which is bodies only. Styles no paragraph carries
// are left out: they have no appearance in this tab to report, and
// get_document is the tool the model calls first.
type NamedStyleInfo struct {
	Style      *doc.NamedStyleDef `json:"style"`
	Paragraphs int                `json:"paragraphs"`
}

// NamedRangeInfo is one named range as get_document reports it.
type NamedRangeInfo struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Segment string `json:"segment,omitempty"`
	Chars   int64  `json:"chars"`
}

// ObjectInfo is one floating object as get_document reports it.
type ObjectInfo struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Title    string  `json:"title,omitempty"`
	WidthPt  float64 `json:"width_pt,omitempty"`
	HeightPt float64 `json:"height_pt,omitempty"`
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
	Text           string       `json:"-"`
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
			Headers: len(t.Headers), Footers: len(t.Footers), Footnotes: len(t.Footnotes), Page: t.Page}
		for _, nr := range t.NamedRanges {
			ti.NamedRanges = append(ti.NamedRanges, NamedRangeInfo{Name: nr.Name, ID: nr.ID, Segment: nr.Segment, Chars: nr.End - nr.Start})
		}
		if len(t.NamedStyles) > 0 {
			use := t.NamedStyleUse()
			ti.NamedStyles = make([]NamedStyleInfo, 0, len(t.NamedStyles))
			for _, ns := range t.NamedStyles {
				if n := use[ns.Type]; n > 0 {
					ti.NamedStyles = append(ti.NamedStyles, NamedStyleInfo{Style: ns, Paragraphs: n})
				}
			}
		}
		for _, id := range slices.Sorted(maps.Keys(t.PositionedObjects)) {
			o := t.PositionedObjects[id]
			ti.FloatingObjects = append(ti.FloatingObjects, ObjectInfo{ID: o.ID, Kind: o.Kind, Title: o.Title, WidthPt: o.WidthPt, HeightPt: o.HeightPt})
		}
		for _, b := range t.Body.Blocks {
			if b.IsHeading() {
				ti.Headings++
			}
		}
		info.Tabs = append(info.Tabs, ti)
	}
	// The document itself was read, so Drive metadata is optional.
	if fr := <-fileCh; fr.err != nil {
		info.Warnings = append(info.Warnings, "Drive metadata unavailable: "+wrapAPI(fr.err, "file").Error())
	} else {
		info.fromFile(fr.file)
	}
	info.Text = info.text()
	return info, nil
}

// fromFile adds what Drive knows about the document.
func (i *Info) fromFile(file *gapi.File) {
	if len(file.Owners) > 0 {
		i.Owner = userLabel(file.Owners[0])
	}
	i.Created = file.CreatedTime
	i.LastModified = file.ModifiedTime
	if file.LastModifyingUser != nil {
		i.LastModifiedBy = userLabel(file.LastModifyingUser)
	}
	if file.WebViewLink != "" {
		i.URL = file.WebViewLink
	}
	if c := file.Capabilities; c != nil {
		i.CanEdit, i.CanComment = &c.CanEdit, &c.CanComment
	}
	if file.Trashed {
		i.Warnings = append(i.Warnings, "this document is in the trash")
	}
}

// text is what get_document shows: the document's identity, what Drive
// knows about it, the structure counts and this server's capabilities.
func (i *Info) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\nrevision %s\n", i.Title, i.URL, i.RevisionID)
	if i.Owner != "" {
		fmt.Fprintf(&b, "owner %s\n", i.Owner)
	}
	if i.LastModified != "" {
		fmt.Fprintf(&b, "last modified %s", i.LastModified)
		if i.LastModifiedBy != "" {
			fmt.Fprintf(&b, " by %s", i.LastModifiedBy)
		}
		b.WriteString("\n")
	}
	st := i.Stats
	fmt.Fprintf(&b, "%d tab(s), %d paragraphs, %d headings, %d tables, %d inline objects, %d footnotes, %d words",
		st.Tabs, st.Paragraphs, st.Headings, st.Tables, st.InlineObjects, st.Footnotes, st.Words)
	if st.FloatingObjects > 0 {
		fmt.Fprintf(&b, ", %d floating object(s)", st.FloatingObjects)
	}
	if st.NamedRanges > 0 {
		fmt.Fprintf(&b, ", %d named range(s)", st.NamedRanges)
	}
	if st.Suggestions > 0 {
		fmt.Fprintf(&b, ", %d pending suggestion(s)", st.Suggestions)
	}
	b.WriteString("\n")
	for _, t := range i.Tabs {
		fmt.Fprintf(&b, "- tab %d %q", t.Number, t.Title)
		if t.ID != "" {
			fmt.Fprintf(&b, " (id %s)", t.ID)
		}
		fmt.Fprintf(&b, ": %d headings, %d blocks\n", t.Headings, t.Blocks)
		t.writeExtras(&b)
	}
	c := i.Capabilities
	fmt.Fprintf(&b, "server: write modes %s (default %s), preview %t, read-only %t\n", strings.Join(c.WriteModes, "/"), c.DefaultWriteMode, c.Preview, c.ReadOnly)
	writeWarnings(&b, i.Warnings)
	return strings.TrimRight(b.String(), "\n")
}

// writeExtras lists what a tab holds that a read of its text does not
// show: its page setup, the objects that float above the text, and the
// ranges the document remembers by name.
func (t TabInfo) writeExtras(b *strings.Builder) {
	if p := t.Page; p != nil && (p.WidthPt > 0 || p.MarginLeftPt > 0) {
		fmt.Fprintf(b, "  page %s, margins %g/%g/%g/%g pt (top/bottom/left/right)",
			pageName(p), p.MarginTopPt, p.MarginBottomPt, p.MarginLeftPt, p.MarginRightPt)
		if p.Landscape {
			b.WriteString(", landscape")
		}
		if p.Background != "" {
			fmt.Fprintf(b, ", background %s", p.Background)
		}
		b.WriteString("\n")
	}
	for _, o := range t.FloatingObjects {
		fmt.Fprintf(b, "  floating %s %s", o.Kind, o.ID)
		if o.Title != "" {
			fmt.Fprintf(b, " %q", o.Title)
		}
		if o.WidthPt > 0 {
			fmt.Fprintf(b, " (%g×%g pt)", o.WidthPt, o.HeightPt)
		}
		b.WriteString("\n")
	}
	for _, ns := range t.NamedStyles {
		fmt.Fprintf(b, "  named style %s (%d paragraph(s) in this tab): %s\n", ns.Style.Type, ns.Paragraphs, render.NamedStyle(ns.Style))
	}
	for _, nr := range t.NamedRanges {
		fmt.Fprintf(b, "  named range %q (id %s, %d character(s))\n", nr.Name, nr.ID, nr.Chars)
	}
}

// pageName says which standard page a size is, so the model need not
// recognise 612×792 as US Letter.
func pageName(p *doc.PageSetup) string {
	switch {
	case p.WidthPt == 0:
		return "default size"
	case near(p.WidthPt, 612) && near(p.HeightPt, 792):
		return "US Letter"
	case near(p.WidthPt, 595) && near(p.HeightPt, 842):
		return "A4"
	case near(p.WidthPt, 612) && near(p.HeightPt, 1008):
		return "US Legal"
	}
	return fmt.Sprintf("%g×%g pt", p.WidthPt, p.HeightPt)
}

func near(a, b float64) bool { return a-b < 1 && b-a < 1 }

// writeWarnings ends a result's text with one line per warning. The
// list_comments and manage_tabs texts append theirs with a leading
// newline instead, because their builders are not newline-terminated.
func writeWarnings(b *strings.Builder, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(b, "warning: %s\n", w)
	}
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
	} else if res.Truncated {
		// The raw format is a JSON array the caller may parse, so the
		// note goes after it, as a comment, and only when it is needed.
		res.Text += fmt.Sprintf("\n// truncated at %d block(s); pass continue_from %s to read on", res.Blocks, res.ContinueFrom)
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
// ones get markers, every one is counted in the footer. The footer shows
// the first post and how many replies follow it, so the posts themselves
// are left for list_comments.
func commentMarks(threads []CommentThread) []render.Mark {
	marks := make([]render.Mark, 0, len(threads))
	for _, t := range threads {
		if t.Deleted {
			continue
		}
		marks = append(marks, render.Mark{TabID: t.Tab, SegmentID: t.Segment, Start: t.Start, End: t.End,
			Thread:  render.Thread{ID: t.ID, Handle: t.Handle, Author: t.Author, Content: t.Content, Resolved: t.Resolved},
			Replies: len(t.Replies)})
	}
	return marks
}
