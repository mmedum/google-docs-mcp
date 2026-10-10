// Package markdown turns markdown written by the model into a neutral
// fragment: headings, paragraphs, list items, code lines and tables with
// styled inline runs. It refuses, loudly, anything the Docs API cannot
// express so nothing degrades silently.
package markdown

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// BlockKind is a fragment block type.
type BlockKind string

// Block kinds.
const (
	KindParagraph BlockKind = "paragraph"
	KindHeading   BlockKind = "heading"
	KindListItem  BlockKind = "list_item"
	KindCode      BlockKind = "code"
	KindTable     BlockKind = "table"
	// KindImage is an image that is a paragraph by itself. An image
	// inside a sentence is refused: content places it in a paragraph of
	// its own, and insert_object puts one inline.
	KindImage BlockKind = "image"
)

// Inline is a run of text with one formatting.
type Inline struct {
	Text   string
	Bold   bool
	Italic bool
	Strike bool
	Code   bool
	Link   string
}

// Style reports the formatting without the text, for merging runs.
func (i Inline) Style() Inline { i.Text = ""; return i }

// Block is one paragraph-level element.
type Block struct {
	Kind    BlockKind
	Level   int // heading level 1-6
	Ordered bool
	Nesting int // list nesting, 0-based
	ListID  int // groups consecutive items of one list
	Inlines []Inline
	Lines   []string // code block lines
	Table   *Table
	Image   *Image
	Line    int // 1-based source line, for errors
}

// Image is a block image: its address and its alt text.
type Image struct {
	URL string
	Alt string
}

// Text returns the block's plain text.
func (b *Block) Text() string {
	switch b.Kind {
	case KindCode:
		return strings.Join(b.Lines, "\n")
	case KindTable:
		var rows []string
		for _, row := range b.Table.Rows {
			var cells []string
			for _, c := range row {
				cells = append(cells, inlineText(c))
			}
			rows = append(rows, strings.Join(cells, "\t"))
		}
		return strings.Join(rows, "\n")
	case KindImage:
		if b.Image.Alt != "" {
			return "[image: " + b.Image.Alt + " (" + b.Image.URL + ")]"
		}
		return "[image: " + b.Image.URL + "]"
	}
	return inlineText(b.Inlines)
}

// Table is a GFM table; the first row is the header.
type Table struct {
	Rows [][][]Inline
}

// Grid is the table's cells as plain text, with its size. GFM gives
// every row the header's width.
func (t *Table) Grid() (cells [][]string, rows, cols int) {
	cells = make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		for _, c := range row {
			cells[i] = append(cells[i], inlineText(c))
		}
	}
	return cells, len(t.Rows), len(t.Rows[0])
}

// Fragment is parsed markdown.
type Fragment struct {
	Blocks []*Block
}

// PlainText joins the blocks' text with newlines.
func (f *Fragment) PlainText() string {
	parts := make([]string, 0, len(f.Blocks))
	for _, b := range f.Blocks {
		parts = append(parts, b.Text())
	}
	return strings.Join(parts, "\n")
}

// Embeds counts the tables and images, which content places in rounds
// after its text.
func (f *Fragment) Embeds() int {
	n := 0
	for _, b := range f.Blocks {
		if b.Kind == KindTable || b.Kind == KindImage {
			n++
		}
	}
	return n
}

// SingleParagraph reports whether the fragment is one plain paragraph
// with no formatting, and returns its text. Such fragments can be
// applied as a minimal diff.
func (f *Fragment) SingleParagraph() (string, bool) {
	if len(f.Blocks) != 1 || f.Blocks[0].Kind != KindParagraph {
		return "", false
	}
	for _, in := range f.Blocks[0].Inlines {
		if in.Style() != (Inline{}) {
			return "", false
		}
	}
	return f.Blocks[0].Text(), true
}

