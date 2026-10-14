package mdfile

import (
	"regexp"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// A working copy is a Google Doc made from a Markdown file, for a person
// and Claude to work in together; its changes come back as Markdown
// through Docs' export, which writes Markdown its own way (spike D):
// front matter as a rule and a heading, inline HTML as its text, `__x__`
// as `**x**`, tables re-spaced, list items with trailing spaces, empty
// `&nbsp;` paragraphs. Merge carries back only what was changed in the
// copy and leaves the file's own way of writing everything else.

// Conflict is a block changed in the working copy that was also changed
// in the file since the copy was made. The file's version is kept.
type Conflict struct {
	// Base is the block as it was when the copy was made, Copy what the
	// working copy made of it ("" for a deletion).
	Base, Copy string
}

// block is one block of Markdown: a paragraph (its lines together), a
// heading, a list item, a table row, a quote line, a rule, an HTML line,
// a footnote definition or a fenced code block. sep is the newlines that
// follow it, to the next block.
type block struct {
	text, sep, norm string
	// chain says the block runs on into a next one of its kind with a
	// single newline: list items, table rows, quote lines.
	chain string
	// at is the block's place in the text it came from; copy says it
	// came from the working copy.
	at   int
	copy bool
}

var (
	fenceRE   = regexp.MustCompile("^ {0,3}(```+|~~~+)")
	headingRE = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t#]*$`)
	setextRE  = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	listRE    = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)
	ruleRE    = regexp.MustCompile(`^ {0,3}([-*_])(?:[ \t]*([-*_])){2,}[ \t]*$`)
	ownLineRE = regexp.MustCompile(`^\s*(?:\||>|<|\[\^[^\]]+\]:)`)
	tagRE     = regexp.MustCompile(`<[^>\n]+>`)
	escapeRE  = regexp.MustCompile("\\\\([!-/:-@\\[-`{-~])")
	strongRE  = regexp.MustCompile(`(^|[^\w*])__([^_\n]+?)__($|[^\w])`)
	emRE      = regexp.MustCompile(`(^|[^\w*_])_([^_\n]+?)_($|[^\w])`)
	imgRE     = regexp.MustCompile(`!\[([^\]\n]*)\]\([^)\n]*\)`)
	spaceRE   = regexp.MustCompile(`[ \t]+`)
	cellRE    = regexp.MustCompile(`^:?-+:?$`)
)

// blankLine only separates blocks: empty, or the `&nbsp;` paragraph Docs'
// export writes after a table or code block.
func blankLine(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || t == "&nbsp;"
}

// ownLine is a line that is a block by itself.
func ownLine(l string) bool {
	return headingRE.MatchString(l) || listRE.MatchString(l) || ruleRE.MatchString(l) || ownLineRE.MatchString(l)
}

func chainOf(l string) string {
	t := strings.TrimSpace(l)
	switch {
	case listRE.MatchString(l):
		return "list"
	case strings.HasPrefix(t, "|"):
		return "table"
	case strings.HasPrefix(t, ">"):
		return "quote"
	}
	return ""
}

// closesFence reports whether l closes a fence opened by open.
func closesFence(open, l string) bool {
	t := strings.TrimSpace(l)
	return len(t) >= len(open) && strings.Trim(t, open[:1]) == ""
}

