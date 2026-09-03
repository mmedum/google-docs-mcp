package doc

import (
	"strings"
	"unicode"
)

// View selects how suggestions are folded into text.
type View int

// Views.
const (
	// ViewInline keeps every run, matching the API's index space.
	ViewInline View = iota
	// ViewCurrent hides suggested insertions: the document as committed.
	ViewCurrent
	// ViewAccepted hides suggested deletions: the document if all
	// suggestions were accepted.
	ViewAccepted
)

// Visible reports whether the run shows in the view.
func (r *Run) Visible(v View) bool {
	switch v {
	case ViewCurrent:
		return !r.IsSuggestedInsertion()
	case ViewAccepted:
		return !r.IsSuggestedDeletion()
	}
	return true
}

// Text returns the paragraph's plain text in the view, without the
// trailing newline. Chips contribute their display text; objects and
// breaks contribute nothing.
func (p *Paragraph) Text(v View) string {
	var b strings.Builder
	for _, r := range p.Runs {
		if !r.Visible(v) {
			continue
		}
		switch r.Kind {
		case RunText, RunPerson, RunRichLink, RunDate:
			b.WriteString(r.Text)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Text returns a block's plain text: paragraphs directly, tables as rows
// of tab-separated cells, other kinds empty.
func (b *Block) Text(v View) string {
	switch {
	case b.Paragraph != nil:
		return b.Paragraph.Text(v)
	case b.Table != nil:
		var rows []string
		for _, row := range b.Table.Cells {
			var cells []string
			for _, c := range row {
				if !c.Covered() {
					cells = append(cells, c.Text(v))
				}
			}
			rows = append(rows, strings.Join(cells, "\t"))
		}
		return strings.Join(rows, "\n")
	case b.TOC != nil:
		var lines []string
		for _, nb := range b.TOC.Blocks {
			lines = append(lines, nb.Text(v))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// Text joins the cell's blocks with newlines.
func (c *Cell) Text(v View) string {
	lines := make([]string, 0, len(c.Blocks))
	for _, b := range c.Blocks {
		lines = append(lines, b.Text(v))
	}
	return strings.Join(lines, "\n")
}

// NormalizeRune maps one character to its comparison form: curly quotes
// to straight, dashes to hyphens, any space to a plain space, zero-width
// characters dropped (keep=false). Text matching and Normalize share it
// so needles and haystacks always agree.
func NormalizeRune(r rune) (rune, bool) {
	switch r {
	case '\u2018', '\u2019', '\u201a', '\u201b':
		return '\'', true
	case '\u201c', '\u201d', '\u201e', '\u201f':
		return '"', true
	case '\u2013', '\u2014', '\u2011':
		return '-', true
	case '\u200b', '\ufeff':
		return 0, false
	}
	if unicode.IsSpace(r) {
		return ' ', true
	}
	return r, true
}

// Normalize makes prose comparable: NormalizeRune applied to every
// character, whitespace runs collapsed to one space, trimmed.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		nr, keep := NormalizeRune(r)
		switch {
		case !keep:
		case nr == ' ':
			space = b.Len() > 0
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(nr)
		}
	}
	return b.String()
}

// Count returns the words and characters of the paragraph's text in the
// view, as WordCount and a rune count of Text would, without building
// the string.
func (p *Paragraph) Count(v View) (words, chars int) {
	inWord := false
	var last rune
	for _, r := range p.Runs {
		if !r.Visible(v) {
			continue
		}
		switch r.Kind {
		case RunText, RunPerson, RunRichLink, RunDate:
		default:
			continue
		}
		for _, c := range r.Text {
			chars++
			last = c
			if unicode.IsSpace(c) {
				inWord = false
			} else if !inWord {
				inWord = true
				words++
			}
		}
	}
	if last == '\n' {
		chars--
	}
	return words, chars
}

// Words counts the block's words in the view: a paragraph's own, a
// table's or table of contents' nested paragraphs summed.
func (b *Block) Words(v View) int {
	switch {
	case b.Paragraph != nil:
		w, _ := b.Paragraph.Count(v)
		return w
	case b.Table != nil:
		n := 0
		for _, row := range b.Table.Cells {
			for _, c := range row {
				if c.Covered() {
					continue
				}
				for _, nb := range c.Blocks {
					n += nb.Words(v)
				}
			}
		}
		return n
	case b.TOC != nil:
		n := 0
		for _, nb := range b.TOC.Blocks {
			n += nb.Words(v)
		}
		return n
	}
	return 0
}

// OneLine collapses whitespace runs, newlines included, to single spaces.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Clip trims s and cuts it to n characters with an ellipsis.
func Clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// WordCount counts whitespace-separated words.
func WordCount(s string) int {
	return len(strings.Fields(s))
}
