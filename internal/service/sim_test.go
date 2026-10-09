package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
)

// simDoc is a one-tab body that applies the requests content rounds send,
// the way spike E saw Google apply them live, and serves itself back as
// a document. It knows paragraphs of text and inline images, and tables
// whose cells hold one paragraph each. A request it does not model fails
// the test rather than passing unapplied, and so does one Google refuses:
// a write at the body's final newline, or a delete that would join a
// paragraph to a table. Text is BMP only, so a rune is one UTF-16 unit.
//
// Suggest mode is applied as direct, with what it inserts marked as one
// suggestion that only the first suggest-mode batch names in its reply:
// Google files a call's later writes under that suggestion (seen live
// 2026-10-09). A deletion is not modeled as a suggestion, so a test shows
// what the rounds send, not what a reviewer sees.
type simDoc struct {
	t        *testing.T
	elems    []*simElem
	revision string
	batches  int
	images   []string // addresses inserted, in order
	// suggesting is set while a suggest-mode batch applies, and named
	// once the first one has said so.
	suggesting, named bool
	// onBatch runs after each applied batch, numbered from 1, to stand in
	// for somebody else editing between rounds; such an edit calls edited.
	onBatch func(n int)
}

// edited moves the revision on, as somebody else's edit does.
func (s *simDoc) edited() { s.revision += "+" }

type simElem struct {
	para      string     // a paragraph's text, ending in its newline
	cells     [][]string // a table's cells, each without its newline
	suggested bool       // inserted by a suggest-mode batch, whole
}

// simSuggestion is the one suggestion every suggest-mode write files under.
const simSuggestion = "suggest.sim1"

// simImage marks an inline image in a paragraph: one UTF-16 unit, as an
// inline object is.
const simImage = '\uE000'

func newSim(t *testing.T, paras ...string) *simDoc {
	s := &simDoc{t: t, revision: "rev-0"}
	for _, p := range paras {
		s.elems = append(s.elems, &simElem{para: p + "\n"})
	}
	return s
}

// String draws the body: a paragraph as its text, an image as <img>, a
// table as [a|b / c|d].
func (s *simDoc) String() string {
	var out []string
	for _, e := range s.elems {
		if e.cells == nil {
			out = append(out, strings.ReplaceAll(strings.TrimSuffix(e.para, "\n"), string(simImage), "<img>"))
			continue
		}
		var rows []string
		for _, r := range e.cells {
			rows = append(rows, strings.Join(r, "|"))
		}
		out = append(out, "["+strings.Join(rows, " / ")+"]")
	}
	return strings.Join(out, "\n")
}

func ulen(s string) int64 { return int64(len([]rune(s))) }

func (e *simElem) length() int64 {
	if e.cells == nil {
		return ulen(e.para)
	}
	n := int64(2)
	for _, r := range e.cells {
		n++
		for _, c := range r {
			n += ulen(c) + 2
		}
	}
	return n
}

// starts lists where each element begins; the body starts after its
// section break, at 1.
func (s *simDoc) starts() []int64 {
	out := make([]int64, len(s.elems))
	at := int64(1)
	for i, e := range s.elems {
		out[i] = at
		at += e.length()
	}
	return out
}

func (s *simDoc) end() int64 {
	st := s.starts()
	return st[len(st)-1] + s.elems[len(s.elems)-1].length()
}

// at finds the element holding index.
func (s *simDoc) at(index int64) (int, int64) {
	for i, st := range s.starts() {
		if index >= st && index < st+s.elems[i].length() {
			return i, st
		}
	}
	s.t.Fatalf("sim: index %d is outside the body (1..%d)", index, s.end())
	return 0, 0
}

// cellAt finds the cell whose paragraph holds index inside a table, and
// the offset into its text.
func (s *simDoc) cellAt(e *simElem, start, index int64) (r, c int, off int64) {
	at := start + 1
	for ri, row := range e.cells {
		at++
		for ci, cell := range row {
			at++
			if index >= at && index <= at+ulen(cell) {
				return ri, ci, index - at
			}
			at += ulen(cell) + 1
		}
	}
	s.t.Fatalf("sim: index %d is a table's structure, not a cell's text", index)
	return 0, 0, 0
}

func splice(s string, off int64, ins string) string {
	r := []rune(s)
	return string(r[:off]) + ins + string(r[off:])
}

