package mdfile

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
)

// Span is a range of the visible text.
type Span struct{ Start, End int }

// unit is one character of the searchable text and the bytes of the
// visible text it stands for. A whitespace run is one space unit.
type unit struct {
	start, end int
}

// index is the visible text normalized the way doc.Normalize normalizes
// a needle, one rune per unit, so a match maps back to a span. It is
// service/textindex.go's segText for bytes instead of a Docs segment.
type index struct {
	text   string // normalized runes
	units  []unit
	textAt []int // where each unit starts in text

	foldOnce sync.Once
	fold     string // text lower-cased rune by rune, still one rune per unit
	foldAt   []int
}

func buildIndex(s string) *index {
	x := &index{units: make([]unit, 0, len(s)), textAt: make([]int, 0, len(s))}
	var tb strings.Builder
	tb.Grow(len(s))
	space := -1 // the open whitespace unit, if any
	for i, r := range s {
		n := utf8.RuneLen(r)
		if r == utf8.RuneError {
			n = 1
		}
		nr, keep := doc.NormalizeRune(r)
		switch {
		case !keep:
			continue
		case nr == ' ' && space >= 0:
			x.units[space].end = i + n
			continue
		}
		if nr == ' ' {
			space = len(x.units)
		} else {
			space = -1
		}
		x.textAt = append(x.textAt, tb.Len())
		x.units = append(x.units, unit{start: i, end: i + n})
		tb.WriteRune(nr)
	}
	x.text = tb.String()
	return x
}

// buildFold lower-cases rune by rune, so the fold keeps one rune per
// unit even where the byte length changes.
func (x *index) buildFold() {
	var fb strings.Builder
	fb.Grow(len(x.text))
	x.foldAt = make([]int, 0, len(x.units))
	for _, r := range x.text {
		x.foldAt = append(x.foldAt, fb.Len())
		fb.WriteRune(unicode.ToLower(r))
	}
	x.fold = fb.String()
}

// Find lists every non-overlapping occurrence of needle in the visible
// text, normalized as targets are (curly quotes, dashes, whitespace
// runs, zero-width characters), within a span; a zero span is the whole
// text. Matching is exact first and case-insensitive when nothing
// matches exactly, as a target in a Google Doc is.
func (f *File) Find(needle string, within Span) []Span {
	if within == (Span{}) {
		within = Span{0, len(f.text)}
	}
	if hits := f.find(needle, false, within); len(hits) > 0 {
		return hits
	}
	return f.find(needle, true, within)
}

// FindAll lists every non-overlapping occurrence of needle, normalized
// as Find normalizes it, case-insensitively unless matchCase.
func (f *File) FindAll(needle string, matchCase bool) []Span {
	return f.find(needle, !matchCase, Span{0, len(f.text)})
}

func (f *File) find(needle string, fold bool, within Span) []Span {
	needle = doc.Normalize(needle)
	if needle == "" {
		return nil
	}
	f.idxOnce.Do(func() { f.idx = buildIndex(f.text) })
	x := f.idx
	hay, at := x.text, x.textAt
	if fold {
		x.foldOnce.Do(x.buildFold)
		hay, at, needle = x.fold, x.foldAt, strings.Map(unicode.ToLower, needle)
	}
	n := utf8.RuneCountInString(needle)
	var out []Span
	for off := 0; off <= len(hay); {
		i := strings.Index(hay[off:], needle)
		if i < 0 {
			break
		}
		first := sort.SearchInts(at, off+i)
		off += i + len(needle)
		last := first + n - 1
		if last >= len(x.units) {
			break
		}
		s := Span{x.units[first].start, x.units[last].end}
		if s.Start >= within.Start && s.End <= within.End {
			out = append(out, s)
		}
	}
	return out
}
