package main

import (
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixture checkout's paths, named once because most rows edit them.
const (
	fxArch       = "docs/architecture.md"
	fxRunbook    = "docs/runbook.md"
	fxReadme     = "README.md"
	fxAgent      = "CLAUDE.md"
	fxChangelog  = "CHANGELOG.md"
	fxClient     = "internal/gapi/client.go"
	fxClientTest = "internal/gapi/client_test.go"
	fxServer     = "cmd/server/main.go"
	fxRaw        = "internal/gapi/raw_integration_test.go"
	fxRecord     = "testdata/api-coverage.tsv"
	fxFields     = "testdata/api-fields.tsv"
	fxGolangci   = ".golangci.yml"
	fxMakefile   = "Makefile"
	fxGitignore  = ".gitignore"
	fxGitleaks   = ".gitleaks.toml"
	fxGates      = "scripts/gates/main.go"
	fxDriver     = "internal/livecheck/live_test.go"
	fxDriverUnit = "internal/livecheck/unit_test.go"
)

// textLines is a file of these lines, so a fixture's line numbers can be
// read off its source: the nth argument is line n.
func textLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// The index rows, named because several rows edit them whole.
const (
	fxRow01 = "| D-01 | Retries back off | accepted | TestRetry, smoke |"
	fxRow02 = "| D-02 | Retry forever | superseded by D-03 | — |"
	fxRow03 = "| D-03 | Retry three times | accepted | forbidigo, lint, review |"
)

// The ends of the problems several tests expect, written once.
const (
	notAHolder = "which is not review, a make target, a gates command, an enabled linter or a test or fuzz function"
	bySlug     = "cite a D-ID, docs/<file>.md#<slug> or a source link"
)

// docsFixture is a checkout the docs gate accepts as it stands. Between
// them its files hold one of everything the gate must stay quiet about:
// each kind of Held by item, a superseded decision cited only where that
// is allowed, a repeated heading, links to a file and a directory, a
// codemap naming each package, live steps of each kind and tests of each
// tag cited across comment lines, config comments, the records' reasons,
// and citations in a fence, a string literal, a testdata page and a
// released changelog section.
func docsFixture() map[string]string {
	return map[string]string{
		fxMakefile: textLines(
			"# The checks CI runs (D-03).",
			"check: lint docs",
			"",
			".PHONY: lint",
			"lint:",
			"\tgolangci-lint run",
			"",
			".PHONY: docs",
			"docs:",
			"\t@go run ./scripts/gates docs",
			"",
			".PHONY: live",
			"live:",
			"\tgo test -tags=live ./internal/livecheck",
		),
		fxGolangci: textLines(
			`version: "2"`,
			"linters:",
			"  default: none",
			"  enable:",
			"    - errcheck",
			"    - forbidigo # stdout carries only frames",
			"  settings:",
			"    govet:",
			"      enable:",
			"        - nilness",
			"    misspell:",
			"      locale: US # American English, as D-01 says",
			"formatters:",
			"  enable:",
			"    - gofmt",
		),
		fxGitignore: textLines("internal/generated/", "# Generated, as docs/runbook.md#steps says (D-01)."),
		fxGitleaks: textLines(
			`title = "fixture"`,
			"# Fixtures are generated (D-01).",
			"[allowlist]",
			"paths = ['''internal/gapi/testdata/'''] # synthetic, as D-01 says",
		),
		"go.mod": textLines("module example.com/fixture", "", "go 1.21"),
		fxGates:  textLines("package main", "", "func main() {}"),
		fxClient: textLines(
			"package gapi",
			"",
			"// Retries back off (D-01), as measured live.",
			"// A redirect is followed once, as RFC 9110 §15.4 allows.",
			`func Retry() string { return "D-99 and §4 in a string are data" }`,
			"",
			"// Uploads are capped. Checked by the live",
			`// step "a file uploads", and by the live steps "a repeat delete`,
			`// reports nothing" and "a file's text is read as a resource".`,
			"func Upload() {}",
			"",
			"// Deletes go for good. Checked by the live steps",
			`// "a file is deleted for good", "a file's card is read as a resource" and "a file uploads".`,
			"func Delete() {}",
			"",
			`// Writes compare checkpoints. Checked by the live steps "a checkpoint goes stale"`,
			`// and "a file is copied twice", and by the live step "a file uploads".`,
			"func Write() {}",
			"",
			`// Lists nest. Checked by the live steps "a list is nested" and "a list is read`,
			"// back\", by the integration tests TestRawRetry and",
			"// TestRawUpload, and by TestLive.",
			"func Nest() {}",
		),
		fxRaw: textLines(
			"//go:build integration",
			"",
			"package gapi",
			"",
			`import "testing"`,
			"",
			"func TestRawRetry(t *testing.T) {}",
			"",
			"func TestRawUpload(t *testing.T) {}",
		),
		fxClientTest: textLines(
			"package gapi",
			"",
			`import "testing"`,
			"",
			"func TestRetry(t *testing.T) { _ = Retry() }",
			"",
			"func FuzzRetry(f *testing.F) { f.Fuzz(func(*testing.T, string) { _ = Retry() }) }",
			"",
			"func BenchmarkRetry(b *testing.B) { _ = Retry() }",
			"",
			"func ExampleRetry() { _ = Retry() }",
		),
		// The driver: a call that heads the transcript with its step's name,
		// a resource read that does too, and the helpers and closures that
		// pass a name on. Five calls name a step, two resource reads, one
		// closure and one prefix joined with ": "; and four calls name
		// none: one from a variable, one built at run time, one through a
		// closure that adds words of its own, and a log line.
		fxDriver: textLines(
			"//go:build live",
			"",
			"package livecheck",
			"",
			`import "testing"`,
			"",
			"type driver struct{ t *testing.T }",
			"",
			"func (d *driver) call(label, tool string) string {",
			`	d.t.Logf("=== %s (isError=%t) ===", label, false)`,
			"	return tool",
			"}",
			"",
			"func (d *driver) ok(label, tool string) string { return d.call(label, tool) }",
			"",
			"func (d *driver) readResource(label, uri string) {",
			`	d.t.Logf("=== %s ===\n%s", label, uri)`,
			"}",
			"",
			"func TestLive(t *testing.T) {",
			"	d := &driver{t: t}",
			`	d.ok("a file uploads", "upload_file")`,
			`	d.call("a repeat delete reports nothing", "delete_comment")`,
			`	d.ok("a checkpoint goes stale", "update_content")`,
			`	d.ok("a file is deleted for good", "delete_file")`,
			`	d.ok("a file is copied twice",`,
			`		"copy_file")`,
			`	label := "a search finds the file"`,
			`	d.ok(label, "search_files")`,
			`	d.ok("dry run: "+label, "search_files")`,
			`	d.readResource("a file's text is read as a resource", "x")`,
			`	d.readResource("a file's card is read as a resource", "x")`,
			`	for _, mode := range []string{"direct", "suggest"} {`,
			`		edit := func(name string) { d.ok(mode+": "+name, "edit_document") }`,
			`		edit("a list is nested")`,
			`		d.ok(mode+": a list is read back", "read_document")`,
			`		read := func(text string) { d.ok(mode+": read for "+text, "read_document") }`,
			`		read("a probe, not a step")`,
			"	}",
			`	t.Logf("=== %s ===", "a log line, not a step")`,
			"}",
		),
		fxDriverUnit: textLines(
			"package livecheck",
			"",
			`func fake(d *driver) { d.ok("a test-only step", "x") }`,
		),
		"internal/gapi/testdata/pg.md": textLines("A fixture page citing E-777 and §5, and `internal/gone`."),
		fxServer:                       textLines("package main", "", "func main() {}"),
		fxRecord: textLines(
			"# api\tmethod\tverdict\treason",
			"drive\tfiles.watch\tout\tpush notifications need an endpoint the server does not have (D-01)",
		),
		fxFields: textLines(
			"# schema\tproperty\tverdict\treason",
			"File\tcontentHints\tout\tthe server writes no thumbnails (D-03)",
		),
		fxChangelog: textLines(
			"# Changelog",
			"",
			"## [Unreleased]",
			"",
			"- Retries back off (D-01), as `docs/architecture.md#codemap` says.",
			"",
			"## [1.0.0]",
			"",
			"- Cites D-99 and `internal/gone`, true when written.",
			"",
			"[Unreleased]: https://example.com/compare/v1.0.0...HEAD",
			"[1.0.0]: https://example.com/releases/v1.0.0",
		),
		fxArch: textLines(
			"# Architecture",
			"",
			"## Codemap",
			"",
			"- `internal/gapi` owns the REST client, in `internal/gapi/client.go`.",
			"- `cmd/server` is the binary, and `internal/livecheck` drives it live.",
			"",
			"## Decisions",
			"",
			"| ID | Decision | Status | Held by |",
			"| --- | --- | --- | --- |",
			fxRow01,
			fxRow02,
			fxRow03,
			"",
			"### D-01 Retries back off",
			"",
			"Google answers a burst with 429, so a retry waits.",
			"",
			"```markdown",
			"### D-09 An example entry, not a decision",
			"It has no Rejected or Cost line.",
			"```",
			"",
			"Rejected: retrying at once, which meets the same 429.",
			"Cost: a burst takes longer to drain.",
			"",
			"### D-02 Retry forever",
			"",
			"Rejected: giving up, which drops the write.",
			"Cost: a write can wait forever.",
			"",
			"### D-03 Retry three times",
			"",
			"Three tries replace D-02, since a write that waits forever never reports.",
			"",
			"Rejected: retrying forever.",
			"Cost: a fourth 429 fails the call.",
			"",
			"Next ID: D-04",
		),
		fxRunbook: textLines(
			"# Runbook",
			"",
			"## Steps",
			"",
			"See [the codemap](architecture.md#codemap) and [the readme](../README.md).",
			"The retry is in `internal/gapi/client.go`, linked as [the client](../internal/gapi/client.go).",
			"",
			"## Steps",
			"",
			"Follow the old runbook, in [the server](/cmd/server).",
			"",
			"Back to [the first steps](#steps).",
			"",
			"[map]: architecture.md#codemap",
		),
		fxReadme: textLines(
			"# Fixture",
			"",
			"Read the [decisions](docs/architecture.md#decisions) and [the second steps](docs/runbook.md#steps-1).",
			"Code cites `docs/architecture.md#d-01-retries-back-off`.",
			"A redirect is followed once, as RFC 9110 §15.4 allows.",
			"The [spec](https://example.com/spec#§2) says so too.",
			"",
			"```text",
			"§3, D-99 and [a link](nowhere.md) in a fence are an example.",
			"```",
		),
		fxAgent: textLines(
			"# CLAUDE.md",
			"",
			"Read `docs/architecture.md` and `docs/architecture.md#codemap` first.",
			"The client is in `internal/gapi/`, and `go.mod` names the module.",
			"Run `make check`, or `go run ./scripts/gates docs` alone; `scripts/gates smoke` needs a binary.",
			"A made-up id says so: `files/1SyntheticFixtureFileIdAAAA`. Streams are named in `main`, never in `cmd/*/x.go`.",
		),
	}
}

// docsEdit changes one fixture file: old replaced by new, the whole file
// written when old is empty, or the file removed when both are.
type docsEdit struct{ file, old, new string }

func replace(file, old, new string) docsEdit { return docsEdit{file, old, new} }
func add(file, body string) docsEdit         { return docsEdit{file, "", body} }
func drop(file string) docsEdit              { return docsEdit{file: file} }

// docsEdited is the fixture with the edits applied. An edit whose old
// text is not there fails the test, so a fixture change cannot quietly
// turn a reject row into a copy of the accepted checkout.
func docsEdited(t *testing.T, edits ...docsEdit) map[string]string {
	t.Helper()
	return applyEdits(t, docsFixture(), edits...)
}

// applyEdits is files with the edits applied, as docsEdited applies them.
func applyEdits(t *testing.T, files map[string]string, edits ...docsEdit) map[string]string {
	t.Helper()
	for _, e := range edits {
		switch body := files[e.file]; {
		case e.old == "" && e.new == "":
			delete(files, e.file)
		case e.old == "":
			files[e.file] = e.new
		case !strings.Contains(body, e.old):
			t.Fatalf("the fixture's %s does not contain %q", e.file, e.old)
		default:
			files[e.file] = strings.Replace(body, e.old, e.new, 1)
		}
	}
	return files
}

// codemapNames adds a line naming pkg to the fixture's codemap, for a
// row that adds a package.
func codemapNames(pkg string) docsEdit {
	return replace(fxArch, "drives it live.\n", "drives it live.\n- `"+pkg+"` is a package.\n")
}

// docsTree writes files to a fresh directory and returns it.
func docsTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	putAll(t, root, files)
	return root
}

