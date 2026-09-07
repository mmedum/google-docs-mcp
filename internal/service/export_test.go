package service

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
)

// The exported filename, which an outside user read in their own
// Downloads folder before anybody here noticed it.
func TestFileName(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"a comma leaves one space, not two", "Poem, typeset", "Poem typeset"},
		{"several of them", "Notes, drafts, and ends", "Notes drafts and ends"},
		{"a run of punctuation is one space", "Q3 -- results!!! (final)", "Q3 -- results final"},
		{"leading and trailing punctuation is gone", ",,, Minutes ,,,", "Minutes"},
		{"safe characters survive", "Release_notes-1.0.md", "Release_notes-1.0.md"},
		{"nothing usable at all", "!!!", "document"},
		{"empty", "", "document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fileName(c.title); got != c.want {
				t.Errorf("fileName(%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
}

// The 80-character limit was a byte slice. A title in a language whose
// characters are not one byte each was cut through the middle of a rune,
// which no test here had a title long enough to notice.
func TestFileNameCutsOnRunes(t *testing.T) {
	// Each of these is three bytes, so 80 bytes falls inside one.
	long := strings.Repeat("æ", 5) + strings.Repeat("ø", 40)
	got := fileName(long)
	if len(got) > 80 {
		t.Errorf("filename is %d bytes, past the limit", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("filename is not valid UTF-8: %q", got)
	}
	if got == "" {
		t.Error("a long title produced no name at all")
	}
}

// The id fragment goes in a filename, so it stays inside ASCII.
func TestExportedNameIsPlainASCII(t *testing.T) {
	name := fileName("Poem, typeset") + "-" + gapi.ShortID("1SyntheticFixtureDocumentIdForThisTest")
	for _, r := range name {
		if r > unicode.MaxASCII {
			t.Errorf("filename %q carries %q, which is outside ASCII", name, r)
		}
	}
}
