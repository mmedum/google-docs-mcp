package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-docs-mcp/v3/internal/gapi"
)

// exportPDF exports the fixture as a PDF into dir.
func exportPDF(t *testing.T, dir string) (*ExportResult, error) {
	t.Helper()
	svc, _ := newService(t)
	svc.opts.ExportDir = dir
	return svc.Export(context.Background(), ExportRequest{Document: fixtureID, Format: "pdf"})
}

// readFile is a file's content, or the error reading it.
func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// An export never overwrites a file already in the directory, an earlier
// export of the same document included: the name gains a counter, up to
// 100 names, and past that the export is refused. It used to replace the
// earlier file without a word.
func TestAnExportNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "Quarterly Report-1Synth.pdf")
	if err := os.WriteFile(first, []byte("an earlier export"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := exportPDF(t, dir)
	if err != nil || res.Path != filepath.Join(dir, "Quarterly Report-1Synth-2.pdf") {
		t.Fatalf("second export: %+v, %v", res, err)
	}
	if got := readFile(first); got != "an earlier export" {
		t.Errorf("the earlier export now holds %q", got)
	}
	if got := readFile(res.Path); got != "%PDF-1.4 fake" {
		t.Errorf("the new export holds %q", got)
	}
	// Names 3 to 99 taken too: the 100th is the last one tried.
	for n := 3; n <= 99; n++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("Quarterly Report-1Synth-%d.pdf", n)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if res, err := exportPDF(t, dir); err != nil || res.Path != filepath.Join(dir, "Quarterly Report-1Synth-100.pdf") {
		t.Fatalf("the 100th name: %+v, %v", res, err)
	}
	if res, err := exportPDF(t, dir); classOf(err) != "blocked" {
		t.Errorf("with 100 names taken: %+v, %v, want [blocked]", res, err)
	}
	if got := readFile(first); got != "an earlier export" {
		t.Errorf("the earlier export now holds %q", got)
	}
}

// An export never writes through a symbolic link at its name, which could
// point anywhere the account can write: the link is a name taken like any
// other, and what it points at is left as it was.
func TestAnExportNeverWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("not an export"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "Quarterly Report-1Synth.pdf")); err != nil {
		t.Skipf("this system cannot make a symbolic link here: %v", err)
	}
	res, err := exportPDF(t, dir)
	if err != nil || res.Path != filepath.Join(dir, "Quarterly Report-1Synth-2.pdf") {
		t.Fatalf("export beside a link: %+v, %v", res, err)
	}
	if got := readFile(outside); got != "not an export" {
		t.Errorf("the link's target now holds %q", got)
	}
}

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
