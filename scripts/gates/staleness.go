package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
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
	// A version heading, as Keep a Changelog writes it.
	versionHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
)

// staleness fails when the documentation drifts from the code. Each
// check answers a question nobody keeps asking by hand: does the README
// list the tools that exist, the scopes login requests and a permission
// rule for each tool it names, does configuration.md name every setting,
// and does the CHANGELOG say what changed. What the docs cite and link is
// the docs gate's.
func staleness(bin string) error {
	var problems []string

	// The README's tool table against the tools the binary registers.
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	var dump schemaDump
	if err := json.Unmarshal(out, &dump); err != nil {
		return fmt.Errorf("parse --dump-schemas: %w", err)
	}
	registered := make([]string, 0, len(dump.Tools))
	for _, t := range dump.Tools {
		registered = append(registered, t.Name)
	}
	sort.Strings(registered)
	if len(registered) == 0 {
		return fmt.Errorf("the binary registered no tools; the check is not looking at a server")
	}

	problems = append(problems, scopeProblems()...)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	var tabled []string
	for _, m := range readmeTool.FindAllStringSubmatch(string(readme), -1) {
		tabled = append(tabled, m[1])
	}
	sort.Strings(tabled)

	// Two directions, two sets, because they are asking different things.
	//
	// Every registered tool has to be documented somewhere, and the ones
	// that register only under GDOCS_ENABLE_DESTRUCTIVE are deliberately
	// not in the table: the table is the surface a person gets by
	// default, and they are described in the paragraph that explains the
	// flag. --dump-schemas dumps every tool, so the missing check reads
	// the whole file.
	//
	// The other direction reads the table only: a row naming
	// something that is not a tool is a real error, while an inline code
	// span is as likely to be a field or an op name.
	mentioned := map[string]bool{}
	for _, m := range readmeMention.FindAllStringSubmatch(string(readme), -1) {
		mentioned[m[1]] = true
	}
	var missing []string
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
	problems = append(problems, permissionProblems(string(readme), registered)...)

	// Every variable the server reads, named on the configuration page,
	// and no other. The list is config.Vars, recorded from the reads
	// rather than typed, so it cannot fall behind.
	conf, err := os.ReadFile(configDocPath)
	if err != nil {
		return err
	}
	vars := config.Vars()
	if len(vars) < config.VarsFloor {
		return fmt.Errorf("config.Vars lists %d variables, below the floor of %d; the check is not looking at the registry",
			len(vars), config.VarsFloor)
	}
	problems = append(problems, configDocProblems(vars, string(conf))...)

	changelog, err := checkChangelog()
	if err != nil {
		return err
	}
	problems = append(problems, changelog...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
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

// checkChangelog holds the checkout the gates run in to changelogRule:
// whether Go source differs from the last tag, or from the commit before
// HEAD when there is none, and which versions have a tag.
func checkChangelog() ([]string, error) {
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		return nil, err
	}
	last, err := lastTag("HEAD")
	if err != nil {
		return nil, couldNotRun{err}
	}
	base := cmp.Or(last, "HEAD~1")
	// An error is a difference or a base that does not exist; on the
	// first commit there is nothing to compare against, and asking for
	// the entry then is the safe way round.
	_, err = gitOutput("diff", "--quiet", base, "--", "*.go")
	changed := err != nil
	tagged := func(version string) bool {
		_, err := gitOutput("rev-parse", "-q", "--verify", "refs/tags/v"+version)
		return err == nil
	}
	return changelogRule(string(raw), changed, tagged), nil
}

// changelogRule requires a change to Go source to be written up: an
// entry under [Unreleased], or, on a release commit, under the newest
// version heading while that version has no tag. A release commit moves
// the entries under the version it is about to tag, and CI runs on it
// before the tag exists.
func changelogRule(text string, sourceChanged bool, tagged func(version string) bool) []string {
	if !sourceChanged {
		return nil
	}
	lines := strings.Split(text, "\n")
	if countEntries(lines, "Unreleased") > 0 {
		return nil
	}
	where := "[Unreleased]"
	if m := versionHeading.FindStringSubmatch(text); m != nil && !tagged(m[1]) {
		if countEntries(lines, m[1]) > 0 {
			return nil
		}
		where = "[" + m[1] + "], the untagged version this commit releases,"
	}
	return []string{"Go source changed and " + changelogPath + " " + where + " has no entry; " +
		"write one under [Unreleased], or under the version heading this release tags"}
}

// countEntries counts the entries in a version's section: the lines that
// open a bullet at the margin. An indented bullet sits inside a
// paragraph, and prose is no entry.
func countEntries(lines []string, version string) int {
	from, to := changelogSection(lines, version)
	n := 0
	for _, line := range lines[from:to] {
		if strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return n
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
func scopeProblems() []string {
	wanted := append(auth.Scopes(false), auth.Scopes(true)...)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		return []string{"README.md: " + err.Error()}
	}
	return scopesAgree(string(readme), wanted)
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
