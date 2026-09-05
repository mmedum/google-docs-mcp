package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Two releases have now been broken by a pin that was not one. `v0.8.0`
// published nothing because a SHA on cosign-installer pins the action
// and not the cosign it installs; and `goreleaser-action` was pinned by
// SHA while being asked for `~> v2`, so the tool that decides what the
// artifacts *are* floated within a major version. A comment beside the
// value saying which half of the pin mattered did not hold it shut in
// either repository that tried one, so this is a check instead.
//
// The rule: an action is referenced by a full commit SHA, and a version
// handed to an action is exactly one version — no range, no `latest`,
// no bare major.
var (
	usesLine    = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exactSemver = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

	// versionKeys are the inputs that name a tool's version. go-version
	// is absent on purpose: every workflow uses go-version-file, which
	// points at go.mod and is a pin by reference.
	versionKeys = []string{"version", "cosign-release", "syft-version", "gitleaks-version", "golangci-lint-version"}
	versionLine = regexp.MustCompile(`(?m)^\s*(` + strings.Join(versionKeys, "|") + `):\s*["']?([^"'\s#]+)["']?`)
)

func TestWorkflowsPinExactly(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	actions, versions := 0, 0
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".yml" && filepath.Ext(e.Name()) != ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range usesLine.FindAllStringSubmatch(string(data), -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue // a local action carries no version of its own
			}
			actions++
			_, rev, ok := strings.Cut(ref, "@")
			if !ok || !fullSHA.MatchString(rev) {
				t.Errorf("%s: %s is not pinned to a full commit SHA (a tag is mutable)", e.Name(), ref)
			}
		}
		for _, m := range versionLine.FindAllStringSubmatch(string(data), -1) {
			key, value := m[1], m[2]
			versions++
			if !exactSemver.MatchString(value) {
				t.Errorf("%s: %s: %q is not exactly one version — a range, a bare major or `latest` "+
					"lets the tool change under a pinned action", e.Name(), key, value)
			}
		}
	}

	// A checker that finds nothing passes for the wrong reason. Both
	// counts are asserted because either could go to zero on its own: a
	// renamed directory takes the actions with it, a renamed input takes
	// the versions.
	if actions < 5 {
		t.Fatalf("only %d action references found; the check is not reading the workflows", actions)
	}
	if versions < 3 {
		t.Fatalf("only %d tool versions found; the input names have probably changed", versions)
	}
	t.Logf("%d actions pinned by SHA, %d tool versions exact", actions, versions)
}

func repoRoot(t *testing.T) string {
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