// docsFixtureFloors keeps the floors out of the rows whose subject is not a
// floor. The fixture is far smaller than the repository, and a pass that
// reads nothing still fails.
var docsFixtureFloors = docsFloors{links: 1, spanPaths: 1, packages: 1, liveSteps: 1, integration: 1}

// docsRun runs every check over files, listed as git lists them, held to
// floors. Listing a checkout is docsFiles' part, and the tests that need
// git test it.
func docsRun(t *testing.T, files map[string]string, floors docsFloors) *docsCheck {
	t.Helper()
	g := newDocsCheck(docsTree(t, files), slices.Sorted(maps.Keys(files)), floors)
	g.run()
	return g
}

// docsCase is one checkout and every problem the gate reports for it. A
// nil want means the gate accepts it.
type docsCase struct {
	name  string
	edits []docsEdit
	want  []string
}

// checkDocsCases runs each row against its own checkout, in parallel,
// and compares the whole problem list, so a row also fails on a problem
// it did not expect.
func checkDocsCases(t *testing.T, cases []docsCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDocs(t, docsFixtureFloors, tc.edits, tc.want)
		})
	}
}

// checkDocs runs the checks, held to floors, over the fixture with edits,
// and wants exactly the problems want, or none when want is nil.
func checkDocs(t *testing.T, floors docsFloors, edits []docsEdit, want []string) {
	t.Helper()
	if got := docsRun(t, docsEdited(t, edits...), floors).problems; !slices.Equal(got, want) {
		t.Errorf("problems\n got %q\nwant %q", got, want)
	}
}

