# CLAUDE.md — google-docs-mcp

A Go MCP server for Google Docs over stdio: one binary per person, their
own OAuth client, nothing hosted (D-01). Its scope is one document and
everything inside it, its history included (D-02). Why the code is the
way it is lives in `docs/architecture.md`, one decision per `D-NN`; read
it before changing the tool surface, the addressing or the write path.

## Commands

- `make check` runs every gate CI runs; its `check` target is the list.
  `pre-commit install`, once per clone, adds gitleaks, `make fmt` and
  `go vet`.
- `make live` drives the built binary against the signed-in account;
  `LIVE_ARGS` passes `go test` flags, such as `-run TestLiveWord`, and
  the delete steps need `GDOCS_ENABLE_DESTRUCTIVE=true`. Read the
  transcript. `CONTRIBUTING.md` covers it and the evals.
- `GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run <Test> -v`
  runs one integration test against Google.
- `make api-diff` and `make schema-refetch` are release steps in
  `docs/release.md`.
- `go run ./scripts/gates` lists the gates; add a name to run one.

## Hard rules

1. **Never commit anything internal**: document ids or URLs, the titles
   or content of real documents, account addresses, organization names,
   Cloud project ids, OAuth client ids or secrets. Not in code, docs,
   fixtures, transcripts, commits, pull requests or logs. Naming a
   sibling server, such as google-drive-mcp, is fine. Fixtures are
   synthetic (D-06). `make leaks` and gitleaks hold it.
2. **Never write to stdout outside `main`.** Stdout carries only MCP
   JSON-RPC frames; logs go to stderr through `slog` (D-03). forbidigo,
   `make smoke` and `TestNothingPrintsOutsideTheBoundary` hold it, with
   `scripts/` excluded.
3. **Never log a document id, revision, title, text or query**, at any
   level (D-04). `TestDebugLogsCarryNoDocumentData` holds it.
4. **Never show the model a UTF-16 index.** Targets are exact text,
   `heading_id`, handles or a named range, and index math stays in
   `internal/plan` and `internal/doc` (D-12, D-13, D-14, D-15).
   `TestUTF16` holds the conversion.
5. **Never overwrite.** A replace is a minimal diff (D-27). A direct
   edit that would delete a comment anchor, a suggestion, an object or a
   footnote reference is refused unless forced (D-28).
   `TestGuardBlocksDirect` holds it.
6. **Never substitute one write mode for another** (D-29).
   `TestSuggestModeRefusesUnsupportedRequests` holds it.
7. **Never send a write unguarded by the revision it was planned on**
   (D-30), and never repeat one that may have landed: it is
   `[ambiguous_outcome]` (D-61). `TestDryRunAndConflictRetry` and
   `TestWriteRetriesOnlyOn429` hold them.
8. **Never write past unsaved editor work** in a Markdown file, `force`
   included (D-44). `TestMarkdownFileWaitsForTheEditor` holds it.
9. **Never register a delete tool without `GDOCS_ENABLE_DESTRUCTIVE`**
   (D-54), and never take a delete whose target is not repeated (D-56).
   `TestDeletesNeedTheTargetRepeated` holds it.
10. **Never ask the person anything, or let a tool take or write the
    server's settings** (D-52, D-59). `destructiveHint` is on every
    write but those that only add (D-57), and no tool carries
    `requiresUserInteraction` (D-58). forbidigo and
    `TestNoToolTakesAServerSetting` hold it.
11. **Never import `google.golang.org/api`** or another project's code;
    extend `internal/gdocs` (D-08). depguard refuses it. Never call a
    method or model a field without a verdict in
    `testdata/api-coverage.tsv` or `testdata/api-fields.tsv` (D-09).
12. **Never rename or drop a released tool, argument, output field or
    resource template.** `make schema-diff` fails on a break no
    `**Breaking:**` entry names; a break ships in a new major (D-77).
13. **Never emit an error outside `[class] message`.** `make classes`
    holds the vocabulary closed (D-60).
14. **Never let the live driver or the evals print except through the
    redactor** (D-07). `make transcript` holds it.
15. **Never write a shell script**: every check is Go in `scripts/gates`
    (D-10). `make parity` holds `make check` and CI to one list (D-85).
16. **Never commit to `main` or push**; the owner pushes, tags and
    merges (D-92).
17. **Never adopt a belief about Google without a primary source**: its
    documentation, the discovery document or a live probe (D-80, D-83).
    Record a verified Google behavior in the comment at the line that
    depends on it, with the source link and the live step or
    integration test that checks it.

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`,
  which needs a commit footer naming each changed item of the tool
  surface (`CONTRIBUTING.md`).
- New behavior has tests, and the schema diff has been read.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or
  becomes a present-tense constraint comment at the line it protects.
  Never write findings into `docs/`.
- Anything touching the write path or an API response shape has had a
  `make live` run whose transcript was read (D-83).

## Traps

- **Green gates are not done.** Every fake is written from belief; only
  `make live` and the integration tests test it (D-80, D-83).
- **`dry_run` is taken by the writes whose input carries it, not every
  write** (D-35). Each returns before sending, with no guard in the API
  client, so a write that takes it needs its own "sends nothing" test.
- **`expect_revision` is checked before a write**, so only an edit
  between the read and the write reaches Google's conflict answer
  (D-30). `TestRawStaleRevision` checks that answer.
- **A handle is valid for the revision it came with** (D-14), and a
  write always fetches again past the five-second cache (D-64).
- **`make check` runs no integration, live or eval test**; `make vet`
  compiles them. `cmd/` is outside the coverage floor (D-81).
- **A linter settings block that fails to load leaves its defaults on**,
  still firing. Check the message text of forbidigo and depguard.
- **Never delete a decision or reuse an ID.** Supersede or deprecate it,
  and take the next ID from the file's `Next ID:` line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the CHANGELOG section verbatim into the
release notes, so the rules in `CONTRIBUTING.md` are not optional.
