package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The pins gate holds every third-party tool to one exact version, the
// same version everywhere it is named (D-86).
//
// An action is pinned by a full commit SHA, with the version it is in
// the comment after it. A tag "can be moved or deleted if a bad actor
// gains access to the repository storing the action", in GitHub's words.
// https://docs.github.com/en/actions/reference/security/secure-use#using-third-party-actions
// A container image, a step's, a job's or a service's, is pinned by its
// sha256 digest for the same reason.
//
// Every tool an action installs is pinned beside it. A SHA pins the
// action, never the tool the action fetches: cosign-installer pinned by
// SHA with no `cosign-release` installs whatever cosign is newest,
// whatever its signing format. goreleaser-action reads `~>` as "a max
// satisfying semver one", so `~> v2.18.1` floats across every 2.18.x and
// reads like a pin.
// https://github.com/goreleaser/goreleaser-action/blob/v7.3.0/README.md#inputs
// Every action is classified as an installer or not, and an unknown one
// fails: being unclassified is how an unpinned tool slips through.
//
// Every version, an input, an env value, a `go run module@version` or a
// Makefile variable, is one exact version, and a tool named in two files
// is named at one version: a local lint or scan otherwise passes on a
// version CI never runs. A Makefile variable no recipe runs with the go
// command pins nothing, since the tool then runs from the PATH, and a
// Makefile tool is one `NAME ?= module@version` variable. A known
// tool's module@version on any line but a comment is held to one exact
// version too, whatever the readers made of the line: they do not follow
// bash -c, eval or a make conditional, and are not grown to. The
// goreleaser config's hooks, signs, SBOMs and publishers run in the
// release, so a known tool's module@version in one is a pin, and none
// floats. Its release-note prose runs nothing and is not read. A
// pre-commit hook runs its tool through a Makefile target, so a version
// in the hooks' config, a remote hook's rev or a module@version, is one
// apart from the Makefile's and is refused.
//
// And every workflow pins its shell at the workflow level. The Windows
// runner's default shell is PowerShell, which does not read
// `-coverprofile=cov.out` the way bash does, and a job-level block fixes
// the forgetting exactly one level up: a job added later is bare again.
// The shell is bash, and no job or step sets another.
// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#defaultsrunshell

// pinsTool is one tool this repository runs, and every way it can be
// named.
type pinsTool struct {
	name string
	// modules match a `module@version` in the Makefile, a run line or the
	// goreleaser config.
	modules []string
	// envKeys are env names whose value is this tool's version.
	envKeys []string
	// actions install this tool, each with the with: input that pins it.
	actions map[string]string
}

// pinsTools is every tool, with the installer actions a workflow uses. A
// row with installers fails when no workflow step uses one.
var pinsTools = []pinsTool{
	// go-version-file names go.mod, which is a pin by reference: go.mod
	// cannot disagree with itself the way two literals can.
	{name: "go", actions: map[string]string{"actions/setup-go": "go-version-file"}},
	{name: "golangci-lint", modules: []string{"github.com/golangci/golangci-lint"},
		actions: map[string]string{"golangci/golangci-lint-action": "version"}},
	{name: "goreleaser", modules: []string{"github.com/goreleaser/goreleaser"},
		actions: map[string]string{"goreleaser/goreleaser-action": "version"}},
	{name: "cosign", actions: map[string]string{"sigstore/cosign-installer": "cosign-release"}},
	{name: "syft", actions: map[string]string{"anchore/sbom-action/download-syft": "syft-version"}},
	{name: "mcp-publisher", envKeys: []string{"PUBLISHER_VERSION"}},
	{name: "gitleaks", modules: []string{"github.com/zricethezav/gitleaks", "github.com/gitleaks/gitleaks"}},
	{name: "govulncheck", modules: []string{"golang.org/x/vuln/cmd/govulncheck"}},
	{name: "go-licenses", modules: []string{"github.com/google/go-licenses"}},
}

