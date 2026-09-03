package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/markdown"
)

func frag(t *testing.T, src string) *markdown.Fragment {
	t.Helper()
	f, err := markdown.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type reqView struct {
	kind  string
	body  map[string]any
	rng   [2]int64
	index int64
}

func view(t *testing.T, r json.RawMessage) reqView {
	t.Helper()
	var m map[string]map[string]any
	if err := json.Unmarshal(r, &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		rv := reqView{kind: k, body: v}
		if rg, ok := v["range"].(map[string]any); ok {
			rv.rng = [2]int64{int64(rg["startIndex"].(float64)), int64(rg["endIndex"].(float64))}
		}
		if loc, ok := v["location"].(map[string]any); ok {
			rv.index = int64(loc["index"].(float64))
		}
		return rv
	}
	t.Fatal("empty request")
	return reqView{}
}

func TestCompileFragmentLayout(t *testing.T) {
	f := frag(t, "# Title\n\nPlain **bold** text.\n\n- one\n    - two\n\n1. first\n\n```\ncode\n```")
	c, err := CompileFragment(f, Loc{Index: 10, TabID: "t.0"}, FragmentOptions{Suffix: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "Title\nPlain bold text.\none\n\ttwo\nfirst\ncode\n" {
		t.Fatalf("text = %q", c.Text)
	}
	if c.Start != 10 || c.End != 10+c.Length || c.Length != 43 {
		t.Fatalf("bounds %d..%d len %d", c.Start, c.End, c.Length)
	}
	var kinds []string
	for _, r := range c.Requests {
		kinds = append(kinds, Kind(r))
	}
	want := "insertText updateTextStyle updateParagraphStyle updateParagraphStyle updateTextStyle updateParagraphStyle updateParagraphStyle updateParagraphStyle updateParagraphStyle updateTextStyle createParagraphBullets createParagraphBullets"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("kinds:\n got %s\nwant %s", got, want)
	}
	ins := view(t, c.Requests[0])
	if ins.index != 10 || ins.body["text"] != c.Text || ins.body["location"].(map[string]any)["tabId"] != "t.0" {
		t.Fatalf("insert: %+v", ins.body)
	}
	if clear := view(t, c.Requests[1]); clear.rng != [2]int64{10, 53} || clear.body["fields"] != "*" {
		t.Fatalf("clear: %+v", clear)
	}
	// "Title" is 10..15 and a heading.
	h := view(t, c.Requests[2])
	if h.rng != [2]int64{10, 15} || h.body["paragraphStyle"].(map[string]any)["namedStyleType"] != "HEADING_1" {
		t.Fatalf("heading: %+v", h)
	}
	// "Plain bold text." starts at 16; bold covers "bold" at 22..26.
	b := view(t, c.Requests[4])
	if b.rng != [2]int64{22, 26} || b.body["textStyle"].(map[string]any)["bold"] != true || b.body["fields"] != "bold" {
		t.Fatalf("bold: %+v", b)
	}
	// Code line "code" at 48..52 gets the code font.
	code := view(t, c.Requests[9])
	if code.rng != [2]int64{48, 52} || code.body["textStyle"].(map[string]any)["weightedFontFamily"].(map[string]any)["fontFamily"] != CodeFont {
		t.Fatalf("code: %+v", code)
	}
	// Lists last, descending: numbered "first" (42..47) before bullets (33..41 covers "one\n\ttwo").
	num := view(t, c.Requests[10])
	bul := view(t, c.Requests[11])
	if num.rng != [2]int64{42, 47} || num.body["bulletPreset"] != NumberedPreset {
		t.Fatalf("numbered: %+v", num)
	}
	if bul.rng != [2]int64{33, 41} || bul.body["bulletPreset"] != BulletPreset {
		t.Fatalf("bullets: %+v", bul)
	}
}

func TestCompileFragmentPrefixAndBullets(t *testing.T) {
	f := frag(t, "tail")
	c, err := CompileFragment(f, Loc{Index: 99, SegmentID: "kix.h1"}, FragmentOptions{Prefix: true, NearBullet: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "\ntail" || c.Start != 100 || c.End != 104 {
		t.Fatalf("prefix layout: %q %d..%d", c.Text, c.Start, c.End)
	}
	kinds := []string{}
	for _, r := range c.Requests {
		kinds = append(kinds, Kind(r))
	}
	if strings.Join(kinds, " ") != "insertText updateTextStyle updateParagraphStyle deleteParagraphBullets" {
		t.Fatalf("kinds = %v", kinds)
	}
	if v := view(t, c.Requests[3]); v.rng != [2]int64{100, 104} || v.body["range"].(map[string]any)["segmentId"] != "kix.h1" {
		t.Fatalf("delete bullets: %+v", v)
	}
	// Empty paragraph ranges cover their newline so the API accepts them.
	c, err = CompileFragment(frag(t, "a\n\n\n\nb"), Loc{Index: 1}, FragmentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "a\nb" {
		t.Fatalf("blank paragraphs collapse in markdown: %q", c.Text)
	}
	if _, err := CompileFragment(&markdown.Fragment{}, Loc{}, FragmentOptions{}); err == nil {
		t.Fatal("empty fragment should fail")
	}
	if _, err := CompileFragment(frag(t, "| a |\n|---|\n| b |"), Loc{}, FragmentOptions{}); err == nil || !strings.Contains(err.Error(), "edit_table") {
		t.Fatalf("table should be refused: %v", err)
	}
}

func TestCompileFragmentUTF16(t *testing.T) {
	c, err := CompileFragment(frag(t, "x 🎉 **y**"), Loc{Index: 5}, FragmentOptions{Suffix: true})
	if err != nil {
		t.Fatal(err)
	}
	// "x 🎉 " is 5 UTF-16 units (emoji counts twice): bold "y" at 10..11.
	if c.Length != 7 || c.Text != "x 🎉 y\n" {
		t.Fatalf("length %d text %q", c.Length, c.Text)
	}
	b := view(t, c.Requests[3])
	if b.rng != [2]int64{10, 11} {
		t.Fatalf("bold range %v", b.rng)
	}
}

func TestRequestBuilders(t *testing.T) {
	r := Rng{Start: 1, End: 5, SegmentID: "s", TabID: "t"}
	checks := map[string]json.RawMessage{
		"insertText":             InsertText("x", Loc{Index: 3, TabID: "t"}),
		"deleteContentRange":     DeleteRange(r),
		"updateTextStyle":        UpdateTextStyle(r, TextStyleSpec{Bold: boolp(false), Font: "none", Foreground: "#ff0000", Background: "none", Link: "none", SizePt: 12, Baseline: "SUPERSCRIPT"}),
		"updateParagraphStyle":   UpdateParagraphStyle(r, ParagraphStyleSpec{NamedStyle: "HEADING_2", Alignment: "CENTER", LineSpacing: 115, SpaceAbovePt: floatp(6), KeepWithNext: boolp(true)}),
		"createParagraphBullets": CreateBullets(r, CheckboxPreset),
		"deleteParagraphBullets": DeleteBullets(r),
		"replaceAllText":         ReplaceAllText("a", "b", true, "t"),
		"insertPageBreak":        InsertPageBreak(Loc{Index: 2}),
		"createHeader":           CreateHeader("t"),
		"createFooter":           CreateFooter(""),
		"createFootnote":         CreateFootnote(Loc{Index: 4}),
		"insertComment":          InsertComment("hi", r, "a@b.test"),
		"acceptSuggestion":       AcceptSuggestion("s1"),
		"rejectSuggestion":       RejectSuggestion("s2"),
	}
	for kind, req := range checks {
		if Kind(req) != kind {
			t.Errorf("%s: kind = %s (%s)", kind, Kind(req), req)
		}
	}
	ts := view(t, checks["updateTextStyle"])
	if ts.body["fields"] != "bold,weightedFontFamily,fontSize,foregroundColor,backgroundColor,link,baselineOffset" {
		t.Fatalf("text style fields = %v", ts.body["fields"])
	}
	fg := ts.body["textStyle"].(map[string]any)["foregroundColor"].(map[string]any)["color"].(map[string]any)["rgbColor"].(map[string]any)
	if fg["red"] != 1.0 || fg["green"] != nil {
		t.Fatalf("colour = %v", fg)
	}
	ps := view(t, checks["updateParagraphStyle"])
	if ps.body["fields"] != "namedStyleType,alignment,lineSpacing,spaceAbove,keepWithNext" {
		t.Fatalf("paragraph fields = %v", ps.body["fields"])
	}
	ra := view(t, checks["replaceAllText"])
	if ra.body["tabsCriteria"].(map[string]any)["tabIds"].([]any)[0] != "t" || ra.body["containsText"].(map[string]any)["matchCase"] != true {
		t.Fatalf("replaceAll = %v", ra.body)
	}
	if !ValidColor("#00ff00") || !ValidColor("none") || !ValidColor("") || ValidColor("red") || ValidColor("#12345") {
		t.Fatal("ValidColor")
	}
	if Kind(json.RawMessage(`nope`)) != "" || Kind(json.RawMessage(`{}`)) != "" {
		t.Fatal("Kind on bad input")
	}
	if !(TextStyleSpec{}).IsZero() || !(ParagraphStyleSpec{}).IsZero() {
		t.Fatal("IsZero")
	}
	if strings.Contains(string(CreateFooter("")), "sectionBreakLocation") {
		t.Fatal("footer without tab should omit location")
	}
}

func floatp(f float64) *float64 { return &f }
