package ooxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
)

// object is the unit the Docs API counts for an element with no text of
// its own, an inline image or a footnote reference; its "aligned" text
// shows one as U+FFFC.
const object = rune(0xFFFC)

// span is where an element sits in document.xml: its start, the end of its
// start tag, and its end. A zero span is an element that is not there;
// one whose start tag ends it, <x/>, is empty.
type span struct{ start, open, end int }

func (s span) present() bool { return s.end > s.start }
func (s span) empty() bool   { return s.open == s.end }

// Para is one paragraph: where it sits in document.xml, its style and
// list membership, and its text.
type Para struct {
	el  span
	pPr span
	// pPrTail is where run properties for the paragraph's mark go in its
	// paragraph properties: before a section's or a change's, else last.
	pPrTail int
	// markRPr are the run properties of the paragraph's mark; markIns and
	// markDel say they mark it a tracked insertion or deletion.
	markRPr          span
	markIns, markDel bool
	// Style is the paragraph style id; NumID and Ilvl its list and level,
	// NumID "" when it is no list item.
	Style string
	NumID string
	Ilvl  int
	// section says the paragraph ends a section, which deleting it would
	// remove; last that it is the last of a table cell, which a cell may
	// not be without.
	section, last bool
	// cell is the start of the table cell holding it, or -1.
	cell int
	runs []*run
	// Text is the paragraph's text as the Docs API reads it with its
	// suggestions inline: deleted text included, a tab as \t, a line
	// break as \v, an inline image or a footnote reference as U+FFFC.
	Text string
	// units are the run each UTF-16 unit of Text comes from.
	units []int
	// Comments are the ids of the comments whose markers the paragraph
	// holds: where one starts or ends, or its reference.
	Comments []string
}

// run is one <w:r>.
type run struct {
	el   span
	rPr  string // its <w:rPr>…</w:rPr>, or ""
	kids []kid
	// u0 and u1 are the paragraph's units the run holds.
	u0, u1 int
	// in is the innermost element holding the run inside its paragraph,
	// a link, a tracked change, a field: nil when the paragraph holds it.
	in *wrap
	// field says the run holds part of a complex field's code.
	field bool
}

type wrap struct {
	name   string // hyperlink, ins, del, sdtContent, fldSimple…
	parent *wrap
}

// kid is one element of a run.
type kid struct {
	start, end int
	text       string // what it counts as: its text, or one unit, or ""
	n          int    // its UTF-16 units
	deleted    bool   // a w:delText
	isText     bool   // a w:t or w:delText, rewritten when cut
}

// containers are the elements inside a paragraph that hold runs.
// A content control, sdt, holds its properties, which hold no text, and
// its content, which holds runs.
var containers = map[string]bool{"hyperlink": true, "ins": true, "del": true, "moveFrom": true, "moveTo": true,
	"smartTag": true, "customXml": true, "sdt": true, "sdtContent": true, "fldSimple": true, "dir": true, "bdo": true}

type parser struct {
	d   *xml.Decoder
	doc []byte
	// cells are the starts of the table cells being read, innermost last.
	cells []int
	// comments are the comment markers of the paragraph being read.
	comments []string
}

// parse reads document.xml into f.Paras.
func (f *File) parse() error {
	p := &parser{d: xml.NewDecoder(bytes.NewReader(f.doc)), doc: f.doc}
	for {
		start := int(p.d.InputOffset())
		tok, err := p.d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "document" && t.Name.Space == nsW {
				f.prefixes(t)
			}
			if t.Name.Space != nsW {
				continue
			}
			switch t.Name.Local {
			case "p":
				para, err := p.para(start)
				if err != nil {
					return err
				}
				f.Paras = append(f.Paras, para)
			case "tc":
				p.cells = append(p.cells, start)
			}
		case xml.EndElement:
			if t.Name.Space == nsW && t.Name.Local == "tc" && len(p.cells) > 0 {
				p.cells = p.cells[:len(p.cells)-1]
			}
		}
	}
	for i, para := range f.Paras {
		para.last = para.cell >= 0 && (i+1 == len(f.Paras) || f.Paras[i+1].cell != para.cell)
	}
	return nil
}

