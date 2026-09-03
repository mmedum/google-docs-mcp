// Package render turns the document model into text for the model to
// read: markdown (default), plain text, and an outline. Every renderer
// works on a block range of one segment so reads can be scoped and
// budgeted.
package render

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Options tune a render.
type Options struct {
	// WithHandles prefixes every block with its handle.
	WithHandles bool
	// WithStyles annotates runs whose formatting markdown cannot express.
	WithStyles bool
	// Suggestions shows pending suggestions as CriticMarkup. When false
	// the committed view is rendered (suggested insertions hidden).
	Suggestions bool
	// MaxChars stops after the block that would cross the budget. 0 = no limit.
	MaxChars int
	// Marks are comment threads to show as {>>c:<id><<} after the text
	// they cover, and to list below the content when CommentFooter is set.
	Marks []Mark
	// CommentFooter appends the list of the threads marked in the
	// rendered range and counts the ones outside it.
	CommentFooter bool
}

// Mark is a comment thread where a read found it. A located thread names
// the segment and range it sits on; an unlocated one (no handle) is only
// counted. Replies is a count, not the posts: the footer says how many
// there are, and list_comments is where they are read.
type Mark struct {
	TabID     string
	SegmentID string
	Start     int64
	End       int64
	Thread    Thread
	Replies   int
}

func (o Options) view() doc.View {
	if o.Suggestions {
		return doc.ViewInline
	}
	return doc.ViewCurrent
}

// span is a run of text sharing one markdown treatment.
type span struct {
	text     string
	style    doc.TextStyle
	inserted string
	deleted  string
}

func suggestionKey(ids []string) string { return strings.Join(ids, ",") }

// locatedIn reports whether the thread was located in this segment.
func (m Mark) locatedIn(seg *doc.Segment) bool {
	return m.Thread.Handle != "" && m.SegmentID == seg.ID && m.TabID == seg.Tab.ID
}

// marksFor keeps the located marks of one segment, sorted by end offset.
func marksFor(marks []Mark, seg *doc.Segment) []Mark {
	out := make([]Mark, 0, len(marks))
	for _, m := range marks {
		if m.locatedIn(seg) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].End < out[j].End })
	return out
}