// pinsNotInstallers are the actions that install no tool, each with the
// reason, so that adding one is a decision rather than an omission.
var pinsNotInstallers = map[string]string{
	"actions/checkout":                "checks out the repository",
	"actions/upload-artifact":         "uploads, installs nothing",
	"actions/download-artifact":       "downloads, installs nothing",
	"actions/attest-build-provenance": "calls the attestation API",
	"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
	"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
}

// pinsPreCommit is the pre-commit framework's configuration.
const pinsPreCommit = ".pre-commit-config.yaml"

var (
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exactSemver = regexp.MustCompile(`^v?` + semverPattern + `$`)
	// pinsModuleAt is a module@version in text. The version ends where a
	// shell word or command does.
	pinsModuleAt = regexp.MustCompile(`([A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+)@([^\s"'` + "`" + `);&|\\]+)`)
	// pinsImageDigest is a container image pinned by its content digest.
	pinsImageDigest = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
	// pinsGoModToolchain and pinsGoModGo are setup-go's own patterns for
	// the toolchain directive and the go line.
	pinsGoModToolchain = regexp.MustCompile(`(?m)^toolchain go(1\.\d+(?:\.\d+|rc\d+)?)`)
	pinsGoModGo        = regexp.MustCompile(`(?m)^go (\d+(?:\.\d+)*)`)
)

// floatingDownload matches a tool fetched at whatever version is newest:
// a GitHub "latest release" download URL, or a Go module at @latest. The
// MCP registry's own publishing example installs its publisher from
// `releases/latest/download`. `runs-on: ubuntu-latest` is not one: an
// image label is not a tool a release artifact comes out of.
var floatingDownload = regexp.MustCompile(`releases/latest/download|@latest\b`)

// pinsFloors are the fewest workflows, action references and pinned
// versions the gate may read.
type pinsFloors struct{ workflows, actions, places int }

// pinsLimits are the repository's floors, each under what it holds.
var pinsLimits = pinsFloors{workflows: 4, actions: 15, places: 15}

// pinsFound is one place a tool's version is written.
type pinsFound struct{ version, at string }

// pinsReport is what the gate read and found.
type pinsReport struct {
	problems                               []string
	workflows, actions, installers, places int
	tools                                  map[string][]pinsFound
	// installed names each tool a workflow step installs.
	installed map[string]bool
	// judged is each tool@version a rule has recorded or refused, which the
	// text rule does not report again.
	judged map[string]bool
}

func (r *pinsReport) fail(format string, a ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, a...))
}

// add records one pin, refusing anything but one exact version.
func (r *pinsReport) add(tool, version, at string) {
	r.tools[tool] = append(r.tools[tool], pinsFound{version, at})
	r.judged[tool+"@"+version] = true
	r.places++
	if !exactSemver.MatchString(version) {
		r.fail("%s: %s is pinned to %q, which is not one exact version", at, tool, version)
	}
}

// judgeLatest marks each known tool's module@latest in text as refused.
func (r *pinsReport) judgeLatest(text string) {
	for _, m := range pinsModuleAt.FindAllStringSubmatch(text, -1) {
		if tool := pinsToolByModule(m[1]); tool != "" && m[2] == "latest" {
			r.judged[tool+"@latest"] = true
		}
	}
}