// prefixes learns what document.xml calls the two namespaces it writes.
func (f *File) prefixes(root xml.StartElement) {
	f.w = "w"
	for _, a := range root.Attr {
		switch {
		case a.Name.Space == "xmlns" && a.Value == nsW:
			f.w = prefix(a.Name.Local)
		case a.Name.Space == "" && a.Name.Local == "xmlns" && a.Value == nsW:
			f.w = ""
		case a.Name.Space == "xmlns" && a.Value == nsR:
			f.r = a.Name.Local
		}
	}
}

// para reads one paragraph, its start tag already read.
func (p *parser) para(start int) (*Para, error) {
	para := &Para{el: span{start: start, open: int(p.d.InputOffset())}, cell: -1}
	if n := len(p.cells); n > 0 {
		para.cell = p.cells[n-1]
	}
	p.comments = nil
	var in *wrap
	for {
		at := int(p.d.InputOffset())
		tok, err := p.d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Space != nsW:
				if err := p.d.Skip(); err != nil {
					return nil, err
				}
			case t.Name.Local == "pPr":
				para.pPr = span{start: at, open: int(p.d.InputOffset())}
				if err := p.pPr(para); err != nil {
					return nil, err
				}
				para.pPr.end = int(p.d.InputOffset())
			case t.Name.Local == "r":
				r, err := p.run(at, in)
				if err != nil {
					return nil, err
				}
				para.runs = append(para.runs, r)
			case containers[t.Name.Local]:
				in = &wrap{name: t.Name.Local, parent: in}
			default:
				// A content control's properties, bookmarks, comment
				// ranges, proofing marks: nothing the text counts.
				if t.Name.Local == "commentRangeStart" || t.Name.Local == "commentRangeEnd" {
					p.comment(attr(t, "id"))
				}
				if err := p.d.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			switch {
			case t.Name.Space == nsW && t.Name.Local == "p":
				para.el.end = int(p.d.InputOffset())
				para.Comments = p.comments
				para.index()
				return para, nil
			case in != nil && t.Name.Space == nsW && t.Name.Local == in.name:
				in = in.parent
			}
		}
	}
}

// pPr reads a paragraph's properties, its start tag already read.
func (p *parser) pPr(para *Para) error {
	depth := 1
	for depth > 0 {
		at := int(p.d.InputOffset())
		tok, err := p.d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Space != nsW {
				continue
			}
			switch t.Name.Local {
			case "pStyle":
				para.Style = attr(t, "val")
			case "numId":
				para.NumID = attr(t, "val")
			case "ilvl":
				para.Ilvl, _ = strconv.Atoi(attr(t, "val"))
			case "sectPr":
				para.section = true
				if depth == 2 && para.pPrTail == 0 {
					para.pPrTail = at
				}
			case "pPrChange":
				// The properties a tracked change says the paragraph had,
				// which are not its own.
				if depth == 2 {
					if para.pPrTail == 0 {
						para.pPrTail = at
					}
					if err := p.d.Skip(); err != nil {
						return err
					}
					depth--
				}
			case "rPr":
				if depth == 2 {
					para.markRPr = span{start: at, open: int(p.d.InputOffset())}
					kids, err := p.children()
					if err != nil {
						return err
					}
					depth--
					para.markRPr.end = int(p.d.InputOffset())
					para.markIns, para.markDel = slices.Contains(kids, "ins"), slices.Contains(kids, "del")
				}
			}
		case xml.EndElement:
			depth--
			if depth == 0 && para.pPrTail == 0 {
				para.pPrTail = at
			}
		}
	}
	if para.NumID == "0" {
		// numId 0 takes a paragraph out of the list its style puts it in.
		para.NumID = ""
	}
	return nil
}

