package markdown

import (
	"errors"
	"strings"
	"testing"
)

func parse(t *testing.T, src string) *Fragment {
	t.Helper()
	f, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return f
}

func TestParagraphsAndHeadings(t *testing.T) {
	f := parse(t, "# Title\n\nFirst para\nsame para.\n\n## Sub\n\nSecond **bold** and *it* and ~~gone~~ and `code` and [link](https://x.test) here.")
	if len(f.Blocks) != 4 {
		t.Fatalf("blocks = %d", len(f.Blocks))
	}
	if f.Blocks[0].Kind != KindHeading || f.Blocks[0].Level != 1 || f.Blocks[0].Text() != "Title" || f.Blocks[0].Line != 1 {
		t.Fatalf("heading: %+v", f.Blocks[0])
	}
	if f.Blocks[1].Kind != KindParagraph || f.Blocks[1].Text() != "First para same para." || f.Blocks[1].Line != 3 {
		t.Fatalf("paragraph: %+v", f.Blocks[1])
	}
	if f.Blocks[2].Level != 2 {
		t.Fatalf("sub heading: %+v", f.Blocks[2])
	}
	in := f.Blocks[3].Inlines
	want := []Inline{
		{Text: "Second "}, {Text: "bold", Bold: true}, {Text: " and "}, {Text: "it", Italic: true}, {Text: " and "},
		{Text: "gone", Strike: true}, {Text: " and "}, {Text: "code", Code: true}, {Text: " and "}, {Text: "link", Link: "https://x.test"}, {Text: " here."},
	}
	if len(in) != len(want) {
		t.Fatalf("inlines = %+v", in)
	}
	for i := range want {
		if in[i] != want[i] {
			t.Errorf("inline %d = %+v, want %+v", i, in[i], want[i])
		}
	}
}

func TestLists(t *testing.T) {
	f := parse(t, "- one\n- two\n    - nested *x*\n- three\n\n1. first\n2. second\n   1. inner\n\nafter")
	kinds := []string{}
	for _, b := range f.Blocks {
		kinds = append(kinds, string(b.Kind)+":"+b.Text()+":"+itoa(b.Nesting)+":"+itoa(b.ListID)+":"+boolStr(b.Ordered))
	}
	want := "list_item:one:0:1:false list_item:two:0:1:false list_item:nested x:1:1:false list_item:three:0:1:false list_item:first:0:2:true list_item:second:0:2:true list_item:inner:1:2:true paragraph:after:0:0:false"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if !f.Blocks[2].Inlines[1].Italic {
		t.Fatal("nested item styling lost")
	}
}

func TestCodeAndTable(t *testing.T) {
	f := parse(t, "```go\nfmt.Println(1)\n\nx := 2\n```\n\n| A | B |\n|---|---|\n| **1** | two \\| three |\n| 3 | |\n")
	if f.Blocks[0].Kind != KindCode || strings.Join(f.Blocks[0].Lines, "|") != "fmt.Println(1)||x := 2" {
		t.Fatalf("code: %+v", f.Blocks[0])
	}
	tbl := f.Blocks[1]
	if tbl.Kind != KindTable || len(tbl.Table.Rows) != 3 || len(tbl.Table.Rows[0]) != 2 {
		t.Fatalf("table: %+v", tbl)
	}
	if tbl.Table.Rows[1][0][0].Bold != true || tbl.Table.Rows[1][1][0].Text != "two | three" || len(tbl.Table.Rows[2][1]) != 0 {
		t.Fatalf("cells: %+v", tbl.Table.Rows[1])
	}
	if got := tbl.Text(); got != "A\tB\n1\ttwo | three\n3\t" {
		t.Fatalf("table text = %q", got)
	}
	f = parse(t, "    indented code\n")
	if f.Blocks[0].Kind != KindCode || f.Blocks[0].Lines[0] != "indented code" {
		t.Fatalf("indented code: %+v", f.Blocks[0])
	}
}

func TestPlainTextAndSingleParagraph(t *testing.T) {
	f := parse(t, "just words, no formatting")
	if txt, ok := f.SingleParagraph(); !ok || txt != "just words, no formatting" {
		t.Fatalf("single: %q %v", txt, ok)
	}
	f = parse(t, "with **bold**")
	if _, ok := f.SingleParagraph(); ok {
		t.Fatal("styled paragraph is not plain")
	}
	f = parse(t, "a\n\nb")
	if _, ok := f.SingleParagraph(); ok {
		t.Fatal("two paragraphs are not single")
	}
	if f.PlainText() != "a\nb" {
		t.Fatalf("plain = %q", f.PlainText())
	}
	if parse(t, "").Blocks != nil {
		t.Fatal("empty input should give no blocks")
	}
	f = parse(t, "line one  \nline two\r\nline three")
	if got := f.Blocks[0].Text(); got != "line one\vline two line three" {
		t.Fatalf("breaks = %q", got)
	}
	f = parse(t, "see <https://auto.test/x> now")
	if in := f.Blocks[0].Inlines; len(in) != 3 || in[1].Link != "https://auto.test/x" || in[1].Text != "https://auto.test/x" {
		t.Fatalf("autolink: %+v", in)
	}
	f = parse(t, "- [ ] task\n- [x] done")
	if f.Blocks[0].Text() != "task" || f.Blocks[1].Text() != "done" {
		t.Fatalf("task list should drop the checkbox: %q %q", f.Blocks[0].Text(), f.Blocks[1].Text())
	}
}

func TestUnsupported(t *testing.T) {
	cases := map[string]string{
		"a\n\n---\n\nb":                       "horizontal rule",
		"> quoted":                            "block quote",
		"<div>x</div>\n":                      "HTML block",
		"text with ![alt](https://i/x)":       "image",
		"text <b>raw</b> html":                "inline HTML",
		"| only header |\n|---|\n| ![i](u) |": "image",
	}
	for src, construct := range cases {
		_, err := Parse(src)
		var ue *UnsupportedError
		if !errors.As(err, &ue) || !strings.Contains(ue.Construct, construct) {
			t.Errorf("Parse(%q) = %v, want unsupported %s", src, err, construct)
			continue
		}
		if ue.Line < 1 || !strings.Contains(ue.Error(), "line") {
			t.Errorf("line info missing: %v", ue)
		}
	}
	_, err := Parse("a\n\nb\n\n---")
	var ue *UnsupportedError
	if !errors.As(err, &ue) || ue.Line != 5 {
		t.Fatalf("line number: %v", err)
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n))) }

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