func pins(out io.Writer, _ []string) error {
	flows, err := readWorkflows()
	if err != nil {
		return err
	}
	mk, err := readMakefile()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	gomod := string(raw)
	goreleaser, err := readGoreleaser()
	if err != nil {
		return err
	}
	hooks, err := readPreCommit()
	if err != nil {
		return err
	}

	r := &pinsReport{tools: map[string][]pinsFound{}, installed: map[string]bool{}, judged: map[string]bool{},
		workflows: len(flows)}
	for _, w := range flows {
		pinsWorkflow(gomod, w, r)
	}
	r.problems = append(r.problems, mk.refused...)
	pinsMakefileRecipes(mk, pinsMakefileVars(mk, r), r)
	r.add("go", pinsGoVersion(gomod), "go.mod")
	pinsGoreleaser(goreleaser, r)
	r.problems = append(r.problems, hooks.refused...)
	for _, e := range hooks.entries {
		pinsFloatingAt(pinsPreCommit, e.line, e.text, r)
	}
	// Last, so a version a reader recorded is not reported twice.
	for _, w := range flows {
		pinsTextVersions(w.path, w.text, func(s string) string { return s }, r)
	}
	pinsTextVersions(makefilePath, mk.text, mk.expand, r)

	for _, t := range pinsTools {
		found := r.tools[t.name]
		if len(found) == 0 {
			r.fail("%s is pinned nowhere: a tool this gate names that no file pins is either gone or unread", t.name)
			continue
		}
		for _, f := range found[1:] {
			if pinsNormal(f.version) != pinsNormal(found[0].version) {
				r.fail("%s is %s at %s and %s at %s: one tool, one version",
					t.name, found[0].version, found[0].at, f.version, f.at)
			}
		}
	}
	// A floor per installer, not a count of steps: one installer used in
	// every job would meet a count alone.
	for _, t := range pinsTools {
		if len(t.actions) > 0 && !r.installed[t.name] {
			r.fail("no workflow step installs %s through %s; the reader is not seeing the step, "+
				"or the table names an installer nothing uses", t.name, strings.Join(slices.Sorted(maps.Keys(t.actions)), " or "))
		}
	}
	// Zero findings and zero inputs look the same otherwise.
	r.problems = append(r.problems, floorProblems("workflows", r.workflows, pinsLimits.workflows)...)
	r.problems = append(r.problems, floorProblems("action references", r.actions, pinsLimits.actions)...)
	r.problems = append(r.problems, floorProblems("pinned versions", r.places, pinsLimits.places)...)
	if len(r.problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(r.problems), strings.Join(r.problems, "\n  "))
	}
	_, _ = fmt.Fprintf(out, "%d workflows with their shell pinned, %d actions by SHA, %d installers name their tool, "+
		"%d tools pinned in %d places, each at one version\n", r.workflows, r.actions, r.installers, len(r.tools), r.places)
	return nil
}

// pinsWorkflow applies the workflow rules to one file, with gomod the
// repository's go.mod.
func pinsWorkflow(gomod string, w workflowFile, r *pinsReport) {
	r.problems = append(r.problems, w.refused...)
	if w.shell == "" {
		r.fail("%s: no workflow-level `defaults: run: shell:`. Every run step goes to a shell, "+
			"so a job-level block only fixes the jobs that exist today", w.path)
	}
	for _, sh := range w.shells {
		// An empty workflow shell is the missing one above.
		if sh.value != "bash" && (sh.value != "" || sh.job != "") {
			r.fail("%s:%d: %s sets the shell to %q, and every run step runs under bash: "+
				"another shell reads the command its own way", w.path, sh.line, sh.what, sh.value)
		}
	}
	pinsFloating(w.path, w.text, r)
	for _, s := range w.steps {
		at := fmt.Sprintf("%s:%d", w.path, s.line)
		switch {
		case strings.HasPrefix(s.uses, "./"):
			// A local action or workflow carries no version of its own.
		case strings.HasPrefix(s.uses, "docker://"):
			r.actions++
			if !pinsImageDigest.MatchString(strings.TrimPrefix(s.uses, "docker://")) {
				r.fail("%s: job %s: %s is a container image with no sha256 digest, so the image it runs can change",
					at, s.job, s.uses)
			}
		default:
			pinsActionRef(at, s, r)
			if !s.reusable {
				pinsStep(gomod, w, s, r)
			}
		}
	}
	for _, img := range w.images {
		if !pinsImageDigest.MatchString(img.value) {
			r.fail("%s:%d: job %s runs %s from %s, which has no sha256 digest, so the image it runs can change",
				w.path, img.line, img.job, img.what, img.value)
		}
	}
	for _, k := range w.env {
		if !workflowVersionKey.MatchString(k.key) {
			continue
		}
		if tool := pinsToolByEnv(k.key); tool != "" {
			r.add(tool, k.value, fmt.Sprintf("%s:%d: %s", w.path, k.line, k.key))
		} else {
			r.fail("%s:%d: the env value %s names a version of a tool pinsTools does not know", w.path, k.line, k.key)
		}
	}
	for _, run := range w.runs {
		for _, g := range pinsGoRuns(run.text) {
			module, version, _ := strings.Cut(g.pkg, "@")
			if tool := pinsToolByModule(module); tool != "" {
				r.add(tool, version, fmt.Sprintf("%s:%d: go %s %s", w.path, run.line, g.verb, module))
			} else {
				r.fail("%s:%d: go %s %s names a tool pinsTools does not know", w.path, run.line, g.verb, g.pkg)
			}
		}
	}
}

