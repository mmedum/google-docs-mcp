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
- `scripts/live-drive.py` drives every Phase 1 tool over stdio against a
  new scratch document; read its header.

## Commits

Commit at each milestone (a phase, a review pass, a live-test fix) with a
message that says what changed and why. Nothing deployer-specific ever
enters the repository: no document ids, account emails, project or client
ids, or content from real documents.
