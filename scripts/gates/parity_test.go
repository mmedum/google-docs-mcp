package main

import (
	"io"
	"strings"
	"testing"
)

// The gate itself, run against this repository. It is what `make parity`
// runs, so a failure here is the failure a maintainer sees.
func TestParityGate(t *testing.T) {
	if err := parity(io.Discard, nil); err != nil {
		t.Errorf("parity gate: %v", err)
	}
}

// A Makefile and a workflow that agree. Every case below is this pair
// with one thing moved, because the divergences this gate exists to
// catch are all one edit away from correct.
const (
	parityMakefile = `# Common dev tasks.
.PHONY: vet
vet:
	go vet ./...
	go vet -tags=integration ./...

.PHONY: check
check: fmt vet cover leaks pins gate-classes parity ## Everything CI runs
`
	parityCI = `name: ci
jobs:
  test:
    steps:
      - run: go vet ./...
      - run: go vet -tags=integration ./...
      - run: go test -race -coverprofile=cov.out ./...
      - name: coverage floor
        run: go run ./scripts/gates coverage cov.out 80
      - run: go run ./scripts/gates leaks
      - run: go run ./scripts/gates pins
      - run: go run ./scripts/gates classes
      - run: go run ./scripts/gates parity
`
)

var parityDeclared = []string{"classes", "coverage", "leaks", "parity", "pins"}

func TestParityAcceptsAMatchedPair(t *testing.T) {
	problems, vetPasses, err := parityCheck(parityMakefile, parityCI, parityDeclared)
	if err != nil {
		t.Fatalf("parityCheck: %v", err)
	}
	if len(problems) > 0 {
		t.Errorf("a matched pair reported %d problems:\n%s", len(problems), strings.Join(problems, "\n"))
	}
	if vetPasses != 1 {
		t.Errorf("vetPasses = %d, want 1", vetPasses)
	}
}

// Both directions, and the two ways a step can be present in the text
// and not run.
func TestParityCatchesDivergence(t *testing.T) {
	const pinsStep = "      - run: go run ./scripts/gates pins\n"
	cases := []struct {
		name     string
		makefile string
		ci       string
		want     string
	}{
		{
			name: "CI stopped running a gate",
			ci:   strings.Replace(parityCI, pinsStep, "", 1),
			want: `gate "pins" is declared in scripts/gates but no `,
		},
		{
			name: "the step is commented out",
			ci:   strings.Replace(parityCI, pinsStep, "      # - run: go run ./scripts/gates pins\n", 1),
			want: `gate "pins" is declared in scripts/gates but no `,
		},
		{
			name: "the gate is named but nothing runs it",
			ci:   strings.Replace(parityCI, pinsStep, "      - name: go run ./scripts/gates pins\n", 1),
			want: `gate "pins" is declared in scripts/gates but no `,
		},
		{
			name:     "`make check` stopped depending on a gate",
			makefile: strings.Replace(parityMakefile, " pins", "", 1),
			want:     "`make check` does not depend on it",
		},
		{
			// The escape hatch this replaced: "CI runs `go test`
			// somewhere" was true of every workflow, so the coverage
			// gate could vanish from CI unnoticed.
			name: "coverage is only reached through go test",
			ci:   strings.Replace(parityCI, "        run: go run ./scripts/gates coverage cov.out 80\n", "", 1),
			want: `gate "coverage" is declared`,
		},
		{
			name: "a tagged vet pass runs only locally",
			ci:   strings.Replace(parityCI, "      - run: go vet -tags=integration ./...\n", "", 1),
			want: "compile only on a maintainer's machine",
		},
		{
			name:     "a tagged vet pass runs only in CI",
			makefile: strings.Replace(parityMakefile, "\tgo vet -tags=integration ./...\n", "", 1),
			want:     "a contributor cannot reproduce it",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			makefile, ci := c.makefile, c.ci
			if makefile == "" {
				makefile = parityMakefile
			}
			if ci == "" {
				ci = parityCI
			}
			problems, _, err := parityCheck(makefile, ci, parityDeclared)
			if err != nil {
				t.Fatalf("parityCheck: %v", err)
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("no problem containing %q; got:\n%s", c.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// A gate that finds too little to be looking at anything must say so
// rather than pass. Each of these once passed vacuously in the shell
// version of some gate in this directory.
func TestParityRefusesToRunOnTooLittle(t *testing.T) {
	cases := []struct {
		name     string
		makefile string
		ci       string
		declared []string
		want     string
	}{
		{
			name: "the registry is not being read",
			ci:   parityCI, makefile: parityMakefile,
			declared: []string{"leaks", "pins"},
			want:     "the registry is not being read",
		},
		{
			name:     "no check target",
			makefile: "build:\n\tgo build ./...\n",
			want:     "the check is reading the wrong file",
		},
		{
			name:     "the check target is a stub",
			makefile: "check: fmt vet\n",
			want:     "that is not the whole list",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			declared := c.declared
			if declared == nil {
				declared = parityDeclared
			}
			ci := c.ci
			if ci == "" {
				ci = parityCI
			}
			_, _, err := parityCheck(c.makefile, ci, declared)
			if err == nil {
				t.Fatal("no error; the gate passed on a file it cannot be reading")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err, c.want)
			}
		})
	}
}
