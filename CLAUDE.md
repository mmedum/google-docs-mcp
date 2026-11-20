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

1. **Keep everything internal out of the repository and what it
   publishes**: document ids or URLs, the titles or content of real
   documents, account addresses, organization names, Cloud project ids,
   OAuth client ids and secrets. That covers code, docs, fixtures,
   transcripts, commits, pull requests and logs, because the repository
   is public and a pushed commit cannot be taken back. Naming a sibling
   server, such as google-drive-mcp, is fine. Fixtures are synthetic
   (D-06). `make leaks` and gitleaks hold it.
2. **Write to stdout only in `main`.** Stdout carries only MCP JSON-RPC
   frames, because one stray print breaks the session; logs go to
   stderr through `slog` (D-03). forbidigo, `make smoke` and
   `TestNothingPrintsOutsideTheBoundary` hold it, with `scripts/`
   excluded.
3. **Log the method, tool, outcome and duration, not a document id,
   revision, title, text or query**, at any level, because a person
   should be able to attach a debug log without auditing it (D-04).
   `TestDebugLogsCarryNoDocumentData` holds it.
4. **Give the model targets, not a UTF-16 index.** Targets are exact
   text, `heading_id`, handles or a named range, because every index
   shifts after each change. Index math stays in `internal/plan` and
   `internal/doc` (D-12, D-13, D-14, D-15). `TestUTF16` holds the
   conversion.
5. **Change only what changed, rather than overwriting.** A replace is a
   minimal diff, because overwriting a range deletes every comment and
   suggestion anchored inside it; a replace that changes the paragraph
   structure says it was not minimal (D-27). A direct edit that would delete
   a comment anchor, a suggestion, an object or a footnote reference is
   refused unless forced (D-28). `TestGuardBlocksDirect` holds it.
6. **Write in the mode asked for, or refuse rather than substitute
   another**, because a mode the person did not choose is a change they
   did not approve (D-29). `TestSuggestModeRefusesUnsupportedRequests`
   holds it.
7. **Guard every write with the revision it was planned on** (D-30),
   and report one that may have landed as `[ambiguous_outcome]` rather
   than repeating it (D-61). `TestDryRunAndConflictRetry` and
   `TestWriteRetriesOnlyOn429` hold them.
8. **Wait briefly for unsaved editor work in a Markdown file, then
   refuse with `[conflict]` rather than writing past it**, `force`
   included (D-44).
   `TestMarkdownFileWaitsForTheEditor` holds it.
9. **Register a delete tool only with `GDOCS_ENABLE_DESTRUCTIVE`**,
   because the control has to be the server's (D-54), and take a delete
   only when the call repeats its target (D-56).
   `TestDeletesNeedTheTargetRepeated` holds it.
10. **Leave approval to the client and the server's settings to the
    operator**: the server asks the person nothing, and no tool takes
    or writes a setting (D-52, D-59). `destructiveHint` is on every
    write but those that only add (D-57), and no tool carries
    `requiresUserInteraction` (D-58). forbidigo and
    `TestNoToolTakesAServerSetting` hold it.
11. **Extend `internal/gdocs` rather than importing
    `google.golang.org/api` or another project's code**, because the
    generated module brings gRPC, OpenTelemetry and the cloud auth stack
    (D-08). depguard refuses it. Give a method or a field a verdict in
    `testdata/api-coverage.tsv` or `testdata/api-fields.tsv` before
    calling or modeling it (D-09).
12. **Keep every released tool, argument, output field and resource
    template rather than renaming or dropping one**, because a client is
    written against the surface. `make schema-diff` fails on a break no
    `**Breaking:**` entry names; a break ships in a new major (D-77).
13. **Emit every error as `[class] message`.** `make classes` holds the
    vocabulary closed (D-60).
14. **Print from the live driver and the evals only through the
    redactor**, because transcripts are pasted into issues and commit
    messages (D-07). `make transcript` holds it.
15. **Write everything the repository runs on itself in Go, checks in
    `scripts/gates`, not as a shell script**, because Go runs the same on
    every platform CI uses (D-10). A script the bundle ships is not one.
    `make parity` holds `make check` and CI to one list (D-85).
16. **Commit on a branch, not `main`, and leave pushing to the owner**,
    who pushes, tags and merges (D-92).
17. **Back every belief about Google with a primary source**: its
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
  Findings go in those places, not into `docs/`.
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
- **Supersede or deprecate a decision rather than deleting it.** Take
  the next ID from the file's `Next ID:` line rather than reusing one.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the CHANGELOG section verbatim into the
release notes, so follow the rules in `CONTRIBUTING.md` exactly.
