package doctest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// LargeSpec sizes a synthetic document.
type LargeSpec struct {
	Sections    int  // H1 sections
	Subsections int  // H2 sections per H1
	Paragraphs  int  // body paragraphs per subsection
	ListItems   int  // list items per subsection, every third one nested
	TableEvery  int  // a 4×3 table after every N-th subsection; 0 = none
	Comments    int  // comment threads, each quoting one paragraph's first sentence
	Anchored    bool // give the comments Developer Preview anchors
	Suggestions int  // paragraphs carrying a suggested insertion and deletion
	Footnotes   int  // paragraphs carrying a footnote reference
	Tabs        int  // tabs, each with the same body; 0 or 1 is one tab
}

// DefaultLarge is about a 150-page document: 6 400 blocks, 130 tables,
// 300 comments, 200 suggestions and 100 footnotes.
var DefaultLarge = LargeSpec{Sections: 50, Subsections: 8, Paragraphs: 8, ListItems: 5, TableEvery: 3, Comments: 300, Anchored: true, Suggestions: 200, Footnotes: 100}

// Large builds a synthetic documents.get response of the given size for
// benchmarks and stress tests. The text is deterministic lorem; nothing
// comes from a real document.
func Large(spec LargeSpec) *gdocs.Document {
	if spec.Tabs < 1 {
		spec.Tabs = 1
	}
	d := &gdocs.Document{DocumentID: "1LargeSyntheticDocumentIdXXXXXXXXXXXXXXXXXXXXX", Title: "Large synthetic document", RevisionID: "rev-large"}
	for tab := 1; tab <= spec.Tabs; tab++ {
		b := &largeBuilder{spec: spec, tab: tab, footnotes: map[string]gdocs.Footnote{}}
		b.build()
		dt := &gdocs.DocumentTab{
			Body:      &gdocs.Body{Content: b.content},
			Footnotes: b.footnotes,
			Lists: map[string]gdocs.List{
				"kix.lst1": {ListProperties: &gdocs.ListProperties{NestingLevels: []*gdocs.NestingLevel{{GlyphSymbol: "●"}, {GlyphSymbol: "○"}, {GlyphSymbol: "■"}}}},
				"kix.lst2": {ListProperties: &gdocs.ListProperties{NestingLevels: []*gdocs.NestingLevel{{GlyphType: "DECIMAL", GlyphFormat: "%0."}, {GlyphType: "ALPHA", GlyphFormat: "%1."}}}},
			},
		}
		if tab == 1 {
			dt.CommentAnchors = b.anchors
			d.Comments = b.comments
		}
		d.Tabs = append(d.Tabs, &gdocs.Tab{
			TabProperties: &gdocs.TabProperties{TabID: "t." + strconv.Itoa(tab), Title: "Tab " + strconv.Itoa(tab), Index: int64(tab - 1)},
			DocumentTab:   dt,
		})
	}
	return d
}

// LargeJSON is Large encoded as the API would send it.
func LargeJSON(spec LargeSpec) []byte {
	data, err := json.Marshal(Large(spec))
	if err != nil {
		panic(err)
	}
	return data
}

var loremWords = strings.Fields("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation ullamco laboris nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate velit esse cillum fugiat nulla pariatur excepteur sint occaecat cupidatat non proident sunt culpa qui officia deserunt mollit anim id est laborum")

type largeBuilder struct {
	spec      LargeSpec
	tab       int
	idx       int64
	content   []*gdocs.StructuralElement
	footnotes map[string]gdocs.Footnote
	anchors   map[string]gdocs.CommentAnchor
	comments  []gdocs.CommentThread

	paragraphs int // paragraphs emitted so far
	quotes     []quote
}

type quote struct {
	text       string
	start, end int64
}

