// Package mdfile works on a Markdown file stored in Drive as text. Google
// Docs opens such a file in place and keeps the comments made in its
// editor inside the bytes: a marker on each side of the commented
// passage, and a gdoc_comments block that must end the file (spike D,
// architecture §18). The package shows the text without that markup,
// finds exact text in it, plans edits that never delete, split or move
// the markup out of order, and checks that the bytes it writes carry
// the same comments as the bytes it read.
//
// Positions are byte offsets into the visible text, the file with its
// markers and its block taken out. The model never sees them.
package mdfile

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// marker is one comment marker Docs wrote beside a commented passage.
type marker struct {
	key   string
	begin bool
	// at is the marker's position in the visible text.
	at  int
	raw string
}

// Comment is one entry of the gdoc_comments block, with the passage its
// markers enclose.
type Comment struct {
	Key    string
	Status string
	// Token says the entry carries the token that ties it to a thread
	// Google stores. An entry without one exists only in these bytes,
	// and its author is whatever they say.
	Token  bool
	Author string
	Time   string
	Body   string
	Quote  string
	// Replies are the entry's replies, in order; a resolve is a reply
	// whose Action is RESOLVE.
	Replies []Reply
	// Start and End are the passage in the visible text, -1 when a
	// marker is missing.
	Start, End int
}

// Reply is one reply in an entry of the block.
type Reply struct {
	Author, Time, Body, Action string
}

// File is a Markdown file as Docs stores it. The zero value is not
// usable; build one with Parse. What a call derives from it — the search
// index, the headings, the line table, the image and footnote spans —
// is built once, on first use.
type File struct {
	src     string
	text    string
	markers []marker
	// sep is the whitespace between the visible text and the block, and
	// block the block from its opening rule to the end of the file. Both
	// are empty when the file has no block.
	sep      string
	block    string
	comments []Comment
	fm       FrontMatter

	idxOnce   sync.Once
	idx       *index
	headOnce  sync.Once
	heads     []Heading
	linesOnce sync.Once
	lines     []int // where each line starts
	spansOnce sync.Once
	spans     []span // images and footnote references
}

// FrontMatter describes YAML front matter at the top of the file, or
// the shape the Docs editor leaves when it rewrites front matter on a
// person's save: a rule, then a heading holding the keys.
type FrontMatter struct {
	Present bool
	// End is where the front matter ends in the visible text.
	End int
	// Mangled says the file opens the way Docs rewrites front matter.
	Mangled bool
}

var markerRE = regexp.MustCompile(`<!-- gdoc_comment_(begin|end): ([A-Za-z0-9_.:-]+) -->`)

// blockOpen opens the block; Docs writes it after a blank line.
const blockOpen = "---\ngdoc_comments:"

// Parse reads a file. It never fails: markup that is not what Docs
// writes is text, which is also how Docs reads it (a block with text
// after it shows as body text).
func Parse(src string) *File {
	f := &File{src: src}
	body := src
	if start, ok := blockStart(src); ok {
		body = src[:start]
		f.block = src[start:]
		trimmed := strings.TrimRight(body, "\n")
		f.sep = body[len(trimmed):]
		body = trimmed
	}
	var b strings.Builder
	b.Grow(len(body))
	last := 0
	for _, m := range markerRE.FindAllStringSubmatchIndex(body, -1) {
		b.WriteString(body[last:m[0]])
		f.markers = append(f.markers, marker{key: body[m[4]:m[5]], begin: body[m[2]:m[3]] == "begin", at: b.Len(), raw: body[m[0]:m[1]]})
		last = m[1]
	}
	b.WriteString(body[last:])
	f.text = b.String()
	f.comments = parseBlock(f.block)
	for i := range f.comments {
		c := &f.comments[i]
		c.Start, c.End = f.span(c.Key)
	}
	// A pair of markers whose key the block does not name is still
	// Docs' markup and is protected the same way.
	for _, m := range f.markers {
		if f.comment(m.key) == nil {
			s, e := f.span(m.key)
			f.comments = append(f.comments, Comment{Key: m.key, Start: s, End: e})
		}
	}
	f.fm = frontMatter(f.text)
	return f
}

// blockStart finds the block that ends src: an opening rule and the
// gdoc_comments key at the start of a line, indented lines after it,
// and a closing rule followed by nothing but newlines.
func blockStart(src string) (int, bool) {
	start := strings.LastIndex(src, "\n"+blockOpen)
	switch {
	case start >= 0:
		start++
	case strings.HasPrefix(src, blockOpen):
		start = 0
	default:
		return 0, false
	}
	lines := strings.Split(strings.TrimRight(src[start+len(blockOpen):], "\n"), "\n")
	if len(lines) < 2 || lines[0] != "" || lines[len(lines)-1] != "---" {
		return 0, false
	}
	for _, l := range lines[1 : len(lines)-1] {
		if l != "" && !strings.HasPrefix(l, " ") {
			return 0, false
		}
	}
	return start, true
}