// children reads the rest of the element just started and returns the
// local names of its own children in the main namespace.
func (p *parser) children() ([]string, error) {
	var names []string
	for {
		tok, err := p.d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsW {
				names = append(names, t.Name.Local)
			}
			if err := p.d.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			return names, nil
		}
	}
}

// comment notes a comment's marker in the paragraph being read.
func (p *parser) comment(id string) {
	if !slices.Contains(p.comments, id) {
		p.comments = append(p.comments, id)
	}
}

// run reads one run, its start tag already read.
func (p *parser) run(start int, in *wrap) (*run, error) {
	r := &run{el: span{start: start, open: int(p.d.InputOffset())}, in: in}
	for {
		at := int(p.d.InputOffset())
		tok, err := p.d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			k, err := p.kid(t, at, r)
			if err != nil {
				return nil, err
			}
			k.end = int(p.d.InputOffset())
			if t.Name.Space == nsW && t.Name.Local == "rPr" {
				r.rPr = string(p.doc[at:k.end])
				continue
			}
			k.n = int(doc.UTF16Len(k.text))
			r.kids = append(r.kids, k)
		case xml.EndElement:
			r.el.end = int(p.d.InputOffset())
			return r, nil
		}
	}
}

// kid reads one element of a run, all of it, and says what it counts as.
func (p *parser) kid(t xml.StartElement, at int, r *run) (kid, error) {
	k := kid{start: at}
	if t.Name.Space != nsW {
		// Markup compatibility around a drawing, most often.
		k.text = string(object)
		return k, p.d.Skip()
	}
	switch t.Name.Local {
	case "t", "delText":
		text, err := p.chars()
		k.text, k.isText, k.deleted = text, true, t.Name.Local == "delText"
		return k, err
	case "tab":
		k.text = "\t"
	case "br":
		k.text = "\v"
		if typ := attr(t, "type"); typ == "page" || typ == "column" {
			k.text = string(object)
		}
	case "cr":
		k.text = "\v"
	case "noBreakHyphen":
		k.text = "\u2011"
	case "softHyphen":
		k.text = "\u00ad"
	case "sym":
		if n, err := strconv.ParseUint(attr(t, "char"), 16, 32); err == nil {
			k.text = string(rune(n))
		}
	case "drawing":
		// An inline drawing is a character of its own; one anchored to the
		// page floats free of the text.
		inline, err := p.inlineDrawing()
		if inline {
			k.text = string(object)
		}
		return k, err
	case "pict", "object", "footnoteReference", "endnoteReference":
		k.text = string(object)
	case "commentReference":
		p.comment(attr(t, "id"))
	case "fldChar", "instrText", "delInstrText":
		r.field = true
	}
	return k, p.d.Skip()
}

// chars is the character data of the element just started, to its end.
func (p *parser) chars() (string, error) {
	var b strings.Builder
	for {
		tok, err := p.d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			if err := p.d.Skip(); err != nil {
				return "", err
			}
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

// inlineDrawing reads a w:drawing and says whether it is inline.
func (p *parser) inlineDrawing() (bool, error) {
	inline := false
	depth := 1
	for depth > 0 {
		tok, err := p.d.Token()
		if err != nil {
			return false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 1 && t.Name.Local == "inline" {
				inline = true
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return inline, nil
}

// index builds the paragraph's Text and where each of its units comes from.
func (para *Para) index() {
	n := 0
	for _, r := range para.runs {
		for _, k := range r.kids {
			n += k.n
		}
	}
	para.units = make([]int, 0, n)
	var b strings.Builder
	for ri, r := range para.runs {
		r.u0 = len(para.units)
		for _, k := range r.kids {
			b.WriteString(k.text)
			for range k.n {
				para.units = append(para.units, ri)
			}
		}
		r.u1 = len(para.units)
	}
	para.Text = b.String()
}

// Len is the paragraph's length in UTF-16 units.
func (para *Para) Len() int { return len(para.units) }

func attr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local && (a.Name.Space == nsW || a.Name.Space == "") {
			return a.Value
		}
	}
	return ""
}
