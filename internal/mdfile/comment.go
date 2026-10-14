package mdfile

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Writing a comment is the one change to the markup this package makes:
// markers around a passage and an entry in the block, in the shape Docs
// writes them, which the Docs editor shows as a comment "From imported
// document" and keeps, with a token, on a person's save (spike D). An
// entry carries no token; Google issues one on that save. A resolve is
// never written: a RESOLVED status in the bytes makes the Docs API fail
// on the whole file.

// NewPost is a comment or reply to write: who, when, what.
type NewPost struct {
	Author string
	// Time is RFC 3339 with milliseconds, as Docs writes it.
	Time string
	Body string
}

// ErrNoPassage is returned for a comment on nothing: Docs anchors a
// comment to text.
var ErrNoPassage = errors.New("a comment needs a passage of text")

// AddComment writes a comment on the visible text [start, end) and
// returns the file it makes, and the entry's key. The result is parsed
// again: it must hold every marker and entry it held, plus the new
// comment on exactly that passage.
func (f *File) AddComment(start, end int, p NewPost) (*File, string, error) {
	if start < 0 || end > len(f.text) || start >= end || strings.TrimSpace(f.text[start:end]) == "" {
		return nil, "", ErrNoPassage
	}
	if ContainsMarkup(p.Body) || ContainsMarkup(p.Author) {
		return nil, "", fmt.Errorf("%w: the comment holds Docs' comment markup", ErrMarkup)
	}
	key := f.freeKey()
	entry := strings.Join([]string{
		"  " + key + ":",
		"    status: ACTIVE",
		"    head:",
		"      author: " + yamlString(p.Author),
		"      time: '" + p.Time + "'",
		"      body: " + yamlString(p.Body),
		"      quote: " + yamlString(f.text[start:end]),
	}, "\n") + "\n"
	markers := append(append([]marker(nil), f.markers...),
		marker{key: key, begin: true, at: start, raw: "<!-- gdoc_comment_begin: " + key + " -->"},
		marker{key: key, at: end, raw: "<!-- gdoc_comment_end: " + key + " -->"})
	// At one position: the ends of comments closing there, then the new
	// comment's end, then its begin, then the begins of comments opening
	// there; so the new comment nests with no other.
	rank := func(m marker) int {
		switch {
		case m.key == key && !m.begin:
			return 1
		case m.key == key:
			return 2
		case m.begin:
			return 3
		}
		return 0
	}
	sort.SliceStable(markers, func(i, j int) bool {
		if markers[i].at != markers[j].at {
			return markers[i].at < markers[j].at
		}
		return rank(markers[i]) < rank(markers[j])
	})
	sep, block := f.sep, f.block
	if block == "" {
		sep, block = "\n\n", "---\ngdoc_comments:\n---\n"
	}
	closing := strings.LastIndex(block, "---")
	block = block[:closing] + entry + block[closing:]
	out := Parse(serialize(f.text, markers, sep, block))
	if err := f.addedComment(out, key, start, end); err != nil {
		return nil, "", err
	}
	return out, key, nil
}

// addedComment checks a file made by AddComment against the one it came
// from.
func (f *File) addedComment(g *File, key string, start, end int) error {
	// A file that gets its first block loses its final newline from the
	// visible text, which then ends where the block's separation begins.
	if strings.TrimRight(g.text, "\n") != strings.TrimRight(f.text, "\n") {
		return fmt.Errorf("%w: writing the comment changed the text", ErrMarkup)
	}
	if len(g.comments) != len(f.comments)+1 || len(g.markers) != len(f.markers)+2 {
		return fmt.Errorf("%w: the comment did not come out as one new entry", ErrMarkup)
	}
	if s, e := g.span(key); s != start || e != end {
		return fmt.Errorf("%w: the new comment is not on its passage", ErrMarkup)
	}
	for _, c := range f.comments {
		if s, e := g.span(c.Key); s != c.Start || e != c.End {
			return fmt.Errorf("%w: comment %s moved", ErrMarkup, c.Key)
		}
	}
	return nil
}

// freeKey is a key no entry or marker uses. Docs renumbers keys on its
// next save, so a key matters only within these bytes.
func (f *File) freeKey() string {
	used := map[string]bool{}
	for _, c := range f.comments {
		used[c.Key] = true
	}
	for n := len(f.comments) + 1; ; n++ {
		if k := "c" + strconv.Itoa(n); !used[k] {
			return k
		}
	}
}

// AddReply writes a reply at the end of an entry's replies and returns
// the file it makes. The text and the markers do not move.
func (f *File) AddReply(key string, p NewPost) (*File, error) {
	if f.comment(key) == nil || f.block == "" {
		return nil, fmt.Errorf("no comment %s in the file", key)
	}
	if ContainsMarkup(p.Body) || ContainsMarkup(p.Author) {
		return nil, fmt.Errorf("%w: the reply holds Docs' comment markup", ErrMarkup)
	}
	lines := strings.Split(f.block, "\n")
	at := -1
	for i, l := range lines {
		if l == "  "+key+":" {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("the block has no entry %s", key)
	}
	// The entry runs to the next line indented two or less, or the
	// closing rule; its replies key, if any, is at four.
	end, hasReplies := at+1, false
	for ; end < len(lines); end++ {
		l := lines[end]
		if l == "    replies:" {
			hasReplies = true
		}
		if l != "" && indentOf(l) <= 2 {
			break
		}
	}
	reply := []string{"    - author: " + yamlString(p.Author), "      time: '" + p.Time + "'", "      body: " + yamlString(p.Body)}
	if !hasReplies {
		reply = append([]string{"    replies:"}, reply...)
	}
	lines = append(lines[:end], append(reply, lines[end:]...)...)
	out := Parse(serialize(f.text, f.markers, f.sep, strings.Join(lines, "\n")))
	before, after := f.comment(key), out.comment(key)
	if out.text != f.text || after == nil || len(after.Replies) != len(before.Replies)+1 || len(out.comments) != len(f.comments) {
		return nil, fmt.Errorf("%w: the reply did not come out as one new reply on %s", ErrMarkup, key)
	}
	return out, nil
}

// yamlString is s as a YAML double-quoted scalar. Go's escapes (\n, \",
// \\, \t, \uXXXX, \xXX) are YAML's too.
func yamlString(s string) string { return strconv.Quote(s) }
