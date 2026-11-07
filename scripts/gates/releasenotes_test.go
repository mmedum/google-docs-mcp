package main

import (
	"bytes"
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const relnotesSample = `# Changelog

The preamble, with an inline [Keep a Changelog](https://keepachangelog.com/) link
that is not a link definition.

## [Unreleased]

## [1.1.2] - 2026-09-13

### Added
- The thing.

### Changed
- The other thing.

## [1.1.1] - 2026-09-13

### Fixed
- An older thing.

[1.1.1]: https://github.com/mmedum/google-docs-mcp/releases/tag/v1.1.1
`

func relnotesFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(relnotesSample), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReleaseNotesTakesOnlyItsOwnVersion(t *testing.T) {
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"v1.1.2", relnotesFile(t)}); err != nil {
		t.Fatalf("releaseNotes: %v", err)
	}
	const want = "## Added\n- The thing.\n\n## Changed\n- The other thing.\n"
	if out.String() != want {
		t.Errorf("notes =\n%q\nwant\n%q", out.String(), want)
	}
}

// The tag carries a leading v and the heading does not.
func TestReleaseNotesAcceptsEitherSpelling(t *testing.T) {
	path := relnotesFile(t)
	var with, without bytes.Buffer
	if err := releaseNotes(&with, []string{"v1.1.1", path}); err != nil {
		t.Fatal(err)
	}
	if err := releaseNotes(&without, []string{"1.1.1", path}); err != nil {
		t.Fatal(err)
	}
	if with.String() != without.String() {
		t.Errorf("the v prefix changed the answer: %q vs %q", with.String(), without.String())
	}
}

// The compare-link footer follows the oldest section with no heading
// between, so without a second stop it would be published as part of it.
func TestReleaseNotesStopsAtTheLinkFooter(t *testing.T) {
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"1.1.1", relnotesFile(t)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "releases/tag") {
		t.Errorf("the link footer was published as release notes:\n%s", out.String())
	}
	if got := out.String(); got != "## Fixed\n- An older thing.\n" {
		t.Errorf("notes = %q", got)
	}
}

// A section runs to the compare-link footer: a definition in the first
// column whose label is Unreleased, in any case, or a version, with or
// without a v. A footnote is no definition, an indented line continues a
// list item, and a definition of another label is prose: each belongs to
// the section it is in.
func TestASectionEndsOnlyAtTheLinkFooter(t *testing.T) {
	const head = "## [1.0.0] - 2026-09-13\n\n- The thing.[^1]\n"
	for _, tc := range []struct {
		name, tail, want string
	}{
		{"a footnote", "\n[^1]: Only on Linux.\n", "- The thing.[^1]\n\n[^1]: Only on Linux."},
		{"a definition in the first column", "\n[1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a version's definition with its URL not yet written", "\n[1.0.0]:\n", "- The thing.[^1]"},
		{"a version's definition with a v", "\n[v1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a version's definition with a capital V", "\n[V1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a prerelease's definition with a v", "\n[v1.1.0-rc.1]: https://example.com/v1.1.0-rc.1\n", "- The thing.[^1]"},
		{"Unreleased's definition", "\n[Unreleased]: https://example.com/compare/v1.0.0...HEAD\n", "- The thing.[^1]"},
		{"Unreleased's definition in lower case", "\n[unreleased]: https://example.com/compare/v1.0.0...HEAD\n",
			"- The thing.[^1]"},
		{"Unreleased's definition in upper case", "\n[UNRELEASED]: https://example.com/compare/v1.0.0...HEAD\n",
			"- The thing.[^1]"},
		{"a definition continuing a list item", "  [1]: https://example.com/issue/1\n",
			"- The thing.[^1]\n  [1]: https://example.com/issue/1"},
		{"a version's definition continuing a list item", "  [v1.0.0]: https://example.com/v1.0.0\n",
			"- The thing.[^1]\n  [v1.0.0]: https://example.com/v1.0.0"},
		{"a definition of another label", "\n[issue]: https://example.com/issue/1\n",
			"- The thing.[^1]\n\n[issue]: https://example.com/issue/1"},
		{"a definition of a prose label", "\n[note]: https://example.com/note\n",
			"- The thing.[^1]\n\n[note]: https://example.com/note"},
		{"a definition of a label that is no version", "\n[v1]: https://example.com/v1\n",
			"- The thing.[^1]\n\n[v1]: https://example.com/v1"},
		{"a definition of a label that only starts as Unreleased", "\n[unreleased-notes]: https://example.com/notes\n",
			"- The thing.[^1]\n\n[unreleased-notes]: https://example.com/notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionFor(head+tc.tail, "1.0.0"); got != tc.want {
				t.Errorf("section = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty [Unreleased] is the normal state of a released changelog.
// Tagging it must fail rather than publish a release that says nothing.
func TestReleaseNotesRefusesAnEmptySection(t *testing.T) {
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"Unreleased", relnotesFile(t)}); err == nil {
		t.Error("an empty section was accepted; the release would publish silence")
	}
}

func TestReleaseNotesRefusesAnAbsentVersion(t *testing.T) {
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"9.9.9", relnotesFile(t)}); err == nil {
		t.Error("a version with no section was accepted")
	}
}

// The registry is what the usage text, the dispatch and parity read.
func TestReleaseNotesRunsInTheRelease(t *testing.T) {
	c, ok := commands["release-notes"]
	if !ok {
		t.Fatal("release-notes is not in the registry")
	}
	if c.runs != inRelease {
		t.Errorf("release-notes runs in pipeline %d, want inRelease: it runs at release time, not in make check", c.runs)
	}
}

