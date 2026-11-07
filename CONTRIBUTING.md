# Contributing to google-docs-mcp

Thanks for considering a contribution. This is a Go MCP server for
Google Docs, run by people against their own documents. Read this once
before your first pull request; CI enforces the rest.

## Before anything else

**Nothing from a real document may enter this repository** (D-06). Not
document ids, or the links carrying them; not the titles or content of
real documents; not account addresses, organization names, Cloud
project ids, OAuth client ids or secrets. This holds for code, docs,
fixtures, goldens, issues, pull requests, commit and tag messages and
logs. Naming the sibling servers, such as google-drive-mcp, is fine.

Fixtures are synthetic (`testdata/sample.json`). gitleaks scans for
credentials. `make leaks` scans for addresses, account and client ids,
user-content links, Drive ids that do not look invented, and compiled
or large binaries. gitleaks runs in the pre-commit hook, in `make check`
and in CI, and `make leaks` in `make check`.

**Commit under your GitHub no-reply address**, the one at
`users.noreply.github.com` that GitHub's email settings give you, and
credit co-authors with theirs. The history is public and cannot be edited, and a work
address in it names your organization for good. `make leaks` does not
read the identity git records or a `Co-authored-by:` line, so this one
is on you.

## Reporting bugs and requesting features

Open a [GitHub issue](https://github.com/mmedum/google-docs-mcp/issues)
with what
[`docs/troubleshooting.md`](docs/troubleshooting.md#reporting-a-problem)
lists. Never paste a tool result: it is your document's content.

For anything security-sensitive, do not open a public issue. Follow
[`SECURITY.md`](SECURITY.md).

## Development setup

```bash
git clone https://github.com/mmedum/google-docs-mcp
cd google-docs-mcp
make build
pre-commit install
```

Go is the only build prerequisite, at the version in `go.mod`;
`GOTOOLCHAIN=auto`, the default, fetches it. `make check` runs
golangci-lint, govulncheck, go-licenses and gitleaks with `go run` at the
versions the Makefile names, and the `pins` gate holds those to the
versions `.github/workflows/ci.yml` pins, so nothing else needs
installing.
Everything the repository runs on itself is Go, in `scripts/gates`, with
no shell scripts (D-10). GoReleaser builds only `./cmd/...`, so none of
it ships.

`pre-commit install` adds the hooks in `.pre-commit-config.yaml`:
gitleaks on the staged changes through `make gitleaks-staged`, at the
version and with the flags `make gitleaks` uses, `make fmt` and
`go vet ./...`.

## Gates that must pass

```bash
make check
```

`make check` runs the gates CI runs, and the `check` target in the
`Makefile` is the list. `make parity` fails when a command that list
runs and `ci.yml` differ, so a check you add goes in both, in the same
words (D-85). CI runs the tests on
Linux, macOS and Windows, where paths, permission bits and line endings
differ (D-87).

`go run ./scripts/gates` lists the checks, and `go run ./scripts/gates
<name>` runs one. They have tests of their own in
`go test ./scripts/gates`. A check derives its lists from the code and
fails when it finds too little to be looking at anything. Before you
trust a gate you changed, break what it holds and watch it fail.

These are worth knowing before they fail on you:

- **The schema diff** builds the newest release tag HEAD reaches in a
  worktree and compares its tool surface, resource templates and
  instructions with this build's
  (D-77). A removed tool, template, argument or output field, or a newly
  required argument, is a break. A break passes only when a
  `**Breaking:**` entry under `[Unreleased]` names the item, and ships
  only in a new major version. Every other change is listed by the part
  that changed, such as `annotations.destructiveHint`.
- **The coverage floor** holds every package `go list` derives, under
  `-coverpkg=./internal/...`, so `cmd/` is outside it. An exemption
  names its reason (D-81).
- **`make leaks`** fails on the identifiers above unless an allow-list
  entry gives its reason (D-06).
- **`make api-coverage`** holds every method of the Docs and Drive
  discovery documents to a verdict in `testdata/api-coverage.tsv`:
  `used`, naming the `internal/gapi` method, or `out` with a reason. A
  call the client makes with no row fails too (D-09).
- **`make api-fields`** does the same for every field of a Docs type the
  server models, in `testdata/api-fields.tsv`: `out` for a published
  field left unmodeled, `extra` for a field public discovery does not
  publish, `alias` for a schema modeled under another name, and `local`
  for a struct that models no schema. A field a tool writes is never
  `out`, since a person should not be able to set what no read shows.
  The Drive types are anonymous structs in `internal/gapi/drive.go`,
  which this check cannot see. `make api-diff` refetches both snapshots
  and is a release step.
- **`make staleness`** holds the README's tool table, scopes and
  permission rules to the binary, and `docs/configuration.md` to every
  setting the code reads (D-69). It also wants a CHANGELOG entry when the
  binary's source changed since the newest release tag HEAD reaches, the
  one the schema diff builds; a test, a script or a dependency bump needs
  none.
- **`make classes`** holds every error class the code emits to the
  closed vocabulary in `internal/service` (D-60).
- **`make transcript`** refuses a live driver or eval line that does not
  go through the redactor (D-07).
- **`make mcpb`** holds the Claude Desktop bundle's manifest to the
  settings the server reads (D-93, D-94).

## Tests

Unit tests are table-driven and never touch the network (D-80). The
document fixture is `testdata/sample.json`, loaded through
`internal/doc/doctest`.

**A fake's error responses are copied from Google's documentation,
reason strings and all.** The reason is what the code branches on:
`userRateLimitExceeded` is a 403 that means slow down, and
`storageQuotaExceeded` a 403 that means the Drive is full. A fake that
refuses without a reason cannot fail on the branch that is missing.
Quote the reason from the
[Drive error guide](https://developers.google.com/workspace/drive/api/guides/handle-errors)
or the Docs reference, and put the link next to the table.

**Renderer goldens** live in `testdata/golden/`.
`go test ./internal/render -update` rewrites them; reading the diff is
the part that matters. `.gitattributes` pins LF, since they are
compared byte for byte.

`make bench` runs the benchmarks over `doctest.Large`, a generated
document, apart from `make check`. A change that makes a read or a write
scale with document size again shows there (D-65).

## Changing the tool surface

Add to a tool's arguments and fields; do not take away. On a pull
request, `make schema-ack` compares the surface at the merge-base and
the head, annotations included (D-78). If it changed, end one of the
pull request's commits with a footer naming each changed item:

```bash
git commit --allow-empty -m "Acknowledge the tool surface change" \
  -m "SCHEMA-CHANGE: add_comment, edit_document: destructiveHint true"
```

The names are the list before the first colon followed by white space;
the text after it describes the change and names nothing. Name a tool
by its name, a resource template by its URI, and the instructions as
`instructions` or `readOnlyInstructions`. Keep the list on one line: a
wrapped line that starts `word: ` begins a new footer, as Conventional
Commits reads it. Names are separated by commas, and a `Token #value`
footer such as `Closes #12` acknowledges nothing. If it breaks something
a release shipped, name that item in a `BREAKING CHANGE:` footer
instead. A refusal prints the footer that fixes it, to add in an empty
commit, never an amend. `make schema-ack` runs the check locally against
`origin/main`.

## Adding a tool or an op

1. Put the behavior in `internal/service`, with tests against the
   fakes. Tools stay thin.
2. Register it in `internal/tools`, with a flat schema (D-73), a
   `snake_case` `verb_noun` name (D-72) and exact annotations (D-57).
3. A read returns text alone. A write returns text and a structured
   result carrying everything the text does (D-19).
4. Errors are `[class] message`, from `service.Classes` (D-60).
5. Add a row to `toolArgs` in `internal/server/server_test.go`, or
   `TestEveryToolHasArgs` fails. The guarantees tested over every tool
   read that table (D-82).
6. Add it to the README's tool table, and any setting to
   `docs/configuration.md`, or `make staleness` fails.
7. Drive it in `internal/livecheck`, or `TestCoverage` fails on the next
   live run (D-83).
8. Read `make schema-diff`, and add the `SCHEMA-CHANGE:` footer above.

## The live run

Every fake in the unit suite is written from what we believe Google
does, and only a live run tests that belief (D-83). Anything touching
the write path or an API response shape needs one, and the transcript
has to be read.

```bash
make live
GDOCS_ENABLE_DESTRUCTIVE=true make live
make live LIVE_ARGS="-run TestLiveWord"
```

- **Your account.** It builds the binary and drives it over stdio as a
  client would, as the account your profile signed in to. It drives
  every tool, every op kind and the `gdocs://` resources.
- **`LIVE_ARGS`** passes flags to `go test`, such as `-run` to run one
  test of the driver.
- **The delete steps** run only with `GDOCS_ENABLE_DESTRUCTIVE=true`.
  The driver reads the server's settings from the same environment, so
  it runs them exactly when the server registers the delete tools.
- **Named steps.** Every step has a unique name, printed above its
  result, and an expected outcome. A step that must be refused asserts
  the refusal, so a green run means the guards fired as well as the
  writes. A comment at the code cites a step as
  `Checked by the live step "<name>".`
- **Coverage.** `TestCoverage` fails when a registered tool or op kind
  has no step.
- **Reading it.** The transcript goes through `internal/redact`, which
  masks ids, links, addresses and the account's name (D-07). Read it
  before sharing it.

**What it leaves behind.** Every document and file it makes is titled
`google-docs-mcp … (safe to delete)`, and stays in your Drive.
The run's last lines say so, and Drive search `title:"safe to delete"`
finds every run's. With the delete tools on, the delete steps remove
the tabs and the comment they made for themselves.

**Checking what no read shows.** A redefined named style moves the
paragraph default with it, so `read_document` `with_styles` cannot show
it. `get_document` reports each tab's style definitions, and
`export_document` `format: html` shows how Google resolves them. The
same export shows the page background and margins, column widths, row
heights and pinned header rows.

### Integration tests

```bash
GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawStaleRevision -v
```

They call the Docs and Drive APIs directly as the `default` profile's
account, and each records how Google answers something the code depends
on. A comment cites one as `Checked by the integration test TestRaw<Name>.`
It names the test, never a subtest.
Each makes scratch documents titled `… (safe to delete)` and leaves them
behind. `make vet` compiles them, and `make check` runs none.

### Evals

```bash
make build && go test -tags=evals ./internal/evals -v -timeout 40m
go test -tags=evals ./internal/evals -v -run TestEvals/replace-suggest
```

Evals score whether a model can do the job through the tools, which the
driver cannot show (D-84). Each task seeds a scratch document through
the server, runs `claude -p` with only this server's tools, and checks
the end state and the tool-call trace. Each task is a subtest, so `-run`
selects one and a failure names the check that failed.

They need a login and the `claude` command, and spend API usage;
`EVAL_MODEL=sonnet` is cheaper, and `EVAL_BUDGET_USD` caps one task.
Traces and a report land in `$LIVE_OUT/evals`; read the trace when a
task fails. The scratch documents are left behind, titled
`google-docs-mcp eval … (safe to delete)`.

## Branch and commit conventions

- `main` is released code and takes pull requests only, the
  maintainer's and release commits included (D-92). Work on a short
  topic branch.
- Write the subject in the imperative, under 72 characters, saying what
  changes. Look at a recent `git log` for the tone.
- The body says the problem, why this change solves it, and the
  alternatives rejected. A fixed review finding is one line at the end
  of the body. A declined one goes in the pull request, or becomes a
  present-tense comment at the line it protects.
- The maintainer pushes, merges, tags and releases.

## Pull requests

1. Open against `main`, with a summary and a test plan.
2. CI must be green on Linux, macOS and Windows.
3. A maintainer reviews and merges.

Every action is pinned to a full commit SHA, and every tool an action
installs to one exact version; `make pins` holds both (D-86). Dependabot
proposes the bumps weekly, and each goes through a pull request like any
other change.

For maintainers: give `main` a ruleset that requires a pull request and
the CI checks, and forbids force-push and deletion. Do not add a
required status check before the workflow producing it is on `main`: a
check that has never run blocks every pull request, the one adding it
included.

## Changelog

Every user-visible change gets an entry under `## [Unreleased]` in
`CHANGELOG.md`, in the same pull request that makes the change. Leave
out changes only contributors see, unless they must act on them.

- **Sections** are the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  set and nothing else: Added, Changed, Deprecated, Removed, Fixed and
  Security, in that order.
- **One line per change, 25 words or fewer, impact first.** Write for
  someone deciding whether to upgrade. The investigation belongs in the
  commit message.
- **Breaking changes** start with `**Breaking:**`, name the item in
  backticks, and may add one sentence saying what to do. Anything
  needing a new login, a new scope or a config change is breaking.
- **Security fixes** name the CVE or GHSA.

`release.yml` lifts the matching section verbatim into the release
notes, so the entry is the release note (D-90). Versioning follows
[SemVer](https://semver.org/spec/v2.0.0.html), read through the tool
surface: tool names, input and output schemas, annotations, resources
and documented behavior.

## Releasing

Maintainers release as [`docs/release.md`](docs/release.md) says.

## Code style

- Match the surrounding code. Do not reformat what you did not touch.
- Comments explain why: a constraint, an invariant, something Google
  does that the code has to work around. Do not narrate what the code
  does.
- A decision in `docs/architecture.md` is `D-NN`. Cite it by ID, or a
  doc by its path and heading anchor, never by section number. What you
  verified about Google goes in the comment at the line that depends on
  it, with the source link and the live step or integration test that
  checks it.
- Write plainly: lead with the outcome, one idea per sentence. This
  holds for comments, commit messages, the CHANGELOG and tool
  descriptions.
- Logs record the call, never the payload (D-04).

`CLAUDE.md` has the short version of the rules, and
[`docs/architecture.md`](docs/architecture.md) the reasoning, one
decision per D-ID, and the [codemap](docs/architecture.md#codemap).

## Code of conduct

Taking part in this project means agreeing to
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md), the Contributor Covenant
3.0. It says how to report a problem, and what happens then.

## License

By contributing, you agree that your contributions will be licensed
under the project's [Apache 2.0 License](./LICENSE).
