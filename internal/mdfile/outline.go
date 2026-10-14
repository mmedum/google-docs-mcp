package mdfile

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Heading is a heading of the visible text and the section it opens.
type Heading struct {
	Level int
	// Text is the heading's markdown as written, without the hashes.
	Text string
	// ID is the heading's slug, the way GitHub names a heading's anchor,
	// with -1, -2 … on repeats. It lasts as long as the heading's text.
	ID string
	// Start is where the heading's line starts; End is where its section
	// ends: at the next heading of the same or a higher level, or at the
	// end of the text.
	Start, End int
	// BodyStart is where the section's text under the heading starts:
	// after the heading's lines (a setext heading's underline included)
	// and the blank lines after them.
	BodyStart int
	// Line is the heading's line, from 1.
	Line int
}

// atxRE is a line that opens an ATX heading; any other heading is a
// setext one, underlined on the line after its text.
var atxRE = regexp.MustCompile(`\A {0,3}#{1,6}(?:[ \t]|\n|\z)`)

var outlineParser = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList, extension.Footnote))

// Headings lists the headings in order, shared: the caller must not
// change them. Front matter is not parsed for them: a heading parser
// reads its closing rule as an underline.
func (f *File) Headings() []Heading {
	f.headOnce.Do(func() { f.heads = f.headings() })
	return f.heads
}

func (f *File) headings() []Heading {
	from := f.fm.End
	src := f.text
	body := []byte(src[from:])
	root := outlineParser.Parser().Parse(text.NewReader(body))
	var out []Heading
	seen := map[string]bool{}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Lines().Len() == 0 {
			continue
		}
		lines := h.Lines()
		start := strings.LastIndexByte(src[:from+lines.At(0).Start], '\n') + 1
		parts := make([]string, 0, lines.Len())
		for i := range lines.Len() {
			seg := lines.At(i)
			parts = append(parts, strings.TrimSpace(string(seg.Value(body))))
		}
		// The heading's lines end with the last content line, and a
		// setext heading's underline is the line after it.
		bodyStart := lineEnd(src, from+lines.At(lines.Len()-1).Start)
		if !atxRE.MatchString(src[start:]) {
			bodyStart = lineEnd(src, bodyStart)
		}
		for bodyStart < len(src) && src[bodyStart] == '\n' {
			bodyStart++
		}
		t := strings.Join(parts, " ")
		id := uniqueSlug(slug(t), seen)
		out = append(out, Heading{Level: h.Level, Text: t, ID: id, Start: start, BodyStart: bodyStart, Line: f.LineOf(start)})
	}
	for i := range out {
		out[i].End = len(src)
		for _, next := range out[i+1:] {
			if next.Level <= out[i].Level {
				out[i].End = next.Start
				break
			}
		}
		out[i].BodyStart = min(out[i].BodyStart, out[i].End)
	}
	return out
}

// lineEnd is where the line holding pos ends, after its newline.
func lineEnd(s string, pos int) int {
	if pos >= len(s) {
		return len(s)
	}
	if i := strings.IndexByte(s[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(s)
}

// uniqueSlug is base, or base-1, base-2 … when base is taken, skipping
// a suffix a literal heading already holds ("Foo", "Foo 1", "Foo" are
// foo, foo-1, foo-2), so no two headings share an id.
func uniqueSlug(base string, seen map[string]bool) string {
	id := base
	for k := 1; seen[id]; k++ {
		id = base + "-" + strconv.Itoa(k)
	}
	seen[id] = true
	return id
}

// slug names a heading the way GitHub does: lower case, markup and
// punctuation dropped, spaces as hyphens.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

// Heading finds a heading by its id.
func (f *File) Heading(id string) (Heading, bool) {
	for _, h := range f.Headings() {
		if h.ID == id {
			return h, true
		}
	}
	return Heading{}, false
}

// Cut is where a read of the visible text from start stops so that it
// holds at most chars characters and ends at a line's end, or mid-line,
// on a character, when one line is longer than that.
func (f *File) Cut(start, chars int) int {
	limit := start
	for n := 0; n < chars && limit < len(f.text); n++ {
		_, size := utf8.DecodeRuneInString(f.text[limit:])
		limit += size
	}
	if limit == len(f.text) {
		return limit
	}
	if i := strings.LastIndexByte(f.text[start:limit], '\n'); i >= 0 {
		return start + i + 1
	}
	return limit
}
