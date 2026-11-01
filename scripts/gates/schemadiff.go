package main

import (
	"errors"
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

	old, err := schemasAt(last)
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

// codeSpan is a name in backticks, which is how a **Breaking:** entry
// names a tool or a resource template.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// breakSubject is what a breaking change is about, which a **Breaking:**
// entry has to name: the item removed from any list of the dump, or the
// tool whose argument or output field broke.
func breakSubject(b string) string {
	if _, item, ok := strings.Cut(b, " removed: "); ok {
		return item
	}
	name, _, _ := strings.Cut(b, ": ")
	return name
}

// A version heading as Keep a Changelog writes it, a prerelease included,
// unlike staleness's versionHeading: a prerelease that keeps the major
// may not break the surface either.
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
		for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
			named[m[1]] = true
		}
	}
	var out []string
	for _, b := range breaking {
		if !named[breakSubject(b)] {
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

// schemasAt builds the binary as it was at a revision and asks it for
// its schemas. The
// worktree is removed whether or not the build succeeds: leaving one
// behind makes every later `git worktree add` fail with a message about
// the path already existing, which is a confusing way to learn that a
// gate crashed an hour ago.
func schemasAt(rev string) (out []byte, err error) {
	tmp, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	src := filepath.Join(tmp, "src")
	if err := runCmd("git", "worktree", "add", "-q", "--detach", src, rev); err != nil {
		return nil, fmt.Errorf("worktree for %s: %w", rev, err)
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
		return nil, fmt.Errorf("build %s: %w: %s", rev, buildErr, b)
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
	tag, err := gitOutput("describe", "--tags", "--abbrev=0")
	if err != nil {
		return "" // no tags yet, or not a checkout
	}
	return tag
}

func runCmd(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

// ackFooter is a footer that acknowledges a change to the tool surface:
// SCHEMA-CHANGE: for any change, BREAKING CHANGE: for one that breaks a
// caller, or BREAKING-CHANGE:, which Conventional Commits makes its
// synonym. The text after the colon says what changed.
var ackFooter = regexp.MustCompile(`^(SCHEMA-CHANGE|BREAKING[ -]CHANGE):`)

// footerToken is a footer line: a token, a colon, a space and a value,
// as Co-Authored-By: is. continuation is a line that starts with white
// space and continues the footer above it.
var (
	footerToken  = regexp.MustCompile(`^(BREAKING CHANGE|[A-Za-z][A-Za-z0-9-]*):[ \t]+\S`)
	continuation = regexp.MustCompile(`^[ \t]+\S`)
)

// paragraphBreak is a blank line, which ends a paragraph.
var paragraphBreak = regexp.MustCompile(`\n[ \t]*\n`)

// footers is a commit message's footer block: the paragraphs at its end
// in which every line is a footer line, never the subject. A line of
// prose that happens to start "BREAKING CHANGE:" is not a footer.
func footers(message string) []string {
	paras := paragraphBreak.Split(strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n")), -1)
	var out []string
	for i := len(paras) - 1; i > 0; i-- {
		lines := strings.Split(strings.Trim(paras[i], "\n"), "\n")
		for j, l := range lines {
			if !footerToken.MatchString(l) && (j == 0 || !continuation.MatchString(l)) {
				return out
			}
		}
		out = append(lines, out...)
	}
	return out
}

// schemaAck fails a pull request whose tool surface differs from its
// merge-base's, unless a commit it adds says so. Annotations count: a
// flipped destructiveHint changes whether a client asks the person.
//
// Both sides are built from commits, the merge-base and the head. In CI
// the checkout is the merge commit, which carries whatever the base
// branch changed since the branch point, and that change is not this
// pull request's to acknowledge.
func schemaAck(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates schema-ack BASE HEAD")
	}
	baseRef, head := args[0], args[1]
	base, err := gitOutput("merge-base", baseRef, head)
	if err != nil {
		return err
	}
	var dumps [2]*schemaDump
	for i, rev := range []string{base, head} {
		raw, err := schemasAt(rev)
		if err != nil {
			return err
		}
		if dumps[i], err = parseSchemas(raw); err != nil {
			return fmt.Errorf("%s: %w", rev, err)
		}
	}
	log, err := gitOutput("log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return err
	}
	return checkAck(w, compareSurfaces(dumps[0], dumps[1]), strings.Split(log, "\x00"), base)
}

// checkAck is the rule, over the changes and the commit messages, so a
// test can hand it both.
func checkAck(w io.Writer, c surfaceChanges, messages []string, base string) error {
	base = shortSHA(base)
	if c.empty() {
		_, err := fmt.Fprintf(w, "schema ack ok: the tool surface is unchanged since %s\n", base)
		return err
	}
	c.write(w)
	var schema, breaking bool
	for _, message := range messages {
		for _, line := range footers(message) {
			if m := ackFooter.FindStringSubmatch(line); m != nil {
				schema = true
				breaking = breaking || m[1] != "SCHEMA-CHANGE"
			}
		}
	}
	switch {
	case len(c.breaking) > 0 && !breaking:
		return fmt.Errorf("the pull request breaks the tool surface and no commit since %s says BREAKING CHANGE:; "+
			"add an empty commit (git commit --allow-empty) whose message carries it, never an amend", base)
	case !schema:
		return fmt.Errorf("the pull request changes the tool surface and no commit since %s says SCHEMA-CHANGE: or "+
			"BREAKING CHANGE:; add an empty commit (git commit --allow-empty) whose message carries it, never an amend", base)
	}
	_, err := fmt.Fprintf(w, "schema ack ok: the change since %s is acknowledged\n", base)
	return err
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// gitOutput runs git and returns its trimmed output, or an error that
// carries git's own message.
func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimLine(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return trimLine(string(out)), nil
}