func TestDocsGateAcceptsAConsistentCheckout(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{name: "the fixture as it stands"},
		{"a Go file under testdata is neither parsed nor read for citations", []docsEdit{
			add("internal/gapi/testdata/fixture.go", "package fixture\n\n// Cites D-99.\nfunc {\n"),
		}, nil},
		{"footnotes, and a link out of the repository", []docsEdit{
			replace(fxReadme, "says so too.", "says so too.[^spec] See [issue 51](../../issues/51)."),
			replace(fxReadme, "```text", "[^spec]: The spec's own words.\n\n```text"),
		}, nil},
		{"a nested doc's anchor in a Go comment, and nested names that are not references", []docsEdit{
			add("internal/livecheck/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live; ./internal/livecheck/NOTES.md#setup says how.\n"+
				"// Each run writes out/summary.md, as https://example.com/x/blob/main/MANIFEST.md says."),
		}, nil},
		{"a doc in a Go comment read from the file's directory, up and out of it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live; testdata/pg.md, ../../README.md#fixture and ../../../outside.md say so."),
		}, nil},
		{"a doc path inside a URL or a query string in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, at https://pkg.go.dev/example.com/fixture@v1.0.0/RUNBOOK.md#setup\n"+
				"// and at /render?page=docs/gone.md."),
		}, nil},
		{"a root doc's anchor, and a lowercase file name, in a Go comment", []docsEdit{
			replace(fxClient, "§15.4 allows.", "§15.4 allows, as README.md#fixture says.\n// Each retry is logged to retries.md."),
		}, nil},
		{"a doc path in a string of a test-helper package, which is fixture content", []docsEdit{
			codemapNames("internal/gapi/gapitest"),
			add("internal/gapi/gapitest/fake.go", textLines(
				"package gapitest",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, nil},
		{"a bare file name in a Go string, and a doc path in a test's string", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, §4 and NOTES.md"),
			replace(fxClientTest, "_ = Retry()", `_ = Retry() + "docs/gone.md"`),
		}, nil},
		{"an escaped pipe in an index cell, unescaped to match the entry", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a \\| b` times |"),
			replace(fxArch, "### D-03 Retry three times", "### D-03 Retry `a | b` times"),
		}, nil},
		{"a link written in a code span", []docsEdit{
			replace(fxReadme, "says so too.", "says so too. Write `[x](docs/example.md#slug)` for a link."),
		}, nil},
		{"a placeholder or a wildcard where a make target, a gates command or a path goes", []docsEdit{
			replace(fxAgent, "needs a binary.", "needs a binary; `make <target>` and `go run ./scripts/gates <name>` run one,\n"+
				"`make ...` and `scripts/gates ...` run more, and `go test ./internal/gone/...` tests them."),
		}, nil},
		{"a gates command that runs by hand, in no pipeline", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke` needs a binary.", "`scripts/gates smoke` needs a binary and `scripts/gates api-diff` a network."),
			replace(fxArch, "| forbidigo, lint, review |", "| forbidigo, api-diff, review |"),
		}, nil},
		{"a workflow by its bare name, and an archive's file, which is not a path here", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `ci.yml` checks it and `checksums.txt` lists an archive"),
		}, nil},
		{"a bare file name in a nested doc, read from its directory, then the root, then as a workflow", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemap`, `go.mod` and `ci.yml` say, the retry is in"),
		}, nil},
		{"a generated table under a package's testdata, which is not a record", []docsEdit{
			add("internal/gapi/testdata/golden.tsv", textLines("method\tnote", "files.get\tD-99 and E-12")),
		}, nil},
		{"what a command writes beside the records: a snapshot, the baseline and a golden file", []docsEdit{
			add("testdata/api-fields.json", textLines(`{"note": "D-99, §4 and E-12"}`)),
			add("testdata/schema-baseline.json", textLines(`{"note": "D-98"}`)),
			add("testdata/golden/card.txt", textLines("D-97 and §5")),
		}, nil},
		{"IDs in a YAML or TOML value, after a # with no space before it, and in a Makefile recipe", []docsEdit{
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      - run: echo D-99 §4 https://example.com/#E-12",
			)),
			replace(fxGitleaks, `title = "fixture"`, `title = "D-99#E-12"`),
			replace(fxMakefile, "\tgolangci-lint run", "\tgolangci-lint run\n\t@echo D-99"),
		}, nil},
	})
}

// What a pass reads is in its summary, so "found nothing" and "looked at
// nothing" cannot read the same. A failure counts its problems and prints
// nothing.
func TestDocsGateSaysWhatItRead(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := docsRun(t, docsFixture(), docsFixtureFloors).verdict(&out); err != nil {
		t.Fatalf("the fixture failed: %v", err)
	}
	want := "3 decisions, 3 packages, 9 live steps, 2 integration tests, 8 links and 14 code-span paths, in 20 files\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}

	out.Reset()
	edited := docsEdited(t, replace(fxClient, "(D-01)", "(D-07)"), replace(fxReadme, "says so too.", "says so too, as D-08 did."))
	err := docsRun(t, edited, docsFixtureFloors).verdict(&out)
	want = "2 problem(s):\n  README.md:6: cites D-08, which is not a decision\n  internal/gapi/client.go:3: cites D-07, which is not a decision"
	if err == nil || err.Error() != want || out.Len() != 0 {
		t.Errorf("failed with %v after printing %q, want %q and nothing printed", err, out.String(), want)
	}
}

