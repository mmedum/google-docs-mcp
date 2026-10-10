//go:build integration

package gapi

// Spike E: what a table and an image in edit_document's content need,
// before content may carry them. Where insertTable puts a table relative
// to a paragraph, at the start and the end of the body; what is left
// after it and whether text can fill that; whether an inline image can
// sit in a paragraph of its own; whether the leftover paragraph can be
// deleted; and whether suggest mode takes a table, its cells and an
// image. Runs against the caller's own account and leaves one clearly
// named scratch document.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawTablesInContent -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// spikeImage is a public PNG Google can fetch: its own logo.
const spikeImage = "https://www.google.com/images/branding/googlelogo/2x/googlelogo_color_272x92dp.png"

func TestRawTablesInContent(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	created, err := c.CreateDocument(ctx, "google-docs-mcp spike E, tables and images (safe to delete)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.DocumentID

	type step struct {
		what string
		reqs func(s []element) []json.RawMessage
		mode string
	}
	probes := []struct {
		name  string
		seed  string
		steps []step
	}{
		{"E1 a table after a paragraph, then text into what is left", "Before.\nAfter.\n", []step{
			{"insertTable 2x2 at the first paragraph's newline", func(s []element) []json.RawMessage {
				return []json.RawMessage{insertTable(t, s[0].end-1, 2, 2)}
			}, ""},
			{"insertText into the paragraph after the table", func(s []element) []json.RawMessage {
				p := after(s, "table")
				return []json.RawMessage{insertAt(t, "Filled.", p.start)}
			}, ""},
		}},
		{"E2 a table at the end of the body", "Only.\n", []step{
			{"insertTable 1x2 at endOfSegmentLocation", func([]element) []json.RawMessage {
				return []json.RawMessage{marshal(t, map[string]any{"insertTable": map[string]any{
					"rows": 1, "columns": 2, "endOfSegmentLocation": map[string]any{}}})}
			}, ""},
		}},
		{"E3 a table before the first paragraph", "First.\n", []step{
			{"insertTable 1x1 at index 1", func([]element) []json.RawMessage {
				return []json.RawMessage{insertTable(t, 1, 1, 1)}
			}, ""},
		}},
		{"E4 the paragraph a table leaves, deleted when text follows", "A.\nC.\n", []step{
			{"insertTable 1x1 at A's newline", func(s []element) []json.RawMessage {
				return []json.RawMessage{insertTable(t, s[0].end-1, 1, 1)}
			}, ""},
			{"deleteContentRange over the paragraph after the table", func(s []element) []json.RawMessage {
				p := after(s, "table")
				return []json.RawMessage{deleteRange(t, p.start, p.end)}
			}, ""},
		}},
		{"E5 an image in a paragraph of its own", "Text.\n", []step{
			{"insertText a newline at Text.'s newline, then insertInlineImage at the new paragraph", func(s []element) []json.RawMessage {
				n := s[0].end - 1
				return []json.RawMessage{insertAt(t, "\n", n), insertImage(t, n+1)}
			}, ""},
		}},
		{"E6 a table, its cells and an image, suggested", "S.\nT.\n", []step{
			{"insertTable 1x2 at S's newline, suggested", func(s []element) []json.RawMessage {
				return []json.RawMessage{insertTable(t, s[0].end-1, 1, 2)}
			}, "SUGGEST"},
			{"insertText into the first cell, suggested", func(s []element) []json.RawMessage {
				tb := first(s, "table")
				return []json.RawMessage{insertAt(t, "cell", tb.cells[0])}
			}, "SUGGEST"},
			{"insertText a newline at T.'s newline and an image there, suggested", func(s []element) []json.RawMessage {
				p := last(s, "paragraph")
				return []json.RawMessage{insertAt(t, "\n", p.end-1), insertImage(t, p.end)}
			}, "SUGGEST"},
		}},
	}
	for _, p := range probes {
		emptyBody(t, c, ctx, id)
		if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: []json.RawMessage{insertAt(t, p.seed, 1)}}); err != nil {
			t.Fatalf("%s: seed: %v", p.name, err)
		}
		t.Logf("=== %s ===\nseeded:\n%s", p.name, show(structure(t, c, ctx, id)))
		for _, st := range p.steps {
			s := structure(t, c, ctx, id)
			var wc *WriteControl
			if st.mode != "" {
				wc = &WriteControl{WriteMode: st.mode}
			}
			res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: st.reqs(s), WriteControl: wc})
			if err != nil {
				t.Logf("%s -> refused: %v", st.what, err)
				continue
			}
			reply, _ := json.Marshal(res.Replies)
			t.Logf("%s -> replies %s\n%s", st.what, reply, show(structure(t, c, ctx, id)))
		}
	}
	t.Logf("one scratch document left behind; Drive search title:\"spike E\" finds it")
}

func insertTable(t *testing.T, index, rows, cols int) json.RawMessage {
	return marshal(t, map[string]any{"insertTable": map[string]any{
		"rows": rows, "columns": cols, "location": map[string]any{"index": index}}})
}

