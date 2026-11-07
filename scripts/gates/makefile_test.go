package main

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestMakefileReadsAsMakeDoes(t *testing.T) {
	mk := parseMakefile(textLines(
		"A ?= one",
		"A ?= two",
		"B = x#comment",
		"B += y",
		"C += z",
		"export D := d",
		"override E = e",
		"F := first",
		"F = second",
		`G = docs/architecture.md\#d-67 # note`,
		`H = a\\#b`,
		`I = a\\\#b # c`,
		"J = one \\",
		"    two",
		"K = $(A) $(G)",
		"check: a",
		"\techo one",
		"check: b a ## a help text",
		"check:",
		"lint:",
		"\t-echo old",
		"lint:",
		"\techo new",
		"quiet:",
		"\t@-echo one",
		"\t+ echo two",
		"\t# a comment the shell would skip",
		"long:",
		"\techo $(A) \\",
		"\t  $$HOME $$(B)",
	))
	// \# is a literal #, and a run of backslashes before a # is halved, as
	// GNU make 4.4.1 reads them.
	wantVars := map[string]string{"A": "one", "B": "x y", "C": "z", "D": "d", "E": "e", "F": "second",
		"G": "docs/architecture.md#d-67", "H": `a\`, "I": `a\#b`, "J": "one two", "K": "$(A) $(G)"}
	if !maps.Equal(mk.vars, wantVars) {
		t.Errorf("vars = %q, want %q", mk.vars, wantVars)
	}
	if got := mk.targets["check"].prereqs; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("check's prerequisites = %q, want [a b]: a target's rules merge them, each once", got)
	}
	if got := mk.recipe("check"); !slices.Equal(got, []string{"echo one"}) {
		t.Errorf("check's recipe = %q, want [echo one]: a rule with no recipe leaves the one before", got)
	}
	if got := mk.recipe("lint"); !slices.Equal(got, []string{"echo new"}) {
		t.Errorf("lint's recipe = %q, want [echo new]: the last recipe is the one that runs", got)
	}
	if got := mk.recipe("quiet"); !slices.Equal(got, []string{"echo one", "echo two"}) {
		t.Errorf("quiet's recipe = %q, want [echo one echo two]: @, - and + are not part of a command", got)
	}
	// They are kept beside it: a - tells make to carry on past a failure.
	if got := mk.targets["quiet"].prefixes; !slices.Equal(got, []string{"@-", "+"}) {
		t.Errorf("quiet's prefixes = %q, want [@- +]", got)
	}
	if got := mk.targets["lint"].prefixes; !slices.Equal(got, []string{""}) {
		t.Errorf("lint's prefixes = %q, want one empty: the last recipe's, as the recipe is", got)
	}
	// $$ is the $ make hands the shell, never the start of a variable.
	if got := mk.recipe("long"); !slices.Equal(got, []string{"echo one $HOME $(B)"}) {
		t.Errorf("long's recipe = %q, want [echo one $HOME $(B)]", got)
	}
	if got := mk.closure("check"); !slices.Equal(got, []string{"a", "b", "check"}) {
		t.Errorf("closure(check) = %q, want [a b check]", got)
	}
	// A variable's value names variables in turn, and expands through them.
	if got := mk.expand("$(K)"); got != "one docs/architecture.md#d-67" {
		t.Errorf("expand($(K)) = %q, want %q", got, "one docs/architecture.md#d-67")
	}
	if got := mk.refs("$(K) $(Z)"); !maps.Equal(got, map[string]bool{"K": true, "A": true, "G": true, "Z": true}) {
		t.Errorf("refs = %v, want K, A, G and Z", got)
	}
	if mk.refused != nil {
		t.Errorf("refused = %q, want nothing: every line is one the reader reads", mk.refused)
	}
}

