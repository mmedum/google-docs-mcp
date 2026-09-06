package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pins is the gate. It was four tests, which meant `make check` ran it
// only as part of `go test ./...` and the target list gave no sign it
// existed. The rules are unchanged; what moved is that a person reading
// the Makefile can now see that pinning is checked.

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

// readWorkflows reads every workflow once and holds the floor itself
// rather than leaving each rule to remember one: zero files and zero
// findings are the same output.
func readWorkflows(root string) ([]workflowFile, error) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
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
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		files = append(files, workflowFile{name: e.Name(), data: string(data)})
	}
	if len(files) < 3 {
		return nil, fmt.Errorf("only %d workflows read; the check is not reading %s", len(files), dir)
	}
	return files, nil
}

func pins(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	files, err := readWorkflows(root)
	if err != nil {
		return err
	}

	var problems []string
	actions, versions := 0, 0
	for _, f := range files {
		for _, m := range usesLine.FindAllStringSubmatch(f.data, -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue // a local action carries no version of its own
			}
			actions++
			_, rev, ok := strings.Cut(ref, "@")
			if !ok || !fullSHA.MatchString(rev) {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is not pinned to a full commit SHA (a tag is mutable)", f.name, ref))
			}
		}
		for _, m := range versionLine.FindAllStringSubmatch(f.data, -1) {
			versions++
			if !exactSemver.MatchString(m[2]) {
				problems = append(problems, fmt.Sprintf(
					"%s: %s: %q is not exactly one version — a range, a bare major or `latest` "+
						"lets the tool change under a pinned action", f.name, m[1], m[2]))
			}
		}
		if !pinsShell(f.data) {
			problems = append(problems, fmt.Sprintf(
				"%s: no workflow-level `defaults: run: shell: bash`; without it the Windows "+
					"runner parses a `run` line as PowerShell", f.name))
		}
	}

	// A checker that finds nothing passes for the wrong reason, and
	// either count can go to zero on its own.
	if actions < 5 {
		return fmt.Errorf("only %d action references found; the check is not reading the workflows", actions)
	}
	if versions < 3 {
		return fmt.Errorf("only %d tool versions found; the input names have probably changed", versions)
	}
	if err := scannersAgree(root); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "pins ok: %d actions by SHA, %d tool versions exact, %d workflows pin the shell\n",
		actions, versions, len(files))
	return err
}

// scannersAgree keeps pre-commit and CI on one gitleaks. Two versions
// means a commit can pass the hook and fail the job, on rules that exist
// in one and not the other.
func scannersAgree(root string) error {
	config, err := os.ReadFile(filepath.Join(root, ".pre-commit-config.yaml"))
	if err != nil {
		return fmt.Errorf("read .pre-commit-config.yaml: %w", err)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return fmt.Errorf("read ci.yml: %w", err)
	}
	hook := gitleaksRev(string(config))
	if hook == "" {
		return fmt.Errorf("no rev under the gitleaks repo in .pre-commit-config.yaml; the check is reading the wrong file")
	}
	var ci string
	for _, m := range versionLine.FindAllStringSubmatch(string(workflow), -1) {
		if strings.EqualFold(m[1], "GITLEAKS_VERSION") {
			ci = m[2]
		}
	}
	if ci == "" {
		return fmt.Errorf("no GITLEAKS_VERSION in ci.yml; the check is reading the wrong file")
	}
	if strings.TrimPrefix(hook, "v") != strings.TrimPrefix(ci, "v") {
		return fmt.Errorf("gitleaks is %s in pre-commit and %s in CI; a commit can pass one and fail the other", hook, ci)
	}
	return nil
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