// The gate reads what git would commit, as it is on disk: an untracked
// doc is read and an ignored file is not. A file deleted in the worktree
// stays in the index until the deletion is staged, and has nothing to
// read.
func TestDocsGateReadsWhatGitWouldCommit(t *testing.T) {
	root := initRepo(t)
	putAll(t, root, docsEdited(t,
		add("internal/old/old.go", "package old\n"),
		add("internal/generated/broken.go", "this is not Go\n"),
	))
	mustGit(t, "add", "-A")
	if err := os.Remove(filepath.Join(root, "internal", "old", "old.go")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "docs/new.md", "A new page cites D-07.\n")
	err := docsGateAt(root, docsFixtureFloors, io.Discard)
	if want := "1 problem(s):\n  docs/new.md:1: cites D-07, which is not a decision"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestDocsGateHoldsDecisionIntegrity(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"an ID in the index twice", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n"+fxRow03),
		}, []string{"docs/architecture.md:15: D-03 is in the index twice"}},
		{"an ID with two entries", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-03 Retry three times\n\nRejected: none.\nCost: none.\n\nNext ID: D-04"),
		}, []string{"docs/architecture.md:40: D-03 has two entries"}},
		{"an ID at Next ID", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-03"),
		}, []string{"docs/architecture.md:14: D-03 is not below Next ID D-03"}},
		{"an ID below Next ID that is gone", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 is below Next ID D-05 and is missing; an ID is never deleted"}},
		{"no Next ID line", []docsEdit{
			replace(fxArch, "\n\nNext ID: D-04", ""),
		}, []string{`docs/architecture.md:38: the last line is not "Next ID: D-NN"`}},
		{"a status outside the set", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off | accepted |", "| D-01 | Retries back off | proposed |"),
		}, []string{`docs/architecture.md:12: D-01 has status "proposed", want accepted, rejected, deprecated or superseded by D-NN`}},
		{"a superseded status that names no decision ID", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-3 |"),
		}, []string{
			`docs/architecture.md:13: D-02 has status "superseded by D-3", want accepted, rejected, deprecated or superseded by D-NN`,
			`docs/architecture.md:13: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a superseded status naming a decision that does not exist", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-07 |"),
		}, []string{"docs/architecture.md:13: cites D-07, which is not a decision"}},
		{"an entry titled unlike its index row", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off |", "| D-01 | Retries back off fast |"),
		}, []string{`docs/architecture.md:16: D-01's entry is titled "Retries back off" and its index row "Retries back off fast"`}},
		{"an entry with no Rejected line", []docsEdit{
			replace(fxArch, "Rejected: giving up, which drops the write.\n", ""),
		}, []string{`docs/architecture.md:28: D-02 has no "Rejected:" line`}},
		{"an entry with no Cost line", []docsEdit{
			replace(fxArch, "Cost: a fourth 429 fails the call.\n", ""),
		}, []string{`docs/architecture.md:33: D-03 has no "Cost:" line`}},
		{"an index row with no entry", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | accepted | live |"),
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:15: D-04 has no entry"}},
		{"an entry with no index row", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 has no index row"}},
		{"an ID with one digit, which the citation scan also reads, reported once", []docsEdit{
			replace(fxArch, "| D-03 | Retry three", "| D-3 | Retry three"),
		}, []string{
			`docs/architecture.md:14: "D-3" is not written as a decision ID like D-03`,
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"a pipe left unescaped in an index cell, even in a code span", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a | b` times |"),
		}, []string{
			"docs/architecture.md:14: an index row has 5 cells, want 4",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index with a column too many", []docsEdit{
			replace(fxArch, "| Held by |\n| --- | --- | --- | --- |", "| Held by | Links |\n| --- | --- | --- | --- | --- |"),
		}, []string{
			`docs/architecture.md:10: the decision index has the columns ["ID" "Decision" "Status" "Held by" "Links"], ` +
				`want ["ID" "Decision" "Status" "Held by"]`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:16: D-01 has no index row",
			"docs/architecture.md:28: D-02 has no index row",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index that is not directly under its heading", []docsEdit{
			replace(fxArch, "## Decisions\n\n|", "## Decisions\n\nThe index.\n\n|"),
		}, []string{
			`docs/architecture.md:8: the decision index is not a table directly under "## Decisions"`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:18: D-01 has no index row",
			"docs/architecture.md:30: D-02 has no index row",
			"docs/architecture.md:35: D-03 has no index row",
		}},
	})
}

func TestDocsGateHoldsHeldByToWhatExists(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a fuzz target, whose seeds go test runs", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| FuzzRetry, smoke |"),
		}, nil},
		{"a test function that does not exist", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetryLater, smoke |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "TestRetryLater", ` + notAHolder}},
		{"a benchmark or an example, which go test does not run by name", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| BenchmarkRetry, ExampleRetry, smoke |"),
		}, []string{
			`docs/architecture.md:12: D-01 is held by "BenchmarkRetry", ` + notAHolder,
			`docs/architecture.md:12: D-01 is held by "ExampleRetry", ` + notAHolder,
		}},
		{"a make target beside a quoted variable is the only target", []docsEdit{
			replace(fxAgent, "`make check`", "`make verify LIVE_ARGS=\"-run Word\"`"),
		}, []string{"CLAUDE.md:5: names make verify, which is not a Makefile target"}},
		{"a gates command that does not exist", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, smok |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "smok", ` + notAHolder}},
		{"a linter configured but not enabled", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| misspell, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "misspell", ` + notAHolder}},
		{"a linter list whose keys carry a comment", []docsEdit{
			replace(fxGolangci, "linters:\n  default: none\n  enable:\n", "linters: # the checks\n  default: none\n  enable: # the list\n"),
		}, nil},
		{"a linter enabled only inside another linter's settings", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| nilness, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "nilness", ` + notAHolder}},
		{"an accepted decision held by a dash", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| — |"),
		}, []string{"docs/architecture.md:14: D-03 is accepted and held by —; an accepted decision names what holds it"}},
		{"a decision held by nothing", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| |"),
		}, []string{"docs/architecture.md:12: D-01 is held by nothing"}},
		// live is the Makefile's target, so it holds only while `make live` does.
		{"live, while the Makefile has its target", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, live |"),
		}, nil},
		{"live, with no make live", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, live |"),
			replace(fxMakefile, "\n.PHONY: live\nlive:\n\tgo test -tags=live ./internal/livecheck\n", ""),
		}, []string{`docs/architecture.md:12: D-01 is held by "live", ` + notAHolder}},
	})
}

// Evidence has no IDs: what was verified upstream is stated at the line
// that depends on it. An E- citation fails wherever another citation is read.
func TestDocsGateRefusesEvidenceIDs(t *testing.T) {
	const noIDs = "; evidence has no IDs: state the behavior at the line that depends on it, with its source link"
	checkDocsCases(t, []docsCase{
		{"an evidence ID in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured in E-001."),
		}, []string{"internal/gapi/client.go:3: cites E-001" + noIDs}},
		{"an evidence ID in a doc, at any width", []docsEdit{
			replace(fxArch, "Google answers a burst with 429, so", "Google answers a burst with 429 (E-12), so"),
		}, []string{"docs/architecture.md:18: cites E-12" + noIDs}},
		{"an evidence ID in a record's reason", []docsEdit{
			replace(fxRecord, "(D-01)", "(E-12)"),
		}, []string{"testdata/api-coverage.tsv:2: cites E-12" + noIDs}},
		{"an evidence ID in a fenced block", []docsEdit{
			replace(fxReadme, "§3, D-99 and", "§3, D-99, E-001 and"),
		}, nil},
		{"an evidence ID in a string literal", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, E-001 and §4"),
		}, nil},
	})
}

// A comment names the live step that checks it, and the name has to be
// one the live driver gives a call or a resource read.
func TestDocsGateHoldsLiveStepsToTheLiveRun(t *testing.T) {
	const undefined = ", which internal/livecheck does not define"
	cites := "Code cites `docs/architecture.md#d-01-retries-back-off`."
	checkDocsCases(t, []docsCase{
		{"a Markdown paragraph, across a line break", []docsEdit{
			replace(fxReadme, cites, cites+" The live\nstep \"a file is uploaded\" checks it."),
		}, []string{`README.md:5: names the live step "a file is uploaded"` + undefined}},
		{"a Markdown paragraph naming a real step", []docsEdit{
			replace(fxReadme, cites, cites+" The live step \"a file uploads\" checks it."),
		}, nil},
		{"a placeholder showing the form", []docsEdit{
			replace(fxReadme, cites, cites+" Cite it as `Checked by the live step \"<name>\".`"),
		}, nil},
		{"a record's reason", []docsEdit{
			replace(fxFields, "the server writes no thumbnails (D-03)", "the live step \"a file is uploaded\" sends none (D-03)"),
		}, []string{`testdata/api-fields.tsv:2: names the live step "a file is uploaded"` + undefined}},
		{"a name wrapped across comment lines", []docsEdit{
			replace(fxClient, "// reports nothing\"", "// reports something\""),
		}, []string{`internal/gapi/client.go:8: names the live step "a repeat delete reports something"` + undefined}},
		{"the second name of a list", []docsEdit{
			replace(fxClient, `and "a file's text is read as a resource".`, `and "a file downloads".`),
		}, []string{`internal/gapi/client.go:9: names the live step "a file downloads"` + undefined}},
		{"the second name of a list joined by or", []docsEdit{
			replace(fxClient, `and "a file's text is read as a resource".`, `or "a file downloads".`),
		}, []string{`internal/gapi/client.go:9: names the live step "a file downloads"` + undefined}},
		{"a step only a unit test beside the driver names", []docsEdit{
			replace(fxClient, `step "a file uploads", and`, `step "a test-only step", and`),
		}, []string{`internal/gapi/client.go:8: names the live step "a test-only step"` + undefined}},
		{"a step named from a variable", []docsEdit{
			replace(fxClient, `"a file is copied twice"`, `"a search finds the file"`),
		}, []string{`internal/gapi/client.go:17: names the live step "a search finds the file"` + undefined}},
		{"a step named by a literal prefix and a variable", []docsEdit{
			replace(fxClient, `"a file is copied twice"`, `"dry run: a search finds the file"`),
		}, []string{`internal/gapi/client.go:17: names the live step "dry run: a search finds the file"` + undefined}},
		{"a closure that adds words of its own to the name", []docsEdit{
			replace(fxClient, `step "a file uploads", and`, `step "a probe, not a step", and`),
		}, []string{`internal/gapi/client.go:8: names the live step "a probe, not a step"` + undefined}},
		{"a log line that is not a step", []docsEdit{
			replace(fxClient, `step "a file uploads", and`, `step "a log line, not a step", and`),
		}, []string{`internal/gapi/client.go:8: names the live step "a log line, not a step"` + undefined}},
		{"a call run alone, renamed", []docsEdit{
			replace(fxDriver, `"a checkpoint goes stale"`, `"a checkpoint is stale"`),
		}, []string{`internal/gapi/client.go:16: names the live step "a checkpoint goes stale"` + undefined}},
		{"a call whose name is joined from two strings", []docsEdit{
			replace(fxDriver, `"a file is deleted for good"`, `"a file is " + "deleted for good"`),
		}, []string{`internal/gapi/client.go:13: names the live step "a file is deleted for good"` + undefined}},
		{"a resource read, renamed", []docsEdit{
			replace(fxDriver, `"a file's card is read as a resource"`, `"a file's card is read"`),
		}, []string{`internal/gapi/client.go:13: names the live step "a file's card is read as a resource"` + undefined}},
		{"a closure that passes the name on, renamed", []docsEdit{
			replace(fxDriver, `edit("a list is nested")`, `edit("a list nests")`),
		}, []string{`internal/gapi/client.go:20: names the live step "a list is nested"` + undefined}},
		{"a literal after a mode prefix, renamed", []docsEdit{
			replace(fxDriver, `mode+": a list is read back"`, `mode+": a list reads back"`),
		}, []string{`internal/gapi/client.go:20: names the live step "a list is read back"` + undefined}},
		{"a literal after a prefix joined without a colon", []docsEdit{
			replace(fxDriver, `mode+": a list is read back"`, `mode+"/a list is read back"`),
		}, []string{`internal/gapi/client.go:20: names the live step "a list is read back"` + undefined}},
		{"a driver whose call heads no transcript entry", []docsEdit{
			replace(fxDriver, `d.t.Logf("=== %s (isError=%t) ===", label, false)`, `d.t.Logf("%s (isError=%t)", label, false)`),
		}, []string{
			`internal/gapi/client.go:8: names the live step "a file uploads"` + undefined,
			`internal/gapi/client.go:8: names the live step "a repeat delete reports nothing"` + undefined,
			`internal/gapi/client.go:13: names the live step "a file is deleted for good"` + undefined,
			`internal/gapi/client.go:13: names the live step "a file uploads"` + undefined,
			`internal/gapi/client.go:16: names the live step "a checkpoint goes stale"` + undefined,
			`internal/gapi/client.go:17: names the live step "a file is copied twice"` + undefined,
			`internal/gapi/client.go:17: names the live step "a file uploads"` + undefined,
			`internal/gapi/client.go:20: names the live step "a list is nested"` + undefined,
			`internal/gapi/client.go:20: names the live step "a list is read back"` + undefined,
		}},
		{"a step call outside the driver's package", []docsEdit{
			add("internal/gapi/steps_test.go", textLines(
				"//go:build live",
				"",
				"package gapi",
				"",
				`func elsewhere(d *driver) { d.ok("a call outside the driver", "x") }`,
			)),
			replace(fxClient, `"a file is copied twice"`, `"a call outside the driver"`),
		}, []string{`internal/gapi/client.go:17: names the live step "a call outside the driver"` + undefined}},
		{"a field of a step that is not its name", []docsEdit{
			replace(fxClient, `// "a file is deleted for good", "a file's card`, `// "delete_file", "a file's card`),
		}, []string{`internal/gapi/client.go:13: names the live step "delete_file"` + undefined}},
		{"two steps of one name", []docsEdit{
			replace(fxDriver, `"a checkpoint goes stale"`, `"a file uploads"`),
		}, []string{
			`internal/livecheck/live_test.go:24: names the live step "a file uploads", which internal/livecheck/live_test.go:22 names too; a step's name is its own`,
			`internal/gapi/client.go:16: names the live step "a checkpoint goes stale"` + undefined,
		}},
	})
}

// A comment names the test that checks it by its kind, and the test has
// to be one a file built only with that kind's tag declares. A live test
// is named for what it is, so its name alone is a citation.
func TestDocsGateHoldsTestCitationsToTheirTag(t *testing.T) {
	const (
		notIntegration = ", which no test file built only with -tags=integration declares"
		notLive        = ", which no test file built only with -tags=live declares"
	)
	cites := "Code cites `docs/architecture.md#d-01-retries-back-off`."
	checkDocsCases(t, []docsCase{
		{"an integration test that does not exist", []docsEdit{
			replace(fxClient, "// TestRawUpload, and", "// TestRawDownload, and"),
		}, []string{"internal/gapi/client.go:22: names the integration test TestRawDownload" + notIntegration}},
		{"the first test of a list, before a line break", []docsEdit{
			replace(fxClient, "tests TestRawRetry and", "tests TestRawRetries and"),
		}, []string{"internal/gapi/client.go:21: names the integration test TestRawRetries" + notIntegration}},
		{"integration tests in a file built without the tag", []docsEdit{
			replace(fxRaw, "//go:build integration\n\n", ""),
		}, []string{
			"0 integration tests read, fewer than 1: the files built with -tags=integration are not being read",
			"internal/gapi/client.go:21: names the integration test TestRawRetry" + notIntegration,
			"internal/gapi/client.go:22: names the integration test TestRawUpload" + notIntegration,
		}},
		{"a test in a file that builds without the tag too", []docsEdit{
			add("internal/gapi/other_test.go", textLines(
				"//go:build integration || !windows",
				"",
				"package gapi",
				"",
				`import "testing"`,
				"",
				"func TestRawOther(t *testing.T) {}",
			)),
			replace(fxClient, "// TestRawUpload, and", "// TestRawOther, and"),
		}, []string{"internal/gapi/client.go:22: names the integration test TestRawOther" + notIntegration}},
		{"a unit test cited as an integration test", []docsEdit{
			replace(fxClient, "// TestRawUpload, and", "// TestRetry, and"),
		}, []string{"internal/gapi/client.go:22: names the integration test TestRetry" + notIntegration}},
		{"a live test named alone that does not exist", []docsEdit{
			replace(fxClient, "and by TestLive.", "and by TestLiveLists."),
		}, []string{"internal/gapi/client.go:22: names the live test TestLiveLists" + notLive}},
		{"live tests cited by kind in Markdown", []docsEdit{
			replace(fxReadme, cites, cites+" The live tests TestLive and\n`TestWord` check it."),
		}, []string{"README.md:5: names the live test TestWord" + notLive}},
		{"an integration test in a code span in Markdown", []docsEdit{
			replace(fxReadme, cites, cites+" The integration test `TestRawRetry` checks it, and `TestRawGone`."),
		}, nil},
		{"an integration test in a code span in Markdown that does not exist", []docsEdit{
			replace(fxReadme, cites, cites+" The integration test `TestRawGone` checks it."),
		}, []string{"README.md:4: names the integration test TestRawGone" + notIntegration}},
		{"a record's reason", []docsEdit{
			replace(fxRecord, "an endpoint the server does not have (D-01)", "an endpoint, as the integration test TestRawWatch shows (D-01)"),
		}, []string{"testdata/api-coverage.tsv:2: names the integration test TestRawWatch" + notIntegration}},
		{"a placeholder showing the form", []docsEdit{
			replace(fxReadme, cites, cites+" Cite one as `Checked by the integration test Test<Name>.`"),
		}, nil},
		// A subtest's name is whatever t.Run is handed, which no reader can
		// follow, so a mistyped one would pass as its test.
		{"a subtest, after integration test and on its own", []docsEdit{
			replace(fxClient, "// TestRawUpload, and by TestLive.", "// TestRawUpload/a_burst, and by TestLive/sub."),
		}, []string{
			"internal/gapi/client.go:22: names the integration test TestRawUpload/a_burst, which is a subtest: cite the integration test, not a subtest",
			"internal/gapi/client.go:22: names the live test TestLive/sub, which is a subtest: cite the live test, not a subtest",
		}},
		// An OS or architecture tag is the go command's to set, as parity
		// reads it, so -tags=integration alone builds this file on Linux.
		{"an integration file an OS tag also builds", []docsEdit{
			replace(fxRaw, "//go:build integration", "//go:build integration && linux"),
		}, nil},
	})
}

func TestDocsGateResolvesCitations(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a superseded decision cited from a Go comment", []docsEdit{
			replace(fxClient, "(D-01)", "(D-02)"),
		}, []string{"internal/gapi/client.go:3: cites D-02, which is superseded by D-03"}},
		{"a deprecated decision cited from a doc", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | deprecated | — |"),
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
			replace(fxReadme, "#d-01-retries-back-off`.", "#d-01-retries-back-off`. Logging was D-04."),
		}, []string{"README.md:4: cites D-04, which is deprecated"}},
		{"an unknown decision cited twice on a line, reported once", []docsEdit{
			replace(fxClient, "(D-01)", "(D-07, and D-07 again)"),
		}, []string{"internal/gapi/client.go:3: cites D-07, which is not a decision"}},
		{"an unknown decision in each record's reason", []docsEdit{
			replace(fxRecord, "(D-01)", "(D-07)"),
			replace(fxFields, "(D-03)", "(D-05), as §4 says"),
		}, []string{
			"testdata/api-coverage.tsv:2: cites D-07, which is not a decision",
			"testdata/api-fields.tsv:2: cites D-05, which is not a decision",
			"testdata/api-fields.tsv:2: cites a section by number (§4); " + bySlug,
		}},
		{"a link slug no heading has", []docsEdit{
			replace(fxReadme, "docs/architecture.md#decisions", "docs/architecture.md#decision"),
		}, []string{"README.md:3: links to docs/architecture.md#decision, and docs/architecture.md has no heading with that slug"}},
		{"a code span slug no heading has", []docsEdit{
			replace(fxReadme, "#d-01-retries-back-off`", "#d-01-retry`"),
		}, []string{"README.md:4: names docs/architecture.md#d-01-retry, and docs/architecture.md has no heading with that slug"}},
		{"a repeated heading's suffix past the last repeat", []docsEdit{
			replace(fxReadme, "#steps-1", "#steps-2"),
		}, []string{"README.md:3: links to docs/runbook.md#steps-2, and docs/runbook.md has no heading with that slug"}},
		{"a same-file anchor no heading has", []docsEdit{
			replace(fxRunbook, "(#steps)", "(#step)"),
		}, []string{"docs/runbook.md:12: links to docs/runbook.md#step, and docs/runbook.md has no heading with that slug"}},
		{"a reference definition no heading answers", []docsEdit{
			replace(fxRunbook, "[map]: architecture.md#codemap", "[map]: architecture.md#codemaps"),
		}, []string{"docs/runbook.md:14: links to docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug"}},
		{"a link to a doc that does not exist", []docsEdit{
			replace(fxReadme, "says so too.", "says so too, in [the guide](docs/gone.md)."),
		}, []string{"README.md:6: links to docs/gone.md, which does not exist"}},
		{"a link written from the root instead of the linking file", []docsEdit{
			replace(fxRunbook, "](architecture.md#codemap)", "](docs/architecture.md#codemap)"),
		}, []string{"docs/runbook.md:5: links to docs/docs/architecture.md, which does not exist"}},
		{"a docs citation in a Go comment to a file that does not exist", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live and in docs/retries.md#backoff."),
		}, []string{"internal/gapi/client.go:3: cites docs/retries.md, which does not exist"}},
		{"a bare section number in a Go comment", []docsEdit{
			replace(fxClient, "as RFC 9110 §15.4 allows", "as §15.4 allows"),
		}, []string{"internal/gapi/client.go:4: cites a section by number (§15); " + bySlug}},
		{"a section number not right after an RFC or inside a URL on its line", []docsEdit{
			replace(fxClient, "RFC 9110 §15.4 allows", "RFC 9110 allows and §15.4 says"),
			replace(fxReadme, "RFC 9110 §15.4", "RFC 9110  §15.4"),
			replace(fxReadme, "spec#§2) says", "spec) §2 says"),
		}, []string{
			"README.md:5: cites a section by number (§15); " + bySlug,
			"README.md:6: cites a section by number (§2); " + bySlug,
			"internal/gapi/client.go:4: cites a section by number (§15); " + bySlug,
		}},
		{"a bare section number in the changelog's Unreleased section", []docsEdit{
			replace(fxChangelog, "#codemap` says.", "#codemap` and §3 say."),
		}, []string{"CHANGELOG.md:5: cites a section by number (§3); " + bySlug}},
		{"a bare section number in a released changelog section", []docsEdit{
			replace(fxChangelog, "Cites D-99 and", "Cites D-99, §3 and"),
		}, nil},
		{"IDs written at the wrong width, in a Go comment and a doc", []docsEdit{
			replace(fxClient, "(D-01)", "(D-1)"),
			replace(fxReadme, "says so too.", "says so too, as D-001 did."),
		}, []string{
			`README.md:6: "D-001" is not written as a decision ID like D-01`,
			`internal/gapi/client.go:3: "D-1" is not written as a decision ID like D-01`,
		}},
		{"a one-line code span in fence backticks opens no fence", []docsEdit{
			replace(fxReadme, "says so too.\n", "says so too.\n```go``` is a span, not a fence.\nAs D-07 said.\n"),
		}, []string{"README.md:8: cites D-07, which is not a decision"}},
		{"a nested doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			add("internal/livecheck/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live, packaging/README.md and internal/livecheck/NOTES.md#teardown."),
		}, []string{
			"internal/gapi/client.go:3: cites packaging/README.md, which does not exist",
			"internal/gapi/client.go:3: cites internal/livecheck/NOTES.md#teardown, and internal/livecheck/NOTES.md has no heading with that slug",
		}},
		{"a doc in a Go comment missing from the file's directory, or up from it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, testdata/gone.md and ../gone.md."),
		}, []string{
			"internal/gapi/client.go:3: cites internal/gapi/testdata/gone.md, which does not exist",
			"internal/gapi/client.go:3: cites internal/gone.md, which does not exist",
		}},
		{"a root doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, RUNBOOK.md and README.md#retries."),
		}, []string{
			"internal/gapi/client.go:3: cites RUNBOOK.md, which does not exist",
			"internal/gapi/client.go:3: cites README.md#retries, and README.md has no heading with that slug",
		}},
		{"doc paths in a Go string that do not resolve", []docsEdit{
			replace(fxClient, "in a string are data", "in a string are data; docs/setup.md and README.md#setup are not"),
		}, []string{
			"internal/gapi/client.go:5: names docs/setup.md, which does not exist",
			"internal/gapi/client.go:5: names README.md#setup, and README.md has no heading with that slug",
		}},
		{"a doc path in an ordinary package's string, and in a test-helper package's comment", []docsEdit{
			codemapNames("internal/gapi/gapiutil"),
			codemapNames("internal/gapi/gapitest"),
			add("internal/gapi/gapiutil/fake.go", textLines(
				"package gapiutil",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
			add("internal/gapi/gapitest/fake.go", textLines(
				"package gapitest",
				"",
				"// The fake's files, as docs/fake.md lists them.",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, []string{
			"internal/gapi/gapitest/fake.go:3: cites docs/fake.md, which does not exist",
			"internal/gapi/gapiutil/fake.go:3: names docs/guide.md, which does not exist",
		}},
		{"a comment line in each kind of config file", []docsEdit{
			replace(fxMakefile, "(D-03)", "(D-3)"),
			replace(fxGitignore, "#steps says", "#stairs says"),
			replace(fxGitleaks, "generated (D-01)", "generated (D-07)"),
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      # Pinned, as E-12 found.",
				"      - run: go test ./...",
			)),
			add(".goreleaser.yaml", textLines("version: 2", "# Archives follow §3.")),
		}, []string{
			".github/workflows/ci.yml:5: cites E-12; evidence has no IDs: state the behavior at the line that depends on it, with its source link",
			".gitignore:2: cites docs/runbook.md#stairs, and docs/runbook.md has no heading with that slug",
			".gitleaks.toml:2: cites D-07, which is not a decision",
			".goreleaser.yaml:2: cites a section by number (§3); " + bySlug,
			`Makefile:1: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a trailing comment on a YAML or TOML line", []docsEdit{
			replace(fxGolangci, "as D-01 says", "as D-07 says"),
			replace(fxGitleaks, "synthetic, as D-01 says", "synthetic, as D-08 says"),
		}, []string{
			".gitleaks.toml:4: cites D-08, which is not a decision",
			".golangci.yml:12: cites D-07, which is not a decision",
		}},
		{"a TOML multi-line string's # is text, and a comment after it is read", []docsEdit{
			replace(fxGitleaks, "as D-01 says\n", "as D-01 says\n"+textLines(
				"description = '''",
				"# D-99 is text in a literal string,",
				"'''",
				`note = """`,
				"as is a # before D-97 in a basic one.",
				`""" # as D-08 says`,
			)),
		}, []string{".gitleaks.toml:10: cites D-08, which is not a decision"}},
	})
}

