package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Result is a rendered block range.
type Result struct {
	Text         string
	Blocks       int
	Chars        int
	Truncated    bool
	ContinueFrom string
}

// Markdown renders blocks [from, to) of a segment as markdown.
func Markdown(seg *doc.Segment, from, to int, o Options) Result {
	r := &mdRenderer{seg: seg, o: o, view: o.view()}
	return r.render(from, to)
}

type mdRenderer struct {
	seg       *doc.Segment
	o         Options
	view      doc.View
	footnotes []string // referenced footnote ids in order
	seen      map[string]bool
}

func (r *mdRenderer) render(from, to int) Result {
	blocks := r.seg.Blocks
	if from < 0 {
		from = 0
	}
	if to > len(blocks) {
		to = len(blocks)
	}
	var out strings.Builder
	var res Result
	var prev *doc.Block
	prevEmpty := false
	for i := from; i < to; i++ {
		b := blocks[i]
		chunk := r.block(b, 0)
		if chunk == "" && b.Kind != doc.KindParagraph {
			// Section breaks and other silent blocks leave no trace
			// unless handles are on.
			continue
		}
		sep := "\n\n"
		switch {
		case prev == nil:
			sep = ""
		case tightNeighbours(prev, b), prevEmpty:
			sep = "\n"
		}
		if r.o.MaxChars > 0 && res.Blocks > 0 && out.Len()+len(sep)+len(chunk) > r.o.MaxChars {
			res.Truncated = true
			res.ContinueFrom = b.Handle
			break
		}
		out.WriteString(sep)
		out.WriteString(chunk)
		res.Blocks++
		prev = b
		prevEmpty = chunk == ""
	}
	if defs := r.footnoteDefs(); defs != "" {
		out.WriteString("\n\n")
		out.WriteString(defs)
	}
	res.Text = out.String()
	res.Chars = len(res.Text)
	return res
}

// tightNeighbours reports whether two blocks belong to the same list run
// and should be separated by a single newline.
func tightNeighbours(a, b *doc.Block) bool {
	return a.Paragraph != nil && b.Paragraph != nil && a.Paragraph.Bullet != nil && b.Paragraph.Bullet != nil
}

func (r *mdRenderer) handle(b *doc.Block) string {
	if !r.o.WithHandles {
		return ""
	}
	return "[" + b.Handle + "] "
}

