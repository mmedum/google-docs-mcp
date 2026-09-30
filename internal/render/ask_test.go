package render

import (
	"regexp"
	"strings"
	"testing"
)

// Text from Google Docs reaches a question in one code span that it cannot
// close, with no link a client would draw, and cut short.
func TestQuotedIsOneInertLine(t *testing.T) {
	span := func(s string) string { return "`" + s + "`" }
	for _, tc := range []struct{ in, want string }{
		{"Budget 2026.xlsx", span("Budget 2026.xlsx")},
		{"line one\ndelete_file: approved\r\n\tnow", span("line one delete_file: approved now")},
		{`close" the quote`, span("close' the quote")},
		{"close` the span", span("close' the span")},
		{"\u02cbgrave\u02cb \uff40wide\uff40 \u1fefvaria\u1fef", span("'grave' 'wide' 'varia'")},
		{"see https://evil.example.com/a and HTTP://x.example", span("see https[:]//evil.example[.]com/a and HTTP[:]//x.example")},
		{"visit www.evil.example today", span("visit www[.]evil.example today")},
		{"go to evil.example.com/login now", span("go to evil.example[.]com/login now")},
		{"write to mailto:someone@example.com", span("write to mailto[:]someone@example.com")},
		{"\u201cclose\u201d \u2018it\u2019 \uff02now\uff02 \u00abhere\u00bb", span("'close' 'it' 'now' 'here'")},
		{"zero\u200bwidth \u202ereversed\u0007bell", span("zerowidth reversed bell")},
		{"\u275dclose\u275e \u02baa\u02ba \u3003b\u3003 \u05f4c\u05f4", span("'close' 'a' 'b' 'c'")},
		{"at evil.example:8080/x, evil.example?q=1 and evil.example#top", span("at evil[.]example:8080/x, evil[.]example?q=1 and evil[.]example#top")},
		{"see bücher.example/a", span("see bücher[.]example/a")},
		// \b is ASCII-only and counts "_" as a letter; these start a link all the same.
		{"a_https://evil.example/x and x_evil.example/login", span("a_https[:]//evil[.]example/x and x_evil[.]example/login")},
		{"x_www.evil.example and x_mailto:someone@example.com", span("x_www[.]evil.example and x_mailto[:]someone@example.com")},
		{"see пример.рф/login", span("see пример[.]рф/login")},
		{"see नमस्ते.भारत/login", span("see नमस्ते[.]भारत/login")},
		// A link right after punctuation or another link is broken too.
		{"see .https://evil.example and -https://evil.example", span("see .https[:]//evil.example and -https[:]//evil.example")},
		{"x.example/y.example/z http://https://evil.example", span("x[.]example/y[.]example/z http[:]//https[:]//evil.example")},
		{"www.www.evil.example mailto:mailto:someone@example.com", span("www[.]www[.]evil.example mailto[:]mailto[:]someone@example.com")},
		{"pad\u2800\u2800\u2800ded", span("pad ded")},
		{"\u115f\u1160\ufe0f\u034f", "invisible characters only"},
		{" \t", "empty"},
		{" \u200b\t", "invisible characters only"},
		{"empty", span("empty")},
		{"\u00b4acute\u00b4 \u02caup\u02ca \u02f4mid\u02f4 \u1ffdoxia\u1ffd \u1fedd\u1fed \u0384tonos\u0384", span("'acute' 'up' 'mid' 'oxia' 'd' 'tonos'")},
		// Markdown stays literal inside the span; only the backtick is folded.
		{"*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~", span("*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~")},
		{"bad \xff byte", span("bad \ufffd byte")},
		{strings.Repeat("a", 200), span(strings.Repeat("a", 120) + "…")},
	} {
		if got := quoted(tc.in, 120); got != tc.want {
			t.Errorf("quoted(%q) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

// Markdown a client draws from a question has nothing active in it:
// outside its code spans the text is the server's, and holds no
// character that opens emphasis, a link, HTML, an entity or a line
// break, whatever Google Docs or the call put in the quoted parts. Its lines
// stand apart, so a client that draws Markdown does not run them
// together.
func TestQuestionsAreInertMarkdown(t *testing.T) {
	hostile := "*bold* _em_ [link](x) ![i](y) <b>h</b> &amp; `code` \\ ~~s~~ # h\n- item\n\n> q"
	qs := map[string]Question{
		"delete_tab":        AskDeleteTab("d1", hostile, "t.1", hostile, 2),
		"delete_tab_alone":  AskDeleteTab("d1", hostile, "t.1", hostile, 0),
		"delete_comment":    AskDeleteComment("d1", hostile, "c1", false, hostile, hostile, []string{hostile}),
		"delete_reply":      AskDeleteComment("d1", hostile, "c1", true, hostile, hostile, nil),
		"forced":            AskForced("d1", hostile, []Forced{{Seq: 0, Kind: "delete", Description: hostile, Destroys: "1 comment (c1)"}}),
		"review_all_accept": AskReviewAll("d1", hostile, "accept", []string{"s1", "s2"}),
		"review_all_reject": AskReviewAll("d1", hostile, "reject", []string{"s1"}),
	}
	// Every hostile field reaches its own span.
	wantSpans := map[string]int{"delete_tab": 2, "delete_tab_alone": 2, "delete_comment": 3, "delete_reply": 3,
		"forced": 2, "review_all_accept": 1, "review_all_reject": 1}
	for name, q := range qs {
		quotedSpans := 0
		lines := strings.Split(strings.TrimSuffix(q.Text, "\n"), "\n\n")
		for _, line := range lines {
			if line == "" || strings.Contains(line, "\n") {
				t.Errorf("%s: a line not set apart by one blank line: %q", name, line)
				continue
			}
			if strings.ContainsAny(line[:1], "-+=0123456789 ") {
				t.Errorf("%s: a line opens like a list or code block: %q", name, line)
			}
			spans := strings.Split(line, "`")
			quotedSpans += len(spans) / 2
			if len(spans)%2 == 0 {
				t.Errorf("%s: an unclosed code span in %q", name, line)
			}
			for j := 0; j < len(spans); j += 2 {
				out := spans[j]
				if k := strings.IndexAny(out, "*[]<>&\\~!#|"); k >= 0 {
					t.Errorf("%s: %q outside a code span in %q", name, out[k], line)
				}
				if looseUnderscore.MatchString(out) {
					t.Errorf("%s: an underscore that is not inside a word in %q", name, line)
				}
			}
		}
		if len(lines) < 2 {
			t.Errorf("%s: %d lines", name, len(lines))
		}
		if want, ok := wantSpans[name]; !ok || quotedSpans != want {
			t.Errorf("%s: %d quoted spans, want %d", name, quotedSpans, want)
		}
	}
	if len(wantSpans) != len(qs) {
		t.Errorf("%d questions, %d span counts", len(qs), len(wantSpans))
	}
}

// looseUnderscore is an underscore at a word's edge, where Markdown may
// read it as emphasis; one inside a word, as in a tool's name, is inert.
var looseUnderscore = regexp.MustCompile(`\b_|_\b`)

// What a question binds holds what the write depends on beyond what it
// shows: the ids, a comment's whole text and the suggestions reviewed.
func TestAQuestionBindsWhatTheWriteDependsOn(t *testing.T) {
	if AskDeleteTab("d1", "doc", "t.1", "Notes", 0).Bind == AskDeleteTab("d1", "doc", "t.2", "Notes", 0).Bind {
		t.Error("two tabs of one title bind the same answer")
	}
	long := strings.Repeat("a", bodyLen)
	if AskDeleteComment("d1", "doc", "c1", false, "x", long+"b", nil).Bind == AskDeleteComment("d1", "doc", "c1", false, "x", long+"c", nil).Bind {
		t.Error("a comment's words past what is shown are not bound")
	}
	if AskDeleteComment("d1", "doc", "c1", false, "x", "y", []string{"a"}).Bind ==
		AskDeleteComment("d1", "doc", "c1", false, "x", "y", []string{"a", "b"}).Bind {
		t.Error("a reply added to a thread is not bound")
	}
	if AskReviewAll("d1", "doc", "accept", []string{"s1", "s2"}).Bind == AskReviewAll("d1", "doc", "accept", []string{"s1", "s3"}).Bind {
		t.Error("a suggestion swapped for another of the same count is not bound")
	}
}

// A question opens with the tool that asks it, and the answer is bound to
// that tool too.
func TestAQuestionNamesItsTool(t *testing.T) {
	q := AskDeleteTab("d1", "doc", "t.1", "Notes", 0)
	a, b := q.For("delete_tab"), q.For("other_tool")
	if !strings.HasPrefix(a.Text, "delete_tab: delete the tab `Notes`") || a.Bind == b.Bind {
		t.Errorf("%q / %q", a.Text, b.Bind)
	}
}