func TestDocsGateHoldsPathsToTheRepository(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a path in CLAUDE.md that does not exist", []docsEdit{
			replace(fxAgent, "`internal/gapi/`", "`internal/api/`"),
		}, []string{"CLAUDE.md:4: names internal/api/, which does not exist"}},
		{"a path in another doc that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi/retry.go`"),
		}, []string{"docs/runbook.md:6: names internal/gapi/retry.go, which does not exist"}},
		{"a package in the codemap that does not exist", []docsEdit{
			replace(fxArch, "drives it live.", "drives it live.\n- `internal/gone` was removed."),
		}, []string{"docs/architecture.md:7: names internal/gone, which does not exist"}},
		{"a link to a file that does not exist", []docsEdit{
			replace(fxRunbook, "(../internal/gapi/client.go)", "(../internal/gapi/retry.go)"),
		}, []string{"docs/runbook.md:6: links to internal/gapi/retry.go, which does not exist"}},
		{"a link from the root to a directory that does not exist", []docsEdit{
			replace(fxRunbook, "(/cmd/server)", "(/cmd/client)"),
		}, []string{"docs/runbook.md:10: links to cmd/client, which does not exist"}},
		{"a file:line pointer, with punctuation after it", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`(see internal/gapi/client.go:3:5),` and `internal/gapi/client.go:3`"),
		}, nil},
		{"a file:line pointer to a file that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi/retry.go:12`"),
		}, []string{"docs/runbook.md:6: names internal/gapi/retry.go:12, which does not exist"}},
		{"a Go name, plain or qualified, in a package that exists", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi.Retry` and `internal/gapi.Client.Do`"),
		}, nil},
		{"a missing file whose name less its extension is a directory", []docsEdit{
			codemapNames("internal/service/approver"),
			add("internal/service/approver/main.go", "package main\n"),
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/service/approver.go` and `scripts/gates.sh`"),
		}, []string{
			"docs/runbook.md:6: names internal/service/approver.go, which does not exist",
			"docs/runbook.md:6: names scripts/gates.sh, which does not exist",
		}},
		{"a Go name in a package that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/api.Retry`"),
		}, []string{"docs/runbook.md:6: names internal/api.Retry, which does not exist"}},
		{"a root file and a source directory that are gone, recognized by their shape", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `.goreleaser.yaml` the archives and `packaging/mcpb/manifest.json` the bundle"),
		}, []string{
			"CLAUDE.md:4: names .goreleaser.yaml, which does not exist",
			"CLAUDE.md:4: names packaging/mcpb/manifest.json, which does not exist",
		}},
		{"a bare workflow name no workflow has", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, and `release.yml` releases it"),
		}, []string{"CLAUDE.md:4: names release.yml, which does not exist"}},
		{"a bare file name in a nested doc, with no such heading in its directory, or found nowhere", []docsEdit{
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemaps` and `gone.md` say, the retry is in"),
		}, []string{
			"docs/runbook.md:6: names docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:6: names gone.md, which does not exist",
		}},
		{"a file named as a directory", []docsEdit{
			replace(fxAgent, "`go.mod`", "`go.mod/`"),
		}, []string{"CLAUDE.md:4: names go.mod/, which is not a directory"}},
		{"a make target that does not exist", []docsEdit{
			replace(fxAgent, "`make check`", "`make verify`"),
		}, []string{"CLAUDE.md:5: names make verify, which is not a Makefile target"}},
		{"a gates command that does not exist", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke`", "`scripts/gates smok`"),
		}, []string{"CLAUDE.md:5: names scripts/gates smok, which is not a gates command"}},
		{"no CLAUDE.md", []docsEdit{
			drop(fxAgent),
		}, []string{"CLAUDE.md is missing"}},
	})
}

