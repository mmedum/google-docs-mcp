# Development

## Gates

`make check` runs what CI runs: gofmt, go vet, golangci-lint, the tests
with the race detector and an 80% statement-coverage floor per core
package, govulncheck, the stdio smoke test, and the staleness check
(README tool table, configuration docs, changelog and architecture status
against the code). `make schema-diff` compares tool schemas with the last
tag and flags removed tools or fields and new required fields.

Before a phase is called done, also run `/simplify` and `/code-review
high` on the changed code and resolve or explicitly defer the findings
(deferred ones go under "Deferred cleanups" in `docs/architecture.md`).

Local tools built with the current Go live under `$(go env GOPATH)/bin`
and are preferred by the Makefile; install them with
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`,
`go install golang.org/x/vuln/cmd/govulncheck@latest`,
`go install github.com/google/go-licenses@latest`.

## Tests

- Unit tests are table-driven and network-free. The document fixture is
  `testdata/sample.json` (synthetic; never add real content) and is
  loaded through `internal/doc/doctest`. Renderer goldens live in
  `testdata/golden/`; regenerate with `go test ./internal/render -update`
  and review the diff.
- **A fake's error responses are copied from Google's documentation,
  reason strings and all.** The reason is what the code branches on —
  `userRateLimitExceeded` is a 403 that means "slow down", `storageQuotaExceeded`
  a 403 that means "your Drive is full" — so a plausible-looking invention
  in a test double tests the invention. A fake that refuses without a
  reason agrees with a bug by omission: it cannot fail on the branch that
  is missing. Quote the reason from the [Drive error
  guide](https://developers.google.com/workspace/drive/api/guides/handle-errors)
  or the Docs reference, and put the link next to the table.
- Integration tests are tagged and need a login:
  `GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestPreviewSpike -v`
  creates a scratch document and checks the Developer Preview features.
- The live driver drives every tool and every op kind against a new
  scratch document, through the binary over stdio exactly as a client
  would, plus the `gdocs://` resources:

  ```
  make build && go test -tags=live ./internal/livecheck -v -timeout 20m
  ```

  It needs `GDOCS_ENABLE_DESTRUCTIVE=true` for the deletion steps, and
  should be run with `GDOCS_PREVIEW` both on and off before a phase is
  called done. Steps that must be refused assert their refusal, so a
  green run means the guards fired as well as the writes; the transcript
  is scrubbed of ids so it can be pasted. The scratch document is left
  behind on purpose and its URL is the last line.
- **Verifying what the server writes but cannot read back.** Some
  properties never come back through a read of the text: a redefined
  named style, for one, since `read_document with_styles` annotates only
  runs that deviate from the paragraph default, and redefining a style
  moves that default with it. `get_document` now reports the definitions
  each tab's paragraphs carry, which is the direct check on a
  `named_style` op; `export_document format: html` remains the check on
  everything else — Google's own export carries the resolved styling, so
  a redefined `HEADING_2` shows as
  `<h2 style="…color:#1a73e8;font-size:18pt;padding-top:20pt…">`. The
  same export shows the page background and margins on `<body>`, column
  widths and row heights on the table, and `<thead>` for pinned header
  rows, which makes it the quickest end-to-end check of a layout change.
- `go vet -tags=integration ./...` keeps the tagged tests compiling even
  though nothing runs them on a pull request. It is part of `make vet`
  and of CI, along with the `live` and `evals` passes; the parity gate is
  what holds those two lists to the same set.
- **The gates are Go, and tested.** `scripts/gates` holds all nine: the
  coverage floor, the staleness check, the schema diff, the stdio smoke
  test, the identifier scan, the workflow pin check, the error-class
  check, the API-coverage check and the parity check. Eight have tests of
  their own, `go test ./scripts/gates`, each watched to fail before it
  was trusted; the schema diff is a drive of the built binary, so `make
  check` running it is the test. They were shell scripts until
  two of them went wrong in ways bash made easy: a hand-written package
  list that fell behind without a sound, and a staleness rule that
  failed on the release pull request it was written to guard. Each
  derives its lists from the code and fails when it finds too little to
  be looking at anything. No shell is left anywhere in the repository —
  the smoke test and the schema-diff worktree driver were the last two,
  and `make check` runs on the Windows runner, where bash is a
  dependency rather than a given.
- **Every method Google publishes has a verdict.** `make api-coverage`
  holds three things to each other: `testdata/api-methods.json`, which is
  every method of the Docs and Drive APIs as their discovery documents
  publish them; `testdata/api-coverage.tsv`, one line per method saying
  `used` (naming the `internal/gapi` method that implements it) or `out`
  (with the reason); and the client itself, read out of the syntax. A
  method Google adds fails the build until somebody judges it, and a call
  the client makes with no row fails it too.

  `make api-diff` refetches and rewrites the snapshot, reporting NEW,
  GONE and CHANGED with the verb and path — a method that keeps its name
  and moves is a break a list of names would hide. It is the only thing
  here that touches the network and it is deliberately **not** a gate: a
  check that fails when Google is slow is one people learn to rerun until
  it passes. The snapshot it writes is what CI reads, so completeness is
  still checked on every pull request.

  Nobody edits the JSON and nothing generates the TSV. Keeping the verb
  and path in the machine's file is the point: a sibling put them in the
  hand-kept one, where the only check on them was a target CI never ran.
