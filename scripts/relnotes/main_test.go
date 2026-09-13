package main

import "testing"

const sample = `# Changelog

Preamble that belongs to no version.

## [Unreleased]

## [1.1.2] - 2026-09-13

### Added
- The thing.

### Changed
- The other thing.

## [1.1.1] - 2026-09-13

### Fixed
- An older thing.
`

func TestSectionTakesOnlyItsOwnVersion(t *testing.T) {
	got, err := Section(sample, "v1.1.2")
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	const want = "### Added\n- The thing.\n\n### Changed\n- The other thing."
	if got != want {
		t.Errorf("Section =\n%q\nwant\n%q", got, want)
	}
}

// The leading v is optional, because the tag has one and the heading does not.
func TestSectionAcceptsEitherSpelling(t *testing.T) {
	with, err := Section(sample, "v1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	without, err := Section(sample, "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if with != without || with != "### Fixed\n- An older thing." {
		t.Errorf("v-prefix changed the answer: %q vs %q", with, without)
	}
}

// The last section in the file has no following heading to stop at.
func TestSectionReadsTheFinalVersion(t *testing.T) {
	got, err := Section(sample, "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "### Fixed\n- An older thing." {
		t.Errorf("final section = %q", got)
	}
}

// An empty [Unreleased] is the normal state of a released changelog, and
// tagging it would publish a release body saying nothing.
func TestSectionRefusesAnEmptySection(t *testing.T) {
	if _, err := Section(sample, "Unreleased"); err == nil {
		t.Error("an empty section was accepted; a release would publish silence")
	}
}

func TestSectionRefusesAnAbsentVersion(t *testing.T) {
	if _, err := Section(sample, "9.9.9"); err == nil {
		t.Error("a version with no section was accepted")
	}
}

// The body must carry the notes AND the footer: goreleaser appends
// neither header nor footer to a --release-notes file, so anything this
// tool leaves out never reaches the release page.
func TestRunWritesNotesAndFooter(t *testing.T) {
	dir := t.TempDir()
	cl := dir + "/CHANGELOG.md"
	if err := writeFile(cl, sample); err != nil {
		t.Fatal(err)
	}
	out := dir + "/notes.md"
	if err := run("v1.1.2", cl, out, "mmedum/google-docs-mcp", "google-docs-mcp", false); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, out)

	for _, want := range []string{"- The thing.", "sha256sum -c checksums.txt", "gh attestation verify", "mmedum/google-docs-mcp"} {
		if !contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	if contains(body, ".mcpb") {
		t.Error("the bundle paragraph appeared without -mcpb")
	}
}

func TestRunAddsTheBundleParagraphOnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	cl := dir + "/CHANGELOG.md"
	if err := writeFile(cl, sample); err != nil {
		t.Fatal(err)
	}
	out := dir + "/notes.md"
	if err := run("v1.1.2", cl, out, "mmedum/google-chat-mcp", "google-chat-mcp", true); err != nil {
		t.Fatal(err)
	}
	if !contains(readFile(t, out), ".mcpb") {
		t.Error("-mcpb did not add the bundle paragraph")
	}
}
