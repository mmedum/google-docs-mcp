package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	// The prerequisites of the `check` target, which is what a person
	// runs and what the Makefile calls everything CI runs.
	checkTarget = regexp.MustCompile(`(?m)^check:\s*([^#\n]*)`)
	// `go run ./scripts/gates NAME` anywhere in a workflow.
	ciGate = regexp.MustCompile(`go run \./scripts/gates ([a-z-]+)`)
	// A vet pass with a build tag, in either place.
	vetTags = regexp.MustCompile(`vet -tags=([a-z]+)`)
)

// parity holds `make check` and the CI workflow to the same set of
// gates.
//
// The two lists live in different files and you are only ever editing
// one of them, so they drift silently and in whichever direction hurts
// most: here the local list ran four `go vet` passes and CI ran one, so
// fourteen build-tagged files compiled on a maintainer's laptop and
// nowhere else. Three of the four servers in this family had the same
// divergence, in one direction or the other, which is what turned it
// from a local tidy-up into a rule.
//
// The comparison is by command rather than by target name, because a
// target and the thing CI runs for it need not be spelled alike — a
// lesson from the first repository to build this, where `make cover`
// runs a script and the target name appears in no workflow at all.
func parity(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return err
	}
	workflows, err := readWorkflows(root)
	if err != nil {
		return err
	}
	ci := ""
	for _, f := range workflows {
		if f.name == "ci.yml" {
			ci = f.data
		}
	}
	if ci == "" {
		return fmt.Errorf("no ci.yml among the workflows; the check is reading the wrong place")
	}

	// Every gate this program declares must be reachable from both.
	var declared []string
	for name, c := range commands {
		if c.gate {
			declared = append(declared, name)
		}
	}
	slices.Sort(declared)
	if len(declared) < 5 {
		return fmt.Errorf("only %d gates declared; the registry is not being read", len(declared))
	}

	m := checkTarget.FindSubmatch(makefile)
	if m == nil {
		return fmt.Errorf("no `check:` target in the Makefile; the check is reading the wrong file")
	}
	prereqs := strings.Fields(string(m[1]))
	if len(prereqs) < 5 {
		return fmt.Errorf("the check target has %d prerequisites; that is not the whole list", len(prereqs))
	}

	inCI := map[string]bool{}
	for _, m := range ciGate.FindAllStringSubmatch(ci, -1) {
		inCI[m[1]] = true
	}
	// A gate CI runs through `go test` rather than through this program
	// still runs; the registry names the command, and the workflow may
	// reach it either way.
	if strings.Contains(ci, "go test") {
		inCI["coverage"] = true
	}

	var problems []string
	for _, name := range declared {
		if !inCI[name] {
			problems = append(problems, fmt.Sprintf(
				"gate %q is declared in scripts/gates but no CI workflow runs it", name))
		}
		if !slices.Contains(prereqs, name) && !slices.Contains(prereqs, makeTargetFor(name)) {
			problems = append(problems, fmt.Sprintf(
				"gate %q is declared in scripts/gates but `make check` does not depend on it", name))
		}
	}

	// The specific divergence that started this: a tagged vet pass that
	// only ever runs locally. Build tags are where a whole package can
	// stop compiling with CI perfectly green.
	local := tagSet(vetTags.FindAllStringSubmatch(string(makefile), -1))
	remote := tagSet(vetTags.FindAllStringSubmatch(ci, -1))
	for _, tag := range local {
		if !slices.Contains(remote, tag) {
			problems = append(problems, fmt.Sprintf(
				"`make vet` runs `-tags=%s` and no workflow does; those files compile only on a "+
					"maintainer's machine", tag))
		}
	}
	for _, tag := range remote {
		if !slices.Contains(local, tag) {
			problems = append(problems, fmt.Sprintf(
				"CI runs `vet -tags=%s` and `make vet` does not; a contributor cannot reproduce it", tag))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "parity ok: %d gates in both, %d tagged vet passes in both\n",
		len(declared), len(local))
	return err
}

// makeTargetFor maps a gate to the Makefile target that runs it, for the
// two whose names differ. Kept deliberately short: a long map here would
// mean the targets and the gates have stopped resembling each other,
// which is its own problem.
func makeTargetFor(gate string) string {
	switch gate {
	case "coverage":
		return "cover"
	case "classes":
		return "gate-classes"
	}
	return gate
}

func tagSet(matches [][]string) []string {
	var tags []string
	for _, m := range matches {
		if !slices.Contains(tags, m[1]) {
			tags = append(tags, m[1])
		}
	}
	slices.Sort(tags)
	return tags
}