// A gate that read nothing passes everything, so each input it depends on
// reading has a floor, and each row here sets one off.
func TestDocsGateRefusesToReadNothing(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"no Go files", []docsEdit{
			drop(fxClient), drop(fxClientTest), drop(fxServer), drop(fxDriver), drop(fxDriverUnit), drop(fxRaw), drop(fxGates),
		}, []string{
			"no .go files read: this gate would be looking at nothing",
			"0 integration tests read, fewer than 1: the files built with -tags=integration are not being read",
			"0 live steps read from internal/livecheck, fewer than 1: the driver's steps are not being read",
			`docs/architecture.md:12: D-01 is held by "TestRetry", ` + notAHolder,
			"CLAUDE.md:5: names ./scripts/gates, which does not exist",
			"CLAUDE.md:5: names scripts/gates, which does not exist",
			"docs/architecture.md:5: names internal/gapi/client.go, which does not exist",
			"docs/architecture.md:6: names cmd/server, which does not exist",
			"docs/architecture.md:6: names internal/livecheck, which does not exist",
			"docs/runbook.md:6: links to internal/gapi/client.go, which does not exist",
			"docs/runbook.md:6: names internal/gapi/client.go, which does not exist",
			"docs/runbook.md:10: links to cmd/server, which does not exist",
			"0 package directories under internal/ and cmd/, fewer than 1: the tree is not being read",
		}},
		{"no step function, so every citation of a step is undefined", []docsEdit{
			add(fxDriver, textLines(
				"//go:build live",
				"",
				"package livecheck",
				"",
				"func (d *driver) ok(label, tool string) string { return tool }",
				"",
				`func steps(d *driver) { d.ok("a file uploads", "upload_file") }`,
			)),
		}, []string{
			"0 live steps read from internal/livecheck, fewer than 1: the driver's steps are not being read",
			`internal/gapi/client.go:8: names the live step "a file uploads", which internal/livecheck does not define`,
			`internal/gapi/client.go:8: names the live step "a repeat delete reports nothing", which internal/livecheck does not define`,
			`internal/gapi/client.go:9: names the live step "a file's text is read as a resource", which internal/livecheck does not define`,
			`internal/gapi/client.go:13: names the live step "a file is deleted for good", which internal/livecheck does not define`,
			`internal/gapi/client.go:13: names the live step "a file's card is read as a resource", which internal/livecheck does not define`,
			`internal/gapi/client.go:13: names the live step "a file uploads", which internal/livecheck does not define`,
			`internal/gapi/client.go:16: names the live step "a checkpoint goes stale", which internal/livecheck does not define`,
			`internal/gapi/client.go:17: names the live step "a file is copied twice", which internal/livecheck does not define`,
			`internal/gapi/client.go:17: names the live step "a file uploads", which internal/livecheck does not define`,
			`internal/gapi/client.go:20: names the live step "a list is nested", which internal/livecheck does not define`,
			`internal/gapi/client.go:20: names the live step "a list is read back", which internal/livecheck does not define`,
			"internal/gapi/client.go:22: names the live test TestLive, which no test file built only with -tags=live declares",
		}},
		{"an index with no rows", []docsEdit{
			replace(fxArch, fxRow01+"\n"+fxRow02+"\n"+fxRow03+"\n", ""),
		}, []string{
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:13: D-01 has no index row",
			"docs/architecture.md:25: D-02 has no index row",
			"docs/architecture.md:30: D-03 has no index row",
		}},
		{"a config that enables no linter", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", ""),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		// The reader takes a block list only. A flow list is read as none,
		// and the floor says so rather than passing over it.
		{"linters listed in flow style", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", "  enable: [errcheck, forbidigo]\n"),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		{"a Makefile with no rules", []docsEdit{
			add(fxMakefile, "GO ?= go\n"),
		}, []string{
			"Makefile: no targets read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "lint", ` + notAHolder,
			"CLAUDE.md:5: names make check, which is not a Makefile target",
		}},
		{"no architecture document", []docsEdit{
			drop(fxArch),
		}, []string{
			"docs/architecture.md is missing",
			"CHANGELOG.md:5: names docs/architecture.md#codemap, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md#codemap, which does not exist",
			"README.md:3: links to docs/architecture.md, which does not exist",
			"README.md:4: names docs/architecture.md#d-01-retries-back-off, which does not exist",
			"docs/runbook.md:5: links to docs/architecture.md, which does not exist",
			"docs/runbook.md:14: links to docs/architecture.md, which does not exist",
		}},
		{"a Go file that does not parse", []docsEdit{
			add("internal/gapi/broken.go", "package gapi\n\nfunc {\n"),
		}, []string{"gates cannot parse internal/gapi/broken.go: internal/gapi/broken.go:3:6: expected 'IDENT', found '{'"}},
	})
}