func insertImage(t *testing.T, index int) json.RawMessage {
	return marshal(t, map[string]any{"insertInlineImage": map[string]any{
		"uri": spikeImage, "location": map[string]any{"index": index}}})
}

// element is one body element, flattened: a paragraph with its text and
// whether it holds an image, or a table with each cell's first index.
type element struct {
	kind       string
	start, end int
	text       string
	image      bool
	suggested  bool
	rows, cols int
	cells      []int
	cellText   []string
}

func first(s []element, kind string) element {
	for _, e := range s {
		if e.kind == kind {
			return e
		}
	}
	return element{}
}

func last(s []element, kind string) element {
	var out element
	for _, e := range s {
		if e.kind == kind {
			out = e
		}
	}
	return out
}

// after is the element right after the first of kind.
func after(s []element, kind string) element {
	for i, e := range s {
		if e.kind == kind && i+1 < len(s) {
			return s[i+1]
		}
	}
	return element{}
}

func show(s []element) string {
	var b strings.Builder
	for _, e := range s {
		switch e.kind {
		case "table":
			fmt.Fprintf(&b, "  [%d,%d) table %dx%d cells at %v %q suggested=%t\n", e.start, e.end, e.rows, e.cols, e.cells, e.cellText, e.suggested)
		default:
			fmt.Fprintf(&b, "  [%d,%d) paragraph %q image=%t suggested=%t\n", e.start, e.end, e.text, e.image, e.suggested)
		}
	}
	return b.String()
}

// structure reads the first tab's body from the raw GET, suggestions
// inline, and flattens it.
func structure(t *testing.T, c *Client, ctx context.Context, id string) []element {
	t.Helper()
	q := url.Values{}
	q.Set("includeTabsContent", "true")
	q.Set("suggestionsViewMode", SuggestionsInline)
	body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var d struct {
		Tabs []struct {
			DocumentTab struct {
				Body struct {
					Content []rawElement `json:"content"`
				} `json:"body"`
			} `json:"documentTab"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var out []element
	for _, el := range d.Tabs[0].DocumentTab.Body.Content {
		switch {
		case el.Paragraph != nil:
			e := element{kind: "paragraph", start: el.StartIndex, end: el.EndIndex}
			for _, pe := range el.Paragraph.Elements {
				if pe.TextRun != nil {
					e.text += pe.TextRun.Content
					e.suggested = e.suggested || len(pe.TextRun.SuggestedInsertionIDs) > 0
				}
				if pe.InlineObjectElement != nil {
					e.image = true
					e.suggested = e.suggested || len(pe.InlineObjectElement.SuggestedInsertionIDs) > 0
				}
			}
			out = append(out, e)
		case el.Table != nil:
			e := element{kind: "table", start: el.StartIndex, end: el.EndIndex, rows: el.Table.Rows, cols: el.Table.Columns,
				suggested: len(el.Table.SuggestedInsertionIDs) > 0}
			for _, row := range el.Table.TableRows {
				for _, cell := range row.TableCells {
					if len(cell.Content) > 0 {
						e.cells = append(e.cells, cell.Content[0].StartIndex)
					}
					var txt string
					for _, ce := range cell.Content {
						if ce.Paragraph != nil {
							for _, pe := range ce.Paragraph.Elements {
								if pe.TextRun != nil {
									txt += pe.TextRun.Content
								}
							}
						}
					}
					e.cellText = append(e.cellText, txt)
				}
			}
			out = append(out, e)
		}
	}
	return out
}

type rawElement struct {
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
	Paragraph  *struct {
		Elements []struct {
			TextRun *struct {
				Content               string   `json:"content"`
				SuggestedInsertionIDs []string `json:"suggestedInsertionIds"`
			} `json:"textRun"`
			InlineObjectElement *struct {
				SuggestedInsertionIDs []string `json:"suggestedInsertionIds"`
			} `json:"inlineObjectElement"`
		} `json:"elements"`
	} `json:"paragraph"`
	Table *struct {
		Rows                  int      `json:"rows"`
		Columns               int      `json:"columns"`
		SuggestedInsertionIDs []string `json:"suggestedInsertionIds"`
		TableRows             []struct {
			TableCells []struct {
				Content []rawElement `json:"content"`
			} `json:"tableCells"`
		} `json:"tableRows"`
	} `json:"table"`
}

// emptyBody empties the body, tables and all, so each probe starts alike.
func emptyBody(t *testing.T, c *Client, ctx context.Context, id string) {
	t.Helper()
	s := structure(t, c, ctx, id)
	if len(s) == 0 {
		return
	}
	end := s[len(s)-1].end
	if end-1 <= 1 {
		return
	}
	if _, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: []json.RawMessage{deleteRange(t, 1, end-1)}}); err != nil {
		t.Fatalf("reset: %v", err)
	}
}
