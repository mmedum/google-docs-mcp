# Contributing

Thanks for helping. A few rules keep this project trustworthy for the
people who run it against their own documents.

## Ground rules

- **Nothing deployer-specific enters the repository.** No document ids or
  URLs, account emails, Cloud project ids, OAuth client ids or secrets,
  and no content from real documents. Test fixtures are synthetic
  (`testdata/sample.json`). gitleaks runs in pre-commit and CI.
- **Stdout is for JSON-RPC.** Log with `slog` to stderr only.
- **The model never sees indices.** New tools address content by text,
  heading id or handle; index math stays in `internal/doc` and
  `internal/plan`.
- **Errors are tool results**, formatted `[class] actionable message`.
- **Schemas stay boring**: flat structs, `omitempty` for optional fields,
  no unions, no dots in tool names.

## Definition of done

```
make check
```

runs gofmt, go vet, golangci-lint, the tests with the race detector and an
80% coverage floor on core packages, govulncheck, and the stdio smoke
test. Also:

- Add or update tests (golden files with `go test ./internal/render -update`).
- Update `README.md`, `docs/`, and `CHANGELOG.md` under `[Unreleased]`
  when behaviour or the tool surface changes.
- Run `./google-docs-mcp --dump-schemas` and check the diff; a removed
  tool or field, or a new required field, is a breaking change.

## Layout

See `docs/architecture.md` §5. In short: `cmd/` is the entrypoint,
`internal/gapi` talks to Google, `internal/doc` is the model,
`internal/render` produces text, `internal/service` orchestrates,
`internal/tools` registers MCP tools.
