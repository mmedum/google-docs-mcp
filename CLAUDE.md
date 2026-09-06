# CLAUDE.md — google-docs-mcp project instructions

Project-specific rules for Claude Code in this repository. The user's
global instructions still apply; this file adds to them.

## Mission

A production-grade Go MCP server for Google Docs, distributed to other
people. The design, its evidence log, the decided constraints and the
phase plan live in `docs/architecture.md`. Read it before changing the
tool surface, the addressing model, or the write path.

## Hard rules

1. **Nothing internal, ever.** No organisation names, document ids or
   URLs, account emails, Cloud project ids, OAuth client ids or secrets,
   and no content from real documents anywhere in the repository, in
   fixtures, in commit messages, or in logs. Fixtures are synthetic.
2. **Stdout carries only MCP JSON-RPC frames.** Logs use `slog` to
   stderr. Never `fmt.Println` on the server path.
3. **The model never sees UTF-16 indices.** Targets are exact text,
   `heading_id`, or handles. Index math lives in `internal/doc` and (later)
   `internal/plan`.
4. **Never overwrite.** Replacements are minimal diffs; direct edits
   refuse to delete ranges holding comment anchors, suggestions, images
   or footnotes unless forced; `suggest` and `comment` modes delete
   nothing. The person chooses the write mode; never substitute one
   silently.
5. **Destructive tools are unregistered** unless
   `GDOCS_ENABLE_DESTRUCTIVE=true`.
6. **Own wire types, raw REST.** Do not import
   `google.golang.org/api`; it drags in gRPC and telemetry. Extend
   `internal/gdocs` instead.
7. **No auto-commit, no auto-push.**
8. **Verify conventions against primary sources** before adopting them;
   record the verdict in the evidence log in `docs/architecture.md`.

## Where things go

- `cmd/google-docs-mcp/` — subcommands and server wiring.
- `internal/config/` env + bound flags; `internal/credentials/` keyring →
  file → env; `internal/userconfig/` non-secret profile file;
  `internal/auth/` loopback OAuth.
- `internal/gapi/` raw REST client; `internal/gdocs/` wire types.
- `internal/doc/` model, handles, sections; `internal/render/` markdown,
  text, outline; `internal/service/` orchestration and scope resolution;
  `internal/tools/` MCP tools; `internal/server/` SDK wiring and schema dump.
- `internal/markdown/` the fragment parser; `internal/plan/` write
  planning and index math; `internal/version/` the build stamp.
- `internal/redact/` the transcript redactor, shared by both drivers and
  carrying no build tag so its tests run in every `make check`. Anything
  that writes an artifact a person may paste goes through it.
- **The dev tooling lives in `scripts/`, as Go, and there is no shell.**
  `scripts/gates/` is one command with a registry: coverage, staleness,
  pins, leaks, classes, schema-diff, smoke and parity. `internal/` is the
  server's own implementation, and a thing that only ever runs on a
  maintainer's machine does not belong there. No shell either — a shell
  script is held to no gofmt, vet, lint or test, `make check` runs on the
  Windows runner where bash is a dependency rather than a given, and a
  script that parses JSON with `sed` is how a quote ends up inside a
  string.
- `internal/livecheck/` the live driver (build tag `live`) plus the
  untagged gates over both drivers; `internal/evals/` the agent evals
  (build tag `evals`); `internal/doc/doctest/` shared fixtures.
- `testdata/sample.json` synthetic fixture; `testdata/golden/` renderer goldens.

## Definition of done

`make check` (gofmt; four vet passes — untagged plus `integration`,
`live` and `evals`; golangci-lint;
race tests with the 80% floor; govulncheck; a licence check; a schema
diff against the last tag; the stdio smoke test; and the staleness check
of README/docs/CHANGELOG against the code) plus tests for new behaviour, `/simplify` and
`/code-review high` on the changed files with findings resolved or
explained, and a look at `--dump-schemas` for breaking changes. Commit
at each milestone with a message that says what and why. Phases end
with a tagged release and wait for an explicit "go".
