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

	// versionLine matches any input that names a tool's version, derived
	// from the shape of the key rather than from a list of them. The
	// list this replaces named two keys that could never match:
	// `gitleaks-version`, which gitleaks-action has never had — it reads
	// `GITLEAKS_VERSION` from the environment — and
	// `golangci-lint-version`, where that action takes plain `version`.
	// Both read as coverage and provided none, and deriving would have
	// covered `GITLEAKS_VERSION` on the day it was added.
	//
	// `go-version-file` ends in `file`, so it is not matched, and it is
	// a pin by reference anyway; a bare `go-version` would be caught,
	// which is what we would want.
	versionLine = regexp.MustCompile(`(?mi)^\s*([A-Za-z0-9_-]*(?:version|release)):\s*["']?([^"'\s#]+)["']?`)
)

// workflowFile is one file under .github/workflows.
type workflowFile struct {
	name string
	data string
}

// workflowFiles reads every workflow once, and holds the floor itself
// rather than leaving each gate to remember one: zero files and zero
// findings are the same output, and that floor is the only reason the
// dead version keys above were ever visible. A gate added later
// inherits the guarantee instead of re-deriving it.
func workflowFiles(t *testing.T) []workflowFile {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var files []workflowFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		files = append(files, workflowFile{name: e.Name(), data: string(data)})
	}
	if len(files) < 3 {
		t.Fatalf("only %d workflows read; the check is not reading %s", len(files), dir)
	}
	return files
}

func TestWorkflowsPinExactly(t *testing.T) {
	actions, versions := 0, 0
	for _, f := range workflowFiles(t) {
		for _, m := range usesLine.FindAllStringSubmatch(f.data, -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue // a local action carries no version of its own
			}
			actions++
			_, rev, ok := strings.Cut(ref, "@")
			if !ok || !fullSHA.MatchString(rev) {
				t.Errorf("%s: %s is not pinned to a full commit SHA (a tag is mutable)", f.name, ref)
			}
		}
		for _, m := range versionLine.FindAllStringSubmatch(f.data, -1) {
			key, value := m[1], m[2]
			versions++
			if !exactSemver.MatchString(value) {
				t.Errorf("%s: %s: %q is not exactly one version — a range, a bare major or `latest` "+
					"lets the tool change under a pinned action", f.name, key, value)
			}
		}
	}

	// A checker that finds nothing passes for the wrong reason, and
	// either count can go to zero on its own: a renamed input takes the
	// versions while the action references still match.
	if actions < 5 {
		t.Fatalf("only %d action references found; the check is not reading the workflows", actions)
	}
	if versions < 3 {
		t.Fatalf("only %d tool versions found; the input names have probably changed", versions)
	}
	t.Logf("%d actions pinned by SHA, %d tool versions exact", actions, versions)
}

// The Windows runner defaults to PowerShell, which read
// `-coverprofile=cov.out` as a file called `cov`: one line meaning two
// things depending on which runner read it. The fix is a workflow-level
// `defaults.run.shell`, and this is what makes it a rule rather than a
// habit — §18 of docs/architecture.md carries why that level rather than
// the job or the step.
func TestWorkflowsPinTheShell(t *testing.T) {
	files := workflowFiles(t)
	for _, f := range files {
		if !pinsShell(f.data) {
			t.Errorf("%s: no workflow-level `defaults: run: shell: bash`; without it the Windows "+
				"runner parses a `run` line as PowerShell", f.name)
		}
	}
	t.Logf("%d workflows pin the shell", len(files))
}

// pinsShell reports whether a workflow sets bash as the default shell
// for the whole file: `defaults:` at column zero, `run:` under it, and
// `shell: bash` nested *under* that.
//
// Depth is the whole point. A first draft that only asked whether both
// keys had been seen said yes to a `shell: bash` sitting *beside*
// `run:`, which is `defaults.shell` — not a key GitHub has, so it pins
// nothing while reading to a maintainer as though it does. The only two
// things read here are "column zero" and "deeper than the `run:` it sits
// under", so four-space and tab-indented files are not false failures.
func pinsShell(data string) bool {
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if indentOf(line) != 0 || withoutComment(line) != "defaults:" {
			continue
		}
		runIndent := -1
		for _, l := range lines[i+1:] {
			key := withoutComment(l)
			if key == "" {
				continue
			}
			if indent := indentOf(l); indent == 0 || indent <= runIndent {
				break // the defaults block, or the run block inside it, ended
			} else if key == "run:" {
				runIndent = indent
			} else if runIndent >= 0 && isShellBash(key) {
				return true
			}
		}
		return false // YAML allows one `defaults:` per mapping, so there is no second chance
	}
	return false
}