// pinsFloating refuses a tool fetched at whatever version is newest on
// any line of text but a comment line.
func pinsFloating(file, text string, r *pinsReport) {
	for i, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") {
			pinsFloatingAt(file, i+1, t, r)
		}
	}
}

// pinsFloatingAt refuses a tool line n of file fetches at whatever version
// is newest.
func pinsFloatingAt(file string, n int, line string, r *pinsReport) {
	if floating := floatingDownload.FindString(line); floating != "" {
		r.fail("%s:%d: %s installs whatever is newest. Name the version: what builds a release must not float",
			file, n, floating)
		r.judgeLatest(line)
	}
}

// pinsGoreleaser holds each command the goreleaser config runs in the
// release: a known tool's module@version is a pin, and nothing is fetched
// at whatever version is newest. Release-note prose is not a command.
func pinsGoreleaser(g goreleaserFile, r *pinsReport) {
	r.problems = append(r.problems, g.refused...)
	for _, run := range g.runs {
		pinsFloatingAt(parityGoreleaser, run.line, run.text, r)
	}
	for _, run := range g.runs {
		for _, m := range pinsModuleAt.FindAllStringSubmatch(run.text, -1) {
			// An @latest the floating rule refused is not reported again.
			if tool := pinsToolByModule(m[1]); tool != "" && !r.judged[tool+"@"+m[2]] {
				r.add(tool, m[2], fmt.Sprintf("%s:%d: %s", parityGoreleaser, run.line, m[1]))
			}
		}
	}
}

// goreleaserKeys are, by key path with sequence items left out, the keys
// whose values the release runs, true, and the keys above them, false: a
// hook, a string or its cmd:, and the cmd and args of a sign, an SBOM or a
// publisher, as goreleaser v2.18.1 reads them
// (https://github.com/goreleaser/goreleaser/blob/v2.18.1/pkg/config/config.go).
var goreleaserKeys = func() map[string]bool {
	keys := map[string]bool{}
	runs := func(paths ...string) {
		for _, path := range paths {
			keys[path] = true
			for at := strings.LastIndexByte(path, '.'); at > 0; at = strings.LastIndexByte(path[:at], '.') {
				if _, ok := keys[path[:at]]; !ok {
					keys[path[:at]] = false
				}
			}
		}
	}
	runs("before.hooks", "publishers.cmd")
	for _, at := range []string{"builds", "universal_binaries", "dockers_v2"} {
		for _, when := range []string{".hooks.pre", ".hooks.post"} {
			runs(at+when, at+when+".cmd")
		}
	}
	for _, at := range []string{"signs", "docker_signs", "binary_signs", "sboms"} {
		runs(at+".cmd", at+".args")
	}
	return keys
}()