// span is the visible passage between a key's markers.
func (f *File) span(key string) (int, int) {
	s, e := -1, -1
	for _, m := range f.markers {
		if m.key != key {
			continue
		}
		if m.begin && s < 0 {
			s = m.at
		}
		if !m.begin {
			e = m.at
		}
	}
	if s < 0 || e < 0 || e < s {
		return -1, -1
	}
	return s, e
}

func (f *File) comment(key string) *Comment {
	for i := range f.comments {
		if f.comments[i].Key == key {
			return &f.comments[i]
		}
	}
	return nil
}

// frontMatterRE is YAML front matter: a rule, a first line that is a
// key, and a closing rule. A rule followed by a blank line is a
// thematic break, not front matter.
var frontMatterRE = regexp.MustCompile(`\A---[ \t]*\n[A-Za-z0-9_"'-][^\n]*:[^\n]*\n(?:[^\n]*\n)*?(?:---|\.\.\.)[ \t]*(?:\n|\z)`)

// mangledRE is what the Docs editor leaves of front matter on a save: a
// rule, a blank line, and a second-level heading holding "key: value".
var mangledRE = regexp.MustCompile(`\A---\n\n## [A-Za-z0-9_-]+: `)

func frontMatter(text string) FrontMatter {
	if mangledRE.MatchString(text) {
		return FrontMatter{Mangled: true}
	}
	if loc := frontMatterRE.FindStringIndex(text); loc != nil {
		return FrontMatter{Present: true, End: loc[1]}
	}
	return FrontMatter{}
}

// Source is the file's bytes.
func (f *File) Source() string { return f.src }

// Text is the visible text: the file without Docs' comment markup.
func (f *File) Text() string { return f.text }

// Comments lists the block's entries, then any marker pair the block
// does not name, in the order the file holds them.
func (f *File) Comments() []Comment { return append([]Comment(nil), f.comments...) }

// Passage is the visible text a comment encloses, or "".
func (f *File) Passage(key string) string {
	if c := f.comment(key); c != nil && c.Start >= 0 {
		return f.text[c.Start:c.End]
	}
	return ""
}

// FrontMatter reports front matter, or its rewritten shape.
func (f *File) FrontMatter() FrontMatter { return f.fm }

// WithMarks is the visible text [start, end) with label(key) written
// where each comment's passage ends, for a read that shows comments.
func (f *File) WithMarks(start, end int, label func(key string) string) string {
	var b strings.Builder
	last := start
	for _, m := range f.markers {
		if m.begin || m.at < start || m.at > end {
			continue
		}
		b.WriteString(f.text[last:m.at])
		b.WriteString(label(m.key))
		last = m.at
	}
	b.WriteString(f.text[last:end])
	return b.String()
}

// Lines counts lines the way an editor numbers them: a final newline
// ends the last line rather than starting another.
func Lines(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// LineOf is the line, from 1, that holds a position of the visible text.
func (f *File) LineOf(pos int) int {
	ls := f.lineStarts()
	return sort.Search(len(ls), func(i int) bool { return ls[i] > pos })
}

// LineStart is where a line of the visible text starts, lines counted
// from 1; past the last line it is the end of the text.
func (f *File) LineStart(line int) int {
	ls := f.lineStarts()
	switch {
	case line < 1:
		return 0
	case line > len(ls):
		return len(f.text)
	}
	return ls[line-1]
}

func (f *File) lineStarts() []int {
	f.linesOnce.Do(func() {
		f.lines = append(make([]int, 0, strings.Count(f.text, "\n")+1), 0)
		for i := 0; i < len(f.text); i++ {
			if f.text[i] == '\n' {
				f.lines = append(f.lines, i+1)
			}
		}
	})
	return f.lines
}

var blockKeyRE = regexp.MustCompile(`(?m)^gdoc_comments:`)

// ContainsMarkup reports whether text holds what Docs would read as its
// comment markup, which content written into a file must not: a marker,
// or the key that opens the block at the start of a line.
func ContainsMarkup(text string) bool {
	return markerRE.MatchString(text) || blockKeyRE.MatchString(text)
}

// String summarizes the file for logs and test failures: sizes only.
func (f *File) String() string {
	return fmt.Sprintf("mdfile(%d bytes, %d visible, %d markers, %d comments)", len(f.src), len(f.text), len(f.markers), len(f.comments))
}
