package mdfile

import (
	"strconv"
	"strings"
)

// The block is YAML that Docs writes:
//
//	---
//	gdoc_comments:
//	  c1:
//	    status: ACTIVE
//	    token: <opaque>
//	    head:
//	      author: <display name>
//	      time: '<RFC 3339 time>'
//	      body: the comment
//	      quote: |-
//	        the quoted passage
//	---
//
// This reads the subset needed to show an entry: nested mappings by
// indentation, plain, quoted and block scalars, and sequences (whose
// items it keeps as children named "-"). The block is never rewritten
// from what this reads; it is carried over byte for byte.

type yamlNode struct {
	key      string
	value    string
	children []*yamlNode
}

func (n *yamlNode) child(key string) *yamlNode {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.key == key {
			return c
		}
	}
	return nil
}

func (n *yamlNode) str(key string) string {
	if c := n.child(key); c != nil {
		return c.value
	}
	return ""
}

func parseBlock(block string) []Comment {
	if block == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	// Drop the rules around the mapping.
	if len(lines) < 2 {
		return nil
	}
	lines = lines[1 : len(lines)-1]
	p := &yamlParser{lines: lines}
	root := &yamlNode{children: p.mapping(0)}
	var out []Comment
	for _, e := range root.child("gdoc_comments").childrenOrNil() {
		head := e.child("head")
		c := Comment{
			Key: e.key, Status: e.str("status"), Token: e.str("token") != "",
			Author: head.str("author"), Time: head.str("time"), Body: head.str("body"), Quote: head.str("quote"),
		}
		for _, r := range e.child("replies").childrenOrNil() {
			c.Replies = append(c.Replies, Reply{Author: r.str("author"), Time: r.str("time"), Body: r.str("body"), Action: r.str("action")})
		}
		out = append(out, c)
	}
	return out
}

func (n *yamlNode) childrenOrNil() []*yamlNode {
	if n == nil {
		return nil
	}
	return n.children
}

type yamlParser struct {
	lines []string
	i     int
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func blank(l string) bool { return strings.TrimSpace(l) == "" }

// mapping reads the entries at indentation min or deeper, stopping at
// the first line indented less than the first entry.
func (p *yamlParser) mapping(min int) []*yamlNode {
	var out []*yamlNode
	at := -1
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if blank(l) {
			p.i++
			continue
		}
		ind := indentOf(l)
		if ind < min || (at >= 0 && ind < at) {
			return out
		}
		if at < 0 {
			at = ind
		}
		if ind > at {
			// A stray deeper line belongs to nothing this reads.
			p.i++
			continue
		}
		rest := l[ind:]
		if strings.HasPrefix(rest, "- ") || rest == "-" {
			out = append(out, p.item(ind))
			continue
		}
		key, value, ok := strings.Cut(rest, ":")
		if !ok {
			p.i++
			continue
		}
		p.i++
		n := &yamlNode{key: strings.TrimSpace(key)}
		if value = strings.TrimSpace(value); value == "" {
			// A sequence may sit at its key's own indentation, which is
			// how Docs writes replies.
			if p.itemAt(ind) {
				n.children = p.sequence(ind)
			} else {
				n.children = p.mapping(ind + 1)
			}
		} else {
			n.value = p.scalar(value, ind)
		}
		out = append(out, n)
	}
	return out
}

// itemAt reports whether the next line with text is a sequence item at
// indentation ind.
func (p *yamlParser) itemAt(ind int) bool {
	for i := p.i; i < len(p.lines); i++ {
		if blank(p.lines[i]) {
			continue
		}
		rest := p.lines[i][min(indentOf(p.lines[i]), len(p.lines[i])):]
		return indentOf(p.lines[i]) == ind && (strings.HasPrefix(rest, "- ") || rest == "-")
	}
	return false
}

// sequence reads the items at indentation ind.
func (p *yamlParser) sequence(ind int) []*yamlNode {
	var out []*yamlNode
	for p.itemAt(ind) {
		for blank(p.lines[p.i]) {
			p.i++
		}
		out = append(out, p.item(ind))
	}
	return out
}

// item reads one sequence item: the text after the dash is read as if
// the dash were spaces.
func (p *yamlParser) item(ind int) *yamlNode {
	rest := strings.TrimPrefix(p.lines[p.i][ind:], "-")
	if blank(rest) {
		p.i++
	} else {
		p.lines[p.i] = strings.Repeat(" ", ind+2) + strings.TrimPrefix(rest, " ")
	}
	return &yamlNode{key: "-", children: p.mapping(ind + 1)}
}

// scalar reads the value that starts on the current key's line, with
// whatever continuation lines it takes.
func (p *yamlParser) scalar(first string, ind int) string {
	switch first[0] {
	case '|', '>':
		return p.blockScalar(first[0] == '>', ind)
	case '\'', '"':
		return p.quoted(first, ind)
	}
	parts := []string{first}
	for p.i < len(p.lines) && !blank(p.lines[p.i]) && indentOf(p.lines[p.i]) > ind {
		parts = append(parts, strings.TrimSpace(p.lines[p.i]))
		p.i++
	}
	return strings.Join(parts, " ")
}

func (p *yamlParser) blockScalar(folded bool, ind int) string {
	var lines []string
	at := -1
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if !blank(l) && indentOf(l) <= ind {
			break
		}
		if !blank(l) && at < 0 {
			at = indentOf(l)
		}
		if blank(l) || at < 0 {
			lines = append(lines, "")
		} else {
			lines = append(lines, l[min(at, indentOf(l)):])
		}
		p.i++
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if folded {
		return foldLines(lines)
	}
	return strings.Join(lines, "\n")
}

// quoted reads a quoted scalar, joining the lines it spans the way YAML
// folds them: a line break is a space, an empty line a newline.
func (p *yamlParser) quoted(first string, ind int) string {
	q := first[0]
	lines := []string{first}
	for !closed(strings.Join(lines, "\n"), q) && p.i < len(p.lines) && (blank(p.lines[p.i]) || indentOf(p.lines[p.i]) > ind) {
		lines = append(lines, strings.TrimSpace(p.lines[p.i]))
		p.i++
	}
	s := foldLines(lines)
	if q == '\'' {
		s = strings.TrimPrefix(s, "'")
		s = strings.TrimSuffix(s, "'")
		return strings.ReplaceAll(s, "''", "'")
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return strings.Trim(s, `"`)
}

// closed reports whether a quoted scalar has its closing quote.
func closed(s string, q byte) bool {
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q && q == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == q:
			return true
		}
	}
	return false
}

func foldLines(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		switch {
		case i == 0:
		case l == "":
			b.WriteByte('\n')
			continue
		case i > 0 && lines[i-1] != "":
			b.WriteByte(' ')
		}
		b.WriteString(l)
	}
	return b.String()
}