// Plain wraps verbatim text as a fragment: one paragraph per line, no
// formatting.
func Plain(text string) *Fragment {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	f := &Fragment{Blocks: make([]*Block, 0, len(lines))}
	for i, line := range lines {
		b := &Block{Kind: KindParagraph, Line: i + 1}
		if line != "" {
			b.Inlines = []Inline{{Text: line}}
		}
		f.Blocks = append(f.Blocks, b)
	}
	return f
}

// UnsupportedError names a construct the Docs API cannot take.
type UnsupportedError struct {
	Construct string
	Line      int
	Hint      string
}

func (e *UnsupportedError) Error() string {
	msg := fmt.Sprintf("unsupported markdown: %s at line %d", e.Construct, e.Line)
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}

var parser = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList))

// Parse converts markdown to a fragment. Plain text is valid markdown,
// so any string parses; only constructs without a Docs equivalent fail.
func Parse(src string) (*Fragment, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	b := []byte(src)
	root := parser.Parser().Parse(text.NewReader(b))
	p := &fragParser{src: b}
	if err := p.blocks(root, 0, 0); err != nil {
		return nil, err
	}
	return &Fragment{Blocks: p.out}, nil
}

type fragParser struct {
	src     []byte
	out     []*Block
	listID  int
	ordered []bool
}

func (p *fragParser) lineOf(n ast.Node) int {
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.Type() != ast.TypeBlock {
			continue
		}
		if cur.Lines() != nil && cur.Lines().Len() > 0 {
			return 1 + strings.Count(string(p.src[:cur.Lines().At(0).Start]), "\n")
		}
		// Blocks without recorded lines (thematic breaks) start on the
		// first non-blank line after the previous sibling.
		if prev := cur.PreviousSibling(); prev != nil && prev.Lines() != nil && prev.Lines().Len() > 0 {
			end := prev.Lines().At(prev.Lines().Len() - 1).Stop
			line := 1 + strings.Count(string(p.src[:end]), "\n")
			for _, l := range strings.Split(string(p.src[end:]), "\n") {
				if strings.TrimSpace(l) != "" {
					return line
				}
				line++
			}
			return line
		}
	}
	return 1
}

func (p *fragParser) blocks(parent ast.Node, nesting int, listID int) error {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		switch v := n.(type) {
		case *ast.Heading:
			in, err := p.inlines(v)
			if err != nil {
				return err
			}
			p.out = append(p.out, &Block{Kind: KindHeading, Level: v.Level, Inlines: lineBreaks(in), Line: p.lineOf(v)})
		case *ast.Paragraph, *ast.TextBlock:
			if imgs, ok := lineImages(v, p.src); ok {
				line := p.lineOf(v)
				if listID != 0 {
					return &UnsupportedError{Construct: "image in a list item", Line: line, Hint: "an image in content goes on a line of its own, outside a list"}
				}
				for i, img := range imgs {
					b, err := p.image(img, line+i)
					if err != nil {
						return err
					}
					p.out = append(p.out, b)
				}
				continue
			}
			in, err := p.inlines(v)
			if err != nil {
				return err
			}
			if listID > 0 {
				p.out = append(p.out, &Block{Kind: KindListItem, Nesting: nesting - 1, ListID: listID, Ordered: p.orderedOf(listID), Inlines: lineBreaks(in), Line: p.lineOf(v)})
				continue
			}
			for _, part := range splitBreaks(in) {
				p.out = append(p.out, &Block{Kind: KindParagraph, Inlines: part, Line: p.lineOf(v)})
			}
		case *ast.List:
			id := listID
			if nesting == 0 {
				p.listID++
				id = p.listID
				p.ordered = append(p.ordered, v.IsOrdered())
			}
			for item := v.FirstChild(); item != nil; item = item.NextSibling() {
				if err := p.blocks(item, nesting+1, id); err != nil {
					return err
				}
			}
		case *ast.FencedCodeBlock:
			p.out = append(p.out, &Block{Kind: KindCode, Lines: p.codeLines(v), Line: p.lineOf(v)})
		case *ast.CodeBlock:
			p.out = append(p.out, &Block{Kind: KindCode, Lines: p.codeLines(v), Line: p.lineOf(v)})
		case *extast.Table:
			t, err := p.table(v)
			if err != nil {
				return err
			}
			p.out = append(p.out, &Block{Kind: KindTable, Table: t, Line: p.lineOf(v)})
		case *ast.ThematicBreak:
			return &UnsupportedError{Construct: "horizontal rule (---)", Line: p.lineOf(v), Hint: "the Docs API cannot insert one; use a paragraph or a page break"}
		case *ast.Blockquote:
			return &UnsupportedError{Construct: "block quote (>)", Line: p.lineOf(v), Hint: "write it as a paragraph and indent it with format_document"}
		case *ast.HTMLBlock:
			return &UnsupportedError{Construct: "HTML block", Line: p.lineOf(v), Hint: "use markdown or plain text"}
		default:
			return &UnsupportedError{Construct: string(n.Kind().String()), Line: p.lineOf(n)}
		}
	}
	return nil
}