// Each pass has a floor of its own, so one that keeps reading cannot make
// up for another that stops. Each row raises one floor one past what the
// fixture holds.
func TestDocsGateHoldsEachPassToItsFloor(t *testing.T) {
	atFloor := docsFloors{links: 8, spanPaths: 14, packages: 3, liveSteps: 9, integration: 2}
	raised := func(raise func(*docsFloors)) docsFloors {
		f := atFloor
		raise(&f)
		return f
	}
	for _, tc := range []struct {
		name   string
		floors docsFloors
		edits  []docsEdit
		want   []string
	}{
		{"each pass at its floor", atFloor, nil, nil},
		{"links one under their floor", raised(func(f *docsFloors) { f.links++ }), nil,
			[]string{"8 Markdown links checked, fewer than 9: the links are not being read"}},
		{"code-span paths one under their floor", raised(func(f *docsFloors) { f.spanPaths++ }), nil,
			[]string{"14 code-span paths checked, fewer than 15: the code spans are not being read"}},
		{"packages one under their floor", raised(func(f *docsFloors) { f.packages++ }), nil,
			[]string{"3 package directories under internal/ and cmd/, fewer than 4: the tree is not being read"}},
		{"live steps one under their floor", raised(func(f *docsFloors) { f.liveSteps++ }), nil,
			[]string{"9 live steps read from internal/livecheck, fewer than 10: the driver's steps are not being read"}},
		{"integration tests one under their floor", raised(func(f *docsFloors) { f.integration++ }), nil,
			[]string{"2 integration tests read, fewer than 3: the files built with -tags=integration are not being read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDocs(t, tc.floors, tc.edits, tc.want)
		})
	}
}

