package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-docs-mcp/v4/internal/auth"
	"github.com/mmedum/google-docs-mcp/v4/internal/config"
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
	// registered is every tool the binary registers, sorted.
	registered   []string
	readme, conf string
}

// readStaleness reads the checkout and the binary's --dump-schemas
// document, dump.
func readStaleness(dump []byte) (stalenessInputs, error) {
	var in stalenessInputs
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
	problems := scopeProblems(prose)
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
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
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
// It is read with its fences blanked: an example documents nothing.
func configDocProblems(vars []config.Var, text string) []string {
	text = strings.Join(docsProse(text), "\n")
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
	return append(problems, configDefaultProblems(vars, documented, text)...)
}

// configDefaultProblems holds each table row's Default cell to the value
// the code falls back to, where that is a literal; a default worked out
// later, such as the write mode, is prose the page explains.
func configDefaultProblems(vars []config.Var, documented map[string]bool, text string) []string {
	cells := map[string]string{}
	var problems []string
	for _, m := range settingRow.FindAllStringSubmatch(text, -1) {
		if _, twice := cells[m[1]]; twice {
			problems = append(problems, fmt.Sprintf("%s has two table rows for %s; give each setting one", configDocPath, m[1]))
		}
		cells[m[1]] = m[2]
	}
	for _, v := range vars {
		// One left out of the page is already reported as that.
		if v.Default == "" || !documented[v.Name] {
			continue
		}
		want := "`" + v.Default + "`"
		switch cell, ok := cells[v.Name]; {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s has no table row, in the form | `NAME` | default | meaning |, for %s to give its default %s",
				configDocPath, v.Name, want))
		case firstCode.FindString(cell) != want:
			problems = append(problems, fmt.Sprintf("%s gives %s a default without %s, the code's", configDocPath, v.Name, want))
		}
	}
	return problems
}

// firstCode is the first code span in a cell: the default it states.
var firstCode = regexp.MustCompile("`[^`]*`")

// settingRow is a row of the configuration page's table: the variable
// and its Default cell.
var settingRow = regexp.MustCompile("(?m)^\\| `(" + config.NamePattern + ")`[^|]*\\| ([^|]*) \\|")

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