// := expands its value once, where it is set, with the variables set so
// far: one set later changes nothing, one never set is empty, and one the
// environment sets, such as PATH, stays as written. The value is final, so
// PATH := $(GOBIN):$(PATH) names PATH once
// (https://www.gnu.org/software/make/manual/html_node/Simple-Assignment.html).
func TestMakefileExpandsASimpleVariableWhereItIsSet(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("MAKEFILE_TEST_UNSET", "")
	if err := os.Unsetenv("MAKEFILE_TEST_UNSET"); err != nil {
		t.Fatal(err)
	}
	mk := parseMakefile(textLines(
		"A := x@v1",
		"R = $(A)",
		"B := $(A)",
		"C := $(R) $(MAKEFILE_TEST_UNSET)end",
		"A := y@v2",
		"GO ?= go",
		"GOBIN := $(shell $(GO) env GOPATH)/bin",
		"export PATH := $(GOBIN):$(PATH)",
		"D := $$HOME $(L)",
		"L = late",
		"S := one",
		"S += $(A)",
		"P := first",
		"P = $(L)",
		"A := z@v3",
		"run:",
		"\techo $(B) $(R) $(PATH) $(D)",
	))
	for ref, want := range map[string]string{
		"$(B)":     "x@v1",
		"$(C)":     "x@v1 end",
		"$(R)":     "z@v3",
		"$(GOBIN)": "$(shell go env GOPATH)/bin",
		"$(PATH)":  "$(shell go env GOPATH)/bin:$(PATH)",
		"$(D)":     "$HOME ",
		"$(S)":     "one y@v2",
		"$(P)":     "late",
	} {
		if got := mk.expand(ref); got != want {
			t.Errorf("expand(%s) = %q, want %q", ref, got, want)
		}
	}
	if got := mk.recipe("run"); !slices.Equal(got, []string{"echo x@v1 z@v3 $(shell go env GOPATH)/bin:$(PATH) $HOME "}) {
		t.Errorf("run's recipe = %q", got)
	}
	// A simple variable names what its value named when it was set.
	if got := mk.refs("$(B)"); !maps.Equal(got, map[string]bool{"B": true, "A": true}) {
		t.Errorf("refs($(B)) = %v, want B and A", got)
	}
}

// A line outside what the reader knows is refused by name, never dropped:
// a recipe or a variable it hides is one no gate would see.
func TestMakefileRefusesWhatItCannotRead(t *testing.T) {
	mk := parseMakefile(textLines(
		"ifeq ($(OS),Windows_NT)",
		"EXE = .exe",
		"endif",
		"-include local.mk",
		"VERSION != git describe",
		"$(BIN): main.go",
		"\tgo build -o $(BIN) \\",
		"\t  ./cmd/x",
		"lint: GOFLAGS = -mod=mod",
		"vet test: gates",
		"%.out: %.in",
		"clean:: cache",
		"fmt: ; gofmt -l .",
		"check: lint # fmt: ; x",
		"\tgo vet ./...",
	))
	want := []string{
		`Makefile:1: "ifeq ($(OS),Windows_NT)" is a directive or an assignment, which this reader does not read`,
		`Makefile:3: "endif" is a directive or an assignment, which this reader does not read`,
		`Makefile:4: "-include local.mk" is a directive or an assignment, which this reader does not read`,
		`Makefile:5: "VERSION != git describe" is a directive or an assignment, which this reader does not read`,
		`Makefile:6: "$(BIN): main.go" is a rule whose target is a variable, which this reader does not read`,
		`Makefile:7: "go build -o $(BIN) ./cmd/x" is a recipe line outside any rule this reader reads`,
		`Makefile:9: "lint: GOFLAGS = -mod=mod" is a target-specific variable, which this reader does not read`,
		`Makefile:10: "vet test: gates" is a rule with more than one target, which this reader does not read`,
		`Makefile:11: "%.out: %.in" is a pattern rule, which this reader does not read`,
		`Makefile:12: "clean:: cache" is a double-colon rule, which this reader does not read`,
		`Makefile:13: "fmt: ; gofmt -l ." is a recipe after a semicolon, which this reader does not read`,
	}
	if !slices.Equal(mk.refused, want) {
		t.Errorf("refused:\n  %s\nwant:\n  %s", strings.Join(mk.refused, "\n  "), strings.Join(want, "\n  "))
	}
	// A refused rule is no target, and a rule beside it is read whole.
	if got := slices.Sorted(maps.Keys(mk.targets)); !slices.Equal(got, []string{"check"}) {
		t.Errorf("targets = %q, want [check]", got)
	}
	if got := mk.recipe("check"); !slices.Equal(got, []string{"go vet ./..."}) {
		t.Errorf("check's recipe = %q, want [go vet ./...]", got)
	}
}
