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
	_, err = fmt.Fprintln(w, notes)
	return err
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
