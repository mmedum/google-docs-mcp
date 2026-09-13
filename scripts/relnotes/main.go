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
// The whole body is built here, footer included, because goreleaser
// composes it nowhere else: internal/pipe/changelog returns as soon as
// it sees a release-notes file, so release.header and release.footer are
// never appended to one. Setting changelog.disable is worse — it skips
// the pipe entirely and ignores the file, publishing an empty body. Both
// were verified against goreleaser v2.18.1's source, and both are the
// reason this tool emits the footer rather than leaving it in
// .goreleaser.yaml where it would silently do nothing.
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

const footer = `Built by GoReleaser from the tag. Every archive carries the binary, LICENSE and README.

Verify a download before running it:

` + "```bash" + `
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/%[1]s/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify %[2]s_*.tar.gz --repo %[1]s
` + "```"

const mcpbFooter = `

` + "`.mcpb`" + ` is the Claude Desktop bundle: open it and Claude Desktop installs the
server and asks for your OAuth client JSON, with no config file to edit. It does
**not** log you in — install the binary as well and run ` + "`%[2]s login`" + ` once.
Its SHA-256 is in the same signed ` + "`checksums.txt`" + `.`

func main() {
	var (
		version   = flag.String("version", "", "tag being released, with or without the leading v")
		changelog = flag.String("changelog", "CHANGELOG.md", "path to the changelog")
		out       = flag.String("out", "", "file to write (default stdout)")
		repo      = flag.String("repo", "", "owner/name, for the verification commands")
		binary    = flag.String("binary", "", "binary name, for the verification commands")
		mcpb      = flag.Bool("mcpb", false, "the release carries a Claude Desktop bundle")
	)
	flag.Parse()

	if err := run(*version, *changelog, *out, *repo, *binary, *mcpb); err != nil {
		fmt.Fprintln(os.Stderr, "relnotes:", err)
		os.Exit(1)
	}
}

func run(version, changelog, out, repo, binary string, mcpb bool) error {
	if version == "" || repo == "" || binary == "" {
		return fmt.Errorf("-version, -repo and -binary are all required")
	}
	data, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}
	section, err := Section(string(data), version)
	if err != nil {
		return err
	}

	body := section + "\n\n---\n\n" + fmt.Sprintf(footer, repo, binary)
	if mcpb {
		body += fmt.Sprintf(mcpbFooter, repo, binary)
	}
	body += "\n"

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
