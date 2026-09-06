package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var (
	// def(&s.Field, "flag-name", "ENV_KEY", default, usage) is how every
	// setting is registered, so the list of settings is derivable rather
	// than typed. The shell version this replaces carried a hand-written
	// case list of eleven names, which is the same shape as the coverage
	// floor's hand-written package list: silent when it falls behind.
	configKey  = regexp.MustCompile(`def\(&s\.\w+,\s*"[a-z-]+",\s*"([A-Z_]+)"`)
	readmeTool = regexp.MustCompile(`(?m)^\| ` + "`" + `([a-z_]+)` + "`" + ` \|`)
	// A version heading, as Keep a Changelog writes it.
	versionHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
	// The status line at the top of each document, which is the first
	// thing anyone reads and the last thing anyone updates.
	// Both spellings count: `**Status:** v1.0.0` is what this repository
	// writes, and `**Status: v1.0.0.**` is what it used to, which is also
	// what a sibling copying this check may still have. Accepting the
	// form you no longer use costs one optional group and saves the next
	// repository a debugging session.
	statusLine = regexp.MustCompile(`(?m)^\*\*Status:(?:\*\*)? +v?(\d+\.\d+\.\d+)`)
)

// staleness fails when the documentation drifts from the code. Each
// check answers a question somebody asked once and then stopped asking:
// does the README list the tools that exist, does configuration.md
// mention every setting, does the CHANGELOG say what changed, does the
// architecture still claim there is no code.
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

	readme, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	var documented []string
	for _, m := range readmeTool.FindAllStringSubmatch(string(readme), -1) {
		documented = append(documented, m[1])
	}
	sort.Strings(documented)
	if missing := difference(registered, documented); len(missing) > 0 {
		problems = append(problems, "README's tool table is missing: "+strings.Join(missing, ", "))
	}
	if extra := difference(documented, registered); len(extra) > 0 {
		problems = append(problems, "README's tool table names tools that are not registered: "+strings.Join(extra, ", "))
	}

	// Every setting config.go registers, mentioned in the configuration
	// document under its full environment name.
	cfg, err := os.ReadFile("internal/config/config.go")
	if err != nil {
		return err
	}
	conf, err := os.ReadFile("docs/configuration.md")
	if err != nil {
		return err
	}
	keys := configKey.FindAllStringSubmatch(string(cfg), -1)
	if len(keys) < 5 {
		return fmt.Errorf("found %d settings in config.go; the def() pattern has probably changed", len(keys))
	}
	for _, m := range keys {
		if !strings.Contains(string(conf), "GDOCS_"+m[1]) {
			problems = append(problems, "docs/configuration.md does not document GDOCS_"+m[1])
		}
	}

	arch, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(string(arch)), "no code yet") {
		problems = append(problems, "docs/architecture.md still says 'no code yet'")
	}

	if err := changelogDocumentsTheChange(); err != nil {
		problems = append(problems, err.Error())
	}

	problems = append(problems, staleStatusLines()...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// changelogDocumentsTheChange requires entries when Go source changed
// since the last tag.
//
// The exception in the middle is a scar: a release commit moves the
// entries out of [Unreleased] and under the version it is about to tag,
// and CI runs before that tag can exist — so this gate used to fail on
// every release pull request, which is the one it was written to guard.
func changelogDocumentsTheChange() error {
	last := strings.TrimSpace(run("git", "describe", "--tags", "--abbrev=0"))
	diffArgs := []string{"diff", "--quiet", "HEAD~1", "--", "*.go"}
	if last != "" {
		diffArgs = []string{"diff", "--quiet", last + "..HEAD", "--", "*.go"}
	}
	if exec.Command("git", diffArgs...).Run() == nil {
		return nil // no Go changed
	}

	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	if entriesUnder(string(changelog), "## [Unreleased]") > 0 {
		return nil
	}
	// Entries under a version heading whose tag does not exist yet are
	// this release's documentation.
	if m := versionHeading.FindStringSubmatch(string(changelog)); m != nil {
		if exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/v"+m[1]).Run() != nil &&
			entriesUnder(string(changelog), "## ["+m[1]+"]") > 0 {
			return nil
		}
	}
	since := last
	if since == "" {
		since = "the previous commit"
	}
	return fmt.Errorf("source changed since %s but CHANGELOG.md documents nothing new\n"+
		"put the entries under [Unreleased], or under the version heading this release is about to tag", since)
}

// entriesUnder counts "- " bullets between a heading and the next one.
func entriesUnder(changelog, heading string) int {
	n := 0
	inside := false
	for line := range strings.Lines(changelog) {
		switch {
		case strings.HasPrefix(line, heading):
			inside = true
		case strings.HasPrefix(line, "## ["):
			inside = false
		case inside && strings.HasPrefix(line, "- "):
			n++
		}
	}
	return n
}

func run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
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

// staleStatusLines checks the version each document claims at the top.
//
// This is here because both of them were wrong at once and nothing
// noticed: the README said v0.5.0 five releases after v0.5.0, and the
// architecture document said v0.9.1 and "phases 0, 1 and 2 are
// implemented" on the day v1.0.0 shipped with phases 0 to 4 done. They
// are the first lines a reader sees and the ones furthest from any test,
// which is exactly the combination this file exists for.
//
// Either the released version or the one about to be released counts,
// because a release commit writes the new version into these lines
// before the tag exists — the same scar changelogDocumentsTheChange
// carries, for the same reason.
func staleStatusLines() []string {
	tag := strings.TrimPrefix(strings.TrimSpace(run("git", "describe", "--tags", "--abbrev=0")), "v")
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return []string{"read CHANGELOG.md: " + err.Error()}
	}
	next := ""
	if m := versionHeading.FindSubmatch(changelog); m != nil {
		next = string(m[1])
	}
	data, err := os.ReadFile(architectureDoc)
	if err != nil {
		return []string{"read " + architectureDoc + ": " + err.Error()}
	}
	return statusProblems(string(data), tag, next)
}

