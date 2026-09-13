package render_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

// unmodelled is a response carrying fields internal/gdocs does not
// model: two the API really publishes and this server really drops
// (sectionStyle.marginTop, tableRow.tableRowStyle), and one invented, to
// stand for every field Google adds after this test was written.
const unmodelled = `{
  "documentId": "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX",
  "revisionId": "r",
  "tabs": [{
    "tabProperties": {"tabId": "t.0", "title": "Tab 1"},
    "documentTab": {"body": {"content": [
      {"endIndex": 1, "sectionBreak": {"sectionStyle": {
        "sectionType": "CONTINUOUS",
        "marginTop": {"magnitude": 72, "unit": "PT"},
        "pageNumberStart": 7
      }}},
      {"startIndex": 1, "endIndex": 12, "paragraph": {
        "paragraphStyle": {"namedStyleType": "NORMAL_TEXT"},
        "elements": [{"startIndex": 1, "endIndex": 12, "textRun": {
          "content": "Some text.\n",
          "textStyle": {},
          "fieldGoogleAddedLater": {"nested": true}
        }}]
      }},
      {"startIndex": 12, "endIndex": 20, "table": {
        "rows": 1, "columns": 1,
        "tableRows": [{
          "startIndex": 12, "endIndex": 20,
          "tableRowStyle": {"minRowHeight": {"magnitude": 30, "unit": "PT"}},
          "tableCells": [{"startIndex": 13, "endIndex": 19, "content": [
            {"startIndex": 13, "endIndex": 19, "paragraph": {
              "paragraphStyle": {"namedStyleType": "NORMAL_TEXT"},
              "elements": [{"startIndex": 13, "endIndex": 19, "textRun": {"content": "Cell\n", "textStyle": {}}}]
            }}
          ]}]
        }]
      }}
    ]}}
  }]
}`

// TestRawReturnsTheAPIsBytes is the point of keeping them: format: raw
// promises the Docs API's JSON, and re-encoding the wire types could
// only ever return the fields those types model. That gap is how #46
// came to be filed as a broken write — the raw read had dropped the
// field that proved the write had worked.
func TestRawReturnsTheAPIsBytes(t *testing.T) {
	var w gdocs.Document
	if err := json.Unmarshal([]byte(unmodelled), &w); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Parse(&w)
	if err != nil {
		t.Fatal(err)
	}
	seg := d.Tabs[0].Body
	res, err := render.Raw(seg, 0, len(seg.Blocks), 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"marginTop"`,             // SectionStyle models 14 fewer fields than the API has
		`"pageNumberStart"`,       // one of them
		`"tableRowStyle"`,         // TableRow has the suggestion map but not the style
		`"minRowHeight"`,          // inside it
		`"fieldGoogleAddedLater"`, /* whatever comes next */
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("format raw dropped %s:\n%s", want, res.Text)
		}
	}
	// Still one JSON array of elements, and still compact: the budget is
	// in characters and Google indents by default, so a raw read that
	// passed the indentation through would return a third of the document
	// for the same max_chars.
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(res.Text), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, res.Text)
	}
	if len(arr) != len(seg.Blocks) {
		t.Errorf("want %d elements, got %d", len(seg.Blocks), len(arr))
	}
	if strings.Contains(res.Text, "\n  ") {
		t.Errorf("output kept the API's indentation:\n%s", res.Text)
	}
	if res.Chars != len(res.Text) {
		t.Errorf("Chars %d does not match the text length %d", res.Chars, len(res.Text))
	}
}

// TestRawFallsBackToTheWireTypes covers a document that never came from
// a response — every fixture built in Go, and anything this server
// constructs — where there are no bytes to return.
func TestRawFallsBackToTheWireTypes(t *testing.T) {
	w := &gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
				{StartIndex: 1, EndIndex: 7, Paragraph: &gdocs.Paragraph{
					ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
					Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: "Built\n"}}},
				}},
			}}},
		}},
	}
	if w.Tabs[0].DocumentTab.Body.Content[0].RawJSON() != nil {
		t.Fatal("an element built in Go should carry no raw bytes")
	}
	d, err := doc.Parse(w)
	if err != nil {
		t.Fatal(err)
	}
	seg := d.Tabs[0].Body
	res, err := render.Raw(seg, 0, len(seg.Blocks), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, `"content":"Built\n"`) {
		t.Errorf("the fallback should encode the wire types:\n%s", res.Text)
	}
}

// TestRawBudgetCutsAtElementBoundaries guards the property the budget
// has always had, now that the strings come from somewhere else.
func TestRawBudgetCutsAtElementBoundaries(t *testing.T) {
	var w gdocs.Document
	if err := json.Unmarshal([]byte(unmodelled), &w); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Parse(&w)
	if err != nil {
		t.Fatal(err)
	}
	seg := d.Tabs[0].Body
	res, err := render.Raw(seg, 0, len(seg.Blocks), 300)
	if err != nil {
		t.Fatal(err)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(res.Text), &arr); err != nil {
		t.Fatalf("a truncated raw read must still be valid JSON: %v\n%s", err, res.Text)
	}
	if len(arr) >= len(seg.Blocks) {
		t.Errorf("a 300-character budget should not fit every element: got %d", len(arr))
	}
}