// units splits Markdown into blocks; lead is the blank lines before the
// first. Joining each block's text and sep after lead gives s back.
func units(s string) (string, []block) {
	endsNL := strings.HasSuffix(s, "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if s == "" {
		return "", nil
	}
	i := 0
	for i < len(lines) && blankLine(lines[i]) {
		i++
	}
	// The separation before the first block, as written.
	lead := ""
	if i > 0 {
		lead = strings.Join(lines[:i], "\n")
		if i < len(lines) || endsNL {
			lead += "\n"
		}
	}
	var us []block
	for i < len(lines) {
		start := i
		switch l := lines[i]; {
		case fenceRE.MatchString(l):
			open := strings.TrimSpace(fenceRE.FindString(l))
			for i++; i < len(lines); i++ {
				if closesFence(open, lines[i]) {
					i++
					break
				}
			}
		case ownLine(l):
			i++
		default:
			// A paragraph runs to a blank line or a block of its own; an
			// underline makes it a setext heading.
			for i++; i < len(lines); i++ {
				if setextRE.MatchString(lines[i]) {
					i++
					break
				}
				if blankLine(lines[i]) || fenceRE.MatchString(lines[i]) || ownLine(lines[i]) {
					break
				}
			}
		}
		u := block{text: strings.Join(lines[start:min(i, len(lines))], "\n"), chain: chainOf(lines[start]), at: len(us)}
		var sep strings.Builder
		for i < len(lines) && blankLine(lines[i]) {
			sep.WriteString("\n" + lines[i])
			i++
		}
		if i < len(lines) || endsNL {
			sep.WriteString("\n")
		}
		u.sep = sep.String()
		u.norm = normalize(u.text)
		us = append(us, u)
	}
	return lead, us
}

// normalize is a block as the file and Docs' export both say it: what
// Docs writes differently without changing what the block says is taken
// out.
func normalize(text string) string {
	lines := strings.Split(text, "\n")
	if fenceRE.MatchString(lines[0]) {
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		return strings.Join(lines, "\n")
	}
	// A setext heading is the ATX heading Docs writes for it.
	if n := len(lines); n >= 2 && setextRE.MatchString(lines[n-1]) {
		level := "##"
		if strings.Contains(lines[n-1], "=") {
			level = "#"
		}
		lines = []string{level + " " + strings.Join(lines[:n-1], " ")}
	}
	s := strings.Join(lines, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = tagRE.ReplaceAllString(s, "")
	s = imgRE.ReplaceAllString(s, "![$1]")
	s = escapeRE.ReplaceAllString(s, "$1")
	s = strongRE.ReplaceAllString(s, "$1**$2**$3")
	s = emRE.ReplaceAllString(s, "$1*$2*$3")
	if m := headingRE.FindStringSubmatch(s); m != nil {
		s = m[1] + " " + m[2]
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "|") {
		cells := strings.Split(strings.Trim(t, "|"), "|")
		for i, c := range cells {
			c = strings.TrimSpace(c)
			if cellRE.MatchString(c) {
				// Alignment is what a delimiter cell says; its width is not.
				canon := "-"
				if strings.HasPrefix(c, ":") {
					canon = ":" + canon
				}
				if strings.HasSuffix(c, ":") {
					canon += ":"
				}
				c = canon
			}
			cells[i] = c
		}
		s = "|" + strings.Join(cells, "|") + "|"
	}
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

// diffUnits is the diff of two block lists by key, as runs.
func diffUnits(a, b []block, key func(block) string) []diffmatchpatch.Diff {
	ids := map[string]rune{}
	runes := func(us []block) []rune {
		out := make([]rune, len(us))
		for i, u := range us {
			k := key(u)
			r, ok := ids[k]
			if !ok {
				// From the supplementary planes: every block a valid rune.
				r = rune(0x10000 + len(ids))
				ids[k] = r
			}
			out[i] = r
		}
		return out
	}
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = 0
	return dmp.DiffMainRunes(runes(a), runes(b), false)
}

// copyEdit is one change made in the working copy: base blocks [from,
// to) replaced by ins, a pure insertion when from == to.
type copyEdit struct {
	from, to int
	ins      []block
}

func copyEdits(b, e []block) []copyEdit {
	var out []copyEdit
	i, j := 0, 0
	var open *copyEdit
	flush := func() {
		if open != nil {
			out = append(out, *open)
			open = nil
		}
	}
	for _, d := range diffUnits(b, e, func(u block) string { return u.norm }) {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			i, j = i+n, j+n
		case diffmatchpatch.DiffDelete:
			if open == nil {
				open = &copyEdit{from: i, to: i}
			}
			open.to += n
			i += n
		case diffmatchpatch.DiffInsert:
			if open == nil {
				open = &copyEdit{from: i, to: i}
			}
			open.ins = append(open.ins, e[j:j+n]...)
			j += n
		}
	}
	flush()
	return out
}

// Merge carries back into current what changed between base and export.
// base is the Markdown the working copy was made from, current the
// Markdown as it is now, export the working copy as Docs exports it, all
// without front matter. Every block the copy did not change keeps the
// file's own text. A change to a block that current also changed since
// base is a Conflict and is left out.
func Merge(base, current, export string) (string, []Conflict) {
	_, b := units(base)
	lead, c := units(current)
	_, e := units(export)
	bc := keptIn(b, c)
	drop := make([]bool, len(c))
	before := map[int][]block{} // blocks to place before current block i; len(c) is the end
	var conflicts []Conflict
	for _, ed := range copyEdits(b, e) {
		at, ok := ed.place(bc)
		if !ok {
			conflicts = append(conflicts, Conflict{Base: joinText(b[ed.from:ed.to]), Copy: joinText(ed.ins)})
			continue
		}
		for k := ed.from; k < ed.to; k++ {
			drop[bc[k]] = true
		}
		for _, u := range ed.ins {
			u.copy = true
			if u.chain == "list" {
				// Docs ends every list item it exports with two spaces.
				u.text = strings.TrimRight(u.text, " \t")
			}
			before[at] = append(before[at], u)
		}
	}
	var out []block
	for k := 0; k <= len(c); k++ {
		out = append(out, before[k]...)
		if k < len(c) && !drop[k] {
			out = append(out, c[k])
		}
	}
	end := ""
	if m := len(c); m > 0 {
		end = c[m-1].sep
	} else if strings.HasSuffix(current, "\n") {
		end = "\n"
	}
	return join(lead, out, end), conflicts
}

// keptIn is where each base block is in current, or -1 where current
// changed or dropped it since.
func keptIn(b, c []block) []int {
	bc := make([]int, len(b))
	for k := range bc {
		bc[k] = -1
	}
	i, j := 0, 0
	for _, d := range diffUnits(b, c, func(u block) string { return u.text }) {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			for k := range n {
				bc[i+k] = j + k
			}
			i, j = i+n, j+n
		case diffmatchpatch.DiffDelete:
			i += n
		case diffmatchpatch.DiffInsert:
			j += n
		}
	}
	return bc
}