// ordered tracks, per top-level list id, whether it is numbered. Nested
// lists inside an ordered list stay numbered, matching one Docs list.
func (p *fragParser) orderedOf(id int) bool { return id > 0 && id <= len(p.ordered) && p.ordered[id-1] }

func (p *fragParser) codeLines(n ast.Node) []string {
	var lines []string
	segs := n.Lines()
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		lines = append(lines, strings.TrimRight(string(s.Value(p.src)), "\n"))
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// lineImages returns the images of a paragraph that holds nothing but
// images, one to a line: each becomes a block of its own.
func lineImages(n ast.Node, src []byte) ([]*ast.Image, bool) {
	var imgs []*ast.Image
	open := true // the next image starts a line
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Image:
			if !open {
				return nil, false
			}
			imgs, open = append(imgs, v), false
		case *ast.Text:
			// Between two images on their own lines goldmark puts an
			// empty text that carries the line break.
			if open || strings.TrimSpace(string(v.Segment.Value(src))) != "" || !v.SoftLineBreak() && !v.HardLineBreak() {
				return nil, false
			}
			open = true
		default:
			return nil, false
		}
	}
	return imgs, len(imgs) > 0 && !open
}

// image turns a block image on the given line into a Block. Google
// fetches the image itself, so only a web address can work.
func (p *fragParser) image(v *ast.Image, line int) (*Block, error) {
	u, err := url.Parse(plainText(v.Destination))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, &UnsupportedError{Construct: "image without a web address", Line: line,
			Hint: "an image needs an http or https address Google can fetch"}
	}
	var alt strings.Builder
	for c := v.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			alt.WriteString(plainText(t.Segment.Value(p.src)))
		}
	}
	return &Block{Kind: KindImage, Image: &Image{URL: u.String(), Alt: alt.String()}, Line: line}, nil
}

func (p *fragParser) table(t *extast.Table) (*Table, error) {
	out := &Table{}
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		var cells [][]Inline
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			in, err := p.inlines(cell)
			if err != nil {
				return nil, err
			}
			cells = append(cells, in)
		}
		out.Rows = append(out.Rows, cells)
	}
	if len(out.Rows) == 0 {
		return nil, &UnsupportedError{Construct: "empty table", Line: p.lineOf(t)}
	}
	return out, nil
}

