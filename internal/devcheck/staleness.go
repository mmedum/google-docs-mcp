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
