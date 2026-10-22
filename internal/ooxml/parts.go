package ooxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Relationship and content types of the parts an edit may add or point to.
const (
	relHyperlink  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	relComments   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments"
	relNumbering  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	typeComments  = "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"
	typeNumbering = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"
	xmlHeader     = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
)

var (
	relID      = regexp.MustCompile(`Id="rId(\d+)"`)
	abstractID = regexp.MustCompile(`abstractNumId="(\d+)"`)
	numIDAttr  = regexp.MustCompile(`numId="(\d+)"`)
)

// addRelationship adds a relationship from document.xml and returns its
// id.
func (f *File) addRelationship(typ, target string, external bool) (string, error) {
	rels, err := f.part(partRels)
	if err != nil {
		return "", err
	}
	if rels == nil {
		rels = []byte(xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`)
	}
	id := "rId" + strconv.Itoa(maxMatch(relID, rels)+1)
	mode := ""
	if external {
		mode = ` TargetMode="External"`
	}
	out, err := insertBefore(partRels, rels, "</Relationships>", fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s"%s/>`, id, typ, escape(target), mode))
	if err != nil {
		return "", err
	}
	f.setPart(partRels, out)
	return id, nil
}

// insertBefore puts s in front of the last occurrence of end in part name's
// bytes b.
func insertBefore(name string, b []byte, end, s string) ([]byte, error) {
	i := bytes.LastIndex(b, []byte(end))
	if i < 0 {
		return nil, fmt.Errorf("%s has no end tag", name)
	}
	return slices.Insert(slices.Clone(b), i, []byte(s)...), nil
}

// partFor is a part's bytes and what it calls the main namespace; a part
// the package lacks comes as an empty root element, fresh, which save
// then adds.
func (f *File) partFor(name, root string) ([]byte, prefix, bool, error) {
	body, err := f.part(name)
	if err != nil {
		return nil, "", false, err
	}
	if body == nil {
		return []byte(xmlHeader + `<w:` + root + ` xmlns:w="` + nsW + `"></w:` + root + `>`), "w", true, nil
	}
	return body, rootPrefix(body), false, nil
}

// save records a part's new bytes, adding the part with its content type
// and its relationship from document.xml when it is fresh.
func (f *File) save(name string, body []byte, fresh bool, contentType, rel string) error {
	if !fresh {
		f.setPart(name, body)
		return nil
	}
	types, err := f.part(partTypes)
	if err != nil {
		return err
	}
	out, err := insertBefore(partTypes, types, "</Types>", fmt.Sprintf(`<Override PartName="/%s" ContentType="%s"/>`, name, contentType))
	if err != nil {
		return err
	}
	f.setPart(partTypes, out)
	if _, err := f.addRelationship(rel, strings.TrimPrefix(name, "word/"), false); err != nil {
		return err
	}
	f.setPart(name, body)
	return nil
}

// rootPrefix is what a part calls the main namespace on its root.
func rootPrefix(b []byte) prefix {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.RawToken()
		if err != nil {
			return "w"
		}
		if t, ok := tok.(xml.StartElement); ok {
			return prefix(t.Name.Space)
		}
	}
}