// goreleaserFile is what the reader takes from the goreleaser config:
// every command line the release runs, a block scalar's as one line, and
// the shapes it refuses to read where a command could hide.
type goreleaserFile struct {
	runs    []workflowRun
	refused []string
}

// readGoreleaser parses the repository's goreleaser config.
func readGoreleaser() (goreleaserFile, error) {
	data, err := os.ReadFile(parityGoreleaser)
	if err != nil {
		return goreleaserFile{}, err
	}
	return parseGoreleaser(string(data)), nil
}

// goreleaserParser is parseGoreleaser's state: the open keys, each by the
// column it starts at, and the key of the command on the line before, or "".
type goreleaserParser struct {
	g     goreleaserFile
	stack []workflowFrame
	ran   string
}

// parseGoreleaser reads the goreleaser config text.
func parseGoreleaser(text string) goreleaserFile {
	var p goreleaserParser
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") && t != "---" {
			i = p.line(lines, i, t)
		}
	}
	return p.g
}

// line reads lines[i], trimmed, and returns the index of the last line it
// took: a block scalar takes the lines under it.
func (p *goreleaserParser) line(lines []string, i int, trimmed string) int {
	col, item := indent(lines[i]), trimmed == "-" || strings.HasPrefix(trimmed, "- ")
	// A sequence may sit at its key's own column (https://yaml.org/spec/1.2.2/#823-block-nodes).
	for n := len(p.stack); n > 0 && (p.stack[n-1].indent > col || p.stack[n-1].indent == col && !item); n-- {
		p.stack = p.stack[:n-1]
	}
	keys := workflowPath(p.stack)
	key, raw, keyCol := "", strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), col
	m := workflowKeyLine.FindStringSubmatch(trimmed)
	switch quoted := workflowQuotedKey.FindStringSubmatch(trimmed); {
	case m != nil:
		key, raw, keyCol = m[2], strings.TrimSpace(m[3]), col+len(m[1])
		keys = append(keys, key)
	case quoted != nil:
		// The reader cannot place a quoted key, nor the commands under it.
		p.refuse(i+1, "%s: a quoted key; write the key plain", quoted[1])
		return i
	case !item && p.ran != "":
		p.refuse(i+1, "%s: a value continued on the next line; write it on one line or as a block scalar", p.ran)
	}
	if !item && key == "" || len(keys) == 0 {
		return i
	}
	// known is a key that runs a command, or holds one that does, and name
	// is the key, or for an item the key it is an item of.
	run, known := goreleaserKeys[strings.Join(keys, ".")]
	name := keys[len(keys)-1]
	p.ran = ""
	if p.refused(i+1, key, name, raw, run, known) {
		return i
	}
	value, _ := workflowValue(raw)
	switch {
	case workflowBlockScalar.MatchString(value):
		body, end := workflowScalarBody(lines, i, keyCol)
		if text := strings.ReplaceAll(workflowJoin(body), "\n", " "); run && text != "" {
			p.g.runs = append(p.g.runs, workflowRun{line: i + 2, text: text})
		}
		return end
	case key != "" && value == "":
		p.stack = append(p.stack, workflowFrame{indent: keyCol, key: key})
	case run && value != "":
		p.g.runs, p.ran = append(p.g.runs, workflowRun{line: i + 1, text: value}), name
	}
	return i
}

// refused refuses line n where a command could hide in it, and reports
// whether it did: a merge key, and on or above a command an anchor, an
// alias, a tag or a flow collection, or a quoted command with an escape.
func (p *goreleaserParser) refused(n int, key, name, raw string, run, known bool) bool {
	switch {
	case key == "<<":
		p.refuse(n, "<<: a merge key; write the mapping out")
	case known && raw != "" && strings.ContainsRune("&*!", rune(raw[0])):
		p.refuse(n, "%s: an anchor, an alias or a tag; write the value out", name)
	case known && raw != "" && strings.ContainsRune("{[", rune(raw[0])):
		p.refuse(n, "%s: a flow collection; write it as a block, one key or item a line", name)
	case run && workflowQuoteEscape(raw):
		p.refuse(n, "%s: a quoted value with an escape in it; write it unquoted or as a block", name)
	default:
		return false
	}
	return true
}