- **Every field Google publishes on a type we model has a verdict.**
  `make api-fields` is the same pair of files one level down:
  `testdata/api-fields.json` is every schema and property the Docs
  discovery document publishes, and `testdata/api-fields.tsv` is one line
  per exception — `out` for a published field this server deliberately
  does not model, `extra` for a field it carries that public discovery
  does not publish (the Developer Preview ones), `alias` for a schema
  modelled under another name (`Break` covers `pageBreak`, `columnBreak`
  and `horizontalRule`), and `local` for a struct that models no
  published schema at all (the Developer Preview comment shapes, and the
  `SuggestedStyle` embeddable whose tags reach the wire through the
  elements that embed it). `local` is the one verdict whose first column
  names a Go struct rather than a schema. The modelled side is read out
  of `internal/gdocs` with `go/ast`, promoting the tags of embedded
  structs, because a field promoted from `Suggested` is on the wire
  exactly as if it had been declared.

  Three directions fail the build: a field Google adds to a type we
  model, a field we carry that nothing publishes, and a struct that
  matches no schema and has no row saying why. The third is what makes
  the set being compared part of the rule, and it is the reason there is
  no floor under the number of schemas matched: there was one, at 80
  against a real 104, and a rename that took a type out of the
  comparison stayed well above it. A row that has outlived the thing it
  describes fails too — an `out` row for a property Google has
  withdrawn, or one the types have since grown.

  The rule the `out` rows are judged against: **if a tool here writes a
  field, the types must carry it**, because a person should not be able
  to set something no read will show them. Twelve `SectionStyle` fields,
  a table's column widths and a row's pinned-header flag were all in that
  state when this gate was written — `layout_document` and `edit_table`
  set them and no read could report them — and the gate is what said so.
  Docs only: the Drive types are inline anonymous structs in
  `internal/gapi/drive.go`, which this rule cannot see, and a gate that
  claimed to cover them would be half met.
- **Before a release, scan the history**: `LEAKCHECK_HISTORY=1 go test
  ./scripts/gates -run TestHistoryCarriesNoIdentifiers` reads every
  blob ever committed, which the ordinary run does not — a file that
  carried an identifier and was cleaned up later still carries it in the
  object store. It takes a few seconds and is skipped without the
  variable.
- **Adding a tool or an op costs three entries, and each one fails loudly
  if you skip it**: a step in `internal/livecheck` (or `TestCoverage`
  fails), a row in `server.toolArgs` (or `TestEveryToolHasArgs` fails),
  and the README's tool table (or the staleness gate fails). The first
  two exist because a guarantee tested through the tools someone
  remembered is a guarantee that quietly shrinks.
- The agent evals score whether a model can do the job through these
  tools. Each task seeds a scratch document through the server, runs
  `claude -p` with only this server's tools, and checks the end state and
  the tool-call trace:

  ```
  make build && go test -tags=evals ./internal/evals -v -timeout 40m
  go test -tags=evals ./internal/evals -v -run TestEvals/replace-suggest
  ```

  Each task is a subtest, so `-run` selects one and a failure names the
  check that failed. It needs a login, the `claude` CLI, and spends API
  usage (about 10-20 cents a task; `EVAL_MODEL=sonnet` is cheaper,
  `EVAL_BUDGET_USD` caps one task). Traces and `report.md` land in
  `$LIVE_OUT/evals`; read the trace when a task fails, because the
  model's final message usually says what it could not see or do.
- `make bench` runs the benchmarks over `doctest.Large`, a generated
  document of about 150 pages (6 400 body blocks, 130 tables, 300
  comments, 200 suggestions, 100 footnotes). The numbers to hold are in
  `docs/architecture.md` §11; a change that makes a read or a write
  scale with document size again should show up there.

## Commits

Commit at each milestone (a phase, a review pass, a live-test fix) with a
message that says what changed and why. Nothing deployer-specific ever
enters the repository: no document ids, account emails, project or client
ids, or content from real documents.

## Releasing

A tag is the whole release process; nothing is published by hand.

1. Put the changes under a `## [N.N.N] - YYYY-MM-DD` heading in
   `CHANGELOG.md`, above the previous release and below `[Unreleased]`.
2. Commit that as `Release N.N.N`.
3. Tag it: `git tag -a vN.N.N -m "vN.N.N — what this release is"`.
4. `make check`. The staleness gate accepts a release commit: entries
   filed under a version heading that has no tag yet count as documented,
   which is what a release commit is. It still refuses a source change
   documented nowhere, and a version heading whose tag already exists.
5. Open the release commit as a pull request, let CI go green on all
   three platforms, and merge it. Direct pushes to `main` are not the
   process, releases included — which is why the gate had to change: CI
   runs on the pull request, before the tag it is preparing can exist.
6. Push the tag once the commit is on `main`. Tags are not covered by the
   branch rules, so this step is a direct push and the release workflow
   takes it from there.

Push tags one at a time: GitHub drops tag events past the third in a
single push, and the release workflow then never runs. `workflow_dispatch`
is there for re-running a release against a tag when that happens.

Pushing a `v*` tag runs `.github/workflows/release.yml`: it builds with
GoReleaser for linux, darwin and windows on amd64 and arm64, and
publishes a GitHub release with the archives and `checksums.txt`. The
release is **not** a draft, so the tag is the decision — a draft nobody
remembers to publish is how releases go missing. `goreleaser release
--snapshot --clean --skip=publish` does the same build locally without
touching GitHub, which is worth running once if the packaging changed.

Actions and tool versions are pinned, not floating: `go-version-file:
go.mod` so CI uses the toolchain the code declares, and explicit versions
for golangci-lint, govulncheck and go-licenses so a green build stays
reproducible. Dependabot proposes the bumps weekly (gomod and
github-actions, `.github/dependabot.yml`); take them through a PR so CI
judges each one.
