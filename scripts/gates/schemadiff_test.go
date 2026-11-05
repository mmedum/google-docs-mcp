package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

const releasedDump = `{"tools": [{
	"name": "add_comment",
	"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
	"outputSchema": {"properties": {"id": {}, "anchored": {}}}
}]}`

func TestSchemaDiffBreaksOnALostOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}}}
	}]}`
	var out strings.Builder
	c := compareSurfaces(mustParse(t, releasedDump), mustParse(t, built))
	c.write(&out)
	if len(c.breaking) == 0 {
		t.Fatal("dropping an output field passed the diff")
	}
	if want := "add_comment: output field removed anchored"; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not name the lost field; want %q in:\n%s", want, out.String())
	}
}

func TestSchemaDiffAllowsANewOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}, "anchored": {}, "handle": {}}}
	}]}`
	if len(compareSurfaces(mustParse(t, releasedDump), mustParse(t, built)).breaking) > 0 {
		t.Error("adding an output field failed the diff")
	}
}

func mustParse(t *testing.T, s string) *schemaDump {
	t.Helper()
	d, err := parseSchemas([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A break passes only when the unreleased CHANGELOG declares it under
// **Breaking:**, naming its tool, and no release there keeps the tag's
// major version.
func TestUndeclaredBreaks(t *testing.T) {
	breaking := []change{{item: "delete_all", text: "tool removed: delete_all"},
		{item: "review_suggestion", text: "review_suggestion: field removed all", field: "input all"},
		{item: "review_suggestion", text: "review_suggestion: new required field ids", field: "input ids"}}
	released := "\n## [2.4.0] - 2026-10-22\n\n- **Breaking:** `delete_all` and `review_suggestion`.\n"
	for _, tc := range []struct {
		name, changelog string
		want            []string
	}{
		{"every one declared", "## [Unreleased]\n\n- **Breaking:** `delete_all` is gone; `review_suggestion` takes `ids`.\n" + released, nil},
		{"one tool not named", "## [Unreleased]\n\n- **Breaking:** `review_suggestion` takes `ids`.\n" + released,
			[]string{"tool removed: delete_all"}},
		{"named outside a Breaking entry", "## [Unreleased]\n\n- `delete_all` and `review_suggestion` changed.\n" + released, texts(breaking)},
		{"declared only in a release made", "## [Unreleased]\n" + released, texts(breaking)},
		{"a release that raises the major", "## [3.0.0] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released, nil},
		{"a release that does not", "## [2.5.0] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released,
			[]string{"release 2.5.0 breaks the surface without raising the major version from 2.4.0"}},
		{"a prerelease that does not", "## [2.5.0-rc.1] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released,
			[]string{"release 2.5.0-rc.1 breaks the surface without raising the major version from 2.4.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := undeclaredBreaks(breaking, "# Changelog\n\n"+tc.changelog, "v2.4.0")
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("undeclared = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	// Without the tag's own heading nothing is known to be unreleased.
	if _, err := undeclaredBreaks(breaking, "# Changelog\n\n## [Unreleased]\n\n- **Breaking:** `delete_all`.\n", "v2.4.0"); err == nil ||
		!strings.Contains(err.Error(), "no ## [2.4.0] heading") {
		t.Errorf("no heading for the tag: err = %v", err)
	}
}

// An item removed from any list of the dump, a resource template or a
// prompt, is declared as a tool is: named in backticks in a
// **Breaking:** entry.
func TestUndeclaredBreaksNamesAResourceTemplate(t *testing.T) {
	breaking := []change{{item: "gdocs://{id}/outline", text: "resource template removed: gdocs://{id}/outline"},
		{item: "summarize", text: "prompts removed: summarize"}}
	released := "\n## [2.4.0] - 2026-10-22\n"
	for _, tc := range []struct {
		name, unreleased string
		want             []string
	}{
		{"both declared", "- **Breaking:** `gdocs://{id}/outline` and `summarize` are gone.\n", nil},
		{"one declared", "- **Breaking:** `gdocs://{id}/outline` is gone.\n", []string{"prompts removed: summarize"}},
		{"neither declared", "- **Breaking:** `delete_all` is gone.\n", texts(breaking)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := undeclaredBreaks(breaking, "# Changelog\n\n## [Unreleased]\n\n"+tc.unreleased+released, "v2.4.0")
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("undeclared = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// plain is a released surface; the tests below change one thing in it.
const plain = `{"server": "google-docs-mcp", "version": "v1.0.0",
	"tools": [{"name": "add_comment", "annotations": {"destructiveHint": false},
		"inputSchema": {"properties": {"document": {}}}}],
	"resourceTemplates": [{"uriTemplate": "gdocs://{id}", "name": "document"}]}`

var flipped = strings.Replace(plain, `"destructiveHint": false`, `"destructiveHint": true`, 1)

// schema-diff names an annotation that changed, down to the hint.
func TestSchemaDiffListsAChangedAnnotation(t *testing.T) {
	var out strings.Builder
	compareSurfaces(mustParse(t, plain), mustParse(t, flipped)).write(&out)
	if want := "changed: [tool add_comment (annotations.destructiveHint)]"; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not name the changed hint; want %q in:\n%s", want, out.String())
	}
}

// The same tool written in another key order is no change, and neither
// is a new build stamp.
func TestSchemaDiffIgnoresKeyOrderAndTheStamp(t *testing.T) {
	built := `{"server": "google-docs-mcp", "version": "v1.0.1-dev",
		"resourceTemplates": [{"name": "document", "uriTemplate": "gdocs://{id}"}],
		"tools": [{"inputSchema": {"properties": {"document": {}}}, "annotations": {"destructiveHint": false},
			"name": "add_comment"}]}`
	if c := compareSurfaces(mustParse(t, plain), mustParse(t, built)); !c.empty() {
		t.Errorf("reordered keys or a new version counted as a change: %+v", c)
	}
}

// A tool that breaks is reported as the break, and still lists every
// part that moved, a flipped hint and its schemas included: the break
// names only the fields it broke, and anything else that moved beside it
// is a change a reviewer has to see.
func TestSchemaDiffListsABrokenToolsOtherChanges(t *testing.T) {
	broken := strings.Replace(flipped, `"document": {}`, `"doc": {}`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, broken))
	if want := []string{"add_comment: field removed document"}; !slices.Equal(texts(c.breaking), want) {
		t.Errorf("breaking = %v, want %v", texts(c.breaking), want)
	}
	if want := []string{"tool add_comment (annotations.destructiveHint, inputSchema.properties)"}; !slices.Equal(texts(c.changed), want) {
		t.Errorf("changed = %v, want %v", texts(c.changed), want)
	}
}

// A key the dump gains is compared like the rest: an item in a new list
// is added.
func TestSchemaDiffComparesANewKey(t *testing.T) {
	built := strings.Replace(plain, `"resourceTemplates"`, `"prompts": [{"name": "summarize"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, built))
	if want := []string{"prompts summarize"}; !slices.Equal(texts(c.added), want) {
		t.Errorf("added = %v, want %v", texts(c.added), want)
	}
}

// A list whose items carry no identity is compared as one value: a
// position is no identity, and an item inserted would shift the rest.
func TestSchemaDiffComparesAnUnkeyedListWhole(t *testing.T) {
	released := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "b"}], "resourceTemplates"`, 1)
	built := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "a"}, {"text": "b"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, released), mustParse(t, built))
	if len(c.breaking) != 0 || !slices.Equal(texts(c.changed), []string{"notes"}) {
		t.Errorf("breaking = %v, changed = %v; want no break and the list named as changed", c.breaking, c.changed)
	}
}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name": "document"`, `"name": "doc"`, 1)
	broken := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate": "gdocs://{id}", "name": "document"}`, ``, 1)
	reworded := strings.Replace(plain, `"version": "v1.0.0",`, `"version": "v1.0.0", "instructions": "new",`, 1)
	const (
		unnamed      = "no footer names add_comment"
		unnamedBreak = "no BREAKING CHANGE: footer names add_comment"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark add_comment destructive", unnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document: destructiveHint true", unnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comments: destructiveHint true", unnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document,\n  add_comment: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", unnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: add_comment", unnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: add_comment", unnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: add_comment footer, which this is not.", unnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: add_comment", unnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\n    go run ./scripts/gates schema-ack", unnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: add_comment: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gdocs://{id}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: add_comment: renamed", "no footer names gdocs://{id}"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"the instructions unnamed", reworded, "Reword them", "no footer names instructions"},
		{"a description word names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document: add_comment is destructive", unnamed},
		{"a tab after the colon starts the description too", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document:\tdestructive, add_comment", unnamed},
		{"a value with no description names its whole list", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document, add_comment", ""},
		{"a value wrapped at the margin continues it", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document,\nadd_comment: destructiveHint true", ""},
		{"prose after a bare list names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document\nadd_comment is destructive now", unnamed},
		{"a bare list ends at its line, and its name counts", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment\nhints flipped", ""},
		{"prose after a trailing comma names nothing", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document,\nwhich add_comment also reads", unnamed},
		{"a list is split on commas, not spaces", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document add_comment: hints", unnamed},
		{"a Token #value footer is a footer", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\nCloses #12", ""},
		{"a Token #value footer acknowledges nothing", flipped, "Mark it\n\nSCHEMA-CHANGE #add_comment", unnamed},
		{"nor does a BREAKING CHANGE #value one", broken, "Rename it\n\nBREAKING CHANGE #add_comment", unnamedBreak},
		{"a footer ends the value before it", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document,\nRefs #12, add_comment", unnamed},
		{"three blank lines end one paragraph", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\n\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a token is read in any case", flipped, "Mark it\n\nschema-change: add_comment: hints", ""},
		{"BREAKING-CHANGE counts only in capitals", broken, "Rename the field\n\nbreaking-change: add_comment: document is doc", unnamedBreak},
		{"a break named only as a change", broken, "Rename the field\n\nSCHEMA-CHANGE: add_comment: document is doc", unnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", broken, "Rename the field\n\nBREAKING CHANGE: add_comment: document is doc", ""},
		{"BREAKING-CHANGE: is its synonym", broken, "Rename the field\n\nBREAKING-CHANGE: add_comment: document is doc", ""},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gdocs://{id}: gone", "no BREAKING CHANGE: footer names gdocs://{id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(mustParse(t, plain), mustParse(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// A break no footer names is asked for once, as a break: the BREAKING
// CHANGE: footer that fixes it names it for both checks.
func TestARefusalAsksForAnUnnamedBreakOnce(t *testing.T) {
	broken := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	err := checkAck(io.Discard, compareSurfaces(mustParse(t, plain), mustParse(t, broken)), nil, "base")
	if want := "the tool surface changed since base, and no BREAKING CHANGE: footer names add_comment. "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkAck = %v, want it to start %q", err, want)
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := mustParse(t, plain)
	base := mustParse(t, strings.Replace(plain, `"tools": [`, `"tools": [{"name": "draft_tool"}, `, 1))
	head := mustParse(t, strings.Replace(plain, `"document": {}`, `"doc": {}`, 1))
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if want := []change{{"add_comment", "add_comment: field removed document", "input document"}}; !slices.Equal(c.breaking, want) {
		t.Errorf("breaking = %v, want %v", c.breaking, want)
	}
	if want := []change{{item: "add_comment", text: "tool add_comment (inputSchema.properties)"},
		{item: "draft_tool", text: "tool removed: draft_tool", field: "tool"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: add_comment: document is doc"
	if err := checkAck(io.Discard, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
}

// A refusal ends with the footer that fixes it, bare so it can be copied:
// BREAKING CHANGE: for the breaks, SCHEMA-CHANGE: for the rest, an item
// the first names left out of the second. The footer it prints passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	built := strings.Replace(strings.Replace(flipped, `"document": {}`, `"doc": {}`, 1), `"name": "document"`, `"name": "doc"`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, built))
	_, fix, _ := strings.Cut(fmt.Sprint(checkAck(io.Discard, c, []string{"Change them"}, "base")), "\n\n")
	if want := "BREAKING CHANGE: add_comment: what broke\nSCHEMA-CHANGE: gdocs://{id}: what changed"; fix != want {
		t.Fatalf("the refusal's fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, []string{"Change them", "Acknowledge them\n\n" + fix}, "base"); err != nil {
		t.Errorf("the printed fix does not pass: %v", err)
	}
}

// A name the footer would split, or trim, is printed in backticks, and
// the footer printed passes: a URI with a comma, and one ending in a full
// stop, which a bare name would lose. Bare, the URI with a comma names
// nothing.
func TestARefusalQuotesANameTheFooterWouldSplit(t *testing.T) {
	for _, tmpl := range []string{"gdocs://{id}{?tab,mode}", "gdocs://{id}."} {
		was := strings.Replace(plain, `"gdocs://{id}"`, `"`+tmpl+`"`, 1)
		c := compareSurfaces(mustParse(t, was), mustParse(t, strings.Replace(was, `"name": "document"`, `"name": "doc"`, 1)))
		_, fix, _ := strings.Cut(fmt.Sprint(checkAck(io.Discard, c, nil, "base")), "\n\n")
		if want := "SCHEMA-CHANGE: `" + tmpl + "`: what changed"; fix != want {
			t.Errorf("the refusal's fix is %q, want %q", fix, want)
			continue
		}
		if err := checkAck(io.Discard, c, []string{"Rename it\n\n" + fix}, "base"); err != nil {
			t.Errorf("the printed fix for %s does not pass: %v", tmpl, err)
		}
		if bare := strings.ReplaceAll(fix, "`", ""); strings.Contains(tmpl, ",") && checkAck(io.Discard, c, []string{"Rename it\n\n" + bare}, "base") == nil {
			t.Errorf("%q passed, though split on its comma it names nothing", bare)
		}
	}
}

// A gate that could not run says so and exits 2; a refusal exits 1.
// Under `go run`, which exits 1 for both, the message tells them apart.
func TestSchemaAckTellsARefusalFromAFailure(t *testing.T) {
	refused := checkAck(io.Discard, compareSurfaces(mustParse(t, plain), mustParse(t, flipped)), nil, "base")
	if refused == nil || exitCode(refused) != 1 || strings.HasPrefix(refused.Error(), "could not run") {
		t.Errorf("a refusal (%v) exits %d, want 1", refused, exitCode(refused))
	}
	failed := schemaAck(io.Discard, []string{"refs/heads/no-such-branch-for-a-test", "HEAD"})
	if failed == nil || !strings.HasPrefix(failed.Error(), "could not run: ") || exitCode(failed) != 2 {
		t.Errorf("a gate that could not run (%v) exits %d, want 2 and a message saying so", failed, exitCode(failed))
	}
}

// A tool the base branch already broke since the release, in one field,
// does not make a pull request's break of another field, one no release
// shipped, a break of the release; nor does an output field of the name
// the pull request removes as an input, nor a released tool of the name
// of a prompt it removes.
func TestABreakOfTheReleaseIsMatchedByItsField(t *testing.T) {
	tool := func(inputs, outputs string) *schemaDump {
		return mustParse(t, `{"tools": [{"name": "add_comment", "inputSchema": {"properties": {`+inputs+
			`}}, "outputSchema": {"properties": {`+outputs+`}}}]}`)
	}
	for _, tc := range []struct {
		name                string
		release, base, head *schemaDump
	}{
		{"another input", tool(`"document": {}, "note": {}`, ``), tool(`"document": {}, "draft": {}`, ``), tool(`"document": {}`, ``)},
		{"an output of the same name", tool(`"document": {}`, `"note": {}`), tool(`"document": {}, "note": {}`, ``), tool(`"document": {}`, ``)},
		{"a prompt of a released tool's name",
			mustParse(t, `{"tools": [{"name": "add_comment"}, {"name": "summarize"}]}`),
			mustParse(t, `{"tools": [{"name": "add_comment"}], "prompts": [{"name": "summarize"}]}`),
			mustParse(t, `{"tools": [{"name": "add_comment"}]}`)},
	} {
		c := releaseBreaksOnly(compareSurfaces(tc.base, tc.head), compareSurfaces(tc.release, tc.head).breaking)
		if len(c.breaking) != 0 {
			t.Errorf("%s: breaking = %v, want the unreleased field's removal as a change", tc.name, c.breaking)
		}
	}
}

// The release is the nearest tag the revision reaches. A branch that
// reaches none has no release, although the repository has tags.
func TestLastTagIsOneTheRevisionReaches(t *testing.T) {
	newDumpRepo(t)
	mustGit(t, "commit", "--quiet", "--allow-empty", "-m", "Start")
	mustGit(t, "branch", "before-any-release")
	mustGit(t, "commit", "--quiet", "--allow-empty", "-m", "Release")
	mustGit(t, "tag", "v1.0.0")
	for rev, want := range map[string]string{"main": "v1.0.0", "before-any-release": ""} {
		if got, err := lastTag(rev); err != nil || got != want {
			t.Errorf("lastTag(%s) = %q, %v; want %q", rev, got, err, want)
		}
	}
}

// Both sides are built from commits: the merge-base and the head. The
// checkout is neither, in CI the merge commit and here main with a change
// nobody committed, so what it would serve is not what is judged.
func TestSchemaAckBuildsBothSidesFromCommits(t *testing.T) {
	r := newDumpRepo(t)
	r.commit(plain, "Base")
	mustGit(t, "switch", "--quiet", "-c", "feature")
	r.commit(flipped, "Flip add_comment")
	mustGit(t, "switch", "--quiet", "main")
	r.write(strings.Replace(plain, `"document": {}`, `"doc": {}`, 1))

	var out strings.Builder
	err := schemaAck(&out, []string{"main", "feature"})
	if err == nil || !strings.HasSuffix(err.Error(), "\n\nSCHEMA-CHANGE: add_comment: what changed") {
		t.Fatalf("schemaAck = %v, want a refusal naming the flip alone", err)
	}
	if want := "changed: [tool add_comment (annotations.destructiveHint)]"; !strings.Contains(out.String(), want) || strings.Contains(out.String(), "removed") {
		t.Errorf("the report is\n%s\nwant %q and no break", out.String(), want)
	}
	mustGit(t, "checkout", "--", ".")
	mustGit(t, "switch", "--quiet", "feature")
	mustGit(t, "commit", "--quiet", "--allow-empty", "-m", "Acknowledge it\n\nSCHEMA-CHANGE: add_comment: destructive")
	if err := schemaAck(io.Discard, []string{"main", "HEAD"}); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	if n := r.worktrees(); n != 1 {
		t.Errorf("%d worktrees left, want the checkout alone", n)
	}
}

// Through the whole gate: a tool no release shipped, removed, is a plain
// change, and an input the last tag shipped, renamed, is a break.
func TestSchemaAckAsksForBreakingChangeOnlyForAReleasedBreak(t *testing.T) {
	r := newDumpRepo(t)
	r.commit(plain, "Release")
	mustGit(t, "tag", "v1.0.0")
	r.commit(strings.Replace(plain, `"tools": [`, `"tools": [{"name": "draft_tool"}, `, 1), "Add draft_tool")
	mustGit(t, "switch", "--quiet", "-c", "feature")
	r.commit(strings.Replace(plain, `"document": {}`, `"doc": {}`, 1), "Drop draft_tool and rename document")
	c, messages, base, err := ackInputs("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	_, fix, _ := strings.Cut(fmt.Sprint(checkAck(io.Discard, c, messages, base)), "\n\n")
	if want := "BREAKING CHANGE: add_comment: what broke\nSCHEMA-CHANGE: draft_tool: what changed"; fix != want {
		t.Fatalf("the fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, append(messages, "Acknowledge them\n\n"+fix), base); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
}

// The release is the newest tag the base branch reaches, not the head's:
// a branch cut before the last release, renaming what that release
// already renamed, breaks no caller of it.
func TestTheReleaseIsTheBaseBranchsNewestTag(t *testing.T) {
	r := newDumpRepo(t)
	renamed := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	r.commit(plain, "Release 1")
	mustGit(t, "tag", "v1.0.0")
	mustGit(t, "branch", "feature")
	r.commit(renamed, "Release 2")
	mustGit(t, "tag", "v2.0.0")
	mustGit(t, "switch", "--quiet", "feature")
	r.commit(renamed, "Rename document")
	err := schemaAck(io.Discard, []string{"main", "HEAD"})
	_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
	if want := "SCHEMA-CHANGE: add_comment: what changed"; fix != want {
		t.Errorf("schemaAck = %v, want the fix %q, since v2.0.0 shipped the rename", err, want)
	}
}

// dumpRepo is a git repository, the working directory for the test,
// whose cmd/google-docs-mcp prints a fixed dump, so the gate's worktree
// builds are exercised without building the real server.
type dumpRepo struct {
	t    *testing.T
	root string
}

func newDumpRepo(t *testing.T) *dumpRepo {
	t.Helper()
	r := &dumpRepo{t: t, root: initRepo(t)}
	put(t, r.root, "go.mod", "module example.invalid/fake\n\ngo 1.21\n")
	mustGit(t, "add", "go.mod")
	return r
}

// commit commits a main that prints dump.
func (r *dumpRepo) commit(dump, message string) {
	r.t.Helper()
	r.write(dump)
	mustGit(r.t, "add", "-A")
	mustGit(r.t, "commit", "--quiet", "-m", message)
}

// write puts a main that prints dump in the working tree, uncommitted.
func (r *dumpRepo) write(dump string) {
	r.t.Helper()
	put(r.t, r.root, "cmd/google-docs-mcp/main.go",
		fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { os.Stdout.WriteString(%q) }\n", dump))
}

func (r *dumpRepo) worktrees() int {
	r.t.Helper()
	out, err := gitOutput("worktree", "list", "--porcelain")
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Count(out, "worktree ")
}