// The page and the file are different documents: GitHub renders the tag
// name as the h1, so a section published unaltered starts at h3 under an
// h1 and skips a rank.
func TestReleaseNotesLiftHeadingsOneLevel(t *testing.T) {
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"v1.1.2", relnotesFile(t)}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "## Added") || !strings.Contains(got, "## Changed") {
		t.Errorf("headings were not lifted:\n%s", got)
	}
	if strings.Contains(got, "### ") {
		t.Errorf("an h3 survived, so the page still skips a rank:\n%s", got)
	}
	// Never a second h1: GitHub already renders one for the tag.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "# ") {
			t.Errorf("emitted an h1, which duplicates the tag heading: %q", line)
		}
	}
}

func TestPromoteHeadingsLeavesFencedCodeAlone(t *testing.T) {
	const body = "### Added\n- a thing\n\n```bash\n# not a heading\n### also not a heading\n```\n\n#### Deeper\n"
	got := promoteHeadings(body)
	for _, want := range []string{"## Added", "# not a heading", "### also not a heading", "### Deeper"} {
		if !strings.Contains(got, want) {
			t.Errorf("promoteHeadings dropped or mangled %q:\n%s", want, got)
		}
	}
}

func TestReadChangelog(t *testing.T) {
	doc := readChangelog(strings.ReplaceAll("# Changelog\n\n## [Unreleased]\n\n- a\n### [8.8.8]\n\n~~~markdown\n- in a fence\n## [9.9.9]\n"+
		"[9.9.9]: https://example.com/x\n~~~\n\n- b\n\n## [2.0.0-rc.1] - 2026-01-01\n\n- c\n##\tLater\n- d\n\n"+
		"[Unreleased]: https://example.com/u\n[2.0.0-rc.1]:\n", "\n", "\r\n"))
	if want := []changelogHeading{{"Unreleased", 2}, {"2.0.0-rc.1", 15}}; !slices.Equal(doc.headings, want) {
		t.Errorf("headings = %v, want %v", doc.headings, want)
	}
	if want := []changelogHeading{{"2.0.0-rc.1", 15}}; !slices.Equal(doc.versions(), want) {
		t.Errorf("versions = %v, want %v", doc.versions(), want)
	}
	if from, to := readChangelog("## [1.0.0]\n- a\n# Notes\n- b\n").section("1.0.0"); from != 1 || to != 2 {
		t.Errorf("a section ends at an h1 too: lines [%d, %d), want [1, 2)", from, to)
	}
	// The footer's own labels end a section, its URL written or not: a link
	// definition with any other label is prose.
	for line, ends := range map[string]bool{
		"[Unreleased]:":                       true,
		"[Unreleased]: https://example.com/u": true,
		"[2.0.0-rc.1]:\thttps://example.com":  true,
		"[note]: see below":                   false,
		"[1.0.0]:x":                           false,
		"[v1.0.0]: https://example.com":       true,
		"[1.0]: https://example.com":          false,
	} {
		if _, to := readChangelog("## [1.0.0]\n- a\n" + line + "\n- b\n").section("1.0.0"); (to == 2) != ends {
			t.Errorf("%q ends the section: %v, want %v", line, to == 2, ends)
		}
	}
	// A fence closes only at a line of its own character, as many or more,
	// and a # with no blank after it starts no heading.
	inner := readChangelog("##\t[1.0.0]\n````\n```bash\n## [9.9.9]\n```\n````\n#hashtag\n####### seven\n- a\n")
	if want := []changelogHeading{{"1.0.0", 0}}; !slices.Equal(inner.versions(), want) {
		t.Errorf("versions = %v, want %v: a tab after ## starts a heading", inner.versions(), want)
	}
	if from, to := inner.section("1.0.0"); from != 1 || to != 10 {
		t.Errorf("section 1.0.0 = lines [%d, %d), want [1, 10)", from, to)
	}
	for label, want := range map[string]string{
		"Unreleased": "\n- a\n### [8.8.8]\n\n~~~markdown\n- in a fence\n## [9.9.9]\n[9.9.9]: https://example.com/x\n~~~\n\n- b\n",
		"2.0.0-rc.1": "\n- c",
		"9.9.9":      "",
	} {
		if from, to := doc.section(label); strings.Join(doc.lines[from:to], "\n") != want {
			t.Errorf("section %s = %q, want %q", label, strings.Join(doc.lines[from:to], "\n"), want)
		}
	}
	if got := doc.entries("Unreleased"); got != 2 {
		t.Errorf("entries(Unreleased) = %d, want 2: a bullet in a fence is an example", got)
	}
}

// The release note is the section the docs gate scans, fenced code kept
// and never cut at, and a fenced heading is not lifted.
func TestReleaseNotesReadTheFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), changelogPath)
	if err := os.WriteFile(path, []byte("## [Unreleased]\n\n## [1.0.0]\n\n### Added\n\n- a\n\n~~~bash\n## not a heading\n### nor this\n~~~\n\n- b\n\n[1.0.0]: https://example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"1.0.0", path}); err != nil {
		t.Fatal(err)
	}
	if want := "## Added\n\n- a\n\n~~~bash\n## not a heading\n### nor this\n~~~\n\n- b\n"; out.String() != want {
		t.Errorf("notes = %q, want %q", out.String(), want)
	}
}

// Versions order as semver orders them, so a release candidate is a
// release and v1.10.0 comes after v1.9.0.
func TestChangelogCompareOrdersAsSemver(t *testing.T) {
	chain := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.9.0", "1.10.0", "10.0.0"}
	for i := range chain {
		for j := range chain {
			if got, want := changelogCompare(chain[i], chain[j]), cmp.Compare(i, j); got != want {
				t.Errorf("changelogCompare(%s, %s) = %d, want %d", chain[i], chain[j], got, want)
			}
		}
	}
}