// statusProblems is the whole rule, separated from the files so it can be
// tested. The paths that matter most cannot run in this repository, which
// always has a tag: they exist for a repository before its first release,
// and a gate that only ever runs one of its branches here would ship a
// defect green and misfire in a sibling. This file's own rule — a gate
// that has never been watched fail is not yet a gate — applies to the
// gate as much as to the code it guards.
//
// Only the architecture document is checked. The README used to carry a
// version too, and the honest fix for a copy of a fact going stale was to
// delete the copy rather than build machinery to maintain it: the release
// badge shows the version, updates itself and cannot be wrong.
// architectureDoc is the only document whose status line is checked.
const architectureDoc = "docs/architecture.md"

func statusProblems(text, tag, next string) []string {
	// Nothing tagged and nothing under a version heading is the normal
	// state of a project that has not shipped, not a failure. The failure
	// is a document claiming a version when nothing can confirm it.
	unreleased := tag == "" && next == ""

	var problems []string
	m := statusLine.FindStringSubmatch(text)
	switch {
	case m == nil && unreleased:
		// Nothing claimed, nothing shipped, nothing to check.
	case m == nil:
		problems = append(problems, architectureDoc+" has no **Status:** line; the check cannot see what it claims")
	case unreleased:
		problems = append(problems, fmt.Sprintf(
			"%s claims Status v%s, but there is no tag and no version heading to confirm it "+
				"— say which phase it is in until the first release", architectureDoc, m[1]))
	case m[1] != tag && m[1] != next:
		problems = append(problems, fmt.Sprintf(
			"%s says Status v%s; %s and the changelog's newest heading is %s",
			architectureDoc, m[1], describeTag(tag), describeVersion(next)))
	}

	// The version is the half a badge could carry. This is the half it
	// cannot: the same sentence claims whether any design decision is
	// open, and that claim was false on the day v1.0.0 shipped. Without
	// it the gate goes green on a release that bumps the number and
	// leaves the sentence beside it wrong, which is the second half of
	// the failure this gate was written for.
	if openInBody := !strings.Contains(sectionBody(text, "## 17."), "None."); openInBody != claimsOpenDecision(text) {
		claimed, actual := "no open decision", "has none"
		if !openInBody {
			claimed = "an open decision"
		} else {
			actual = "lists one"
		}
		problems = append(problems, fmt.Sprintf(
			"%s's status line claims %s while §17 %s", architectureDoc, claimed, actual))
	}
	return problems
}

// claimsOpenDecision reports whether the status paragraph says a design
// decision is open. Both spellings the document has used are accepted;
// neither is a shape worth being clever about.
func claimsOpenDecision(text string) bool {
	head := text
	if i := strings.Index(text, "\n## "); i > 0 {
		head = text[:i]
	}
	lower := strings.ToLower(head)
	// The negation first. "No design decision is open" contains "decision
	// is open", so a plain substring test reads a denial as a claim —
	// which is what it did, on the commit that closed §17.
	for _, denial := range []string{"no design decision is open", "no design decisions are open"} {
		if strings.Contains(lower, denial) {
			return false
		}
	}
	return strings.Contains(lower, "decision is open") || strings.Contains(lower, "decisions are open")
}

// sectionBody returns the text under a heading, up to the next one.
func sectionBody(text, heading string) string {
	i := strings.Index(text, heading)
	if i < 0 {
		return ""
	}
	rest := text[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// describeTag keeps the message readable when git told us nothing —
// run() discards its error, so an empty tag means "no tags" or "git
// failed", and "the released tag is v" reads like a truncated string.
func describeTag(tag string) string {
	if tag == "" {
		return "no released tag was found (no tags, or git is unavailable)"
	}
	return "the released tag is v" + tag
}

func describeVersion(v string) string {
	if v == "" {
		return "absent"
	}
	return "v" + v
}
