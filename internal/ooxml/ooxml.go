// Package ooxml edits a Word file (.docx) the way it is stored. It reads
// word/document.xml into paragraphs whose text lines up with the Docs
// API's view of the same file, unit for unit, and writes every change as
// a splice of the bytes it read: a part it does not change is copied from
// the zip untouched, and so is every byte of document.xml outside an edit.
// encoding/xml cannot write a document back with its namespace prefixes
// as they were (golang/go#9519, #11496), so nothing is re-encoded.
//
// The Docs API reads such a file and refuses every write to it (spike G):
// a Word tracked change reads there as a suggestion, a Word comment as an
// anchored comment, and an uploaded revision is read again within seconds
// (spike H). So a suggestion is written as a tracked change and a comment
// as a Word comment.
package ooxml

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The namespaces a Word file's main part is written in.
const (
	nsW = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

// Part names inside the package.
const (
	partDocument  = "word/document.xml"
	partRels      = "word/_rels/document.xml.rels"
	partTypes     = "[Content_Types].xml"
	partComments  = "word/comments.xml"
	partNumbering = "word/numbering.xml"
	partStyles    = "word/styles.xml"
	maxPartBytes  = 64 << 20
)

// File is a .docx as read, with the edits planned on it so far.
type File struct {
	zr      *zip.Reader
	doc     []byte
	parts   map[string][]byte // parts read, then changed or added, by name
	changed map[string]bool   // parts changed or added
	added   []string          // parts added, in order
	// w and r are what document.xml calls the main and the relationships
	// namespaces, which written markup must use.
	w prefix
	r string
	// Paras are the body's paragraphs in document order, the paragraphs
	// of table cells included, which is the order the Docs API lists
	// them in (spike H).
	Paras   []*Para
	splices []splice
	queued  map[int][]pending // per paragraph, until written
	// nextID is the next annotation id, 0 until one is first needed.
	nextID int
	// sheet and lists are styles.xml's and numbering.xml's, read once.
	sheet *styleSheet
	lists *numbering
}

type splice struct {
	start, end int
	text       string
}

// Open reads a .docx.
func Open(data []byte) (*File, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a Word file: %w", err)
	}
	f := &File{zr: zr, parts: map[string][]byte{}, changed: map[string]bool{}}
	if f.doc, err = f.part(partDocument); err != nil {
		return nil, err
	}
	if f.doc == nil {
		return nil, errors.New("not a Word file: it has no word/document.xml")
	}
	if err := f.parse(); err != nil {
		return nil, fmt.Errorf("word/document.xml: %w", err)
	}
	return f, nil
}

var idAttr = regexp.MustCompile(`:id="(\d+)"`)

// id is a new annotation id. Annotation ids, of tracked changes, comments
// and bookmarks, share one space in practice, so a new one is past every
// w:id document.xml and comments.xml use.
func (f *File) id() int {
	if f.nextID == 0 {
		f.nextID = maxMatch(idAttr, f.doc) + 1
		if c, _ := f.part(partComments); c != nil {
			f.nextID = max(f.nextID, maxMatch(idAttr, c)+1)
		}
	}
	f.nextID++
	return f.nextID - 1
}

// maxMatch is the highest number re's first group captures in b, or 0.
func maxMatch(re *regexp.Regexp, b []byte) int {
	m := 0
	for _, s := range re.FindAllSubmatch(b, -1) {
		if n, err := strconv.Atoi(string(s[1])); err == nil && n > m {
			m = n
		}
	}
	return m
}

// part is a part's bytes, as changed when it has been, nil when the
// package has none.
func (f *File) part(name string) ([]byte, error) {
	if b, ok := f.parts[name]; ok {
		return b, nil
	}
	for _, zf := range f.zr.File {
		if zf.Name != name {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(b) > maxPartBytes {
			return nil, fmt.Errorf("%s is larger than %d MB", name, maxPartBytes>>20)
		}
		f.parts[name] = b
		return b, nil
	}
	return nil, nil
}

// setPart records a part's new bytes.
func (f *File) setPart(name string, b []byte) {
	if !f.has(name) && !slices.Contains(f.added, name) {
		f.added = append(f.added, name)
	}
	f.parts[name], f.changed[name] = b, true
}

func (f *File) has(name string) bool {
	return slices.ContainsFunc(f.zr.File, func(zf *zip.File) bool { return zf.Name == name })
}

// Text is the body's text, one line per paragraph. It is what a preview
// compares.
func (f *File) Text() string {
	var b strings.Builder
	for _, p := range f.Paras {
		b.WriteString(strings.ReplaceAll(p.Text, "\n", " "))
		b.WriteString("\n")
	}
	return b.String()
}

// Changed says an edit has been planned.
func (f *File) Changed() bool {
	return len(f.splices) > 0 || len(f.queued) > 0 || len(f.changed) > 0
}

// Bytes is the .docx with the planned edits made: document.xml spliced,
// the other parts that changed rewritten, every other entry copied from
// the zip as it was. The result is read again, and must parse; the file
// read is returned with it.
func (f *File) Bytes() ([]byte, *File, error) {
	f.flush()
	doc, err := applySplices(f.doc, f.splices)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, zf := range f.zr.File {
		body, changed := f.parts[zf.Name], f.changed[zf.Name]
		if zf.Name == partDocument {
			body, changed = doc, len(f.splices) > 0
		}
		if !changed {
			if err := zw.Copy(zf); err != nil {
				return nil, nil, err
			}
			continue
		}
		if err := writeEntry(zw, zf.Name, body); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range f.added {
		if err := writeEntry(zw, name, f.parts[name]); err != nil {
			return nil, nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	out := buf.Bytes()
	again, err := Open(out)
	if err != nil {
		return nil, nil, fmt.Errorf("the edited file does not read back: %w", err)
	}
	return out, again, nil
}

func writeEntry(zw *zip.Writer, name string, body []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// applySplices makes the planned edits to b. They may not overlap.
func applySplices(b []byte, ss []splice) ([]byte, error) {
	ss = append([]splice(nil), ss...)
	// By start, an insertion before a change that starts where it is.
	sort.SliceStable(ss, func(i, j int) bool {
		return ss[i].start < ss[j].start || ss[i].start == ss[j].start && ss[i].end < ss[j].end
	})
	for i := 1; i < len(ss); i++ {
		if ss[i].start < ss[i-1].end {
			return nil, errors.New("two edits reach the same text")
		}
	}
	var out bytes.Buffer
	at := 0
	for _, s := range ss {
		out.Write(b[at:s.start])
		out.WriteString(s.text)
		at = s.end
	}
	out.Write(b[at:])
	return out.Bytes(), nil
}

// prefix is what a part calls the main namespace; markup added to the
// part writes its names with it.
type prefix string

func (w prefix) tag(local string) string {
	if w == "" {
		return local
	}
	return string(w) + ":" + local
}

// el is an element holding inner.
func (w prefix) el(local, inner string) string {
	return "<" + w.tag(local) + ">" + inner + "</" + w.tag(local) + ">"
}

// empty is an element holding nothing.
func (w prefix) empty(local string) string { return "<" + w.tag(local) + "/>" }

// val is an empty element whose w:val is v.
func (w prefix) val(local, v string) string {
	return fmt.Sprintf(`<%s %s="%s"/>`, w.tag(local), w.tag("val"), escape(v))
}

// escape is text as XML character data or an attribute's value.
func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
