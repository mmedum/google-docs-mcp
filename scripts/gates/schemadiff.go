package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// schemaDiff compares the current build's tool schemas with the last
// tag's. A removed tool, a removed field or a newly required field
// breaks a caller, and this is what makes the version promise in the
// README enforceable rather than stated.
//
// It was a shell script. The reason it is not one any more is the whole
// of §1: this builds an old revision in a worktree, parses two JSON
// documents and compares them, and every one of those steps in bash is a
// place where a quote ends up inside a string. It also has to run on the
// Windows runner, where bash is a dependency rather than a given.
func schemaDiff(w io.Writer, args []string) error {
	bin := binOr(args)

	current, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	if err := os.WriteFile("schemas.json", current, 0o644); err != nil {
		return fmt.Errorf("write schemas.json: %w", err)
	}

	last := lastTag()
	if last == "" {
		_, err := fmt.Fprintln(w, "no previous tag; schemas.json written")
		return err
	}

	old, err := schemasAtTag(last)
	if err != nil {
		return err
	}
	oldDump, err := parseSchemas(old)
	if err != nil {
		return fmt.Errorf("%s: %w", last, err)
	}
	newDump, err := parseSchemas(current)
	if err != nil {
		return err
	}
	breaking := diff(w, oldDump, newDump)
	if len(breaking) == 0 {
		return nil
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	undeclared, err := undeclaredBreaks(breaking, string(changelog), last)
	if err != nil {
		return err
	}
	if len(undeclared) > 0 {
		for _, b := range undeclared {
			_, _ = fmt.Fprintln(w, "undeclared:", b)
		}
		return fmt.Errorf("the tool surface changed in a way that breaks a caller since %s, and the CHANGELOG does not declare it; "+
			"a release that breaks a tool names it, in backticks, in a **Breaking:** entry above [%s], and raises the major version",
			last, strings.TrimPrefix(last, "v"))
	}
	_, _ = fmt.Fprintf(w, "%d breaking change(s) since %s, each declared under **Breaking:** for a major release\n", len(breaking), last)
	return nil
}

// A version heading as Keep a Changelog writes it, a prerelease included.
var changelogHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+[^\]]*)\]`)

// undeclaredBreaks is each breaking change that the unreleased part of
// the CHANGELOG, everything above the tag's own heading, does not
// declare: a **Breaking:** entry there names its tool in backticks. A
// version heading there that does not raise the tag's major version is
// one too, since only a major release may break the surface (README,
// Versioning).
func undeclaredBreaks(breaking []string, changelog, tag string) ([]string, error) {
	version := strings.TrimPrefix(tag, "v")
	unreleased, _, found := strings.Cut(changelog, "\n## ["+version+"]")
	if !found {
		// Without the tag's heading nothing tells unreleased from
		// released, so no entry can declare a break.
		return nil, fmt.Errorf("CHANGELOG.md has no ## [%s] heading for %s, so no break can be declared against it", version, tag)
	}
	named := map[string]bool{}
	for line := range strings.Lines(unreleased) {
		if !strings.Contains(line, "**Breaking:**") {
			continue
		}
		for _, m := range readmeMention.FindAllStringSubmatch(line, -1) {
			named[m[1]] = true
		}
	}
	var out []string
	for _, b := range breaking {
		tool, _, _ := strings.Cut(strings.TrimPrefix(b, "tool removed: "), ":")
		if !named[tool] {
			out = append(out, b)
		}
	}
	major := func(v string) int {
		m, _, _ := strings.Cut(v, ".")
		n, _ := strconv.Atoi(m)
		return n
	}
	for _, m := range changelogHeading.FindAllStringSubmatch(unreleased, -1) {
		if major(m[1]) <= major(version) {
			out = append(out, fmt.Sprintf("release %s breaks the surface without raising the major version from %s", m[1], version))
		}
	}
	return out, nil
}

// schemasAtTag builds the binary as it was at a tag and asks it for its
// schemas. The worktree is removed whether or not the build succeeds:
// leaving one behind makes every later `git worktree add` fail with a
// message about the path already existing, which is a confusing way to
// learn that a gate crashed an hour ago.
func schemasAtTag(tag string) (out []byte, err error) {
	tmp, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	src := filepath.Join(tmp, "src")
	if err := runCmd("git", "worktree", "add", "-q", src, tag); err != nil {
		return nil, fmt.Errorf("worktree for %s: %w", tag, err)
	}
	defer func() {
		if rmErr := runCmd("git", "worktree", "remove", "-f", src); rmErr != nil && err == nil {
			err = fmt.Errorf("remove worktree: %w", rmErr)
		}
	}()

	oldBin := filepath.Join(tmp, "old")
	build := exec.Command("go", "build", "-o", oldBin, "./cmd/google-docs-mcp")
	build.Dir = src
	if b, buildErr := build.CombinedOutput(); buildErr != nil {
		return nil, fmt.Errorf("build %s: %w: %s", tag, buildErr, b)
	}
	return dumpSchemas(oldBin)
}

// dumpSchemas asks a build for its schemas.
//
// The binary decides what that means: --dump-schemas emits
// tools.FullSurface, so both sides of the comparison carry every tool
// that can register, including the ones behind a flag. That belongs in
// the binary rather than here — a gate setting an environment variable
// only fixes the gate, while every other reader of --dump-schemas keeps
// the partial answer, and it has to be kept in step with a gate list it
// cannot see.
func dumpSchemas(bin string) ([]byte, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	return out, nil
}

func lastTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "" // no tags yet, or not a checkout
	}
	return trimLine(string(out))
}

func runCmd(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}