// commentFooter lists the threads marked in the rendered blocks
// [from, res.To) of the segment and counts the ones elsewhere or
// unlocated.
func commentFooter(seg *doc.Segment, from int, res Result, marks []Mark) string {
	if len(marks) == 0 {
		return "\n\n<!-- no comments -->"
	}
	var start, end int64
	if res.To > from && res.To <= len(seg.Blocks) {
		start, end = seg.Blocks[from].Start, seg.Blocks[res.To-1].End
	}
	var sb strings.Builder
	other := 0
	for _, m := range marks {
		inRange := m.locatedIn(seg) && m.End > start && m.Start < end
		if !inRange {
			other++
			continue
		}
		sb.WriteString("\n")
		threadLine(&sb, m.Thread, "c:")
		if n := m.Replies; n > 0 {
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

// withFooter appends the comment footer when asked for.
func withFooter(seg *doc.Segment, from int, res Result, o Options) Result {
	if o.CommentFooter {
		res.Text += commentFooter(seg, from, res, o.Marks)
		res.Chars = len(res.Text)
	}
	return res
}

// inline renders a paragraph's runs as markdown inline content. marks
// are the segment's marks sorted by end.
func inline(p *doc.Paragraph, seg *doc.Segment, o Options, marks []Mark, inTable bool) string {
	var spans []span
	var b strings.Builder
	flush := func() {
		for _, s := range spans {
			b.WriteString(markSpan(s, o))
		}
		spans = nil
	}
	addText := func(text string, r *doc.Run) {
		s := span{text: text, style: r.Style, inserted: suggestionKey(r.Inserted), deleted: suggestionKey(r.Deleted)}
		if inTable {
			s.text = tableCellText(s.text)
		}
		if n := len(spans); n > 0 && spans[n-1].style == s.style && spans[n-1].inserted == s.inserted && spans[n-1].deleted == s.deleted {
			spans[n-1].text += s.text
		} else {
			spans = append(spans, s)
		}
	}
	marks = marksIn(marks, p)
	for _, r := range p.Runs {
		if !r.Visible(o.view()) {
			continue
		}
		if r.Kind == doc.RunText {
			text := strings.TrimSuffix(r.Text, "\n")
			// Split the run where comment ranges end so the marker lands
			// right after the commented text.
			pos := r.Start
			for _, m := range marks {
				if m.End <= pos || m.End > r.End {
					continue
				}
				cut := min(doc.UTF16ToByte(text, m.End-pos), len(text))
				addText(text[:cut], r)
				flush()
				b.WriteString("{>>c:" + m.Thread.ID + "<<}")
				text, pos = text[cut:], m.End
			}
			addText(text, r)
			continue
		}
		s := span{text: objectText(r, seg.Tab), inserted: suggestionKey(r.Inserted), deleted: suggestionKey(r.Deleted)}
		if s.text == "" {
			continue
		}
		if inTable {
			s.text = tableCellText(s.text)
		}
		flush()
		b.WriteString(markSuggestion(s.text, s.inserted, s.deleted, o))
	}
	flush()
	return b.String()
}

// marksIn is the sub-slice of sorted marks ending inside the paragraph.
func marksIn(marks []Mark, p *doc.Paragraph) []Mark {
	if len(marks) == 0 || len(p.Runs) == 0 {
		return nil
	}
	start, end := p.Runs[0].Start, p.Runs[len(p.Runs)-1].End
	lo := sort.Search(len(marks), func(i int) bool { return marks[i].End > start })
	hi := sort.Search(len(marks), func(i int) bool { return marks[i].End > end })
	return marks[lo:hi]
}

// textWithMarks is the paragraph's plain text with comment markers after
// the text they cover, for the plain renderer.
func textWithMarks(p *doc.Paragraph, view doc.View, marks []Mark) string {
	marks = marksIn(marks, p)
	if len(marks) == 0 {
		return p.Text(view)
	}
	var b strings.Builder
	for _, r := range p.Runs {
		if !r.Visible(view) {
			continue
		}
		switch r.Kind {
		case doc.RunText:
			text := strings.TrimSuffix(r.Text, "\n")
			pos := r.Start
			for _, m := range marks {
				if m.End <= pos || m.End > r.End {
					continue
				}
				cut := min(doc.UTF16ToByte(text, m.End-pos), len(text))
				b.WriteString(text[:cut] + "{>>c:" + m.Thread.ID + "<<}")
				text, pos = text[cut:], m.End
			}
			b.WriteString(text)
		case doc.RunPerson, doc.RunRichLink, doc.RunDate:
			b.WriteString(r.Text)
		}
	}
	return b.String()
}

// tableCellText escapes what would break a markdown table cell.
func tableCellText(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", "<br>")
}

// objectText renders a non-text run: chips, breaks, objects, references.
func objectText(r *doc.Run, tab *doc.Tab) string {
	switch r.Kind {
	case doc.RunInlineObject:
		return objectMarkdown(r.ObjectID, tab)
	case doc.RunFootnoteRef:
		return "[^" + footnoteLabel(r) + "]"
	case doc.RunPageBreak:
		return "<!-- page break -->"
	case doc.RunColumnBreak:
		return "<!-- column break -->"
	case doc.RunHorizontalRule:
		return "\n---\n"
	case doc.RunPerson:
		return "@" + r.Text
	case doc.RunRichLink:
		if r.LinkURI != "" {
			return "[" + r.Text + "](" + r.LinkURI + ")"
		}
		return r.Text
	case doc.RunDate:
		return r.Text
	case doc.RunEquation:
		return "{equation}"
	case doc.RunAutoText:
		return "{" + strings.ToLower(strings.ReplaceAll(r.AutoTextType, "_", " ")) + "}"
	}
	return ""
}

func footnoteLabel(r *doc.Run) string {
	if r.FootnoteNumber != "" {
		return r.FootnoteNumber
	}
	return r.FootnoteID
}

func objectMarkdown(id string, tab *doc.Tab) string {
	info := tab.InlineObjects[id]
	if info == nil {
		return "![object](" + id + ")"
	}
	label := info.Kind
	if info.Title != "" {
		label += ": " + info.Title
	} else if info.Description != "" {
		label += ": " + info.Description
	}
	size := ""
	if info.WidthPt > 0 || info.HeightPt > 0 {
		size = fmt.Sprintf(", %.0f×%.0fpt", info.WidthPt, info.HeightPt)
	}
	return "![" + label + "](" + id + size + ")"
}

// markSpan wraps a text span in markdown emphasis, keeping surrounding
// whitespace outside the markers so the markdown stays valid.
func markSpan(s span, o Options) string {
	text := s.text
	if text == "" {
		return ""
	}
	lead := text[:len(text)-len(strings.TrimLeftFunc(text, unicode.IsSpace))]
	trail := text[len(strings.TrimRightFunc(text, unicode.IsSpace)):]
	core := strings.TrimSpace(text)
	if core == "" {
		return markSuggestion(text, s.inserted, s.deleted, o)
	}
	st := s.style
	switch {
	case st.Monospace():
		core = "`" + core + "`"
	default:
		if st.Bold {
			core = "**" + core + "**"
		}
		if st.Italic {
			core = "*" + core + "*"
		}
		if st.Strikethrough {
			core = "~~" + core + "~~"
		}
	}
	if st.LinkURL != "" {
		core = "[" + core + "](" + st.LinkURL + ")"
	} else if st.LinkHeadingID != "" {
		core = "[" + core + "](#" + st.LinkHeadingID + ")"
	}
	if o.WithStyles {
		if ann := styleAnnotation(st); ann != "" {
			core += ann
		}
	}
	return lead + markSuggestion(core, s.inserted, s.deleted, o) + trail
}

// markSuggestion applies CriticMarkup for suggested insertions and
// deletions, tagging the suggestion id.
func markSuggestion(text, inserted, deleted string, o Options) string {
	if !o.Suggestions {
		return text
	}
	switch {
	case inserted != "":
		return "{++" + text + "++}{>>s:" + inserted + "<<}"
	case deleted != "":
		return "{--" + text + "--}{>>s:" + deleted + "<<}"
	}
	return text
}

// NamedStyle describes what a named style means, for the reads that
// report the definitions layout_document can rewrite. Unlike
// styleAnnotation it names bold and italic too: here there is no
// markdown around it to show them.
func NamedStyle(d *doc.NamedStyleDef) string {
	st := d.Text
	var parts []string
	if f := fontLabel(st); f != "" {
		parts = append(parts, f)
	}
	for _, f := range []struct {
		on   bool
		name string
	}{
		{st.Bold, "bold"}, {st.Italic, "italic"}, {st.Underline, "underline"},
		{st.Strikethrough, "strikethrough"}, {st.SmallCaps, "small caps"},
	} {
		if f.on {
			parts = append(parts, f.name)
		}
	}
	if st.Baseline != "" {
		parts = append(parts, strings.ToLower(st.Baseline))
	}
	if st.Foreground != "" {
		parts = append(parts, "color "+st.Foreground)
	}
	if st.Background != "" {
		parts = append(parts, "background "+st.Background)
	}
	// Every value the response carries is named, even START and 100%: a
	// named style inherits from the tab's NORMAL_TEXT, not from the
	// API's defaults, so "single spaced" is news when the body text is
	// not. Only what Google left out is left out here.
	if d.Alignment != "" && d.Alignment != "ALIGNMENT_UNSPECIFIED" {
		parts = append(parts, "align "+strings.ToLower(d.Alignment))
	}
	if d.LineSpacing > 0 {
		parts = append(parts, fmt.Sprintf("line spacing %g%%", d.LineSpacing))
	}
	for _, f := range []struct {
		pt   float64
		name string
	}{
		{d.SpaceAbovePt, "space above"}, {d.SpaceBelowPt, "space below"},
		{d.IndentStartPt, "indent"}, {d.IndentFirstLinePt, "first line indent"},
	} {
		if f.pt != 0 {
			parts = append(parts, fmt.Sprintf("%s %gpt", f.name, f.pt))
		}
	}
	if d.KeepWithNext {
		parts = append(parts, "keep with next")
	}
	if d.PageBreakBefore {
		parts = append(parts, "page break before")
	}
	if len(parts) == 0 {
		return "no formatting of its own"
	}
	return strings.Join(parts, ", ")
}

// fontLabel is a font family and size as both readers of a style write
// them: "Arial 11pt", or whichever half is set.
func fontLabel(st doc.TextStyle) string {
	switch {
	case st.FontFamily != "" && st.FontSizePt > 0:
		return fmt.Sprintf("%s %gpt", st.FontFamily, st.FontSizePt)
	case st.FontFamily != "":
		return st.FontFamily
	case st.FontSizePt > 0:
		return fmt.Sprintf("%gpt", st.FontSizePt)
	}
	return ""
}

// aligned reports whether an alignment is worth naming: unset and START
// are what a paragraph does anyway.
func aligned(a string) bool {
	return a != "" && a != "START" && a != "ALIGNMENT_UNSPECIFIED"
}

// styleAnnotation names formatting markdown cannot carry.
func styleAnnotation(st doc.TextStyle) string {
	var parts []string
	// A monospace font is already carried by the backticks around the run.
	if st.FontFamily != "" && !st.Monospace() {
		parts = append(parts, "font: "+fontLabel(st))
	} else if st.FontSizePt > 0 {
		parts = append(parts, fmt.Sprintf("size: %gpt", st.FontSizePt))
	}
	if st.Foreground != "" {
		parts = append(parts, "color: "+st.Foreground)
	}
	if st.Background != "" {
		parts = append(parts, "background: "+st.Background)
	}
	if st.Underline && !st.HasLink() {
		parts = append(parts, "underline")
	}
	if st.SmallCaps {
		parts = append(parts, "small caps")
	}
	if st.Baseline != "" {
		parts = append(parts, strings.ToLower(st.Baseline))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// paragraphAnnotation names paragraph formatting markdown cannot carry.
func paragraphAnnotation(p *doc.Paragraph) string {
	var parts []string
	if p.IsTitle {
		parts = append(parts, "style: TITLE")
	}
	if p.IsSubtitle {
		parts = append(parts, "style: SUBTITLE")
	}
	if aligned(p.Alignment) {
		parts = append(parts, "align: "+strings.ToLower(p.Alignment))
	}
	if p.IndentStartPt > 0 && p.Bullet == nil {
		parts = append(parts, fmt.Sprintf("indent: %gpt", p.IndentStartPt))
	}
	if len(parts) == 0 {
		return ""
	}
	return " {" + strings.Join(parts, ", ") + "}"
}