func (p *goreleaserParser) refuse(n int, format string, a ...any) {
	p.g.refused = append(p.g.refused, fmt.Sprintf("%s:%d: %s", parityGoreleaser, n, fmt.Sprintf(format, a...)))
}

// preCommitFile is what the reader takes from the pre-commit config: each
// hook's entry, the command line the hook runs, and the shapes it refuses.
type preCommitFile struct {
	entries []workflowRun
	refused []string
}

// preCommitEntryKey is an entry: key anywhere on a line, as a flow mapping
// writes it.
var preCommitEntryKey = regexp.MustCompile(`(?:^|[\s{,\[])["']?entry["']?[ \t]*:`)

// readPreCommit parses the repository's pre-commit config.
func readPreCommit() (preCommitFile, error) {
	data, err := os.ReadFile(pinsPreCommit)
	if err != nil {
		return preCommitFile{}, err
	}
	return parsePreCommit(string(data)), nil
}

// parsePreCommit reads each hook's entry, a plain key with its command on
// its line, in the workflow reader's grammar. A hook runs its tool through
// a Makefile target, whose version the pins gate holds, so a version
// anywhere in the file, a rev or a module@version, is refused, and so is
// an entry written any other way.
func parsePreCommit(text string) preCommitFile {
	var f preCommitFile
	// under is the column of the entry above, or -1: a line indented past
	// it continues the entry's value. reported is whether that value is
	// refused already.
	under, reported := -1, false
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		n, trimmed := i+1, strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if under >= 0 && indent(line) > under {
			if !reported {
				f.refuse(n, "entry: a value continued on the next line; write it on one line")
				reported = true
			}
			continue
		}
		under = -1
		code, _ := yamlCutComment(trimmed)
		m := workflowKeyLine.FindStringSubmatch(trimmed)
		quoted := workflowQuotedKey.FindStringSubmatch(trimmed)
		switch {
		case pinsModuleAt.MatchString(code) || m != nil && m[2] == "rev":
			f.refuse(n, "a hook names a version; run its tool through a Makefile target, whose version this gate holds")
		case quoted != nil:
			f.refuse(n, "%s: a quoted key; write the key plain", quoted[1])
		case m == nil || m[2] != "entry":
			if preCommitEntryKey.MatchString(code) {
				f.refuse(n, "entry: is not a block key on its own line; write the hook in block style")
			}
		default:
			raw := strings.TrimSpace(m[3])
			value, _ := workflowValue(raw)
			shape := preCommitShape(raw, value)
			under, reported = indent(line)+len(m[1]), shape != ""
			if shape != "" {
				f.refuse(n, "entry: %s; write the command on the key's line, unquoted or quoted with no escape", shape)
			} else {
				f.entries = append(f.entries, workflowRun{line: n, text: value})
			}
		}
	}
	return f
}

// preCommitShape names the shape of an entry the reader cannot take whole,
// or is "".
func preCommitShape(raw, value string) string {
	switch {
	case value == "":
		return "the value is on another line"
	case workflowBlockScalar.MatchString(value):
		return "a block scalar"
	case strings.ContainsRune("&*!", rune(raw[0])):
		return "an anchor, an alias or a tag"
	case strings.ContainsRune("{[", rune(raw[0])):
		return "a flow collection"
	case workflowQuoteEscape(raw):
		return "a quoted value with an escape in it"
	}
	return ""
}

func (f *preCommitFile) refuse(n int, format string, a ...any) {
	f.refused = append(f.refused, fmt.Sprintf("%s:%d: %s", pinsPreCommit, n, fmt.Sprintf(format, a...)))
}

