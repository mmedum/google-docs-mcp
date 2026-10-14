package mdfile

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// bytesOf is what Apply wrote, for a test that compares bytes.
func bytesOf(f *File, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return f.Source(), nil
}

// diff is empty when want and got are deeply equal, and otherwise shows
// both.
func diff(want, got any) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	return fmt.Sprintf("want %#v\n got %#v", want, got)
}

// docsFile is synthetic, in the shape the Docs editor writes a .md file
// that holds a comment (spike D, live 2026-10-13): markers around the
// quoted passage, which spans two paragraphs, and the block at the end.
const docsFile = "# Notes\n\nIntro with **bold** text.\n\n" +
	"> <!-- gdoc_comment_begin: c1 -->A quoted line.\n\n" +
	"Text with a footnote.[^1]<!-- gdoc_comment_end: c1 -->\n\n" +
	"## Lists\n\n- one  \n- two  \n\n![An image](https://example.com/a.png)\n\n" +
	"[^1]: The footnote body.\n\n" +
	"---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    token: AbC123opaque\n    head:\n" +
	"      author: Ada Example\n      time: '2026-01-02T03:04:05.000Z'\n      body: this is a comment\n" +
	"      quote: |-\n        A quoted line.\n        Text with a footnote.\n---\n"

const docsVisible = "# Notes\n\nIntro with **bold** text.\n\n" +
	"> A quoted line.\n\nText with a footnote.[^1]\n\n" +
	"## Lists\n\n- one  \n- two  \n\n![An image](https://example.com/a.png)\n\n" +
	"[^1]: The footnote body."

func TestParseSeparatesTheMarkup(t *testing.T) {
	f := Parse(docsFile)
	if d := diff(docsVisible, f.Text()); d != "" {
		t.Errorf("Text() :\n%s", d)
	}
	cs := f.Comments()
	if len(cs) != 1 {
		t.Fatalf("Comments() = %d entries, want 1", len(cs))
	}
	c := cs[0]
	if got := f.Text()[c.Start:c.End]; got != "A quoted line.\n\nText with a footnote.[^1]" {
		t.Errorf("c1 encloses %q", got)
	}
	c.Start, c.End = 0, 0
	want := Comment{Key: "c1", Status: "ACTIVE", Token: true, Author: "Ada Example", Time: "2026-01-02T03:04:05.000Z",
		Body: "this is a comment", Quote: "A quoted line.\nText with a footnote."}
	if d := diff(want, c); d != "" {
		t.Errorf("c1 :\n%s", d)
	}
}

func TestParseReadsABlockFollowedByTextAsText(t *testing.T) {
	// Docs showed such a block as body text, name and token included.
	src := docsFile + "\nAppended after the block.\n"
	f := Parse(src)
	if len(f.Comments()) != 1 || f.Comments()[0].Token || f.Comments()[0].Body != "" {
		t.Errorf("Comments() = %+v, want only the marker pair", f.Comments())
	}
	if !strings.Contains(f.Text(), "gdoc_comments:\n  c1:") {
		t.Errorf("Text() does not hold the block: %q", f.Text())
	}
}

func TestParseWithoutMarkupIsTheText(t *testing.T) {
	src := "# Plain\n\nNo comments here.\n"
	f := Parse(src)
	if f.Text() != src || len(f.Comments()) != 0 {
		t.Errorf("Parse(%q) = text %q, %d comments", src, f.Text(), len(f.Comments()))
	}
}

func TestApplyWithoutEditsKeepsTheBytes(t *testing.T) {
	for _, src := range []string{docsFile, "plain\n", "", "no newline", "---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n---\n"} {
		got, err := bytesOf(Parse(src).Apply(nil))
		if err != nil || got != src {
			t.Errorf("Apply(nil) on %q = %q, %v", src, got, err)
		}
	}
}

