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
- `go vet -tags=integration ./...` (part of `make vet`) keeps the tagged
  tests compiling even though CI never runs them.
- **The gates are Go, and tested.** `internal/devcheck` holds the
  coverage floor, the staleness check, the tool-name and schema
  comparisons and the workflow pin check, each with tests of its own —
  `go test ./internal/devcheck`. They were shell scripts until two of
  them went wrong in ways bash made easy: a hand-written package list
  that fell behind without a sound, and a staleness rule that failed on
  the release pull request it was written to guard. Each derives its
  lists from the code and fails when it finds too little to be looking
  at anything. Only the stdio smoke test and the schema-diff worktree
  driver are still shell, because both are process plumbing rather than
  logic.
- **Before a release, scan the history**: `LEAKCHECK_HISTORY=1 go test
  ./internal/leakcheck -run TestHistoryCarriesNoIdentifiers` reads every
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