// pinsActionRef holds one action reference to a commit SHA, with the
// version it is in the comment after it.
func pinsActionRef(at string, s workflowStep, r *pinsReport) {
	r.actions++
	action, sha, ok := strings.Cut(s.uses, "@")
	switch {
	case !ok:
		r.fail("%s: %s has no ref at all", at, action)
	case !fullSHA.MatchString(sha):
		r.fail("%s: %s is pinned to %q, a tag or branch that can be moved; pin the 40-character commit SHA",
			at, action, sha)
	case s.comment == "":
		r.fail("%s: %s is pinned by SHA with no comment saying which version it is", at, action)
	}
}

// pinsStep checks one step that uses an action: an installer sets the
// key that pins its tool, any other action is classified, and every
// version input names one exact version.
func pinsStep(gomod string, w workflowFile, s workflowStep, r *pinsReport) {
	file, action := w.path, s.action()
	at := fmt.Sprintf("%s:%d: %s", file, s.line, action)
	installer := false
	for _, t := range pinsTools {
		key, ok := t.actions[action]
		if !ok {
			continue
		}
		installer = true
		r.installers++
		r.installed[t.name] = true
		v, set := s.with[key]
		switch {
		case !set:
			r.fail("%s is pinned by SHA and does not set %s, so the tool it installs is whatever "+
				"is current that morning", at, key)
		case t.name == "go" && path.Clean(v.value) != "go.mod":
			r.fail("%s sets %s to %q, which is not the repository's go.mod", at, key, v.value)
		case t.name == "go":
			r.add(t.name, pinsGoVersion(gomod), at)
		default:
			r.add(t.name, v.value, fmt.Sprintf("%s:%d: %s", file, v.line, action))
		}
	}
	if _, known := pinsNotInstallers[action]; !installer && !known {
		r.fail("%s is not classified: add it to pinsTools with the input that pins its tool, "+
			"or to pinsNotInstallers with the reason", at)
	}
	for _, key := range slices.Sorted(maps.Keys(s.with)) {
		v := s.with[key]
		installs := slices.ContainsFunc(pinsTools, func(t pinsTool) bool { return t.actions[action] == key })
		if installs || !workflowVersionKey.MatchString(key) {
			continue
		}
		if !exactSemver.MatchString(v.value) {
			r.fail("%s:%d: %s: %s is %q, which is not one exact version", file, v.line, action, key, v.value)
		}
	}
}

// pinsGoRun is one module@version the go command runs or installs.
type pinsGoRun struct{ verb, pkg string }

// pinsGoRuns is each module@version a command line runs or installs with
// the go command, read as the shell splits it, its flags skipped. A
// package of this module has no version and is not one.
func pinsGoRuns(line string) []pinsGoRun {
	var out []pinsGoRun
	for _, run := range goRunsIn(line) {
		if strings.Contains(run.pkg, "@") {
			out = append(out, pinsGoRun{"run", run.pkg})
		}
	}
	for _, c := range goCommands(line) {
		if c.sub != "install" {
			continue
		}
		for _, pkg := range goAfterFlags(c.args) {
			if strings.Contains(pkg, "@") {
				out = append(out, pinsGoRun{"install", pkg})
			}
		}
	}
	return out
}

// pinsGoVersion is the Go a go.mod runs: its toolchain directive, or else
// its go line. setup-go installs that, and the go command switches to it
// (https://github.com/actions/setup-go/blob/b7ad1dad31e06c5925ef5d2fc7ad053ef454303e/src/installer.ts#L645,
// setup-go v7.0.0).
func pinsGoVersion(gomod string) string {
	if m := pinsGoModToolchain.FindStringSubmatch(gomod); m != nil {
		return m[1]
	}
	if m := pinsGoModGo.FindStringSubmatch(gomod); m != nil {
		return m[1]
	}
	return ""
}