// place is where an edit goes in current: before the current block its
// first replaced base block is, or after the nearest base block before
// it that current kept. ok is false when current changed a block the
// edit replaces.
func (ed copyEdit) place(bc []int) (int, bool) {
	for k := ed.from; k < ed.to; k++ {
		if bc[k] < 0 {
			return 0, false
		}
	}
	if ed.from < ed.to {
		return bc[ed.from], true
	}
	for k := ed.from - 1; k >= 0; k-- {
		if bc[k] >= 0 {
			return bc[k] + 1, true
		}
	}
	return 0, true
}

// join writes the blocks out. Neighbors in the file keep what was
// between them; where the copy's blocks meet the file's, blocks of one
// list, table or quote run on with one newline, any other two are a
// blank line apart, and the copy's &nbsp; lines are dropped.
func join(lead string, out []block, end string) string {
	var s strings.Builder
	s.WriteString(lead)
	for k, u := range out {
		s.WriteString(u.text)
		if k == len(out)-1 {
			break
		}
		next := out[k+1]
		if !u.copy && !next.copy && next.at == u.at+1 {
			s.WriteString(u.sep)
			continue
		}
		sep := strings.ReplaceAll(u.sep, "&nbsp;", "")
		switch {
		case u.chain != "" && u.chain == next.chain:
			sep = "\n"
		case strings.Count(sep, "\n") < 2, u.copy:
			sep = "\n\n"
		}
		s.WriteString(sep)
	}
	if len(out) > 0 {
		// The file ends as it ended.
		s.WriteString(end)
	}
	return s.String()
}

func joinText(us []block) string {
	parts := make([]string, len(us))
	for i, u := range us {
		parts[i] = u.text
	}
	return strings.Join(parts, "\n\n")
}