// indentOf counts the leading whitespace, which is the only measure of
// depth this file needs. A space and a tab each count as one.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// withoutComment trims a trailing `#` comment and the surrounding space,
// so a block annotated the way this repository annotates things still
// reads as pinned. A gate that fails a workflow which does carry the
// block teaches people to distrust the gate.
func withoutComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// isShellBash accepts `shell: bash` however YAML lets it be written,
// quotes included. Comparing the whole line as a literal made `shell:
// "bash"` an unpinned workflow.
func isShellBash(entry string) bool {
	name, value, ok := strings.Cut(entry, ":")
	if !ok || strings.TrimSpace(name) != "shell" {
		return false
	}
	return strings.Trim(strings.TrimSpace(value), `"'`) == "bash"
}

// The scanner runs twice with two chances to disagree: pre-commit
// installs it from a git tag, CI names a version to gitleaks-action.
// Rules added between two versions then fire in one place and not the
// other, so a commit passes locally and fails in CI — or worse, passes
// CI on a rule the hook would have caught before it was ever pushed.
func TestScannersAgree(t *testing.T) {
	root := repoRoot(t)
	config, err := os.ReadFile(filepath.Join(root, ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatalf("read .pre-commit-config.yaml: %v", err)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}

	hook := gitleaksRev(string(config))
	if hook == "" {
		t.Fatal("no rev under the gitleaks repo in .pre-commit-config.yaml; the check is reading the wrong file")
	}
	var ci string
	for _, m := range versionLine.FindAllStringSubmatch(string(workflow), -1) {
		if strings.EqualFold(m[1], "GITLEAKS_VERSION") {
			ci = m[2]
		}
	}
	if ci == "" {
		t.Fatal("no GITLEAKS_VERSION in ci.yml; the check is reading the wrong file")
	}
	for _, v := range []string{hook, ci} {
		if !exactSemver.MatchString(v) {
			t.Errorf("%q is not exactly one gitleaks version", v)
		}
	}

	// The workflow spells it without the `v`, because that is the form
	// the action's README documents; pre-commit needs the git tag.
	if strings.TrimPrefix(hook, "v") != strings.TrimPrefix(ci, "v") {
		t.Errorf("gitleaks is %s in pre-commit and %s in CI; a commit can pass one and fail the other",
			hook, ci)
	}
	t.Logf("both scanners are gitleaks %s", ci)
}

// gitleaksRev returns the tag pre-commit installs gitleaks from, found
// by walking down from the gitleaks repository rather than by taking the
// first `rev:` in the file. Anchoring on position would let a second
// remote hook added above this one silently substitute that tool's tag —
// a pattern that matches, but not the thing it names, which is the
// failure this file exists to catch.
func gitleaksRev(config string) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		if withoutComment(line) != "- repo: https://github.com/gitleaks/gitleaks" {
			continue
		}
		for _, l := range lines[i+1:] {
			entry := withoutComment(l)
			if strings.HasPrefix(entry, "- repo:") {
				break // the next hook began and this one named no rev
			}
			if rev, ok := strings.CutPrefix(entry, "rev:"); ok {
				return strings.TrimSpace(rev)
			}
		}
	}
	return ""
}

func TestPinsShell(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{"workflow level", "on: push\ndefaults:\n  run:\n    shell: bash\n\njobs:\n  a:\n", true},
		{"with a comment inside", "defaults:\n  # why\n  run:\n    shell: bash\n", true},
		{"another key beside shell", "defaults:\n  run:\n    working-directory: .\n    shell: bash\n", true},
		{"comments on both keys", "defaults: # workflow-wide\n  run:\n    shell: bash # for pipefail too\n", true},
		{"a quoted scalar", "defaults:\n  run:\n    shell: \"bash\"\n", true},
		{"four-space indentation", "defaults:\n    run:\n        shell: bash\n", true},
		{"tab indentation", "defaults:\n\trun:\n\t\tshell: bash\n", true},
		{"job level only", "jobs:\n  a:\n    defaults:\n      run:\n        shell: bash\n", false},
		{"absent", "on: push\n\njobs:\n  a:\n    steps:\n      - run: go build ./...\n", false},
		{"a different shell", "defaults:\n  run:\n    shell: pwsh\n", false},
		{"a different shell with a comment", "defaults:\n  run:\n    shell: pwsh # not bash\n", false},
		{"shell outside a run block", "defaults:\n  shell: bash\n", false},
		{"shell as a sibling of run", "defaults:\n  run:\n  shell: bash\n", false},
		{"shell under a key beside run", "defaults:\n  run:\n    working-directory: .\n  other:\n    shell: bash\n", false},
		{"block ends before the shell", "defaults:\n  run:\n    working-directory: .\njobs:\n    shell: bash\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pinsShell(c.yaml); got != c.want {
				t.Errorf("pinsShell = %v, want %v", got, c.want)
			}
		})
	}
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
