package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// OutlineHeading is one heading in the outline.
type OutlineHeading struct {
	Handle    string `json:"handle"`
	HeadingID string `json:"heading_id,omitempty"`
	Level     int    `json:"level"`
	Text      string `json:"text"`
	Blocks    int    `json:"blocks"`
	Words     int    `json:"words"`
}

// OutlineTab is one tab's outline.
type OutlineTab struct {
	Number     int              `json:"number"`
	ID         string           `json:"id,omitempty"`
	Title      string           `json:"title"`
	Nesting    int              `json:"nesting,omitempty"`
	Paragraphs int              `json:"paragraphs"`
	Tables     int              `json:"tables"`
	Words      int              `json:"words"`
	Headers    int              `json:"headers"`
	Footers    int              `json:"footers"`
	Footnotes  int              `json:"footnotes"`
	Preamble   int              `json:"preamble_blocks"`
	Headings   []OutlineHeading `json:"headings"`
}

// OutlineData computes the outline for the tabs given (all when nil).
func OutlineData(d *doc.Document, only *doc.Tab) []OutlineTab {
	var out []OutlineTab
	for _, t := range d.Tabs {
		if only != nil && t != only {
			continue
		}
		ot := OutlineTab{Number: t.Number, ID: t.ID, Title: t.Title, Nesting: t.Nesting,
			Headers: len(t.Headers), Footers: len(t.Footers), Footnotes: len(t.Footnotes)}
		// words[i] is the word count of blocks [0, i), so a section's
		// words are one subtraction.
		words := make([]int, len(t.Body.Blocks)+1)
		for i, b := range t.Body.Blocks {
			n := b.Words(doc.ViewCurrent)
			words[i+1] = words[i] + n
			switch {
			case b.Paragraph != nil:
				ot.Paragraphs++
				ot.Words += n
			case b.Table != nil:
				ot.Tables++
				ot.Words += n
			}
		}
		ot.Preamble = t.Body.Preamble().To
		for _, sec := range t.Body.Sections() {
			ot.Headings = append(ot.Headings, OutlineHeading{Handle: sec.Heading.Handle, HeadingID: sec.Heading.Paragraph.HeadingID, Level: sec.Level,
				Text: sec.Heading.Paragraph.Text(doc.ViewCurrent), Blocks: sec.To - sec.From, Words: words[sec.To] - words[sec.From]})
		}
		out = append(out, ot)
	}
	return out
}

// Outline renders the outline as markdown.
func Outline(d *doc.Document, tabs []OutlineTab) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", d.Title)
	for _, t := range tabs {
		title := t.Title
		if title == "" {
			title = fmt.Sprintf("Tab %d", t.Number)
		}
		fmt.Fprintf(&sb, "\n## Tab %d: %s", t.Number, title)
		if t.ID != "" {
			fmt.Fprintf(&sb, " (id %s)", t.ID)
		}
		fmt.Fprintf(&sb, "\n%d paragraphs, %d tables, %d words", t.Paragraphs, t.Tables, t.Words)
		var extras []string
		if t.Headers > 0 {
			extras = append(extras, fmt.Sprintf("%d header(s)", t.Headers))
		}
		if t.Footers > 0 {
			extras = append(extras, fmt.Sprintf("%d footer(s)", t.Footers))
		}
		if t.Footnotes > 0 {
			extras = append(extras, fmt.Sprintf("%d footnote(s)", t.Footnotes))
		}
		if len(extras) > 0 {
			sb.WriteString("; " + strings.Join(extras, ", "))
		}
		sb.WriteString("\n")
		if t.Preamble > 0 {
			fmt.Fprintf(&sb, "- (preamble: %d block(s) before the first heading)\n", t.Preamble)
		}
		if len(t.Headings) == 0 {
			sb.WriteString("- (no headings)\n")
		}
		for _, h := range t.Headings {
			indent := strings.Repeat("  ", max(h.Level-1, 0))
			id := ""
			if h.HeadingID != "" {
				id = " {" + h.HeadingID + "}"
			}
			fmt.Fprintf(&sb, "%s- [%s] H%d %s%s — %d block(s), %d words\n", indent, h.Handle, h.Level, h.Text, id, h.Blocks, h.Words)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
