package main

import (
	"bytes"
	"os"
	"path/filepath"
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

// The registry is what the usage text and the dispatch both read.
func TestReleaseNotesIsRegisteredAndIsNotAGate(t *testing.T) {
	c, ok := commands["release-notes"]
	if !ok {
		t.Fatal("release-notes is not in the registry")
	}
	if c.gate {
		t.Error("release-notes is marked as a gate; it runs at release time, not in make check")
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
