package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// pins is the gate. It is a command rather than a test, so a person
// reading the Makefile's target list can see that pinning is checked.

var (
	usesLine    = regexp.MustCompile(`(?m)^[ \t]*-?[ \t]*uses:[ \t]*([^\s#]+)`)
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
	actions, versions, installers := 0, 0, 0
	for _, f := range files {
		i, toolProblems := unpinnedTools(f)
		installers += i
		problems = append(problems, toolProblems...)
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
	if installers < 3 {
		return fmt.Errorf("only %d tool installers found; the classification table has probably "+
			"drifted from the actions the workflows use", installers)
	}
	if err := scannersAgree(root); err != nil {
		problems = append(problems, err.Error())
	}
	if err := makefileAgrees(root); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "pins ok: %d actions by SHA, %d tool versions exact, %d installers name their tool's version, "+
		"%d workflows pin the shell, %d Makefile pins match CI\n",
		actions, versions, installers, len(files), len(makefilePins))
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

// makefilePins are the tools the Makefile and CI both run. The Makefile
// runs each as module@$(variable); ci.yml runs it as module@version, or,
// with action set, installs it through that action's input.
var makefilePins = []struct {
	variable, module string
	action, input    string
}{
	{"GOLANGCI_VERSION", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", "golangci/golangci-lint-action", "version"},
	{"GOVULNCHECK_VERSION", "golang.org/x/vuln/cmd/govulncheck", "", ""},
	{"GO_LICENSES_VERSION", "github.com/google/go-licenses", "", ""},
}

// makefileAgrees keeps the Makefile on the tools CI runs. Two versions
// means `make check` passes on a tool CI never runs, or fails on one it
// has moved past.
func makefileAgrees(root string) error {
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return fmt.Errorf("read Makefile: %w", err)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return fmt.Errorf("read ci.yml: %w", err)
	}
	var problems []error
	for _, p := range makefilePins {
		tool := path.Base(p.module)
		if !strings.Contains(string(mk), p.module+"@$("+p.variable+")") {
			problems = append(problems, fmt.Errorf("the Makefile does not run %s@$(%s), the pin this check holds to CI",
				p.module, p.variable))
			continue
		}
		local := makefileVar(string(mk), p.variable)
		if local == "" {
			problems = append(problems, fmt.Errorf("the Makefile does not set %s, which it runs %s at", p.variable, tool))
			continue
		}
		ci := ciPins(string(workflow), p.module, p.action, p.input)
		if len(ci) == 0 {
			problems = append(problems, fmt.Errorf("ci.yml pins no %s, which the Makefile runs at $(%s)", tool, p.variable))
		}
		for _, v := range ci {
			if strings.TrimPrefix(v, "v") != strings.TrimPrefix(local, "v") {
				problems = append(problems, fmt.Errorf("%s is %s in the Makefile's %s and %s in ci.yml; make check runs one and CI the other",
					tool, local, p.variable, v))
			}
		}
	}
	return errors.Join(problems...)
}

// makefileVar is the value a Makefile assignment gives name, with =, :=
// or ?=, or empty.
func makefileVar(mk, name string) string {
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `[ \t]*[:?]?=[ \t]*(\S+)`).FindStringSubmatch(mk)
	if m == nil {
		return ""
	}
	return m[1]
}

// ciPins is every version a workflow pins module at: each `module@version`
// it runs and, when action is set, input on each step that uses action.
func ciPins(workflow, module, action, input string) []string {
	var out []string
	for _, m := range regexp.MustCompile(regexp.QuoteMeta(module)+`@([^\s"']+)`).FindAllStringSubmatch(workflow, -1) {
		out = append(out, m[1])
	}
	if action == "" {
		return out
	}
	key := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(input) + `:\s*["']?([^"'\s#]+)`)
	for _, loc := range usesLine.FindAllStringSubmatchIndex(workflow, -1) {
		if ref, _, _ := strings.Cut(workflow[loc[2]:loc[3]], "@"); ref != action {
			continue
		}
		if m := key.FindStringSubmatch(stepBlock(workflow, loc[0])); m != nil {
			out = append(out, m[1])
		}
	}
	return out
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

// A SHA pins the wrapper, not the tool. The checks above can only judge
// the versions that are *written*; an action that installs a tool and
// names no version at all is an absence, and nothing here could see it.
// cosign-installer pinned by SHA with no `cosign-release` takes whatever
// cosign is newest, and a new cosign major can change what signing
// needs. `download-syft` has the same hole one step below it.
//
// So every action is classified, and an unknown one fails the gate
// rather than passing quietly: an unclassified action is how a floating
// tool goes unseen.
var (
	// installerPins maps an action to the input keys that pin the tool
	// it installs. Any one of them satisfies the rule.
	installerPins = map[string][]string{
		"sigstore/cosign-installer":         {"cosign-release"},
		"anchore/sbom-action/download-syft": {"syft-version"},
		"goreleaser/goreleaser-action":      {"version"},
		"golangci/golangci-lint-action":     {"version"},
		// A file is a pin by reference, and a better one: go.mod cannot
		// disagree with itself the way two literals can.
		"actions/setup-go": {"go-version-file", "go-version"},
		// This wrapper has never had a version input; it reads the
		// scanner's version from the environment.
		"gitleaks/gitleaks-action": {"GITLEAKS_VERSION"},
	}

	// notInstallers are the actions that install no tool, each with the
	// reason, so that adding one is a decision rather than an omission.
	notInstallers = map[string]string{
		"actions/checkout":                "checks out the repository",
		"actions/upload-artifact":         "uploads, installs nothing",
		"actions/download-artifact":       "downloads, installs nothing",
		"actions/attest-build-provenance": "calls the attestation API",
		"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
		"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
	}
)

// stepBlock returns the lines belonging to the step whose `uses:` line
// starts at start, so that a `with:` or `env:` key is read from the step
// that owns it rather than from the next one down the file.
func stepBlock(data string, start int) string {
	lines := strings.Split(data[start:], "\n")
	base := indentOf(lines[0])
	var out []string
	for i, ln := range lines {
		if i > 0 && strings.TrimSpace(ln) != "" {
			in := indentOf(ln)
			if in < base || (in == base && strings.HasPrefix(strings.TrimSpace(ln), "- ")) {
				break
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// unpinnedTools reports every action whose tool is left to float, and
// every action nobody has classified.
func unpinnedTools(f workflowFile) (installers int, problems []string) {
	for _, loc := range usesLine.FindAllStringSubmatchIndex(f.data, -1) {
		ref := f.data[loc[2]:loc[3]]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
			continue
		}
		action, _, _ := strings.Cut(ref, "@")
		keys, isInstaller := installerPins[action]
		if !isInstaller {
			if _, known := notInstallers[action]; !known {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is not classified in scripts/gates/pins.go — add it to installerPins "+
						"with the input that pins its tool, or to notInstallers with the reason. "+
						"A SHA pins the wrapper, not the tool", f.name, action))
			}
			continue
		}
		installers++
		if !namesAnyKey(stepBlock(f.data, loc[0]), keys) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is pinned by SHA but the tool it installs is not — set %s. "+
					"A SHA pins the wrapper, not the tool", f.name, action, strings.Join(keys, " or ")))
		}
	}
	return installers, problems
}

// namesAnyKey reports whether the step sets one of the given keys.
func namesAnyKey(block string, keys []string) bool {
	for _, k := range keys {
		if regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(k) + `:\s*\S`).MatchString(block) {
			return true
		}
	}
	return false
}
