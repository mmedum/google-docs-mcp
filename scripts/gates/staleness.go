package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-docs-mcp/v3/internal/auth"
	"github.com/mmedum/google-docs-mcp/v3/internal/config"
)

var (
	readmeTool = regexp.MustCompile(`(?m)^\| ` + "`" + `([a-z_]+)` + "`" + ` \|`)
	// A tool named anywhere in the prose, for the tools that register
	// only behind a flag and so are described rather than tabulated.
	readmeMention = regexp.MustCompile("`" + `([a-z_]+)` + "`")
)

// staleness fails when the documentation drifts from the code. Each
// check answers a question nobody keeps asking by hand: does the README
// list the tools that exist, the scopes login requests and a permission
// rule for each tool it names, does configuration.md name every setting,
// and does the CHANGELOG say what changed. What the docs cite and link is
// the docs gate's.
func staleness(bin string) error {
	dump, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	in, err := readStaleness(dump)
	if err != nil {
		return err
	}
	return stalenessCheck(in)
}

// stalenessInputs are what the gate reads, so a test can hand it a dump
// made in process.
type stalenessInputs struct {
	// changelog is what the CHANGELOG rule found, and unrun why it could
	// not run, or nil.
	changelog []string
	unrun     error
	// registered is every tool the binary registers, sorted.
	registered   []string
	readme, conf string
}

// readStaleness reads the checkout and the binary's --dump-schemas
// document, dump. A CHANGELOG rule that cannot run holds back no other
// check.
func readStaleness(dump []byte) (stalenessInputs, error) {
	var in stalenessInputs
	if in.changelog, in.unrun = checkChangelog("."); in.unrun != nil && !errors.As(in.unrun, new(couldNotRun)) {
		return in, in.unrun
	}
	var d schemaDump
	if err := json.Unmarshal(dump, &d); err != nil {
		return in, fmt.Errorf("parse --dump-schemas: %w", err)
	}
	for _, t := range d.Tools {
		in.registered = append(in.registered, t.Name)
	}
	sort.Strings(in.registered)
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return in, err
	}
	conf, err := os.ReadFile(configDocPath)
	if err != nil {
		return in, err
	}
	in.readme, in.conf = string(readme), string(conf)
	return in, nil
}

