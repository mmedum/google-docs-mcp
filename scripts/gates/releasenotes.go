package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// changelogPath is the changelog, from the module root.
const changelogPath = "CHANGELOG.md"

// releaseNotes prints one version's section of the changelog, which is
// what the release workflow passes to goreleaser as --release-notes.
//
// The entry is the release note. Generating notes from commit subjects
// instead publishes "Put the schema surface where it belongs, and name
// the gate after the gate (#52)" to somebody deciding whether to
// upgrade, which is not who that sentence was written for.
//
// The generated list is stopped by deleting goreleaser's changelog
// block, never by changelog.disable, which empties the release body;
// .goreleaser.yaml says why.
func releaseNotes(w io.Writer, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: gates release-notes VERSION [CHANGELOG]")
	}
	version := strings.TrimPrefix(args[0], "v")
	file := changelogPath
	if len(args) == 2 && args[1] != "" {
		file = args[1]
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	notes := sectionFor(string(raw), version)
	if notes == "" {
		return fmt.Errorf("no CHANGELOG section for %s in %s", version, file)
	}
	_, err = fmt.Fprintln(w, promoteHeadings(notes))
	return err
}

// promoteHeadings lifts every heading in the section one level, because
// the file and the page are two different documents.
//
// In CHANGELOG.md the version is an h2 and its change kinds are h3s
// underneath it. On the release page the version heading is gone —
// GitHub renders the tag name as the page's h1 — so an unaltered section
// starts at h3 directly under an h1. That is a skipped rank, which the
// W3C's heading guidance says to avoid. Lifting one level gives h1 then
// h2, with nothing missing in between, and needs
// no wrapper heading repeating either the word "changelog" or the
// version number GitHub is already showing.
//
// Fenced code is left alone, read as the docs gate reads it: a
// `# comment` inside a shell block is not a heading, and the
// verification snippets here are full of them. Only h3 and deeper are
// lifted, so this can never emit a second h1.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	code := docsCodeLines(lines)
	for i, line := range lines {
		if !code[i] && strings.HasPrefix(line, "###") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// sectionFor returns the body under `## [version]`, without the blank
// lines that top and tail it.
func sectionFor(changelog, version string) string {
	lines := strings.Split(changelog, "\n")
	from, to := changelogSection(lines, version)
	body := lines[from:to]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	return joinTrimmed(body)
}

// changelogSection is the range of lines under `## [version]`, empty when
// there is no such heading.
//
// It stops at the next heading and at the link footer. The footer is not
// part of any section, but it follows the oldest one with no heading in
// between — so without that second stop the oldest entry's notes would
// end with a block of compare links.
func changelogSection(lines []string, version string) (from, to int) {
	want := "## [" + version + "]"
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, want) })
	if start < 0 {
		return 0, 0
	}
	to = start + 1
	for to < len(lines) && !strings.HasPrefix(lines[to], "## ") && !isLinkDefinition(lines[to]) {
		to++
	}
	return start + 1, to
}

// joinTrimmed drops the blank lines at the end, so a section that runs to
// the end of the file reads the same as one followed by a heading.
func joinTrimmed(body []string) string {
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
}

// isLinkDefinition reports whether the line is a markdown link
// definition, as a changelog's compare-link footer is made of. It is the
// docs gate's rule, so a footnote is part of a section, held to the first
// column: an indented one continues a list item.
func isLinkDefinition(line string) bool {
	return !strings.HasPrefix(line, " ") && docsMDLinkDef.MatchString(line)
}