func (b *largeBuilder) build() {
	b.content = append(b.content, &gdocs.StructuralElement{StartIndex: 0, EndIndex: 1, SectionBreak: &gdocs.SectionBreak{SectionStyle: &gdocs.SectionStyle{SectionType: "CONTINUOUS"}}})
	b.idx = 1
	total := b.spec.Sections * b.spec.Subsections * b.spec.Paragraphs
	commentEvery, suggestEvery, footnoteEvery := every(total, b.spec.Comments), every(total, b.spec.Suggestions), every(total, b.spec.Footnotes)
	sub := 0
	for s := 1; s <= b.spec.Sections; s++ {
		b.heading(1, fmt.Sprintf("Section %d", s), fmt.Sprintf("h.t%d.s%d", b.tab, s))
		for j := 1; j <= b.spec.Subsections; j++ {
			sub++
			b.heading(2, fmt.Sprintf("Topic %d.%d", s, j), fmt.Sprintf("h.t%d.s%d.%d", b.tab, s, j))
			for p := 0; p < b.spec.Paragraphs; p++ {
				n := b.paragraphs + 1
				b.paragraph(n, commentEvery > 0 && n%commentEvery == 0, suggestEvery > 0 && n%suggestEvery == 0, footnoteEvery > 0 && n%footnoteEvery == 0)
			}
			list := "kix.lst1"
			if sub%2 == 0 {
				list = "kix.lst2"
			}
			for i := 0; i < b.spec.ListItems; i++ {
				nesting := int64(0)
				if i%3 == 2 {
					nesting = 1
				}
				b.listItem(list, nesting, fmt.Sprintf("Item %d of topic %d.%d %s", i+1, s, j, sentence(sub*7+i)))
			}
			if b.spec.TableEvery > 0 && sub%b.spec.TableEvery == 0 {
				b.table(sub, 4, 3)
			}
		}
	}
	if b.tab == 1 {
		b.comment()
	}
}

// every spreads n events over total items.
func every(total, n int) int {
	if n <= 0 || total == 0 {
		return 0
	}
	return max(total/n, 1)
}

// sentence is a deterministic lorem sentence of 6 to 12 words.
func sentence(seed int) string {
	n := 6 + seed%7
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[(seed*7+i*13)%len(loremWords)]
	}
	s := strings.Join(words, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func (b *largeBuilder) run(text string, style *gdocs.TextStyle) *gdocs.ParagraphElement {
	start := b.idx
	b.idx += doc.UTF16Len(text)
	return &gdocs.ParagraphElement{StartIndex: start, EndIndex: b.idx, TextRun: &gdocs.TextRun{Content: text, TextStyle: style}}
}

func (b *largeBuilder) emit(p *gdocs.Paragraph, start int64) {
	b.content = append(b.content, &gdocs.StructuralElement{StartIndex: start, EndIndex: b.idx, Paragraph: p})
}

func (b *largeBuilder) heading(level int, text, id string) {
	start := b.idx
	p := &gdocs.Paragraph{ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "HEADING_" + strconv.Itoa(level), HeadingID: id}}
	p.Elements = append(p.Elements, b.run(text+"\n", nil))
	b.emit(p, start)
}

