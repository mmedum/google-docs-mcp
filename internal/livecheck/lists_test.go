//go:build live

package livecheck

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
)

// The shapes spike F measured, written through the server: list items
// next to existing ones must land at the levels the content gives them,
// in the list they belong to, with no tab left in the text.
const listContent = `# Lists

- A
    - a1
- B
    - b1

Para one.

1. One
2. Two

Para two.`

// listWant is the document once every edit has landed: each paragraph's
// level, or -1 for no list, and which list it is in. Items sharing a
// letter share a list; different letters are different lists.
var listWant = []struct {
	text  string
	level int
	list  string
}{
	{"A", 0, "L"},
	{"Note.", -1, ""},
	{"n", 0, "N"},
	{"n1", 1, "N"},
	{"a1", 1, "L"},
	{"B", 0, "L"},
	{"b0", 1, "L"},
	{"b1", 1, "L"},
	{"c", 1, "L"},
	{"Para one.", -1, ""},
	{"Zero", 0, "M"},
	{"Zero-a", 1, "M"},
	{"One", 0, "M"},
	{"Two", 0, "M"},
	{"Para two.", -1, ""},
	{"tail", 0, "T"},
	{"tail child", 1, "T"},
}

func TestLiveLists(t *testing.T) {
	d := start(t)
	for _, mode := range []string{"direct", "suggest"} {
		_, sc := d.okStruct(mode+": create", "create_document", map[string]any{
			"title": "google-docs-mcp live lists " + mode + " (safe to delete)", "content": listContent})
		doc := str(sc, "id")
		if doc == "" {
			t.Fatal(shown(mode+": create returned no document id", 100))
		}
		// Bottom up, so a pending suggestion never sits above a target.
		handle := func(text string) string {
			read := d.ok(mode+": read for "+text, "read_document", map[string]any{"document": doc, "with_handles": true})
			h := first(regexp.MustCompile(`(?m)^\[(p\d+)\][ \t]+(?:-|\d+\.)[ \t]+`+regexp.QuoteMeta(text)+`$`), read)
			if h == "" {
				t.Fatal(shown(fmt.Sprintf("%s: no list item %q in the read:\n%s", mode, text, read), 1200))
			}
			return h
		}
		edit := func(label string, op map[string]any) {
			d.ok(mode+": "+label, "edit_document", map[string]any{"document": doc, "mode": mode, "ops": []any{op}})
		}
		edit("append a nested list", map[string]any{"op": "append", "content": "- tail\n    - tail child"})
		edit("an item's text made bold", map[string]any{"op": "replace", "content": "**Two**",
			"target": map[string]any{"text": "Two"}})
		edit("a nested item at the top of a numbered list", map[string]any{"op": "insert", "content": "1. Zero\n    1. Zero-a",
			"location": map[string]any{"at": "before", "of": map[string]any{"handle": handle("One")}}})
		edit("an item after the last nested one", map[string]any{"op": "insert", "content": "- c",
			"location": map[string]any{"at": "after", "of": map[string]any{"handle": handle("b1")}}})
		edit("an item after a nested one gains a child", map[string]any{"op": "replace", "content": "- B\n    - b0",
			"target": map[string]any{"handle": handle("B")}})
		edit("a paragraph and a list inside a nested list", map[string]any{"op": "insert", "content": "Note.\n\n- n\n    - n1",
			"location": map[string]any{"at": "before", "of": map[string]any{"handle": handle("a1")}}})
		if mode == "suggest" {
			d.ok("suggest: accept them all", "review_suggestion", map[string]any{"document": doc, "action": "accept", "all": true})
		}
		checkLists(t, mode, d.ok(mode+": read the lists", "read_document", map[string]any{"document": doc, "format": "raw", "max_chars": 100000}))
	}
}

// checkLists holds a raw read to listWant.
func checkLists(t *testing.T, mode, raw string) {
	t.Helper()
	var elems []*gdocs.StructuralElement
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		t.Fatal(shown(fmt.Sprintf("%s: the raw read is not the elements: %v\n%s", mode, err, raw), 800))
	}
	// Collected and logged once, through the redactor.
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, mode+": "+fmt.Sprintf(format, args...)) }
	defer func() {
		if len(problems) > 0 {
			t.Error(shown(strings.Join(problems, "\n"), 4000))
		}
	}()
	type para struct {
		level  int
		list   string
		indent float64
	}
	got := map[string]para{}
	for _, el := range elems {
		p := el.Paragraph
		if p == nil {
			continue
		}
		var text strings.Builder
		for _, e := range p.Elements {
			if e.TextRun != nil {
				text.WriteString(e.TextRun.Content)
			}
		}
		s := strings.TrimSuffix(text.String(), "\n")
		if strings.Contains(s, "\t") {
			bad("a tab is left in %q", s)
		}
		g := para{level: -1}
		if p.Bullet != nil {
			g.level, g.list = int(p.Bullet.NestingLevel), p.Bullet.ListID
		}
		if p.ParagraphStyle != nil && p.ParagraphStyle.IndentStart != nil {
			g.indent = p.ParagraphStyle.IndentStart.Magnitude
		}
		got[strings.TrimSpace(s)] = g
	}
	lists := map[string]string{} // listWant's letter -> list id
	ids := map[string]string{}   // list id -> letter
	for _, w := range listWant {
		g, ok := got[w.text]
		if !ok {
			bad("no paragraph %q", w.text)
			continue
		}
		if g.level != w.level {
			bad("%q at level %d, want %d", w.text, g.level, w.level)
		}
		// A list's indent is 36pt a level; a paragraph out of a list has
		// none left over from a bullet it dropped.
		if want := 36 * float64(w.level+1); g.indent != want {
			bad("%q indented %gpt, want %gpt", w.text, g.indent, want)
		}
		if w.list == "" {
			continue
		}
		if id, seen := lists[w.list]; seen && id != g.list {
			bad("%q is in another list than the items before it of list %s", w.text, w.list)
		}
		if letter, seen := ids[g.list]; seen && letter != w.list {
			bad("%q shares a list with list %s's items", w.text, letter)
		}
		lists[w.list], ids[g.list] = g.list, w.list
	}
}
