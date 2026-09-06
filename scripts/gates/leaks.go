package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// leaks is the confidentiality gate: nothing that identifies a person, a
// document or a deployment may be in this repository.
//
// It lived in internal/leakcheck as a test. The rules are unchanged; it
// is a command now because the Makefile's target list is what a person
// reads to find out what is checked, and a gate running invisibly inside
// `go test ./...` is one nobody can audit without grepping for it.

var skipFiles = map[string]bool{"go.sum": true}

var (
	// An address at a domain someone could actually own. RFC 2606 and
	// RFC 6761 reserve the rest for documentation and tests.
	email        = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	safeDomains  = map[string]bool{"example.com": true, "example.org": true, "example.net": true}
	safeSuffixes = []string{".test", ".invalid", ".localhost", ".example"}

	// A Google subject id: 21 digits, and nothing else in this project
	// is.
	subjectID = regexp.MustCompile(`\b[0-9]{21}\b`)

	// An OAuth client id, which is not a secret and is still an
	// identifier of the person's Cloud project.
	clientID = regexp.MustCompile(`[0-9]{6,}-[a-z0-9]{20,}\.apps\.googleusercontent\.com`)

	// A profile photo or user-content URL carries an account id in its
	// path. The bare host is allowed: the API client's allowlist names
	// it, and must.
	userContent = regexp.MustCompile(`\b[a-z0-9\-]+\.googleusercontent\.com/[A-Za-z0-9_\-/]{8,}`)

	// A Drive or Docs resource id.
	resourceID = regexp.MustCompile(`\b1[A-Za-z0-9_\-]{25,}\b`)

	// What an invented id looks like: a marker word, or the run of one
	// letter that no real id has. RE2 has no backreferences, so the run
	// is counted rather than matched.
	inventedWord = regexp.MustCompile(`(?i)synthetic|fixture|nosuch|unknown|example|scratch|placeholder`)
)

func leaks(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	// --others --exclude-standard adds files that are not committed yet
	// but would be by the next `git add -A`. Scanning only the index
	// meant a brand-new file was invisible until someone staged it, so
	// `make check` went green on a working tree carrying an address.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		// Outside a git checkout there is nothing to enumerate and
		// nothing to be wrong: a module-cache copy or a source tarball
		// is not a repository. Any other failure is a broken check, and
		// a broken check must not pass.
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) &&
			strings.Contains(string(ee.Stderr), "not a git repository") {
			_, err := fmt.Fprintln(w, "leaks: not a git checkout; nothing to enumerate")
			return err
		}
		return fmt.Errorf("git ls-files: %w", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 20 {
		return fmt.Errorf("only %d files; the scan is not seeing the repository", len(files))
	}

	var problems []string
	scanned := 0
	for _, name := range files {
		if skipFiles[filepath.Base(name)] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue // deleted or unreadable is not this gate's business
		}
		text := string(data)
		if strings.IndexByte(text, 0) >= 0 {
			continue // binary
		}
		scanned++
		for _, found := range findLeaks(allowed(text)) {
			problems = append(problems, name+": "+found)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "leaks ok: %d files scanned\n", scanned)
	return err
}

// hasRun reports whether s repeats one character n times in a row.
func hasRun(s string, n int) bool {
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 1
	}
	return false
}

// allowed drops the lines that carry the marker. The planted strings in
// this file's own table are the case it exists for: a scanner has to
// contain the shapes it catches, and the marker is per line, so nothing
// else in the file is excused. gitleaks reads its own marker the same
// way and on the same lines, for the same reason.
func allowed(text string) string {
	var b strings.Builder
	for line := range strings.Lines(text) {
		if strings.Contains(line, "leakcheck:"+"allow") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// findLeaks reports everything in text that looks like it identifies
// somebody. Separate from the walk so the rules can be tested against
// planted strings — a scanner nobody has watched fail is a scanner that
// might match nothing at all.
func findLeaks(text string) []string {
	var out []string
	for _, m := range email.FindAllStringSubmatch(text, -1) {
		if !safeDomain(m[1]) {
			out = append(out, "address at a real domain: "+m[0])
		}
	}
	for _, re := range []struct {
		re   *regexp.Regexp
		what string
	}{
		{subjectID, "a 21-digit account id"},
		{clientID, "an OAuth client id"},
		{userContent, "a user-content URL with an id in its path"},
	} {
		if m := re.re.FindString(text); m != "" {
			out = append(out, re.what+": "+m)
		}
	}
	for _, m := range resourceID.FindAllString(text, -1) {
		if !invented(m) {
			out = append(out, "a resource id that does not look invented: "+m+
				" (if it is synthetic, say so in the value: Synthetic, NoSuch, or a run of XXXX)")
		}
	}
	return out
}
func safeDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if safeDomains[domain] {
		return true
	}
	for _, s := range safeSuffixes {
		if strings.HasSuffix(domain, s) {
			return true
		}
	}
	return false
}
func invented(id string) bool {
	return inventedWord.MatchString(id) || hasRun(id, 4)
}
