package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// releaseNotes prints one version's section of the changelog, which is
// what the release workflow passes to goreleaser as --release-notes.
//
// The entry is the release note. Generating notes from commit subjects
// instead publishes "Put the schema surface where it belongs, and name
// the gate after the gate (#52)" to somebody deciding whether to
// upgrade, which is not who that sentence was written for — and it is
// what this project's release pages carried until 2026-09-13.
//
// Never reach for changelog.disable in .goreleaser.yaml to stop the
// generated list. It is read in the changelog pipe's Skip, which runs
// before Run, so ctx.ReleaseNotes is never assigned and the file named
// by --release-notes is never opened: the body collapses to the footer
// alone. A sibling server shipped exactly that and nobody read the page.
// Deleting the changelog block is the way. release.footer keeps working
// either way, because internal/pipe/release/body.go wraps ReleaseNotes
// in it on every path.
func releaseNotes(w io.Writer, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: gates release-notes VERSION [CHANGELOG]")
	}
	version := strings.TrimPrefix(args[0], "v")
	file := "CHANGELOG.md"
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
// W3C's heading guidance says to avoid, and it is what every release
// page here looked like when the extractor was first written. Lifting
// one level gives h1 then h2, with nothing missing in between, and needs
// no wrapper heading repeating either the word "changelog" or the
// version number GitHub is already showing.
//
// Fenced code is left alone: a `# comment` inside a shell block is not a
// heading, and the verification snippets here are full of them. Only h3
// and deeper are lifted, so this can never emit a second h1.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "###") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// sectionFor returns the body under `## [version]`, without the blank
// lines that top and tail it.
//
// It stops at the next heading and at the link footer. The footer is not
// part of any section, but it follows the oldest one with no heading in
// between — so without that second stop the oldest entry's notes would
// end with a block of compare links.
func sectionFor(changelog, version string) string {
	want := "## [" + version + "]"
	var body []string
	inside := false
	for _, line := range strings.Split(changelog, "\n") {
		switch {
		case strings.HasPrefix(line, want):
			inside = true
			continue
		case !inside:
			continue
		case strings.HasPrefix(line, "## "), isLinkDefinition(line):
			return joinTrimmed(body)
		case len(body) == 0 && strings.TrimSpace(line) == "":
			// The blank lines under the heading.
			continue
		}
		body = append(body, line)
	}
	return joinTrimmed(body)
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
// definition, as a changelog's compare-link footer is made of.
func isLinkDefinition(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	close := strings.Index(line, "]")
	return close > 0 && strings.HasPrefix(line[close:], "]: ")
}
