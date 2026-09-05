// Package leakcheck holds one test: nothing identifying anybody is
// committed to this repository.
//
// gitleaks runs in CI and cannot help here. A document id, an account
// address, a Cloud project number and a Google subject id are not
// secrets — no rule matches them, no scanner flags them — and hard rule
// 1 in CLAUDE.md is about exactly those. Until this test existed the
// rule was enforced by remembering, and the repository has already had
// one accident of the kind (two of Claude Code's settings temp files
// swept in by `git add -A`).
//
// Every rule below is an allow-list: a shape is refused unless it looks
// invented. The opposite arrangement — a deny-list naming the domain or
// the organisation to watch for — would itself be the disclosure, and it
// would be committed here in the clear.
package leakcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Files whose content is machine-generated hashes, not prose or code.
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

func TestNothingIdentifyingIsCommitted(t *testing.T) {
	root := moduleRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 20 {
		t.Fatalf("only %d tracked files; the scan is not seeing the repository", len(files))
	}

	for _, name := range files {
		if skipFiles[filepath.Base(name)] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue // a deleted or unreadable file is not this test's business
		}
		text := string(data)
		if strings.IndexByte(text, 0) >= 0 {
			continue // binary
		}
		for _, found := range findLeaks(allowed(text)) {
			t.Errorf("%s: %s", name, found)
		}
	}
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

func TestRulesCatchPlantedIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		leaks bool
	}{
		{"real address", "contact alice@acme.co.uk for access", true}, // leakcheck:allow
		{"documentation address", "owner o@example.test signed in", false},
		{"test domain", "a@b.test", false},
		{"subject id", "sub 109876543210987654321 logged in", true},                                     // leakcheck:allow
		{"client id", "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com", true}, // leakcheck:allow gitleaks:allow
		{"api host on its own", "https://docs.googleapis.com/v1/documents", false},
		{"photo url", "https://lh3.googleusercontent.com/a-/AOh14GhAbCdEfGhIjK", true},     // leakcheck:allow
		{"real-looking document id", "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms", true}, // leakcheck:allow
		{"synthetic document id", "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", false},
		{"invented by repetition", "1AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH", false},
		{"a commit sha is not an id", "pinned at f06c13b6b1a9625abc9e6e439d9c05a8f2190e94", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := findLeaks(tc.text)
			if tc.leaks && len(got) == 0 {
				t.Errorf("planted identifier went unnoticed: %s", tc.text)
			}
			if !tc.leaks && len(got) > 0 {
				t.Errorf("false positive on %s: %v", tc.text, got)
			}
		})
	}
}

// TestHistoryCarriesNoIdentifiers scans every blob ever committed, not
// just the files as they stand. The tree scan cannot see an identifier
// that was committed and later edited out — and that is the shape of the
// accident this repository has already had once, when two of Claude
// Code's settings files were swept in by `git add -A` and removed in a
// later commit. gitleaks in CI scans a pull request's own commits, so
// these rules have never run over the whole history either.
//
// Guarded by LEAKCHECK_HISTORY=1: it reads the object store, which is
// not a per-push cost. Run it before a release, or after a scare.
func TestHistoryCarriesNoIdentifiers(t *testing.T) {
	if os.Getenv("LEAKCHECK_HISTORY") == "" {
		t.Skip("set LEAKCHECK_HISTORY=1 to scan every blob in the history")
	}
	root := moduleRoot(t)
	out, err := exec.Command("git", "-C", root, "rev-list", "--objects", "--all").Output()
	if err != nil {
		t.Skipf("git rev-list: %v", err)
	}

	type blob struct{ sha, path string }
	var blobs []blob
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sha, path, ok := strings.Cut(line, " ")
		if !ok || path == "" || seen[sha] {
			continue // a commit or a tree, or a blob under a second name
		}
		if skipFiles[filepath.Base(path)] {
			continue
		}
		seen[sha] = true
		blobs = append(blobs, blob{sha, path})
	}
	if len(blobs) < 50 {
		t.Fatalf("only %d blobs; the scan is not seeing the history", len(blobs))
	}

	scanned := 0
	for _, b := range blobs {
		data, err := exec.Command("git", "-C", root, "cat-file", "blob", b.sha).Output()
		if err != nil {
			continue
		}
		text := string(data)
		if strings.IndexByte(text, 0) >= 0 {
			continue // binary
		}
		scanned++
		for _, found := range findLeaks(allowed(text)) {
			// The commit is named, because a finding in history is not
			// fixed by editing a file: it needs the history rewritten, or
			// a decision that this one is harmless.
			t.Errorf("%s (blob %s, first seen as %s): %s", b.path, b.sha[:12], b.path, found)
		}
	}
	t.Logf("scanned %d text blobs across the whole history", scanned)
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

// moduleRoot walks up to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
