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
- Integration tests are tagged and need a login:
  `GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestPreviewSpike -v`
  creates a scratch document and checks the Developer Preview features.
- `scripts/live-drive.py` drives every tool and every op kind over stdio
  against a new scratch document, plus the `gdocs://` resources; read its
  header. It needs `GDOCS_ENABLE_DESTRUCTIVE=true` for the deletion
  steps, and should be run with `GDOCS_PREVIEW` both on and off before a
  phase is called done. Every `isError=True` in its output should be an
  expected refusal (the overwrite guard, suggest mode where the API
  refuses it, an unknown resource); anything else is a regression.
- **Verifying what the server writes but cannot read back.** Some
  properties never come back through a read: a redefined named style, for
  one, since `read_document with_styles` annotates only runs that deviate
  from the paragraph default, and redefining a style moves that default
  with it. `export_document format: html` is the check — Google's own
  export carries the resolved styling, so a redefined `HEADING_2` shows
  as `<h2 style="…color:#1a73e8;font-size:18pt;padding-top:20pt…">`. The
  same export shows the page background and margins on `<body>`, column
  widths and row heights on the table, and `<thead>` for pinned header
  rows, which makes it the quickest end-to-end check of a layout change.
- `go vet -tags=integration ./...` (part of `make vet`) keeps the tagged
  tests compiling even though CI never runs them.
- `scripts/evals/run.py` runs the agent evals: each task seeds a
  scratch document through the server, runs `claude -p` with only this
  server's tools, and scores the end state and the tool-call trace. It
  needs a login, the `claude` CLI, and spends API usage (about 20-40
  cents per task with the default model); `--list`, task names, and
  `--report` are the arguments. Read the traces in `$LIVE_OUT/evals`
  when a task fails: the model's final message usually says what it
  could not see or do.
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
