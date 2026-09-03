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
				cells = append(cells, c.Text(v))
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

var normalizer = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
	"\u201c", `"`, "\u201d", `"`, "\u201e", `"`, "\u201f", `"`,
	"\u00a0", " ", "\u2002", " ", "\u2003", " ", "\u2009", " ", "\u200a", " ", "\u202f", " ", "\u3000", " ",
	"\u2013", "-", "\u2014", "-", "\u2011", "-",
	"\u2026", "...",
	"\u200b", "", "\ufeff", "",
)

// Normalize makes prose comparable: curly quotes to straight, dashes to
// hyphens, exotic spaces to spaces, whitespace runs collapsed, trimmed.
func Normalize(s string) string {
	s = normalizer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// WordCount counts whitespace-separated words.
func WordCount(s string) int {
	return len(strings.FieldsFunc(s, unicode.IsSpace))
}