// resplit turns paragraph text holding several newlines into paragraphs.
func resplit(text string) []*simElem {
	var out []*simElem
	for _, p := range strings.SplitAfter(text, "\n") {
		if p != "" {
			out = append(out, &simElem{para: p})
		}
	}
	return out
}

func (s *simDoc) insertText(text string, index int64) {
	if text == "" {
		s.t.Fatalf("sim: an empty insertText at %d; an insert needs text", index)
	}
	i, st := s.at(index)
	e := s.elems[i]
	if e.cells != nil {
		r, c, off := s.cellAt(e, st, index)
		if strings.Contains(text, "\n") {
			s.t.Fatalf("sim: a newline into a cell is not modeled")
		}
		e.cells[r][c] = splice(e.cells[r][c], off, text)
		return
	}
	paras := resplit(splice(e.para, index-st, text))
	for _, p := range paras {
		p.suggested = e.suggested || s.suggesting
	}
	s.elems = slices.Replace(s.elems, i, i+1, paras...)
}

func (s *simDoc) deleteRange(from, to int64) {
	if to > s.end()-1 {
		s.t.Fatalf("sim: delete [%d,%d) takes the body's final newline", from, to)
	}
	i, st := s.at(from)
	if e := s.elems[i]; e.cells != nil {
		r, c, off := s.cellAt(e, st, from)
		cell := []rune(e.cells[r][c])
		if off+to-from > int64(len(cell)) {
			s.t.Fatalf("sim: delete [%d,%d) leaves its cell", from, to)
		}
		e.cells[r][c] = string(cell[:off]) + string(cell[off+to-from:])
		return
	}
	j, jst := s.at(to - 1)
	if to == jst+s.elems[j].length() {
		// The last paragraph's newline goes, so the next one joins it.
		j++
	}
	var text string
	for k := i; k <= j; k++ {
		if s.elems[k].cells != nil {
			s.t.Fatalf("sim: delete [%d,%d) reaches a table", from, to)
		}
		text += s.elems[k].para
	}
	r := []rune(text)
	s.elems = slices.Replace(s.elems, i, j+1, resplit(string(r[:from-st])+string(r[to-st:]))...)
}

// insertTable puts a newline at index and the table right after it, as
// spike E saw: at a paragraph's newline the table follows that paragraph
// and its old newline becomes an empty paragraph after the table; at a
// paragraph's start an empty paragraph appears before the table.
func (s *simDoc) insertTable(rows, cols int, index int64) {
	if i, _ := s.at(index); s.elems[i].cells != nil {
		s.t.Fatalf("sim: a table inside a table is not modeled")
	}
	s.insertText("\n", index)
	i, st := s.at(index)
	if st+s.elems[i].length() != index+1 {
		s.t.Fatalf("sim: the newline at %d does not end a paragraph", index)
	}
	cells := make([][]string, rows)
	for r := range cells {
		cells[r] = make([]string, cols)
	}
	s.elems = slices.Insert(s.elems, i+1, &simElem{cells: cells, suggested: s.suggesting})
}

