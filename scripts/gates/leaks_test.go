package main

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The gate itself, over this repository.
func TestLeaksGate(t *testing.T) {
	if err := leaks(io.Discard, nil); err != nil {
		t.Errorf("leaks gate: %v", err)
	}
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
		// The reserved names are whole domains or final labels. One that
		// only starts or contains like them is a domain someone can own.
		{"real domain named like a reserved one", "a@example.dk", true},                                 // leakcheck:allow
		{"reserved label inside a real domain", "a@corp.test.com", true},                                // leakcheck:allow
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
	root := mustModuleRoot(t)
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

// The rule that would have caught the 8 MB `gates` binary this gate,
// `make check` and eight green CI checks all passed. Binary files are
// unreadable to every other rule here, so what is asserted about them is
// that they are neither built nor large.
func TestArtifactRule(t *testing.T) {
	big := append([]byte("fixture\x00"), bytes.Repeat([]byte{0x7f}, maxBinary)...)
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"an ELF binary", append([]byte("\x7fELF\x02\x01"), 0x00, 0x99), true},
		{"a Windows PE", []byte("MZ\x90\x00\x03\x00"), true},
		{"a Mach-O", []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00}, true},
		{"a fat Mach-O", []byte{0xca, 0xfe, 0xba, 0xbe, 0x00, 0x02}, true},
		{"over the size limit", big, true},
		{"a small binary fixture", []byte("PNG\x00\x1a\n"), false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := artifact(c.data) != ""; got != c.want {
				t.Errorf("artifact() rejected = %v, want %v (%q)", got, c.want, artifact(c.data))
			}
		})
	}
}

// The gate over the whole repository, with one planted. A rule proved
// only through its own helper is a rule nobody has watched work.
func TestLeaksCatchesAPlantedBinary(t *testing.T) {
	root := mustModuleRoot(t)
	path := filepath.Join(root, "planted-binary-for-test")
	if err := os.WriteFile(path, []byte("\x7fELF\x02\x01\x01\x00planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	err := leaks(io.Discard, nil)
	if err == nil {
		t.Fatal("the gate passed with a compiled executable in the tree")
	}
	if !strings.Contains(err.Error(), "planted-binary-for-test") ||
		!strings.Contains(err.Error(), "compiled executable") {
		t.Errorf("the failure does not name the file and why: %v", err)
	}
}

// fakeRepo makes a git checkout of n files in a temporary directory and
// moves the test into it, so the whole gate runs over a tree the test
// controls.
func fakeRepo(t *testing.T, n int, extra map[string]string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	files := map[string]string{"go.mod": "module fake\n"}
	for i := range n - 1 {
		files[fmt.Sprintf("f%02d.txt", i)] = "nothing here\n"
	}
	maps.Copy(files, extra)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// A gate that reads almost nothing passes everything, so too small a
// tree is a failure rather than a clean result.
func TestLeaksRefusesATreeTooSmallToBeTheRepository(t *testing.T) {
	fakeRepo(t, 5, nil)
	err := leaks(io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "only 5 files") {
		t.Fatalf("leaks over a 5-file tree = %v, want the \"only 5 files\" refusal", err)
	}
}

// The text rules through the whole gate, not just findLeaks: a planted
// address is reported with the file that carries it, and the same tree
// without it passes.
func TestLeaksCatchesAPlantedAddress(t *testing.T) {
	fakeRepo(t, 25, map[string]string{"notes.md": "ask someone@acme.co.uk\n"}) // leakcheck:allow
	err := leaks(io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "notes.md: address at a real domain") {
		t.Fatalf("leaks with a planted address = %v, want it named in notes.md", err)
	}
	fakeRepo(t, 25, map[string]string{"notes.md": "ask someone@example.test\n"})
	var out bytes.Buffer
	if err := leaks(&out, nil); err != nil || out.String() != "leaks ok: 26 files scanned\n" {
		t.Fatalf("leaks over a clean tree = %v, output %q, want \"leaks ok: 26 files scanned\"", err, out.String())
	}
}