func (p *fragParser) inlines(parent ast.Node) ([]Inline, error) {
	var out []Inline
	var walk func(n ast.Node, style Inline) error
	walk = func(n ast.Node, style Inline) error {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			s := style
			switch v := c.(type) {
			case *ast.Text:
				// A code span keeps its backslashes and ampersands, and a
				// line break inside one is a space.
				txt := strings.ReplaceAll(string(v.Segment.Value(p.src)), "\n", " ")
				if !s.Code {
					txt = plainText(v.Segment.Value(p.src))
				}
				if v.SoftLineBreak() {
					txt += " "
				} else if v.HardLineBreak() {
					txt += "\v"
				}
				out = appendInline(out, s, txt)
				continue
			case *ast.String:
				out = appendInline(out, s, string(v.Value))
				continue
			case *ast.Emphasis:
				if v.Level >= 2 {
					s.Bold = true
				} else {
					s.Italic = true
				}
			case *extast.Strikethrough:
				s.Strike = true
			case *ast.CodeSpan:
				s.Code = true
			case *ast.Link:
				s.Link = plainText(v.Destination)
			case *ast.AutoLink:
				u := string(v.URL(p.src))
				s.Link = u
				out = appendInline(out, s, string(v.Label(p.src)))
				continue
			case *ast.Image:
				return &UnsupportedError{Construct: "image inside text", Line: p.lineOf(c),
					Hint: "an image in content goes on a line of its own; to put one inside a sentence, use insert_object"}
			case *ast.RawHTML:
				if !isBreak(v, p.src) {
					return &UnsupportedError{Construct: "inline HTML", Line: p.lineOf(c)}
				}
				// A paragraph break, which the caller places: it is how a
				// read joins a table cell's paragraphs, and how GFM breaks
				// a line in a cell.
				out = appendInline(out, s, "\n")
				continue
			case *extast.TaskCheckBox:
				continue
			}
			if err := walk(c, s); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(parent, Inline{}); err != nil {
		return nil, err
	}
	// Trim the trailing soft-break space a paragraph ends with.
	if n := len(out); n > 0 {
		out[n-1].Text = strings.TrimRight(out[n-1].Text, " ")
		if out[n-1].Text == "" {
			out = out[:n-1]
		}
	}
	return out, nil
}

// plainText resolves what CommonMark resolves in text and addresses:
// backslash escapes, and entity and numeric references. goldmark keeps
// them in the source and resolves them as it writes HTML, so the text is
// written as goldmark writes it, and that HTML's escaping undone.
func plainText(src []byte) string {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	gmhtml.DefaultWriter.Write(w, src)
	_ = w.Flush()
	return html.UnescapeString(b.String())
}

// brTag is a <br> tag in any of its spellings.
var brTag = regexp.MustCompile(`(?i)^<br\s*/?>$`)

// isBreak reports whether inline HTML is a <br> tag.
func isBreak(v *ast.RawHTML, src []byte) bool {
	var tag strings.Builder
	for i := 0; i < v.Segments.Len(); i++ {
		s := v.Segments.At(i)
		tag.Write(s.Value(src))
	}
	return brTag.MatchString(tag.String())
}

// splitBreaks cuts a paragraph's inlines into one paragraph per <br>.
func splitBreaks(in []Inline) [][]Inline {
	parts := [][]Inline{nil}
	for _, x := range in {
		for i, piece := range strings.Split(x.Text, "\n") {
			if i > 0 {
				parts = append(parts, nil)
			}
			if piece != "" {
				x.Text = piece
				parts[len(parts)-1] = append(parts[len(parts)-1], x)
			}
		}
	}
	return parts
}

// lineBreaks turns each <br> into a line break, where a new paragraph
// would make a second heading or list item.
func lineBreaks(in []Inline) []Inline {
	for i := range in {
		in[i].Text = strings.ReplaceAll(in[i].Text, "\n", "\v")
	}
	return in
}

func appendInline(out []Inline, style Inline, txt string) []Inline {
	if txt == "" {
		return out
	}
	if n := len(out); n > 0 && out[n-1].Style() == style.Style() {
		out[n-1].Text += txt
		return out
	}
	style.Text = txt
	return append(out, style)
}

func inlineText(in []Inline) string {
	var b strings.Builder
	for _, i := range in {
		b.WriteString(i.Text)
	}
	return b.String()
}