func (s *simDoc) apply(req *gapi.BatchUpdateRequest) (*gapi.BatchUpdateResponse, error) {
	if req.WriteControl == nil || req.WriteControl.RequiredRevisionID != s.revision {
		return nil, &gapi.APIError{Status: 400, Message: "The required revision ID does not match the latest revision."}
	}
	s.suggesting = req.WriteControl.WriteMode == "SUGGEST"
	created := ""
	if s.suggesting && !s.named {
		s.named = true
		created = fmt.Sprintf(`,"suggestionResponses":[{"createdSuggestionIds":[%q]}]`, simSuggestion)
	}
	for _, raw := range req.Requests {
		var r map[string]struct {
			Text     string `json:"text"`
			URI      string `json:"uri"`
			Rows     int    `json:"rows"`
			Columns  int    `json:"columns"`
			Location *struct {
				Index     int64  `json:"index"`
				SegmentID string `json:"segmentId"`
			} `json:"location"`
			Range *struct {
				StartIndex int64  `json:"startIndex"`
				EndIndex   int64  `json:"endIndex"`
				SegmentID  string `json:"segmentId"`
			} `json:"range"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			s.t.Fatal(err)
		}
		for kind, v := range r {
			if (v.Location != nil && v.Location.SegmentID != "") || (v.Range != nil && v.Range.SegmentID != "") {
				s.t.Fatalf("sim: %s outside the body", kind)
			}
			if strings.HasPrefix(kind, "insert") && v.Location == nil || kind == "deleteContentRange" && v.Range == nil {
				s.t.Fatalf("sim: %s without an index or range is not modeled", kind)
			}
			switch kind {
			case "insertText":
				s.insertText(v.Text, v.Location.Index)
			case "deleteContentRange":
				s.deleteRange(v.Range.StartIndex, v.Range.EndIndex)
			case "insertTable":
				s.insertTable(v.Rows, v.Columns, v.Location.Index)
			case "insertInlineImage":
				if i, _ := s.at(v.Location.Index); s.elems[i].cells != nil {
					s.t.Fatalf("sim: an image in a cell is not modeled")
				}
				s.insertText(string(simImage), v.Location.Index)
				s.images = append(s.images, v.URI)
			case "updateParagraphStyle", "updateTextStyle", "deleteParagraphBullets":
				// Styles move no index.
			default:
				s.t.Fatalf("sim: %s is not modeled", kind)
			}
		}
	}
	s.batches++
	s.revision = fmt.Sprintf("rev-%d", s.batches)
	raw := fmt.Sprintf(`{"replies":[],"writeControl":{"requiredRevisionId":%q}%s}`, s.revision, created)
	var out gapi.BatchUpdateResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		s.t.Fatal(err)
	}
	out.Raw = json.RawMessage(raw)
	if s.onBatch != nil {
		s.onBatch(s.batches)
	}
	return &out, nil
}

// json is the body as the API serves it.
func (s *simDoc) json() []byte {
	content := []*gdocs.StructuralElement{{StartIndex: 0, EndIndex: 1, SectionBreak: &gdocs.SectionBreak{}}}
	images := 0
	paragraph := func(text string, at int64, suggested bool) *gdocs.StructuralElement {
		p := &gdocs.Paragraph{}
		start := at
		var mark gdocs.Suggested
		if suggested {
			mark.SuggestedInsertionIDs = []string{simSuggestion}
		}
		for _, part := range strings.SplitAfter(text, string(simImage)) {
			if run := strings.TrimSuffix(part, string(simImage)); run != "" {
				p.Elements = append(p.Elements, &gdocs.ParagraphElement{StartIndex: at, EndIndex: at + ulen(run), TextRun: &gdocs.TextRun{Suggested: mark, Content: run}})
				at += ulen(run)
			}
			if strings.HasSuffix(part, string(simImage)) {
				images++
				p.Elements = append(p.Elements, &gdocs.ParagraphElement{StartIndex: at, EndIndex: at + 1,
					InlineObjectElement: &gdocs.InlineObjectElement{Suggested: mark, InlineObjectID: fmt.Sprintf("kix.img%d", images)}})
				at++
			}
		}
		return &gdocs.StructuralElement{StartIndex: start, EndIndex: at, Paragraph: p}
	}
	for i, st := range s.starts() {
		e := s.elems[i]
		if e.cells == nil {
			content = append(content, paragraph(e.para, st, e.suggested))
			continue
		}
		t := &gdocs.Table{Rows: int64(len(e.cells)), Columns: int64(len(e.cells[0]))}
		if e.suggested {
			t.SuggestedInsertionIDs = []string{simSuggestion}
		}
		at := st + 1
		for _, row := range e.cells {
			tr := &gdocs.TableRow{StartIndex: at}
			at++
			for _, c := range row {
				cell := &gdocs.TableCell{StartIndex: at}
				at++
				cell.Content = []*gdocs.StructuralElement{paragraph(c+"\n", at, e.suggested)}
				at += ulen(c) + 1
				cell.EndIndex = at
				tr.TableCells = append(tr.TableCells, cell)
			}
			tr.EndIndex = at
			t.TableRows = append(t.TableRows, tr)
		}
		content = append(content, &gdocs.StructuralElement{StartIndex: st, EndIndex: st + e.length(), Table: t})
	}
	d := &gdocs.Document{DocumentID: fixtureID, Title: "Simulated", RevisionID: s.revision, SuggestionsViewMode: gapi.SuggestionsInline,
		Tabs: []*gdocs.Tab{{TabProperties: &gdocs.TabProperties{TabID: "t.0", Title: "Main"},
			DocumentTab: &gdocs.DocumentTab{Body: &gdocs.Body{Content: content}}}}}
	b, err := json.Marshal(d)
	if err != nil {
		s.t.Fatal(err)
	}
	return b
}