// stalenessCheck holds the docs to the code. The README's tools and scopes
// are read from its prose, its fences blanked: an example documents
// nothing. Its permission rules sit in a fence of their own.
func stalenessCheck(in stalenessInputs) error {
	if len(in.registered) == 0 {
		return fmt.Errorf("the binary registered no tools; the check is not looking at a server")
	}
	prose := strings.Join(docsProse(in.readme), "\n")
	problems := slices.Clone(in.changelog)
	problems = append(problems, scopeProblems(prose)...)
	problems = append(problems, readmeToolProblems(prose, in.registered)...)
	problems = append(problems, permissionProblems(in.readme, in.registered)...)

	// Every variable the server reads, named on the configuration page,
	// and no other. The list is config.Vars, recorded from the reads
	// rather than typed, so it cannot fall behind.
	vars := config.Vars()
	if len(vars) < config.VarsFloor {
		return fmt.Errorf("config.Vars lists %d variables, below the floor of %d; the check is not looking at the registry",
			len(vars), config.VarsFloor)
	}
	problems = append(problems, configDocProblems(vars, in.conf)...)

	if len(problems) > 0 {
		if in.unrun != nil {
			problems = append(problems, in.unrun.Error())
		}
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return in.unrun
}

// readmeToolProblems holds the README's prose to the tools the binary
// registers, registered.
//
// Two directions, two sets, because they are asking different things.
//
// Every registered tool has to be documented somewhere, and the ones
// that register only under GDOCS_ENABLE_DESTRUCTIVE are deliberately
// not in the table: the table is the surface a person gets by
// default, and they are described in the paragraph that explains the
// flag. --dump-schemas dumps every tool, so the missing check reads
// the whole prose.
//
// The other direction reads the table only: a row naming
// something that is not a tool is a real error, while an inline code
// span is as likely to be a field or an op name.
func readmeToolProblems(prose string, registered []string) []string {
	rows := readmeTool.FindAllStringSubmatch(prose, -1)
	tabled := make([]string, 0, len(rows))
	for _, m := range rows {
		tabled = append(tabled, m[1])
	}
	sort.Strings(tabled)
	mentioned := map[string]bool{}
	for _, m := range readmeMention.FindAllStringSubmatch(prose, -1) {
		mentioned[m[1]] = true
	}
	var problems, missing []string
	for _, name := range registered {
		if !mentioned[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "README does not document: "+strings.Join(missing, ", "))
	}
	if extra := difference(tabled, registered); len(extra) > 0 {
		problems = append(problems, "README's tool table names tools that are not registered: "+strings.Join(extra, ", "))
	}
	return problems
}

// configDocPath is the page every setting is documented on.
const configDocPath = "docs/configuration.md"

// configDocProblems holds the configuration page, text, to the variables
// the server reads, vars, both ways. It names every one as a whole word,
// a removed one it refuses and the two read outside Define included, so
// a person whose server will not start can find why, names no other, and
// says what a profile name is in the words the server refuses one with.
func configDocProblems(vars []config.Var, text string) []string {
	var problems []string
	documented := map[string]bool{}
	for _, m := range settingWord.FindAllStringSubmatch(text, -1) {
		documented[m[2]] = true
	}
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
		if !documented[v.Name] {
			problems = append(problems, fmt.Sprintf("%s does not document %s", configDocPath, v.Name))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		if !known[name] {
			problems = append(problems, fmt.Sprintf("%s documents %s, which the code does not read", configDocPath, name))
		}
	}
	if !strings.Contains(text, config.ProfileRule) {
		problems = append(problems, fmt.Sprintf("%s does not say a profile name is %q", configDocPath, config.ProfileRule))
	}
	return problems
}

// settingWord is a setting's name, by config.NamePattern, standing as a
// word of its own, all of it: a longer name that contains one, such as
// GDOCS_READ_ONLY_X, is that longer name and not GDOCS_READ_ONLY.
var settingWord = regexp.MustCompile(`(^|[^A-Z0-9_])(` + config.NamePattern + `)`)

// permissionRule is a Claude Code permission rule for one of this
// server's tools, under the server name the README's install line gives.
var permissionRule = regexp.MustCompile(`"mcp__google-docs__([a-z0-9_]+)"`)

// permissionRuleTools is every tool the README's permission rules name.
func permissionRuleTools(readme string) []string {
	matches := permissionRule.FindAllStringSubmatch(readme, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// permissionProblems holds the README's Claude Code permission rules to
// the registered tools. A rule for a tool that does not exist matches
// nothing, so the person it was meant to ask is never asked; and with no
// rule at all the README does not say how to be asked, since the
// server asks nobody itself.
func permissionProblems(readme string, registered []string) []string {
	rules := permissionRuleTools(readme)
	if len(rules) == 0 {
		return []string{`README gives no Claude Code permission rule ("mcp__google-docs__<tool>")`}
	}
	var problems []string
	for _, name := range difference(rules, registered) {
		problems = append(problems, "README gives a permission rule for "+name+", which the server does not register")
	}
	return problems
}

// checkChangelog holds the checkout at dir to changelogRule, measured from
// lastTag, the release schema-diff measures from too. No such tag, or a
// git or go call that cannot answer, is couldNotRun, never "source
// changed".
func checkChangelog(dir string) ([]string, error) {
	last, err := lastTag(dir, "HEAD")
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	if last == "" {
		return nil, changelogUnrun(dir, errors.New("HEAD reaches no release tag (vX.Y.Z) to measure from"))
	}
	list, err := gitOutput("-C", dir, "tag")
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	changed, err := shippedChangedSince(dir, last)
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, changelogPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", changelogPath, err)
	}
	// A version is released once a tag names it on any line of history.
	tags := strings.Fields(list)
	tagged := func(version string) bool { return slices.Contains(tags, "v"+version) }
	return changelogRule(string(raw), changed, tagged), nil
}

// changelogUnrun is why the CHANGELOG rule could not run in the checkout at
// dir. A shallow clone fetches no tag and too little history, so the
// reason names one.
func changelogUnrun(dir string, err error) error {
	if shallow, _ := gitOutput("-C", dir, "rev-parse", "--is-shallow-repository"); shallow == "true" {
		err = fmt.Errorf("%w; the checkout is a shallow clone: check it out with fetch-depth: 0", err)
	}
	return couldNotRun{fmt.Errorf("the CHANGELOG rule: %w", err)}
}

// changelogRule requires a change to the binary's source to be written
// up. Normally that means entries under [Unreleased]; on a release commit
// those notes have just moved under the new version heading, which is only
// acceptable while that version has no tag yet.
func changelogRule(text string, sourceChanged bool, tagged func(version string) bool) []string {
	if !sourceChanged {
		return nil
	}
	doc := readChangelog(text)
	if doc.entries("Unreleased") > 0 {
		return nil
	}
	where := "[Unreleased]"
	if v := doc.versions(); len(v) > 0 && !tagged(v[0].label) {
		if doc.entries(v[0].label) > 0 {
			return nil
		}
		where = "[" + v[0].label + "] (untagged, so this is a release commit)"
	}
	return []string{"the binary's source changed but " + changelogPath + " " + where + " is empty"}
}

// stalenessNewestRelease is the newest release tag's version by semver
// order, a prerelease included, or "" when there is none. git's version
// sort puts v2.0.0-rc.1 after v2.0.0, so the order is semver's.
func stalenessNewestRelease(tags []string) string {
	newest := ""
	for _, tag := range tags {
		v, ok := strings.CutPrefix(tag, "v")
		if ok && changelogVersion.MatchString(v) && (newest == "" || changelogCompare(v, newest) > 0) {
			newest = v
		}
	}
	return newest
}

// shippedChangedSince reports whether the commits since..HEAD of the
// checkout at dir change the binary's source: a Go file other than a test
// in a package of this module the binary imports, a file such a package
// embeds, or go.mod outside its require and tool lines. A package counts
// by its directory, so a file another platform builds counts, and so does
// a file removed from it. Commits are compared, as CI sees them.
func shippedChangedSince(dir, since string) (bool, error) {
	out, err := gitOutput("-C", dir, "diff", since+"..HEAD", "--name-only", "--no-renames", "-z")
	if err != nil {
		return false, err
	}
	var shipped *shippedSource
	for _, file := range strings.Split(out, "\x00") {
		switch file {
		case "":
		case "go.mod":
			if changed, err := goModChangedSince(dir, since); err != nil || changed {
				return changed, err
			}
		default:
			if shipped == nil {
				if shipped, err = listShipped(dir); err != nil {
					return false, err
				}
			}
			if shipped.has(file) {
				return true, nil
			}
		}
	}
	return false, nil
}

// shippedSource is what the binary is built from in this module, on this
// platform: its package directories and the files they embed, each
// slash-separated from the module root.
type shippedSource struct{ dirs, embeds map[string]bool }

func (s *shippedSource) has(file string) bool {
	return s.embeds[file] || strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") && s.dirs[path.Dir(file)]
}

// listShipped asks the go command what ./cmd/... is built from in the
// module at dir. A package of another module is a dependency, which
// go.mod's require lines name.
func listShipped(dir string) (*shippedSource, error) {
	const what = "go list -deps ./cmd/..."
	cmd := exec.Command("go", "list", "-deps", "-json=Dir,EmbedFiles,Module", "./cmd/...")
	cmd.Dir = dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return nil, fmt.Errorf("%s: %s", what, strings.TrimSpace(string(exit.Stderr)))
	case err != nil:
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	s := &shippedSource{dirs: map[string]bool{}, embeds: map[string]bool{}}
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p struct {
			Dir        string
			EmbedFiles []string
			Module     *struct {
				Main bool
				Dir  string
			}
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		pkg := filepath.ToSlash(rel)
		s.dirs[pkg] = true
		for _, f := range p.EmbedFiles {
			s.embeds[path.Join(pkg, f)] = true
		}
	}
	// An empty listing is not reading the binary, and would excuse every
	// change.
	if len(s.dirs) == 0 {
		return nil, fmt.Errorf("%s named no package of this module, so it is not reading the binary", what)
	}
	return s, nil
}

// goModChangedSince reports whether go.mod at HEAD in the checkout at dir
// differs from since's outside its require and tool lines: a go, toolchain
// or replace line changes what the binary is built with.
func goModChangedSince(dir, since string) (bool, error) {
	before, err := gitOutput("-C", dir, "show", since+":go.mod")
	if err != nil {
		return false, err
	}
	after, err := gitOutput("-C", dir, "show", "HEAD:go.mod")
	if err != nil {
		return false, err
	}
	return goModBuildLines(before) != goModBuildLines(after), nil
}

// goModBuildLines is a go.mod's lines, each trimmed and the blank ones
// dropped, without its require and tool directives, a block's included
// (https://go.dev/ref/mod#go-mod-file-require). A tool directive adds a
// package `go tool` runs (https://go.dev/ref/mod#go-mod-file-tool). ( is a
// token of its own, so `require(` opens a block as `require (` does
// (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren || len(fields) > 1 && strings.HasPrefix(fields[1], "(")
		default:
			kept = append(kept, strings.Join(fields, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// difference returns the members of a that are not in b. Both sorted.
func difference(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}

// scopeProblems holds the README's OAuth scope list to the scopes the
// code actually requests.
//
// The setup step is the one page a person follows exactly once, with no
// way to tell whether it worked until a login fails. Read-only mode
// requests scopes of its own, and a mode-gated scope is the one nobody
// notices, because it is absent from every run that does not use the
// mode.
//
// The list is derived from auth.Scopes rather than typed here, so adding
// a scope fails this until the README says so.
func scopeProblems(readme string) []string {
	wanted := append(auth.Scopes(false), auth.Scopes(true)...)
	slices.Sort(wanted)
	return scopesAgree(readme, slices.Compact(wanted))
}

// scopesAgree is the rule itself, over the README's text. It is split
// from the reading because a rule that lives in the reader is one a test
// of the rule can walk past.
func scopesAgree(text string, wanted []string) []string {
	if len(wanted) < 2 {
		return []string{"auth.Scopes returned fewer than two scopes; the derivation is not reading the code"}
	}

	var problems []string
	for _, scope := range wanted {
		// The README writes them abbreviated, as `.../auth/documents`.
		if !strings.Contains(text, scope) && !strings.Contains(text, shortScope(scope)) {
			problems = append(problems, "README does not list the OAuth scope "+scope+
				", which login requests; an External app's consent screen built from it will refuse that mode")
		}
	}
	for _, found := range readmeScopes.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(wanted, "https://www.googleapis.com/auth/"+found[1]) {
			problems = append(problems, "README lists the OAuth scope "+found[1]+
				", which login never requests; a reader who checks will find it missing and doubt the rest")
		}
	}
	return problems
}

// readmeScopes matches a scope however the README abbreviates it.
var readmeScopes = regexp.MustCompile(`(?:https://www\.googleapis\.com|\.\.\.)/auth/([a-z.]+)`)

// shortScope is the abbreviation the README uses in prose.
func shortScope(scope string) string {
	return ".../auth/" + strings.TrimPrefix(scope, "https://www.googleapis.com/auth/")
}