// addComment writes a comment's text into comments.xml, made when the
// file has none.
func (f *File) addComment(id string, c NewComment) error {
	body, w, fresh, err := f.partFor(partComments, "comments")
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<%s %s="%s" %s="%s" %s="%s" %s="%s">`, w.tag("comment"), w.tag("id"), id, w.tag("author"), escape(c.Author),
		w.tag("date"), escape(c.Date), w.tag("initials"), escape(c.Initials))
	for _, line := range strings.Split(c.Text, "\n") {
		b.WriteString(w.el("p", w.el("r", fmt.Sprintf(`<%s xml:space="preserve">%s</%s>`, w.tag("t"), escape(line), w.tag("t")))))
	}
	b.WriteString("</" + w.tag("comment") + ">")
	out, err := insertBefore(partComments, body, "</"+w.tag("comments")+">", b.String())
	if err != nil {
		return err
	}
	return f.save(partComments, out, fresh, typeComments, relComments)
}

// styleSheet is styles.xml's paragraph styles: the id of each by its
// name, lower-cased, every id in use, the style each is based on, and the
// list each puts a paragraph in.
type styleSheet struct {
	byName  map[string]string
	ids     map[string]bool
	basedOn map[string]string
	lists   map[string]*styleList
	// defaultPara is the paragraph style a paragraph naming none takes.
	defaultPara string
}

// styleList is the numbering a style gives, as far as it says: a list,
// a level, or both.
type styleList struct {
	numID   string
	ilvl    int
	ilvlSet bool
}

func (f *File) styles() (*styleSheet, error) {
	if f.sheet != nil {
		return f.sheet, nil
	}
	body, err := f.part(partStyles)
	if err != nil {
		return nil, err
	}
	h := &styleSheet{byName: map[string]string{}, ids: map[string]bool{}, basedOn: map[string]string{}, lists: map[string]*styleList{}}
	d := xml.NewDecoder(bytes.NewReader(body))
	var id, typ string
	list := func() *styleList {
		if h.lists[id] == nil {
			h.lists[id] = &styleList{}
		}
		return h.lists[id]
	}
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", partStyles, err)
		}
		t, ok := tok.(xml.StartElement)
		if !ok || t.Name.Space != nsW {
			continue
		}
		switch t.Name.Local {
		case "style":
			id, typ = attr(t, "styleId"), attr(t, "type")
			h.ids[id] = true
			if d := attr(t, "default"); typ == "paragraph" && (d == "1" || d == "true") {
				h.defaultPara = id
			}
		case "name":
			if typ == "paragraph" {
				h.byName[strings.ToLower(attr(t, "val"))] = id
			}
		case "basedOn":
			h.basedOn[id] = attr(t, "val")
		case "numId":
			list().numID = attr(t, "val")
		case "ilvl":
			list().ilvl, _ = strconv.Atoi(attr(t, "val"))
			list().ilvlSet = true
		}
	}
	f.sheet = h
	return h, nil
}

// ListOf is the list and the level paragraph i is in; "" for none. The
// list and the level are each the paragraph's own, else what its style
// gives ("List Bullet", which is numbered in styles.xml, not on the
// paragraph), the default paragraph style's for a paragraph naming none;
// Docs reads them so. numId 0 on the paragraph takes it out of its
// style's list (ISO/IEC 29500-1, numId), and Docs reads it as no list
// item.
func (f *File) ListOf(i int) (string, int) {
	p := f.Paras[i]
	if p.unlisted {
		return "", 0
	}
	style := p.Style
	if h, err := f.styles(); err == nil && style == "" {
		style = h.defaultPara
	}
	id, lvl := f.styleList(style)
	if p.NumID != "" {
		id = p.NumID
	}
	if p.ilvlSet {
		lvl = p.Ilvl
	}
	if id == "" {
		return "", 0
	}
	return id, lvl
}

// styleList is the list and the level paragraph style id gives, each its
// own or from the nearest style it is based on that sets it; "" for no
// list, which numId 0 also says.
func (f *File) styleList(id string) (string, int) {
	h, err := f.styles()
	if err != nil {
		return "", 0
	}
	numID, ilvl, lvlSet := "", 0, false
	// Bounded: a cycle of basedOn is a broken file, not a reason to hang.
	for range 16 {
		if l := h.lists[id]; l != nil {
			if numID == "" {
				numID = l.numID
			}
			if !lvlSet && l.ilvlSet {
				ilvl, lvlSet = l.ilvl, true
			}
		}
		if id = h.basedOn[id]; id == "" || numID != "" && lvlSet {
			break
		}
	}
	if numID == "0" {
		numID = ""
	}
	return numID, ilvl
}

// HeadingStyle is the id of the paragraph style for a heading of the
// level. A style's id is the file's own, often in the language the file
// was made in, but a built-in style's name is "heading N" in every
// language. A file without one gets it.
func (f *File) HeadingStyle(level int) (string, error) {
	if level < 1 || level > 6 {
		return "", fmt.Errorf("heading level %d", level)
	}
	styles, err := f.part(partStyles)
	if err != nil {
		return "", err
	}
	if styles == nil {
		return "", unsupported("the file has no styles part to hold a heading style")
	}
	h, err := f.styles()
	if err != nil {
		return "", err
	}
	name := "heading " + strconv.Itoa(level)
	if id, ok := h.byName[name]; ok {
		return id, nil
	}
	id := "Heading" + strconv.Itoa(level)
	for h.ids[id] {
		id += "x"
	}
	w := rootPrefix(styles)
	style := fmt.Sprintf(`<%s %s="paragraph" %s="%s">%s%s%s%s%s%s</%s>`,
		w.tag("style"), w.tag("type"), w.tag("styleId"), id, w.val("name", name), w.val("basedOn", "Normal"), w.val("next", "Normal"),
		w.empty("qFormat"), w.el("pPr", w.empty("keepNext")+w.val("outlineLvl", strconv.Itoa(level-1))),
		w.el("rPr", w.empty("b")+w.val("sz", strconv.Itoa(40-4*level))), w.tag("style"))
	out, err := insertBefore(partStyles, styles, "</"+w.tag("styles")+">", style)
	if err != nil {
		return "", err
	}
	f.setPart(partStyles, out)
	h.byName[name], h.ids[id] = id, true
	return id, nil
}

// numbering is numbering.xml's lists: each list's definition, and each
// definition's number format by level.
type numbering struct {
	abstract map[string]string         // by numId
	formats  map[string]map[int]string // by abstractNumId, then level
}

func (f *File) numbering() *numbering {
	if f.lists != nil {
		return f.lists
	}
	n := &numbering{abstract: map[string]string{}, formats: map[string]map[int]string{}}
	f.lists = n
	body, err := f.part(partNumbering)
	if err != nil || body == nil {
		return n
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	var curAbs, curNum string
	curLvl := -1
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != nsW {
				continue
			}
			switch t.Name.Local {
			case "abstractNum":
				curAbs = attr(t, "abstractNumId")
				n.formats[curAbs] = map[int]string{}
			case "lvl":
				curLvl, _ = strconv.Atoi(attr(t, "ilvl"))
			case "numFmt":
				if curAbs != "" && curLvl >= 0 {
					n.formats[curAbs][curLvl] = attr(t, "val")
				}
			case "num":
				curNum = attr(t, "numId")
			case "abstractNumId":
				if curNum != "" {
					n.abstract[curNum] = attr(t, "val")
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "abstractNum":
				curAbs, curLvl = "", -1
			case "num":
				curNum = ""
			}
		}
	}
	return n
}

// ListOrdered says whether the list numId numbers its items, at level
// ilvl, rather than bulleting them; ok is false when the file does not
// say.
func (f *File) ListOrdered(numID string, ilvl int) (ordered, ok bool) {
	n := f.numbering()
	numFmt := n.formats[n.abstract[numID]][ilvl]
	if numFmt == "" {
		return false, false
	}
	return numFmt != "bullet" && numFmt != "none", true
}

// NewList adds a list of its own, bulleted or numbered, nine levels deep
// with each level indented 0.5 inch more, and returns its numId.
func (f *File) NewList(ordered bool) (string, error) {
	body, w, fresh, err := f.partFor(partNumbering, "numbering")
	if err != nil {
		return "", err
	}
	absID, numID := strconv.Itoa(maxMatch(abstractID, body)+1), strconv.Itoa(maxMatch(numIDAttr, body)+1)
	bullets := []string{"\u25CF", "\u25CB", "\u25A0"}
	formats := []string{"decimal", "lowerLetter", "lowerRoman"}
	levels := map[int]string{}
	var a strings.Builder
	fmt.Fprintf(&a, `<%s %s="%s">%s`, w.tag("abstractNum"), w.tag("abstractNumId"), absID, w.val("multiLevelType", "hybridMultilevel"))
	for l := range 9 {
		numFmt, text := "bullet", bullets[l%3]
		if ordered {
			numFmt, text = formats[l%3], "%"+strconv.Itoa(l+1)+"."
		}
		levels[l] = numFmt
		ind := fmt.Sprintf(`<%s %s="%d" %s="360"/>`, w.tag("ind"), w.tag("left"), 720*(l+1), w.tag("hanging"))
		fmt.Fprintf(&a, `<%s %s="%d">%s%s%s%s%s</%s>`, w.tag("lvl"), w.tag("ilvl"), l,
			w.val("start", "1"), w.val("numFmt", numFmt), w.val("lvlText", text), w.val("lvlJc", "left"), w.el("pPr", ind), w.tag("lvl"))
	}
	a.WriteString("</" + w.tag("abstractNum") + ">")
	// Every abstract definition comes before the first list that uses one.
	at := bytes.Index(body, []byte("<"+w.tag("num")+" "))
	if at < 0 {
		at = bytes.LastIndex(body, []byte("</"+w.tag("numbering")+">"))
	}
	if at < 0 {
		return "", fmt.Errorf("%s has no end tag", partNumbering)
	}
	out := slices.Insert(slices.Clone(body), at, []byte(a.String())...)
	num := fmt.Sprintf(`<%s %s="%s">%s</%s>`, w.tag("num"), w.tag("numId"), numID, w.val("abstractNumId", absID), w.tag("num"))
	if out, err = insertBefore(partNumbering, out, "</"+w.tag("numbering")+">", num); err != nil {
		return "", err
	}
	if err := f.save(partNumbering, out, fresh, typeNumbering, relNumbering); err != nil {
		return "", err
	}
	n := f.numbering()
	n.abstract[numID], n.formats[absID] = absID, levels
	return numID, nil
}