// Outside a git checkout the gate has no file list. It reached no
// verdict, so it says so and exits 2, as a gate that could not run does.
func TestDocsGateNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	err := docsGateAt(dir, docsFixtureFloors, io.Discard)
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 ||
		!strings.HasPrefix(err.Error(), "could not run: git -C "+dir+" ls-files") {
		t.Errorf("got %v; want a git ls-files failure that could not run", err)
	}
}

// The codemap is held to the tree: every package directory under
// internal/ and cmd/, nested ones included, is named in a code span in
// the "## Codemap" section, and every directory it names holds Go.
func TestDocsGateHoldsTheCodemapToTheTree(t *testing.T) {
	const redact = "internal/redact/redact.go"
	checkDocsCases(t, []docsCase{
		{"a package named under a subheading of the codemap", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n### The logs\n\n`internal/redact` keeps the payload out.\n"),
		}, nil},
		{"a package the codemap leaves out", []docsEdit{
			add(redact, "package redact\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a nested package the codemap leaves out", []docsEdit{
			add("internal/gapi/gapitest/server.go", "package gapitest\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/gapi/gapitest`}},
		{"a package named only by a file in it", []docsEdit{
			add(redact, "package redact\n"),
			codemapNames(redact),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only in a fenced example", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n```markdown\n- `internal/redact` keeps the payload out.\n```\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only after the section ends", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "so a retry waits.", "so a retry waits, logged by `internal/redact`."),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a directory the codemap names that holds no .go file", []docsEdit{
			add("internal/assets/logo.txt", "a logo\n"),
			codemapNames("internal/assets"),
		}, []string{`docs/architecture.md:7: "## Codemap" names internal/assets, which holds no .go file`}},
		{"no codemap heading", []docsEdit{
			replace(fxArch, "## Codemap", "## Packages"),
		}, []string{
			"CHANGELOG.md:5: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"CLAUDE.md:3: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:5: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:14: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			`docs/architecture.md: no "## Codemap" heading`,
		}},
	})
}

// Under [Unreleased]'s Removed heading a code span, a live step and a
// test name what the release takes away, so they need not exist. A
// decision and a link are still held.
func TestDocsGateLetsTheRemovedSectionNameWhatIsGone(t *testing.T) {
	const undefined = ", which internal/livecheck does not define"
	unreleased := "#codemap` says.\n"
	checkDocsCases(t, []docsCase{
		{"a removed path, target, live step and test", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- `internal/gone`, `make gone`, the live step \"gone now\", "+
				"TestLiveGone and the integration test TestRawGone.\n"),
		}, nil},
		{"a level-4 heading inside Removed keeps it", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n#### Tools\n\n- `internal/gone`.\n"),
		}, nil},
		{"a decision and a link under Removed", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- D-07, and [the page](docs/gone.md).\n"),
		}, []string{
			"CHANGELOG.md:9: cites D-07, which is not a decision",
			"CHANGELOG.md:9: links to docs/gone.md, which does not exist",
		}},
		{"the heading after Removed ends it", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- `internal/gone`.\n\n### Fixed\n\n- `internal/lost` and the live step \"lost\".\n"),
		}, []string{
			"CHANGELOG.md:13: names internal/lost, which does not exist",
			`CHANGELOG.md:13: names the live step "lost"` + undefined,
		}},
		{"a Removed heading outside the changelog", []docsEdit{
			replace(fxRunbook, "Back to [the first steps](#steps).", "### Removed\n\n`internal/gone`."),
		}, []string{"docs/runbook.md:14: names internal/gone, which does not exist"}},
	})
}

// The scan reads the [Unreleased] lines the release note lifts: a heading
// in a fence does not end them, and a tab after ## starts a heading.
func TestDocsGateScansWhatTheReleaseNoteLifts(t *testing.T) {
	unreleased := "#codemap` says.\n"
	checkDocsCases(t, []docsCase{
		{"a heading in a fence", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n~~~markdown\n## [1.0.0]\n~~~\n\n- As D-07 says.\n"),
		}, []string{"CHANGELOG.md:11: cites D-07, which is not a decision"}},
		{"a heading with a tab", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n##\tLater\n\n- As D-07 says.\n"),
		}, nil},
	})
}

// A package directory holds a .go file of its own under internal/ or
// cmd/, at any depth, and outside testdata.
func TestDocsPackageDirs(t *testing.T) {
	files := []string{
		"cmd/google-docs-mcp/main.go",
		"cmd/google-docs-mcp/main_test.go",
		"internal/doc.go",
		"internal/gapi/client.go",
		"internal/gapi/gapitest/server.go",
		"internal/gapi/testdata/fixture.go",
		"internal/render/testdata/golden/card.txt",
		"internal/assets/logo.txt",
		"scripts/gates/main.go",
		"README.md",
	}
	want := []string{"cmd/google-docs-mcp", "internal/gapi", "internal/gapi/gapitest"}
	if got := docsPackageDirs(files); !slices.Equal(got, want) {
		t.Errorf("docsPackageDirs = %q, want %q", got, want)
	}
}

// These slugs are what GitHub renders for the same headings.
func TestGitHubSlug(t *testing.T) {
	for _, tc := range []struct{ heading, want string }{
		{"Codemap", "codemap"},
		{"D-01 Stdout carries only MCP JSON-RPC frames", "d-01-stdout-carries-only-mcp-json-rpc-frames"},
		{"What a write may reach", "what-a-write-may-reach"},
		{"The `register` function", "the-register-function"},
		{"C# and F#?", "c-and-f"},
		{"snake_case stays", "snake_case-stays"},
		{"Two  spaces", "two--spaces"},
		{"Straße über alles", "straße-über-alles"},
		{"A [link](https://example.com) here", "a-link-here"},
	} {
		if got := docsGitHubSlug(tc.heading); got != tc.want {
			t.Errorf("docsGitHubSlug(%q) = %q, want %q", tc.heading, got, tc.want)
		}
	}
}