func (r *mdRenderer) block(b *doc.Block, depth int) string {
	switch {
	case b.Paragraph != nil:
		return r.paragraph(b)
	case b.Table != nil:
		return r.table(b, depth)
	case b.SectionBreak != nil:
		if !r.o.WithHandles {
			return ""
		}
		t := strings.ToLower(strings.ReplaceAll(b.SectionBreak.Type, "_", " "))
		if t == "" {
			t = "section"
		}
		return r.handle(b) + "<!-- section break: " + t + " -->"
	case b.TOC != nil:
		var lines []string
		lines = append(lines, r.handle(b)+"<!-- table of contents -->")
		for _, nb := range b.TOC.Blocks {
			lines = append(lines, r.block(nb, depth+1))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func (r *mdRenderer) paragraph(b *doc.Block) string {
	p := b.Paragraph
	r.collectFootnotes(p)
	content := inline(p, r.seg.Tab, r.o, false)
	if p.Bullet == nil && p.Level == 0 && !p.IsTitle {
		content = escapeLineStart(content)
	}
	var line string
	switch {
	case p.Level > 0:
		line = strings.Repeat("#", p.Level) + " " + content
		if r.o.WithHandles && p.HeadingID != "" {
			line += " {" + p.HeadingID + "}"
		}
	case p.IsTitle:
		line = "# " + content
	case p.IsSubtitle:
		if content != "" {
			line = "*" + content + "*"
		}
	case p.Bullet != nil:
		marker := "- "
		if p.Bullet.Ordered {
			marker = strconv.Itoa(ListNumber(r.seg, b)) + ". "
		}
		line = strings.Repeat("    ", p.Bullet.Nesting) + marker + content
	default:
		line = content
	}
	if r.o.WithStyles {
		line += paragraphAnnotation(p)
	}
	if line == "" && r.o.WithHandles {
		return strings.TrimSpace(r.handle(b))
	}
	return r.handle(b) + line
}

// ListNumber counts the block's position among preceding siblings in the
// same list and nesting level, restarting after any interruption.
func ListNumber(seg *doc.Segment, b *doc.Block) int {
	siblings := seg.Blocks
	if b.Cell != nil {
		siblings = b.Cell.Blocks
	}
	n := 0
	for _, s := range siblings {
		if s.Paragraph == nil || s.Paragraph.Bullet == nil {
			if n > 0 && s == b {
				break
			}
			n = 0
			continue
		}
		bl := s.Paragraph.Bullet
		if bl.ListID != b.Paragraph.Bullet.ListID {
			n = 0
			continue
		}
		if bl.Nesting == b.Paragraph.Bullet.Nesting {
			n++
		} else if bl.Nesting < b.Paragraph.Bullet.Nesting {
			n = 0
		}
		if s == b {
			break
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

func (r *mdRenderer) table(b *doc.Block, depth int) string {
	t := b.Table
	var sb strings.Builder
	if r.o.WithHandles {
		fmt.Fprintf(&sb, "%stable %d×%d (cells %s:r1c1 … %s:r%dc%d)\n", r.handle(b), t.Rows, t.Cols, b.Handle, b.Handle, t.Rows, t.Cols)
	}
	if depth > 0 {
		sb.WriteString("[nested table]")
		return sb.String()
	}
	for ri, row := range t.Cells {
		sb.WriteString("|")
		for _, c := range row {
			sb.WriteString(" " + r.cell(c) + " |")
		}
		sb.WriteString("\n")
		if ri == 0 {
			sb.WriteString("|")
			for range row {
				sb.WriteString(" --- |")
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (r *mdRenderer) cell(c *doc.Cell) string {
	var parts []string
	for _, b := range c.Blocks {
		switch {
		case b.Paragraph != nil:
			r.collectFootnotes(b.Paragraph)
			text := inline(b.Paragraph, r.seg.Tab, r.o, true)
			if b.Paragraph.Bullet != nil {
				text = "• " + text
			}
			parts = append(parts, text)
		case b.Table != nil:
			parts = append(parts, "[nested table]")
		}
	}
	return strings.Join(parts, "<br>")
}

func (r *mdRenderer) collectFootnotes(p *doc.Paragraph) {
	for _, run := range p.Runs {
		if run.Kind == doc.RunFootnoteRef && run.Visible(r.view) {
			if r.seen == nil {
				r.seen = map[string]bool{}
			}
			if !r.seen[run.FootnoteID] {
				r.seen[run.FootnoteID] = true
				r.footnotes = append(r.footnotes, run.FootnoteID)
			}
		}
	}
}

func (r *mdRenderer) footnoteDefs() string {
	if len(r.footnotes) == 0 || r.seg.Tab == nil {
		return ""
	}
	byID := map[string]*doc.Segment{}
	for _, fs := range r.seg.Tab.Footnotes {
		byID[fs.ID] = fs
	}
	var lines []string
	for _, id := range r.footnotes {
		fs := byID[id]
		if fs == nil {
			continue
		}
		label := fs.FootnoteNumber
		if label == "" {
			label = id
		}
		sub := &mdRenderer{seg: fs, o: Options{Suggestions: r.o.Suggestions, WithStyles: r.o.WithStyles}, view: r.view}
		text := strings.ReplaceAll(sub.render(0, len(fs.Blocks)).Text, "\n\n", "\n    ")
		lines = append(lines, "[^"+label+"]: "+text)
	}
	return strings.Join(lines, "\n")
}

// escapeLineStart keeps prose that starts like markdown syntax from being
// read as a heading, list item or rule.
func escapeLineStart(s string) string {
	trim := strings.TrimLeft(s, " ")
	switch {
	case strings.HasPrefix(trim, "#"), strings.HasPrefix(trim, "- "), strings.HasPrefix(trim, "+ "), strings.HasPrefix(trim, "* "), strings.HasPrefix(trim, ">"), trim == "---", trim == "***":
		return "\\" + s
	}
	if i := strings.IndexAny(trim, ".)"); i > 0 && i < 4 {
		if _, err := strconv.Atoi(trim[:i]); err == nil && len(trim) > i+1 && trim[i+1] == ' ' {
			return trim[:i] + "\\" + trim[i:]
		}
	}
	return s
}
