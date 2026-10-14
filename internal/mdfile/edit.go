package mdfile

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// Edit replaces the visible text [Start, End) with Insert; Start == End
// is a pure insertion.
type Edit struct {
	Start, End int
	Insert     string
}

// lineDiffAbove is the size above which a diff is found line by line
// first: a character-level diff of a long rewrite can run into
// diffmatchpatch's one-second timeout.
const lineDiffAbove = 16 << 10

// MinimalEdits is the smallest set of edits that turn old into new,
// placed at start in the visible text, in ascending order. What is
// unchanged keeps its markup: a comment around an untouched word stays
// where it is. It is plan.MinimalEdits counted in bytes rather than
// UTF-16 units.
func MinimalEdits(old, new string, start int) []Edit {
	if old == new {
		return nil
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(old, new, len(old)+len(new) > lineDiffAbove))
	var out []Edit
	pos := start
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			pos += len(d.Text)
		case diffmatchpatch.DiffDelete:
			if n := len(out); n > 0 && out[n-1].End == pos {
				out[n-1].End += len(d.Text)
			} else {
				out = append(out, Edit{Start: pos, End: pos + len(d.Text)})
			}
			pos += len(d.Text)
		case diffmatchpatch.DiffInsert:
			if n := len(out); n > 0 && out[n-1].End == pos {
				out[n-1].Insert += d.Text
			} else {
				out = append(out, Edit{Start: pos, End: pos, Insert: d.Text})
			}
		}
	}
	return out
}

// BlockInsert places content between blocks at pos, adding the blank
// lines that keep it a block of its own and keep what follows one.
func (f *File) BlockInsert(pos int, content string) Edit {
	c := strings.Trim(content, "\n")
	before, after := f.text[:pos], f.text[pos:]
	var b strings.Builder
	if before != "" {
		b.WriteString(strings.Repeat("\n", max(0, 2-trailingNewlines(before))))
	}
	b.WriteString(c)
	if after == "" {
		if strings.HasSuffix(f.text, "\n") || f.text == "" {
			b.WriteString("\n")
		}
	} else {
		leading := len(after) - len(strings.TrimLeft(after, "\n"))
		b.WriteString(strings.Repeat("\n", max(0, 2-leading)))
	}
	return Edit{Start: pos, End: pos, Insert: b.String()}
}

// BlockReplace is the text that replaces the block span [start, end)
// with content, which stands as blocks of its own: a blank line before
// it unless it starts the file, and one after it unless it ends the
// file, where the file keeps the ending it had. A span may be empty, a
// section with nothing under its heading. Content that is only newlines
// replaces the span with nothing.
func (f *File) BlockReplace(start, end int, content string) string {
	c := strings.Trim(content, "\n")
	if c == "" {
		return ""
	}
	before, old, after := f.text[:start], f.text[start:end], f.text[end:]
	var b strings.Builder
	if before != "" {
		b.WriteString(strings.Repeat("\n", max(0, 2-trailingNewlines(before))))
	}
	b.WriteString(c)
	if after == "" {
		b.WriteString(old[len(strings.TrimRight(old, "\n")):])
	} else {
		lead := len(after) - len(strings.TrimLeft(after, "\n"))
		b.WriteString(strings.Repeat("\n", max(trailingNewlines(old), 2-lead)))
	}
	return b.String()
}

func trailingNewlines(s string) int { return len(s) - len(strings.TrimRight(s, "\n")) }

// Hit is something a deletion would destroy.
type Hit struct {
	Kind string // comment, image, footnote
	ID   string
}

// span is a stretch of the visible text that a deletion must not touch
// unasked, and what it is.
type span struct {
	start, end int
	hit        Hit
}