// pinsMakefileVars reads every `module@version` variable, and returns each
// module@version every such variable's value holds, by variable.
func pinsMakefileVars(mk *makefile, r *pinsReport) map[string][]string {
	pinnedBy := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(mk.vars)) {
		// Recipes are read expanded, so a version kept in another
		// variable is compared as the recipe runs it.
		value := mk.expand("$(" + name + ")")
		if workflowVersionKey.MatchString(name) {
			r.fail("%s: %s holds a version apart from its module; write the tool as one `NAME ?= module@version` variable",
				makefilePath, name)
			continue
		}
		found := pinsModuleAt.FindAllStringSubmatch(value, -1)
		for _, m := range found {
			pinnedBy[name] = append(pinnedBy[name], m[0])
		}
		if strings.Contains(value, "@latest") {
			r.fail("%s: %s is pinned to @latest", makefilePath, name)
			r.judgeLatest(value)
			continue
		}
		for _, m := range found {
			if tool := pinsToolByModule(m[1]); tool != "" {
				r.add(tool, m[2], makefilePath+": "+name)
			} else {
				r.fail("%s: %s names %s, which pinsTools does not know", makefilePath, name, m[1])
			}
		}
	}
	return pinnedBy
}

// pinsMakefileRecipes holds what the recipes run with the go command. A
// module written into a recipe is a pin of its own, and a variable in
// pinnedBy whose module@version no recipe naming it runs pins nothing: the
// tool then runs from the PATH.
func pinsMakefileRecipes(mk *makefile, pinnedBy map[string][]string, r *pinsReport) {
	// ran is each variable and module@version a recipe naming it runs.
	ran := map[[2]string]bool{}
	for _, target := range slices.Sorted(maps.Keys(mk.targets)) {
		for _, raw := range mk.targets[target].recipe {
			refs := mk.refs(raw)
			for _, g := range pinsGoRuns(mk.expand(raw)) {
				named := false
				for name := range refs {
					if slices.Contains(pinnedBy[name], g.pkg) {
						named = true
						ran[[2]string{name, g.pkg}] = true
					}
				}
				if named {
					continue
				}
				module, version, _ := strings.Cut(g.pkg, "@")
				if tool := pinsToolByModule(module); tool != "" {
					r.add(tool, version, makefilePath+": "+target)
				} else {
					r.fail("%s: %s runs go %s %s, a tool pinsTools does not know", makefilePath, target, g.verb, g.pkg)
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(pinnedBy)) {
		for _, pkg := range pinnedBy[name] {
			if !ran[[2]string{name, pkg}] {
				r.fail("%s: %s pins %s and no recipe runs it with the go command, so the tool runs from the PATH",
					makefilePath, name, pkg)
			}
		}
	}
}

// pinsTextVersions holds a known tool's module@version on any line of text
// but a comment line to one exact version, whatever the readers made of
// the line, expanded by expand first. A version a rule already recorded or
// refused is not reported again.
func pinsTextVersions(file, text string, expand func(string) string, r *pinsReport) {
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range pinsModuleAt.FindAllStringSubmatch(expand(line), -1) {
			tool, version := pinsToolByModule(m[1]), m[2]
			if tool == "" || exactSemver.MatchString(version) || r.judged[tool+"@"+version] {
				continue
			}
			r.fail("%s:%d: %s is pinned to %q, which is not one exact version", file, i+1, tool, version)
		}
	}
}

// pinsToolByModule is the tool a module path is, or one of its packages
// is: a path that only starts with the same text is another module.
func pinsToolByModule(module string) string {
	for _, t := range pinsTools {
		for _, m := range t.modules {
			if module == m || strings.HasPrefix(module, m+"/") {
				return t.name
			}
		}
	}
	return ""
}

func pinsToolByEnv(key string) string {
	for _, t := range pinsTools {
		if slices.Contains(t.envKeys, key) {
			return t.name
		}
	}
	return ""
}

func pinsNormal(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func indent(line string) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return -1 // a blank line has no indentation of its own
}