func (b *largeBuilder) paragraph(n int, commented, suggested, footnoted bool) {
	b.paragraphs++
	start := b.idx
	p := &gdocs.Paragraph{ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"}}
	first := fmt.Sprintf("Paragraph %d %s", n, sentence(n))
	if n%37 == 0 {
		first += " Don’t forget the 🙂 case."
	}
	firstRun := b.run(first+" ", nil)
	p.Elements = append(p.Elements, firstRun)
	if commented && len(b.quotes) < b.spec.Comments {
		b.quotes = append(b.quotes, quote{text: first, start: firstRun.StartIndex, end: firstRun.StartIndex + doc.UTF16Len(first)})
	}
	if footnoted {
		id := fmt.Sprintf("kix.fn%d", len(b.footnotes)+1)
		num := strconv.Itoa(len(b.footnotes) + 1)
		p.Elements = append(p.Elements, &gdocs.ParagraphElement{StartIndex: b.idx, EndIndex: b.idx + 1, FootnoteReference: &gdocs.FootnoteReference{FootnoteID: id, FootnoteNumber: num}})
		b.idx++
		text := "Footnote " + num + " " + sentence(n+3) + "\n"
		b.footnotes[id] = gdocs.Footnote{FootnoteID: id, Content: []*gdocs.StructuralElement{{StartIndex: 0, EndIndex: doc.UTF16Len(text),
			Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 0, EndIndex: doc.UTF16Len(text), TextRun: &gdocs.TextRun{Content: text}}}}}}}
	}
	p.Elements = append(p.Elements, b.run(sentence(n+1), &gdocs.TextStyle{Bold: true}))
	if suggested {
		id := fmt.Sprintf("suggest.t%d.%d", b.tab, n)
		ins := b.run(" (inserted)", nil)
		ins.TextRun.SuggestedInsertionIDs = []string{id}
		p.Elements = append(p.Elements, ins)
		del := b.run(" (deleted)", nil)
		del.TextRun.SuggestedDeletionIDs = []string{id}
		p.Elements = append(p.Elements, del)
	}
	p.Elements = append(p.Elements, b.run(" "+sentence(n+2)+"\n", nil))
	b.emit(p, start)
}

func (b *largeBuilder) listItem(list string, nesting int64, text string) {
	start := b.idx
	p := &gdocs.Paragraph{ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"}, Bullet: &gdocs.Bullet{ListID: list, NestingLevel: nesting}}
	p.Elements = append(p.Elements, b.run(text+"\n", nil))
	b.emit(p, start)
}

// table lays cells out as the API does: the table starts one index
// before its first row, each cell's paragraph starts one index after
// the cell, and the table ends one index after its last row.
func (b *largeBuilder) table(n, rows, cols int) {
	start := b.idx
	t := &gdocs.Table{Rows: int64(rows), Columns: int64(cols)}
	b.idx++
	for r := 1; r <= rows; r++ {
		row := &gdocs.TableRow{StartIndex: b.idx}
		for c := 1; c <= cols; c++ {
			cell := &gdocs.TableCell{StartIndex: b.idx}
			b.idx++
			pstart := b.idx
			text := fmt.Sprintf("R%dC%d of table %d\n", r, c, n)
			if r == 1 {
				text = fmt.Sprintf("Column %d\n", c)
			}
			p := &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{b.run(text, nil)}}
			cell.Content = []*gdocs.StructuralElement{{StartIndex: pstart, EndIndex: b.idx, Paragraph: p}}
			cell.EndIndex = b.idx
			row.TableCells = append(row.TableCells, cell)
		}
		row.EndIndex = b.idx
		t.TableRows = append(t.TableRows, row)
	}
	b.idx++
	b.content = append(b.content, &gdocs.StructuralElement{StartIndex: start, EndIndex: b.idx, Table: t})
}

func (b *largeBuilder) comment() {
	for i, q := range b.quotes {
		id := strconv.Itoa(i + 1)
		th := gdocs.CommentThread{CommentID: "comment." + id, Status: "OPEN", PlainTextQuote: q.text,
			HeadPost: gdocs.Post{PostID: "post." + id, Content: "Comment " + id + " " + sentence(i), Author: gdocs.PostAuthor{DisplayName: "Reviewer"}, CreateTime: "2026-09-01T00:00:00Z"}}
		if b.spec.Anchored {
			th.AnchorID = "anchor." + id
			if b.anchors == nil {
				b.anchors = map[string]gdocs.CommentAnchor{}
			}
			b.anchors[th.AnchorID] = gdocs.CommentAnchor{AnchorID: th.AnchorID, Ranges: []*gdocs.Range{{StartIndex: q.start, EndIndex: q.end}}}
		}
		b.comments = append(b.comments, th)
	}
}