var (
	imageRE    = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)|!\[[^\]\n]*\]\[[^\]\n]*\]`)
	footnoteRE = regexp.MustCompile(`\[\^[^\]\s]+\]`)
)

// guarded is every image and footnote reference or definition, found
// once per file.
func (f *File) guarded() []span {
	f.spansOnce.Do(func() {
		for _, kind := range []struct {
			name string
			re   *regexp.Regexp
		}{{"image", imageRE}, {"footnote", footnoteRE}} {
			for _, loc := range kind.re.FindAllStringIndex(f.text, -1) {
				f.spans = append(f.spans, span{loc[0], loc[1], Hit{Kind: kind.name, ID: f.text[loc[0]:loc[1]]}})
			}
		}
	})
	return f.spans
}

// Destroys lists what deleting the visible text [start, end) would take
// with it: a commented passage it overlaps, an image or a footnote
// reference or definition. A comment is counted even where only part of
// its passage goes, as the overwrite guard does for a Docs comment
// anchor; the probe found such a comment survives, but the person did
// not ask for their comment's text to change.
func (f *File) Destroys(start, end int) []Hit {
	if start >= end {
		return nil
	}
	var hits []Hit
	for _, c := range f.comments {
		if c.Start >= 0 && c.Start < c.End && start < c.End && end > c.Start {
			hits = append(hits, Hit{Kind: "comment", ID: c.Key})
		}
	}
	for _, s := range f.guarded() {
		if start < s.end && end > s.start {
			hits = append(hits, s.hit)
		}
	}
	return hits
}

// ErrMarkup is returned when content would add or remove comment markup.
var ErrMarkup = errors.New("comment markup")

// Apply makes edits to the visible text and returns the file it makes.
// Edits must not overlap. A comment marker is never deleted: it keeps
// its place against the text around it, by these rules —
//
//   - inside a replaced range, a begin marker moves before the new text
//     and an end marker after it, so the comment covers what replaced its
//     text;
//   - at the edges of a replaced range a marker stays outside it;
//   - at a pure insertion point the new text goes outside a comment that
//     begins or ends there, and inside one that spans it.
//
// The result is parsed again and must name the same markers and carry
// the same block, byte for byte; anything else is ErrMarkup.
func (f *File) Apply(edits []Edit) (*File, error) {
	edits = sortEdits(edits)
	for i, e := range edits {
		if e.Start < 0 || e.End < e.Start || e.End > len(f.text) {
			return nil, fmt.Errorf("edit %d: range [%d,%d) outside the text", i, e.Start, e.End)
		}
		if i > 0 && e.Start < edits[i-1].End {
			return nil, fmt.Errorf("edits overlap at %d", e.Start)
		}
		if ContainsMarkup(e.Insert) {
			return nil, fmt.Errorf("%w: the new text holds Docs' comment markup (a gdoc_comment marker or a gdoc_comments block)", ErrMarkup)
		}
	}
	var text strings.Builder
	last := 0
	for _, e := range edits {
		text.WriteString(f.text[last:e.Start])
		text.WriteString(e.Insert)
		last = e.End
	}
	text.WriteString(f.text[last:])

	moved := make([]marker, len(f.markers))
	for i, m := range f.markers {
		m.at = f.newPos(m, edits)
		moved[i] = m
	}
	sort.SliceStable(moved, func(i, j int) bool { return moved[i].at < moved[j].at })
	out := Parse(f.serialize(text.String(), moved))
	if err := f.sameMarkup(out); err != nil {
		return nil, err
	}
	return out, nil
}

// sortEdits orders edits by position. At one position the insertions
// come first, in the order given, then the replacement that starts
// there: what is inserted at a point goes before what is replaced from
// it, and several insertions at one point land in the order they came.
func sortEdits(edits []Edit) []Edit {
	edits = slices.Clone(edits)
	sort.SliceStable(edits, func(i, j int) bool {
		a, b := edits[i], edits[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Start == a.End && b.Start < b.End
	})
	return edits
}

// newPos is where a marker lands after the edits.
func (f *File) newPos(m marker, edits []Edit) int {
	shift := 0
	for _, e := range edits {
		grow := len(e.Insert) - (e.End - e.Start)
		switch {
		case e.End < m.at, e.Start < e.End && e.End == m.at:
			shift += grow
			continue
		case e.Start > m.at, e.Start == m.at && e.Start < e.End:
			return m.at + shift
		case e.Start == e.End: // a pure insertion at the marker
			if m.begin && !f.empty(m.key) {
				// After it, and after any insertion or replacement
				// that follows at the same point.
				shift += len(e.Insert)
				continue
			}
			return m.at + shift
		}
		// Strictly inside a replaced range.
		if m.begin {
			return e.Start + shift
		}
		return e.Start + shift + len(e.Insert)
	}
	return m.at + shift
}

// empty reports whether a comment's markers enclose nothing, so that an
// insertion there keeps them together.
func (f *File) empty(key string) bool {
	c := f.comment(key)
	return c != nil && c.Start >= 0 && c.Start == c.End
}

// serialize writes visible text with markers at their positions and the
// block after it, keeping the file's separation from the block.
func (f *File) serialize(text string, markers []marker) string {
	return serialize(text, markers, f.sep, f.block)
}

func serialize(text string, markers []marker, sep, block string) string {
	var b strings.Builder
	b.Grow(len(text) + len(block) + 64*len(markers))
	last := 0
	for _, m := range markers {
		b.WriteString(text[last:m.at])
		b.WriteString(m.raw)
		last = m.at
	}
	b.WriteString(text[last:])
	if block == "" {
		return b.String()
	}
	return strings.TrimRight(b.String(), "\n") + sep + block
}

// sameMarkup is the check every write ends with: the new bytes carry the
// markers and the block the old ones did.
func (f *File) sameMarkup(g *File) error {
	count := func(ms []marker) map[string]int {
		c := map[string]int{}
		for _, m := range ms {
			c[m.raw]++
		}
		return c
	}
	if g.block != f.block {
		return fmt.Errorf("%w: the gdoc_comments block would not survive the edit as the end of the file", ErrMarkup)
	}
	a, b := count(f.markers), count(g.markers)
	for k, n := range a {
		if b[k] != n {
			return fmt.Errorf("%w: a comment marker would be lost", ErrMarkup)
		}
	}
	for k, n := range b {
		if a[k] != n {
			return fmt.Errorf("%w: the edit would add a comment marker", ErrMarkup)
		}
	}
	for _, c := range f.comments {
		if c.Start >= 0 {
			if s, _ := g.span(c.Key); s < 0 {
				return fmt.Errorf("%w: comment %s would lose its passage", ErrMarkup, c.Key)
			}
		}
	}
	return nil
}
