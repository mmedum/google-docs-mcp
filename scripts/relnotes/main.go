// Command relnotes builds the body of a GitHub release from the section
// of CHANGELOG.md that matches the tag being released.
//
//	go run ./scripts/relnotes -version v1.1.2 -out dist/notes.md
//
// It exists because the release page and the changelog had drifted into
// two different accounts of the same release: goreleaser was publishing
// a machine list of commit SHAs, including the "Release X.Y.Z" commit
// itself, while the notes a person actually wrote sat in CHANGELOG.md
// and reached nobody.
//
// This emits the section and nothing else. The footer belongs in
// .goreleaser.yaml, where it still works: internal/pipe/release/body.go
// wraps ctx.ReleaseNotes in Config.Release.Header and
// Config.Release.Footer on every path, --release-notes included. What
// the changelog pipe's early return skips is the --release-header and
// --release-footer *flags*, which is a different pair.
//
// The trap is changelog.disable. It is read in the pipe's Skip method,
// which runs before Run, so ctx.ReleaseNotes is never set and the file
// passed to --release-notes is never read: the body collapses to header
// plus footer. That is not a guess — it is why a sibling server's
// release page shows a footer and nothing above it while its workflow
// passes --release-notes. Verified against goreleaser v2.18.1.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// heading matches a Keep a Changelog version heading: "## [1.1.2] - 2026-09-13".
var heading = regexp.MustCompile(`(?m)^## \[([^\]]+)\]`)

func main() {
	var (
		version   = flag.String("version", "", "tag being released, with or without the leading v")
		changelog = flag.String("changelog", "CHANGELOG.md", "path to the changelog")
		out       = flag.String("out", "", "file to write (default stdout)")
	)
	flag.Parse()

	if err := run(*version, *changelog, *out); err != nil {
		fmt.Fprintln(os.Stderr, "relnotes:", err)
		os.Exit(1)
	}
}

func run(version, changelog, out string) error {
	if version == "" {
		return fmt.Errorf("-version is required")
	}
	data, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}
	section, err := Section(string(data), version)
	if err != nil {
		return err
	}

	body := section + "\n"

	if out == "" {
		_, err := os.Stdout.WriteString(body)
		return err
	}
	return os.WriteFile(out, []byte(body), 0o644)
}

// Section returns the entries under the heading for version, without the
// heading itself. A tag with no section is an error rather than an empty
// release body: it means the release commit never moved the entries out
// of [Unreleased], and publishing silence is how a release goes out
// saying nothing.
func Section(changelog, version string) (string, error) {
	want := strings.TrimPrefix(strings.TrimSpace(version), "v")

	locs := heading.FindAllStringSubmatchIndex(changelog, -1)
	for i, loc := range locs {
		if changelog[loc[2]:loc[3]] != want {
			continue
		}
		start := loc[1] // just past the heading line's "## [x]"
		if nl := strings.IndexByte(changelog[start:], '\n'); nl >= 0 {
			start += nl + 1
		}
		end := len(changelog)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		section := strings.TrimSpace(changelog[start:end])
		if section == "" {
			return "", fmt.Errorf("the section for %s is empty", want)
		}
		return section, nil
	}
	return "", fmt.Errorf("no section for %s in the changelog", want)
}