func TestApplyKeepsMarkersWhereTheTextIs(t *testing.T) {
	f := Parse(docsFile)
	at := strings.Index(f.Text(), "bold")
	got, err := bytesOf(f.Apply([]Edit{{Start: at, End: at + len("bold"), Insert: "strong"}}))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(docsFile, "**bold**", "**strong**", 1); got != want {
		t.Errorf("Apply() =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyInsideAComment(t *testing.T) {
	// The probe found the comment survives and its anchor grows.
	f := Parse(docsFile)
	at := strings.Index(f.Text(), "quoted")
	got, err := bytesOf(f.Apply([]Edit{{Start: at, End: at + len("quoted"), Insert: "cited"}}))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(docsFile, "c1 -->A quoted line.", "c1 -->A cited line.", 1); got != want {
		t.Errorf("Apply() =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyPlacesMarkersByRule(t *testing.T) {
	// Visible "ab[c]de": the comment covers c, begin at 2 and end at 3.
	src := "ab<!-- gdoc_comment_begin: c1 -->c<!-- gdoc_comment_end: c1 -->de"
	b, e := "<!-- gdoc_comment_begin: c1 -->", "<!-- gdoc_comment_end: c1 -->"
	tests := []struct {
		name string
		edit Edit
		want string
	}{
		{"insertion where the comment begins goes before it", Edit{2, 2, "X"}, "abX" + b + "c" + e + "de"},
		{"insertion where the comment ends goes after it", Edit{3, 3, "X"}, "ab" + b + "c" + e + "Xde"},
		{"a replacement across the begin marker takes it to the front", Edit{1, 3, "XY"}, "a" + b + "XY" + e + "de"},
		{"a replacement across the end marker takes it to the back", Edit{2, 4, "XY"}, "ab" + b + "XY" + e + "e"},
		{"a replacement of the passage stays inside it", Edit{2, 3, "XY"}, "ab" + b + "XY" + e + "de"},
		{"a replacement ending at the begin marker stays outside", Edit{0, 2, "XY"}, "XY" + b + "c" + e + "de"},
		{"a replacement starting at the end marker stays outside", Edit{3, 5, "XY"}, "ab" + b + "c" + e + "XY"},
		{"a replacement around the passage encloses it", Edit{1, 4, "XYZ"}, "a" + b + "XYZ" + e + "e"},
		{"an insertion into an empty passage keeps it empty", Edit{0, 0, ""}, src},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bytesOf(Parse(src).Apply([]Edit{tc.edit}))
			if err != nil || got != tc.want {
				t.Errorf("Apply(%+v) = %q, %v; want %q", tc.edit, got, err, tc.want)
			}
		})
	}
	empty := "a" + b + e + "b"
	got, err := bytesOf(Parse(empty).Apply([]Edit{{1, 1, "X"}}))
	if want := "a" + b + e + "Xb"; err != nil || got != want {
		t.Errorf("insertion at an empty passage = %q, %v; want %q", got, err, want)
	}
}

func TestApplyKeepsTheBlockLast(t *testing.T) {
	f := Parse(docsFile)
	end := len(f.Text())
	got, err := bytesOf(f.Apply([]Edit{{Start: end, End: end, Insert: "\n\nAppended paragraph.\n"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(docsFile, "[^1]: The footnote body.\n\n---", "[^1]: The footnote body.\n\nAppended paragraph.\n\n---", 1)
	if got != want {
		t.Errorf("Apply() =\n%s\nwant\n%s", got, want)
	}
}

func TestApplyRefusesMarkup(t *testing.T) {
	f := Parse(docsFile)
	for _, ins := range []string{
		"<!-- gdoc_comment_begin: c9 -->forged",
		"<!-- gdoc_comment_end: c1 -->",
		"\n\n---\ngdoc_comments:\n  c9:\n    status: ACTIVE\n---\n",
	} {
		if _, err := f.Apply([]Edit{{Start: 0, End: 0, Insert: ins}}); !errors.Is(err, ErrMarkup) {
			t.Errorf("Apply(insert %q) = %v, want ErrMarkup", ins, err)
		}
	}
	// Prose about the block is not the block.
	if _, err := f.Apply([]Edit{{Start: 0, End: 0, Insert: "Docs writes a gdoc_comments: block.\n\n"}}); err != nil {
		t.Errorf("prose naming the key: %v", err)
	}
}

func TestApplyRefusesOverlaps(t *testing.T) {
	f := Parse("abcdef")
	for _, es := range [][]Edit{
		{{0, 3, "x"}, {2, 4, "y"}},
		{{1, 1, "x"}, {0, 3, "y"}},
		{{0, 7, "x"}},
		{{3, 2, "x"}},
	} {
		if _, err := f.Apply(es); err == nil {
			t.Errorf("Apply(%+v) accepted", es)
		}
	}
	// Edits that only touch apply in a defined order: insertions at one
	// point as given, and before a replacement starting there.
	for _, tc := range []struct {
		edits []Edit
		want  string
	}{
		{[]Edit{{3, 4, "Y"}, {0, 1, "X"}, {1, 3, "-"}}, "X-Yef"},
		{[]Edit{{1, 3, "y"}, {1, 1, "x"}}, "axydef"},
		{[]Edit{{0, 2, "x"}, {2, 2, "y"}}, "xycdef"},
		{[]Edit{{6, 6, "1"}, {6, 6, "2"}}, "abcdef12"},
	} {
		if got, err := bytesOf(f.Apply(tc.edits)); err != nil || got != tc.want {
			t.Errorf("Apply(%+v) = %q, %v; want %q", tc.edits, got, err, tc.want)
		}
	}
}

func TestDestroys(t *testing.T) {
	f := Parse(docsFile)
	at := func(s string) (int, int) {
		i := strings.Index(f.Text(), s)
		if i < 0 {
			t.Fatalf("%q not in the text", s)
		}
		return i, i + len(s)
	}
	tests := []struct {
		text string
		want []Hit
	}{
		{"Intro with", nil},
		{"quoted", []Hit{{"comment", "c1"}}},
		{"> A", []Hit{{"comment", "c1"}}},
		{"## Lists", nil},
		{"(https://example.com/a.png)", []Hit{{"image", "![An image](https://example.com/a.png)"}}},
		{"[^1]: The", []Hit{{"footnote", "[^1]"}}},
		{"footnote.[^1]", []Hit{{"comment", "c1"}, {"footnote", "[^1]"}}},
	}
	for _, tc := range tests {
		s, e := at(tc.text)
		if d := diff(tc.want, f.Destroys(s, e)); d != "" {
			t.Errorf("Destroys(%q) :\n%s", tc.text, d)
		}
	}
	s, _ := at("quoted")
	if got := f.Destroys(s, s); got != nil {
		t.Errorf("an insertion destroys %v", got)
	}
	// "> " ends where the passage begins, so deleting it touches nothing.
	if s, e := at("> "); f.Destroys(s, e) != nil {
		t.Errorf("Destroys(\"> \") = %v", f.Destroys(s, e))
	}
}

func TestMinimalEdits(t *testing.T) {
	got := MinimalEdits("The quick brown fox.", "The slow brown fox jumps.", 10)
	want := []Edit{{14, 19, "slow"}, {29, 29, " jumps"}}
	if d := diff(want, got); d != "" {
		t.Errorf("MinimalEdits :\n%s", d)
	}
	if got := MinimalEdits("same", "same", 0); got != nil {
		t.Errorf("MinimalEdits of equal text = %v", got)
	}
}

func TestFind(t *testing.T) {
	f := Parse("Say “hello” to the\n  world.\n\nHELLO again. Hello.")
	tests := []struct {
		needle string
		within Span
		want   []Span
	}{
		// Bytes: the quotes take three each, so "hello" is [7,12),
		// "world." [25,31), "HELLO" [33,38) and "Hello" [46,51).
		{`"hello" to the world`, Span{}, []Span{{4, 30}}},
		{"hello", Span{}, []Span{{7, 12}}},
		{"Hello", Span{}, []Span{{46, 51}}},
		{"HELLO", Span{}, []Span{{33, 38}}},
		{"hello again", Span{}, []Span{{33, 44}}},
		{"world. HELLO", Span{}, []Span{{25, 38}}},
		{"Hello", Span{0, 40}, []Span{{7, 12}, {33, 38}}},
		{"Hello", Span{0, 11}, nil},
		{"   ", Span{}, nil},
	}
	for _, tc := range tests {
		got := f.Find(tc.needle, tc.within)
		if d := diff(tc.want, got); d != "" {
			t.Errorf("Find(%q, %v):\n%s", tc.needle, tc.within, d)
		}
	}
	if got := f.FindAll("hello", false); len(got) != 3 {
		t.Errorf("FindAll(hello, any case) = %v, want three", got)
	}
	if got := f.FindAll("Hello", true); len(got) != 1 || got[0] != (Span{46, 51}) {
		t.Errorf("FindAll(Hello, matching case) = %v", got)
	}
	// Markers are not text: a match runs across them.
	g := Parse(docsFile)
	hits := g.Find("> A quoted line. Text with", Span{})
	if len(hits) != 1 || g.Text()[hits[0].Start:hits[0].End] != "> A quoted line.\n\nText with" {
		t.Errorf("Find across the markers = %v", hits)
	}
}

func TestHeadings(t *testing.T) {
	src := "---\ntitle: x\n---\n\n# Top\n\ntext\n\n## Same\n\n```\n# not a heading\n```\n\n## Same\n\nSetext\n------\n\n# Next *one*\n"
	f := Parse(src)
	// Front matter is 17 bytes; "# Top" starts at 18, "## Same" at 31
	// and 65, "Setext" at 74, "# Next *one*" at 89, and the text is 102.
	// A body starts after the heading's line and the blank one after it:
	// "text" at 25, the fence at 40; the second "Same" and "Setext" have
	// no body, so theirs is where they end; a setext heading's body is
	// after its underline.
	want := []Heading{
		{Level: 1, Text: "Top", ID: "top", Start: 18, End: 89, BodyStart: 25, Line: 5},
		{Level: 2, Text: "Same", ID: "same", Start: 31, End: 65, BodyStart: 40, Line: 9},
		{Level: 2, Text: "Same", ID: "same-1", Start: 65, End: 74, BodyStart: 74, Line: 15},
		{Level: 2, Text: "Setext", ID: "setext", Start: 74, End: 89, BodyStart: 89, Line: 17},
		{Level: 1, Text: "Next *one*", ID: "next-one", Start: 89, End: 102, BodyStart: 102, Line: 20},
	}
	if d := diff(want, f.Headings()); d != "" {
		t.Errorf("Headings() :\n%s", d)
	}
	if hd, ok := f.Heading("same-1"); !ok || hd.Start != 65 {
		t.Errorf("Heading(same-1) = %+v, %t", hd, ok)
	}
}

func TestFrontMatter(t *testing.T) {
	tests := []struct {
		src  string
		want FrontMatter
	}{
		// A rule, then a blank line, is a thematic break.
		{"---\n\n# Title\n\nIntro: one\n\n---\n\nMore\n", FrontMatter{}},
		{"---\ntitle: x\ntags: [a]\n---\n# Body\n", FrontMatter{Present: true, End: 27}},
		{"---\n\n## title: x tags: \\[a\\]\n\n# Body\n", FrontMatter{Mangled: true}},
		{"# Body\n\n---\n\nrule above\n", FrontMatter{}},
		{"---\nnever closed\n", FrontMatter{}},
	}
	for _, tc := range tests {
		if got := Parse(tc.src).FrontMatter(); got != tc.want {
			t.Errorf("FrontMatter(%q) = %+v, want %+v", tc.src, got, tc.want)
		}
	}
}

func TestWithMarks(t *testing.T) {
	f := Parse(docsFile)
	label := func(k string) string { return "{>>c:" + k + "<<}" }
	got := f.WithMarks(0, len(f.Text()), label)
	if !strings.Contains(got, "footnote.[^1]{>>c:c1<<}\n\n## Lists") || strings.Contains(got, "gdoc_comment") {
		t.Errorf("WithMarks(whole) = %q", got)
	}
	at := strings.Index(f.Text(), "## Lists")
	if got := f.WithMarks(at, len(f.Text()), label); strings.Contains(got, "{>>") || !strings.HasPrefix(got, "## Lists") {
		t.Errorf("WithMarks(after the passage) = %q", got)
	}
}

func TestBlockScalars(t *testing.T) {
	src := "x\n\n---\ngdoc_comments:\n" +
		"  c1:\n    status: RESOLVED\n    head:\n      author: 'O''Brien'\n      body: \"line one\\nline two\"\n" +
		"      quote: >-\n        folded\n        text\n" +
		"    replies:\n      - author: Someone\n        body: a reply\n" +
		"  c2:\n    status: ACTIVE\n    head:\n      author: Plain\n      body: a long plain\n        scalar on two lines\n---\n"
	got := Parse(src).Comments()
	for i := range got {
		got[i].Start, got[i].End = 0, 0
	}
	want := []Comment{
		{Key: "c1", Status: "RESOLVED", Author: "O'Brien", Body: "line one\nline two", Quote: "folded text", Replies: []Reply{{Author: "Someone", Body: "a reply"}}},
		{Key: "c2", Status: "ACTIVE", Author: "Plain", Body: "a long plain scalar on two lines"},
	}
	if d := diff(want, got); d != "" {
		t.Errorf("Comments() :\n%s", d)
	}
}

func FuzzParseApply(f *testing.F) {
	for _, s := range []string{docsFile, "", "plain\n", "a<!-- gdoc_comment_begin: k -->b<!-- gdoc_comment_end: k -->c", "---\ngdoc_comments:\n  -\n---"} {
		f.Add(s, 0, 0, "x")
	}
	f.Fuzz(func(t *testing.T, src string, start, length int, insert string) {
		p := Parse(src)
		if got, err := bytesOf(p.Apply(nil)); err != nil || got != src {
			t.Fatalf("Apply(nil) = %q, %v; want the bytes back", got, err)
		}
		for _, h := range p.Headings() {
			if h.Start < 0 || h.Start > h.End || h.End > len(p.Text()) {
				t.Fatalf("heading %+v outside the text", h)
			}
		}
		for _, s := range p.Find(insert, Span{}) {
			if s.Start < 0 || s.Start > s.End || s.End > len(p.Text()) {
				t.Fatalf("hit %+v outside the text", s)
			}
		}
		n := len(p.Text())
		if n == 0 {
			return
		}
		s := ((start % n) + n) % n
		e := min(s+((length%8)+8)%8, n)
		q, err := p.Apply([]Edit{{s, e, insert}})
		if err != nil {
			return
		}
		want := p.Text()[:s] + insert + p.Text()[e:]
		if p.block != "" {
			want = strings.TrimRight(want, "\n")
		}
		if q.Text() != want {
			t.Fatalf("visible text after the edit = %q, want %q", q.Text(), want)
		}
	})
}

func TestHeadingBodies(t *testing.T) {
	tests := []struct {
		src  string
		body string
	}{
		{"# Title\n\n---\n\nAfter the rule.\n", "---\n\nAfter the rule.\n"},
		{"Two lines\nof heading\n===\n\nBody.\n", "Body.\n"},
		{"## Tight\nBody.\n", "Body.\n"},
	}
	for _, tc := range tests {
		f := Parse(tc.src)
		hs := f.Headings()
		if len(hs) != 1 || f.Text()[hs[0].BodyStart:hs[0].End] != tc.body {
			t.Errorf("Headings(%q) = %+v, want one whose body is %q", tc.src, hs, tc.body)
		}
	}
}

func TestCut(t *testing.T) {
	f := Parse("ab\ncdé\nfgh\n")
	tests := []struct{ start, chars, want int }{
		{0, 2, 2},    // one line longer than the budget: cut on a character
		{0, 3, 3},    // the first line and its newline
		{0, 6, 3},    // "cdé" ends at 7 bytes, 6 characters: not inside it
		{0, 7, 8},    // the second line ends at byte 8, character 7
		{3, 100, 12}, // past the end is the end, byte 12
	}
	for _, tc := range tests {
		if got := f.Cut(tc.start, tc.chars); got != tc.want {
			t.Errorf("Cut(%d, %d) = %d, want %d", tc.start, tc.chars, got, tc.want)
		}
	}
}

func TestLines(t *testing.T) {
	f := Parse("one\ntwo\n\nfour")
	for _, tc := range []struct{ pos, line int }{{0, 1}, {3, 1}, {4, 2}, {8, 3}, {9, 4}, {12, 4}} {
		if got := f.LineOf(tc.pos); got != tc.line {
			t.Errorf("LineOf(%d) = %d, want %d", tc.pos, got, tc.line)
		}
	}
	for _, tc := range []struct{ line, pos int }{{0, 0}, {1, 0}, {2, 4}, {4, 9}, {5, 13}} {
		if got := f.LineStart(tc.line); got != tc.pos {
			t.Errorf("LineStart(%d) = %d, want %d", tc.line, got, tc.pos)
		}
	}
}

func TestBlockInsert(t *testing.T) {
	tests := []struct {
		text    string
		pos     int
		content string
		want    string
	}{
		// At the end of a file that ends a line: one newline before, and
		// the file still ends one.
		{"# A\n\nBody.\n", 11, "New.", "\nNew.\n"},
		// At the end of one that does not: a blank line before, none after.
		{"# A\n\nBody.", 10, "New.\n", "\n\nNew."},
		// Before a heading: a blank line after, so it stays a heading.
		{"# A\n\n## B\n", 5, "New.", "New.\n\n"},
		{"", 0, "New.", "New.\n"},
	}
	for _, tc := range tests {
		if got := Parse(tc.text).BlockInsert(tc.pos, tc.content); got.Insert != tc.want || got.Start != tc.pos || got.End != tc.pos {
			t.Errorf("BlockInsert(%q, %d, %q) = %+v, want insert %q", tc.text, tc.pos, tc.content, got, tc.want)
		}
	}
	for _, tc := range []struct {
		text       string
		start, end int
		content    string
		want       string
	}{
		// A section's span keeps the blank line that ended it.
		{"# A\n\n## B\n\nOld.\n\n## C\n", 5, 17, "## B\n\nNew.", "## B\n\nNew.\n\n"},
		// An empty section body: blank lines on both sides, so the
		// next heading stays one.
		{"## A\n\n## B\n", 6, 6, "Filled.", "Filled.\n\n"},
		{"## A\n## B\n", 5, 5, "Filled.", "\nFilled.\n\n"},
		// At the end of the file the ending stays as it was.
		{"## A\n\nOld.", 6, 10, "New.", "New."},
		{"## A\n\nOld.", 6, 10, "\n", ""},
	} {
		if got := Parse(tc.text).BlockReplace(tc.start, tc.end, tc.content); got != tc.want {
			t.Errorf("BlockReplace(%q, %d, %d, %q) = %q, want %q", tc.text, tc.start, tc.end, tc.content, got, tc.want)
		}
	}
}

func TestHeadingIDsAreUnique(t *testing.T) {
	var ids []string
	for _, h := range Parse("# Foo\n\n# Foo 1\n\n# Foo\n\n# Foo\n").Headings() {
		ids = append(ids, h.ID)
	}
	if d := diff([]string{"foo", "foo-1", "foo-2", "foo-3"}, ids); d != "" {
		t.Errorf("ids:\n%s", d)
	}
}

func TestLinesCount(t *testing.T) {
	for _, tc := range []struct {
		s string
		n int
	}{{"", 0}, {"a", 1}, {"a\n", 1}, {"a\nb", 2}, {"a\n\n", 2}} {
		if got := Lines(tc.s); got != tc.n {
			t.Errorf("Lines(%q) = %d, want %d", tc.s, got, tc.n)
		}
	}
}

func TestApplyKeepsACommentAroundInsertions(t *testing.T) {
	// Two insertions where a comment begins both go before it, in order.
	src := "a<!-- gdoc_comment_begin: c1 -->b<!-- gdoc_comment_end: c1 -->c"
	got, err := bytesOf(Parse(src).Apply([]Edit{{1, 1, "X"}, {1, 1, "Y"}}))
	if want := "aXY<!-- gdoc_comment_begin: c1 -->b<!-- gdoc_comment_end: c1 -->c"; err != nil || got != want {
		t.Errorf("Apply = %q, %v; want %q", got, err, want)
	}
}

func TestAddComment(t *testing.T) {
	src := "We ship in May.\n"
	f := Parse(src)
	at := strings.Index(f.Text(), "ship in May")
	g, key, err := f.AddComment(at, at+len("ship in May"), NewPost{Author: "Ada \"A\" Example", Time: "2026-10-14T10:00:00.000Z", Body: "Is May real?\nAsk ops."})
	if err != nil {
		t.Fatal(err)
	}
	want := "We <!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
		"---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    head:\n" +
		"      author: \"Ada \\\"A\\\" Example\"\n      time: '2026-10-14T10:00:00.000Z'\n" +
		"      body: \"Is May real?\\nAsk ops.\"\n      quote: \"ship in May\"\n---\n"
	if key != "c1" || g.Source() != want {
		t.Errorf("AddComment = %q, %q; want %q, %q", key, g.Source(), "c1", want)
	}
	c := g.Comments()[0]
	if c.Author != "Ada \"A\" Example" || c.Body != "Is May real?\nAsk ops." || c.Quote != "ship in May" || c.Token {
		t.Errorf("the entry reads back as %+v", c)
	}
	// A second comment goes into the same block, under a free key, and
	// the first keeps its passage.
	h, key2, err := g.AddComment(0, 2, NewPost{Author: "Bo", Time: "2026-10-14T10:01:00.000Z", Body: "x"})
	if err != nil || key2 != "c2" || len(h.Comments()) != 2 || h.Passage("c1") != "ship in May" || h.Passage("c2") != "We" {
		t.Errorf("a second comment: %v, %q, %+v", err, key2, h.Comments())
	}
	if _, _, err := f.AddComment(3, 3, NewPost{Body: "x"}); !errors.Is(err, ErrNoPassage) {
		t.Errorf("a comment on nothing: %v", err)
	}
	if _, _, err := f.AddComment(0, 2, NewPost{Body: "<!-- gdoc_comment_end: c1 -->"}); !errors.Is(err, ErrMarkup) {
		t.Errorf("markup in a comment: %v", err)
	}
}

func TestAddCommentNestsWithOthers(t *testing.T) {
	// A comment that begins where another ends, and one inside another.
	f := Parse(docsFile)
	q := strings.Index(f.Text(), "quoted")
	g, key, err := f.AddComment(q, q+len("quoted"), NewPost{Author: "Bo", Time: "t", Body: "inner"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Passage(key) != "quoted" || g.Passage("c1") != f.Passage("c1") {
		t.Errorf("nested: %q in %q", g.Passage(key), g.Passage("c1"))
	}
	end := f.Comments()[0].End
	g, key, err = f.AddComment(end, end+len("\n\n## Lists"), NewPost{Author: "Bo", Time: "t", Body: "after"})
	if err != nil || g.Passage(key) != "\n\n## Lists" || g.Passage("c1") != f.Passage("c1") {
		t.Errorf("adjacent: %v, %q", err, g.Passage(key))
	}
}

func TestAddReply(t *testing.T) {
	f := Parse(docsFile)
	g, err := f.AddReply("c1", NewPost{Author: "Bo", Time: "2026-10-14T10:02:00.000Z", Body: "Agreed."})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(docsFile, "        Text with a footnote.\n---\n",
		"        Text with a footnote.\n    replies:\n    - author: \"Bo\"\n      time: '2026-10-14T10:02:00.000Z'\n      body: \"Agreed.\"\n---\n", 1)
	if g.Source() != want {
		t.Errorf("AddReply =\n%s\nwant\n%s", g.Source(), want)
	}
	h, err := g.AddReply("c1", NewPost{Author: "Cy", Time: "t", Body: "Me too."})
	if err != nil || len(h.Comments()[0].Replies) != 2 || h.Comments()[0].Replies[1].Body != "Me too." {
		t.Errorf("a second reply: %v, %+v", err, h.Comments())
	}
	if _, err := f.AddReply("c9", NewPost{Body: "x"}); err == nil {
		t.Error("a reply to no comment was written")
	}
}

func TestRepliesAsDocsWritesThem(t *testing.T) {
	// The shape a person's save in the editor wrote (spike D, stage 3):
	// the reply list at its key's own indentation, a resolve as a reply
	// with an action and no body.
	src := "x <!-- gdoc_comment_begin: c1 -->y<!-- gdoc_comment_end: c1 -->\n\n---\ngdoc_comments:\n" +
		"  c1:\n    status: RESOLVED\n    token: AbC\n    head:\n      author: Ada\n      time: '2026-10-14T09:00:00.000Z'\n" +
		"      body: wup wup\n      quote: y\n    replies:\n    - author: Bo\n      time: '2026-10-14T09:01:00.000Z'\n" +
		"      body: another\n    - author: Ada\n      time: '2026-10-14T09:02:00.000Z'\n      action: RESOLVE\n---\n"
	c := Parse(src).Comments()
	want := []Reply{{Author: "Bo", Time: "2026-10-14T09:01:00.000Z", Body: "another"}, {Author: "Ada", Time: "2026-10-14T09:02:00.000Z", Action: "RESOLVE"}}
	if len(c) != 1 || c[0].Status != "RESOLVED" || !c[0].Token {
		t.Fatalf("Comments() = %+v", c)
	}
	if d := diff(want, c[0].Replies); d != "" {
		t.Errorf("replies:\n%s", d)
	}
}

func TestUnitsRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "a\n", "a\n\n", "\n\na\n\nb", "&nbsp;", " \n&nbsp;\n", "&nbsp;\nx", "# H\n\n- x  \n- y\n\n| a | b |\n| :- | -: |\n\n&nbsp;\n\n```go\nx\n\n```\n\ntext\nmore\n===\n"} {
		lead, us := units(s)
		var b strings.Builder
		b.WriteString(lead)
		for _, u := range us {
			b.WriteString(u.text + u.sep)
		}
		if b.String() != s {
			t.Errorf("units(%q) joined = %q", s, b.String())
		}
	}
}

// mergeBase is a file before a working copy is made from it, written
// the way a person writes Markdown: underscores, padded table cells,
// inline HTML, a wrapped paragraph.
const mergeBase = "# Plan\n\nIntro with __bold__ and _italic_ that\nwraps onto a second line.\n\n" +
	"| Name | Value |\n|:-----|------:|\n| alpha | 1 |\n\n<span style=\"color:red\">Watch out</span>\n\n" +
	"## Steps\n\n- first\n- second\n\n## Risks\n\nNone yet.\n"

// mergeExport is mergeBase as Docs exports its working copy, unchanged
// (the dialect measured in spike D).
const mergeExport = "# Plan\n\nIntro with **bold** and *italic* that wraps onto a second line.\n\n" +
	"| Name | Value |\n| :---- | ----: |\n| alpha | 1 |\n\n&nbsp;\n\nWatch out\n\n" +
	"## Steps\n\n- first  \n- second  \n\n## Risks\n\nNone yet.\n"

func TestMergeLeavesTheFileAloneWhenTheCopyIsUnchanged(t *testing.T) {
	got, conflicts := Merge(mergeBase, mergeBase, mergeExport)
	if got != mergeBase || len(conflicts) != 0 {
		t.Errorf("Merge of an unchanged copy =\n%s\nconflicts %v", got, conflicts)
	}
}

func TestMergeCarriesTheCopysChanges(t *testing.T) {
	export := strings.Replace(mergeExport, "None yet.", "Two risks.", 1)
	export = strings.Replace(export, "- second  \n", "- second  \n- third  \n", 1)
	export = strings.Replace(export, "## Risks", "## Budget\n\nSmall.\n\n## Risks", 1)
	got, conflicts := Merge(mergeBase, mergeBase, export)
	want := strings.Replace(mergeBase, "None yet.", "Two risks.", 1)
	want = strings.Replace(want, "- second\n", "- second\n- third\n", 1)
	want = strings.Replace(want, "## Risks", "## Budget\n\nSmall.\n\n## Risks", 1)
	if got != want || len(conflicts) != 0 {
		t.Errorf("Merge =\n%s\nwant\n%s\nconflicts %v", got, want, conflicts)
	}
}

func TestMergeKeepsChangesMadeToTheFileSince(t *testing.T) {
	current := strings.Replace(mergeBase, "None yet.", "One risk, added in the file.", 1)
	export := strings.Replace(mergeExport, "- first  \n", "- first, edited in the copy  \n", 1)
	got, conflicts := Merge(mergeBase, current, export)
	want := strings.Replace(current, "- first\n", "- first, edited in the copy\n", 1)
	if got != want || len(conflicts) != 0 {
		t.Errorf("Merge =\n%s\nwant\n%s\nconflicts %v", got, want, conflicts)
	}
	// Both changed the same block: the file's version stays, reported.
	export = strings.Replace(mergeExport, "None yet.", "Copy says three.", 1)
	got, conflicts = Merge(mergeBase, current, export)
	if got != current || len(conflicts) != 1 || conflicts[0].Base != "None yet." || conflicts[0].Copy != "Copy says three." {
		t.Errorf("a conflict: %q, %+v", got, conflicts)
	}
}

func TestMergeDeletes(t *testing.T) {
	export := strings.Replace(mergeExport, "## Risks\n\nNone yet.\n", "", 1)
	got, _ := Merge(mergeBase, mergeBase, export)
	if want := strings.Replace(mergeBase, "\n\n## Risks\n\nNone yet.", "", 1); got != want {
		t.Errorf("Merge of a deletion =\n%q\nwant\n%q", got, want)
	}
}

func FuzzMerge(f *testing.F) {
	f.Add(mergeBase, mergeBase)
	f.Add(mergeBase, "# Other\n\ntext")
	f.Add("", "a\n\n&nbsp;\n")
	f.Fuzz(func(t *testing.T, base, current string) {
		// A copy that changed nothing changes nothing.
		if got, conflicts := Merge(base, current, base); got != current || len(conflicts) != 0 {
			t.Fatalf("Merge(base, current, base) = %q, %v; want current %q", got, conflicts, current)
		}
		_, _ = Merge(current, base, current+"\n\nmore\n")
	})
}
