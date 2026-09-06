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

	declared := declaredGates()
	problems, vetPasses, err := parityCheck(string(makefile), ci, declared)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "parity ok: %d gates in both, %d tagged vet passes in both\n",
		len(declared), vetPasses)
	return err
}

// declaredGates is every command the registry marks as running in both
// places, sorted.
func declaredGates() []string {
	var declared []string
	for name, c := range commands {
		if c.gate {
			declared = append(declared, name)
		}
	}
	slices.Sort(declared)
	return declared
}

// parityCheck is the rule itself, over the text of the two files rather
// than the files. A gate that has never been watched fail is not yet a
// gate, and this one cannot be watched fail against the repository
// without first breaking the repository — so the reading is up there and
// the rule is here, where a test can hand it a Makefile and a workflow
// that disagree.
func parityCheck(makefile, ci string, declared []string) (problems []string, vetPasses int, err error) {
	if len(declared) < 5 {
		return nil, 0, fmt.Errorf("only %d gates declared; the registry is not being read", len(declared))
	}

	m := checkTarget.FindStringSubmatch(makefile)
	if m == nil {
		return nil, 0, fmt.Errorf("no `check:` target in the Makefile; the check is reading the wrong file")
	}
	prereqs := strings.Fields(m[1])
	if len(prereqs) < 5 {
		return nil, 0, fmt.Errorf("the check target has %d prerequisites; that is not the whole list", len(prereqs))
	}

	// Both sides are read with their commented-out lines removed, and the
	// CI side only counts a gate named by a `run:` step. That narrower
	// rule is safe only because the pattern is the invocation
	// (`go run ./scripts/gates NAME`) rather than the gate's bare name,
	// so it cannot appear anywhere but a `run:`. A sibling keyed the same
	// map on tool names, where `lint` is satisfied by a `uses:` action —
	// there the rule has to accept a `uses:` reference too. If a gate here
	// is ever satisfied by an action, this needs widening with it. Matching the raw
	// text meant that commenting out a step to unblock a red build left
	// parity green — the one divergence it exists to catch, achieved by
	// typing one `#`. What remains uncovered is a step disabled by an
	// `if:` that is never true; seeing that needs a YAML parse, and this
	// gate is not worth a dependency.
	inCI := map[string]bool{}
	for _, m := range ciGate.FindAllStringSubmatch(runLines(ci), -1) {
		inCI[m[1]] = true
	}

	for _, name := range declared {
		if !inCI[name] {
			problems = append(problems, fmt.Sprintf(
				"gate %q is declared in scripts/gates but no `run:` step in ci.yml runs it", name))
		}
		if !slices.Contains(prereqs, name) && !slices.Contains(prereqs, makeTargetFor(name)) {
			problems = append(problems, fmt.Sprintf(
				"gate %q is declared in scripts/gates but `make check` does not depend on it", name))
		}
	}

	// The specific divergence that started this: a tagged vet pass that
	// only ever runs locally. Build tags are where a whole package can
	// stop compiling with CI perfectly green.
	local := tagSet(vetTags.FindAllStringSubmatch(code(makefile), -1))
	remote := tagSet(vetTags.FindAllStringSubmatch(code(ci), -1))
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
	return problems, len(local), nil
}

// code drops whole-line comments, which both file formats spell with a
// leading `#`. Only whole lines: a `#` further along a line can be
// inside a string, and a trailing comment does not stop the command in
// front of it from running anyway.
func code(text string) string {
	var kept []string
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// runLines keeps the lines of a workflow that actually run something: a
// `run:` line, and the body of a `run: |` block, which is every line
// indented past the `run:` that opened it. Requiring the command on the
// `run:` line itself was safe here and wrong in general — it fails
// closed, so nothing slips through, but the first multi-line step
// somebody writes reports a gate CI plainly runs as missing. Block
// scalars are the one piece of YAML this needs to know, and indentation
// is all it takes to find the end of one.
func runLines(ci string) string {
	var kept []string
	body := -1
	for _, l := range strings.Split(code(ci), "\n") {
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		if body >= 0 {
			if strings.TrimSpace(l) == "" || indent > body {
				kept = append(kept, l)
				continue
			}
			body = -1
		}
		i := strings.Index(l, "run:")
		if i < 0 {
			continue
		}
		kept = append(kept, l)
		switch strings.TrimSpace(l[i+len("run:"):]) {
		case "|", "|-", "|+", ">", ">-", ">+":
			body = indent
		}
	}
	return strings.Join(kept, "\n")
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
