package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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

	last, err := lastTag("HEAD")
	if err != nil {
		return err
	}
	if last == "" {
		_, err := fmt.Fprintln(w, "no previous tag; schemas.json written")
		return err
	}

	oldDump, err := dumpAt(last)
	if err != nil {
		return err
	}
	newDump, err := parseSchemas(current)
	if err != nil {
		return err
	}
	c := compareSurfaces(oldDump, newDump)
	c.write(w)
	if len(c.breaking) == 0 {
		return nil
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	undeclared, err := undeclaredBreaks(c.breaking, string(changelog), last)
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
	_, _ = fmt.Fprintf(w, "%d breaking change(s) since %s, each declared under **Breaking:** for a major release\n", len(c.breaking), last)
	return nil
}

// codeSpan is a name in backticks, which is how a **Breaking:** entry
// names a tool or a resource template.
var codeSpan = regexp.MustCompile("`([^`]+)`")

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
func undeclaredBreaks(breaking []change, changelog, tag string) ([]string, error) {
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
		if !named[b.item] {
			out = append(out, b.text)
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
	if _, err := gitOutput("worktree", "add", "--quiet", "--detach", src, rev); err != nil {
		// A checkout that failed can leave its registration behind; prune
		// drops it once the directory is gone.
		_ = os.RemoveAll(tmp)
		_, _ = gitOutput("worktree", "prune")
		return nil, err
	}
	defer func() {
		if _, rmErr := gitOutput("worktree", "remove", "--force", src); rmErr != nil && err == nil {
			err = fmt.Errorf("remove worktree: %w", rmErr)
		}
	}()

	oldBin := filepath.Join(tmp, "old")
	if runtime.GOOS == "windows" {
		oldBin += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", oldBin, "./cmd/google-docs-mcp")
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
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

// lastTag is the newest release on rev's history: the nearest tag it
// reaches. It is empty when rev reaches no tag, before the first release
// or on a fork carrying the upstream's tags, which is a state, not a
// failure; any git error is one.
func lastTag(rev string) (string, error) {
	tags, err := gitOutput("tag", "--merged", rev)
	if err != nil || tags == "" {
		return "", err
	}
	return gitOutput("describe", "--tags", "--abbrev=0", rev)
}

// dumpAt builds the server at a revision and reads its dump.
func dumpAt(rev string) (*schemaDump, error) {
	raw, err := schemasAt(rev)
	if err != nil {
		return nil, err
	}
	d, err := parseSchemas(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rev, err)
	}
	return d, nil
}

// footerStart is a line that starts a footer: a token, then ": " or
// " #", then its value, as Co-Authored-By: and Closes #12 are
// (Conventional Commits 1.0.0, items 8 and 9).
var footerStart = regexp.MustCompile(`^(BREAKING CHANGE|[A-Za-z][A-Za-z0-9-]*)(:[ \t]+| #)(\S.*)$`)

// paragraphBreak is one or more blank lines, which end a paragraph.
var paragraphBreak = regexp.MustCompile(`\n([ \t]*\n)+`)

// A footer is one token and its value, the lines that continue it joined
// on, each on its own line. ref marks a "Token #value" footer, which
// refers to something, such as an issue, rather than describing.
type footer struct {
	token, text string
	ref         bool
}

// footers is a commit message's footer block: the paragraphs at its end
// whose first line starts a footer, never the subject. Inside one, a line
// that starts a footer ends the value before it, and any other line
// continues that value, wrapped at the margin or indented (item 10). A
// paragraph whose first line is prose ends the block, so a line of prose
// that happens to start "BREAKING CHANGE:" is not a footer.
func footers(message string) []footer {
	paras := paragraphBreak.Split(strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n")), -1)
	var out []footer
	for i := len(paras) - 1; i > 0; i-- {
		var block []footer
		for _, l := range strings.Split(strings.Trim(paras[i], "\n"), "\n") {
			switch m := footerStart.FindStringSubmatch(l); {
			case m != nil:
				block = append(block, footer{m[1], m[3], m[2] == " #"})
			case len(block) > 0:
				block[len(block)-1].text += "\n" + strings.TrimSpace(l)
			default:
				return out
			}
		}
		out = append(block, out...)
	}
	return out
}

// ackToken says whether a footer's token acknowledges a change to the
// tool surface, and whether a break of a release. Conventional Commits
// reads a token in any case but BREAKING CHANGE, which must be
// uppercase, and BREAKING-CHANGE is its synonym (items 15 and 16).
func ackToken(token string) (isBreak, ok bool) {
	switch {
	case token == "BREAKING CHANGE" || token == "BREAKING-CHANGE":
		return true, true
	case strings.EqualFold(token, "SCHEMA-CHANGE"):
		return false, true
	}
	return false, false
}

// nameSeparator is what separates the names in a footer's list, and
// descriptionStart is the colon and white space that end the list.
var (
	nameSeparator    = regexp.MustCompile("[\\s,;()'\"]+")
	descriptionStart = regexp.MustCompile(`:\s`)
)

// footerNames is every name a footer's value lists: the part before its
// first colon and white space, ending sooner at a line that does not end
// with a comma, each an item between commas. What follows is the description, free
// text that names nothing, so a tool whose name is an ordinary word is
// not acknowledged by a sentence that uses it. A colon and a full stop
// are trimmed from a name rather than split on, since a URI holds both;
// a name in backticks is taken whole, trimmed of nothing, so a URI with
// a comma in it can be named.
func footerNames(text string) []string {
	list := text
	if at := descriptionStart.FindStringIndex(text); at != nil {
		list = text[:at[0]]
	}
	// A list wraps only after a comma: a line that does not end with one
	// ends it, and what follows describes, so prose on the next line of a
	// bare list names nothing.
	lines := strings.Split(list, "\n")
	for i, l := range lines {
		if !strings.HasSuffix(strings.TrimSpace(l), ",") {
			list = strings.Join(lines[:i+1], "\n")
			break
		}
	}
	var out []string
	for _, m := range codeSpan.FindAllStringSubmatch(list, -1) {
		out = append(out, m[1])
	}
	// The rest is split on commas alone, so prose is one item that names
	// nothing rather than words that each might.
	for _, n := range strings.Split(codeSpan.ReplaceAllString(list, ""), ",") {
		n = strings.TrimRight(strings.Trim(n, " \t\n;()'\""), ":.")
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// acknowledged reads every message's footers for the items they name:
// named holds each one a SCHEMA-CHANGE: or BREAKING CHANGE: footer
// names, breaks each one a BREAKING CHANGE: footer names. A name is a
// whole token, so add_comment does not name add_comments.
func acknowledged(messages []string) (named, breaks map[string]bool) {
	named, breaks = map[string]bool{}, map[string]bool{}
	for _, message := range messages {
		for _, f := range footers(message) {
			// A "Token #value" footer acknowledges nothing: Conventional
			// Commits writes BREAKING CHANGE with a colon and a space (item
			// 12), and its value has no description to tell from names.
			if f.ref {
				continue
			}
			isBreak, ok := ackToken(f.token)
			if !ok {
				continue
			}
			for _, n := range footerNames(f.text) {
				named[n] = true
				if isBreak {
					breaks[n] = true
				}
			}
		}
	}
	return named, breaks
}

// schemaAck fails a pull request whose tool surface differs from its
// merge-base's, unless the commits it adds name every item that changed
// in a footer. Annotations count: a flipped destructiveHint changes
// whether a client asks the person.
//
// Both sides are built from commits, the merge-base and the head. In CI
// the checkout is the merge commit, which carries whatever the base
// branch changed since the branch point, and that change is not this
// pull request's to acknowledge. A gate that could not run says so
// (couldNotRun).
func schemaAck(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates schema-ack BASE HEAD")
	}
	c, messages, base, err := ackInputs(args[0], args[1])
	if err != nil {
		return couldNotRun{err}
	}
	return checkAck(w, c, messages, base)
}

// ackInputs is what checkAck judges: the changes from the merge-base of
// baseRef and head to head, each break kept as one only if it also
// breaks the newest release the base branch reaches; the messages of the
// commits in between; and the merge-base.
func ackInputs(baseRef, head string) (c surfaceChanges, messages []string, base string, err error) {
	if base, err = gitOutput("merge-base", baseRef, head); err != nil {
		return surfaceChanges{}, nil, "", err
	}
	var dumps [2]*schemaDump
	for i, rev := range []string{base, head} {
		if dumps[i], err = dumpAt(rev); err != nil {
			return surfaceChanges{}, nil, "", err
		}
	}
	c = compareSurfaces(dumps[0], dumps[1])
	if len(c.breaking) > 0 {
		// The newest release the pull request lands on is the base
		// branch's, not the head's: a branch cut before the last tag
		// would otherwise be measured against an older release.
		tag, err := lastTag(baseRef)
		if err != nil {
			return surfaceChanges{}, nil, "", err
		}
		var released []change
		if tag != "" {
			rel, err := dumpAt(tag)
			if err != nil {
				return surfaceChanges{}, nil, "", err
			}
			released = compareSurfaces(rel, dumps[1]).breaking
		}
		c = releaseBreaksOnly(c, released)
	}
	log, err := gitOutput("log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return surfaceChanges{}, nil, "", err
	}
	return c, strings.Split(log, "\x00"), base, nil
}

// releaseBreaksOnly keeps as breaks only those the head also makes
// against the released surface. Any other break narrows something no
// release shipped, so it breaks no caller and is a plain change. With
// no release, nothing is a break. A break is matched by its item and
// field, never its text.
func releaseBreaksOnly(c surfaceChanges, released []change) surfaceChanges {
	type spot struct{ item, field string }
	inRelease := map[spot]bool{}
	for _, b := range released {
		inRelease[spot{b.item, b.field}] = true
	}
	var breaking []change
	for _, b := range c.breaking {
		if inRelease[spot{b.item, b.field}] {
			breaking = append(breaking, b)
		} else {
			c.changed = append(c.changed, b)
		}
	}
	c.breaking = breaking
	return c
}

// checkAck is the rule, over the changes and the commit messages, so a
// test can hand it both. Every added or changed item must be named in an
// acknowledging footer, and every break in a BREAKING CHANGE: one. A
// refusal names the footer that fixes it.
func checkAck(w io.Writer, c surfaceChanges, messages []string, base string) error {
	base = shortSHA(base)
	if c.empty() {
		_, err := fmt.Fprintf(w, "schema ack ok: the tool surface is unchanged since %s\n", base)
		return err
	}
	c.write(w)
	named, breaks := acknowledged(messages)
	var problems, fix []string
	breakItems := unnamed(c.breaking, breaks)
	if len(breakItems) > 0 {
		problems = append(problems, "no BREAKING CHANGE: footer names "+strings.Join(breakItems, ", "))
		fix = append(fix, "BREAKING CHANGE: "+footerList(breakItems)+": what broke")
	}
	// A BREAKING CHANGE: footer names an item for both checks, so an
	// unnamed break is not asked for twice.
	items := unnamed(slices.Concat(c.added, c.changed), named)
	if rest := slices.DeleteFunc(items, func(i string) bool { return slices.Contains(breakItems, i) }); len(rest) > 0 {
		problems = append(problems, "no footer names "+strings.Join(rest, ", "))
		fix = append(fix, "SCHEMA-CHANGE: "+footerList(rest)+": what changed")
	}
	if len(problems) > 0 {
		return fmt.Errorf("the tool surface changed since %s, and %s. Add an empty commit (git commit --allow-empty), "+
			"never an amend, whose message ends with:\n\n%s", base, strings.Join(problems, ", and "), strings.Join(fix, "\n"))
	}
	_, err := fmt.Fprintf(w, "schema ack ok: the change since %s is acknowledged\n", base)
	return err
}

// footerList is names as a footer has to write them: a name the footer
// would split, or trim, in backticks, so the footer printed is one that
// passes.
func footerList(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n
		if nameSeparator.MatchString(n) || strings.TrimRight(n, ":.") != n {
			out[i] = "`" + n + "`"
		}
	}
	return strings.Join(out, ", ")
}

// unnamed is each item of cs that names does not hold, sorted, once.
func unnamed(cs []change, names map[string]bool) []string {
	var out []string
	for _, c := range cs {
		if !names[c.item] {
			out = append(out, c.item)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
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
