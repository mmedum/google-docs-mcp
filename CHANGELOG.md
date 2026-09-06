# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

### Changed
- The dev tooling is Go under `scripts/gates`, and there is no shell.
  `internal/devcheck` moved out of the server's own tree, because
  something that only ever runs on a maintainer's machine does not belong
  in `internal/`; `scripts/schema-diff.sh` and `scripts/stdio-smoke.sh`
  are Go commands. A shell script is held to no gofmt, vet, lint or test,
  `make check` runs on the Windows runner where bash is a dependency
  rather than a given, and a script that parses JSON with `sed` is how a
  quote ends up inside a string.
- `leaks`, `pins` and `classes` are named targets rather than tests that
  happened to run. They always ran, inside `go test ./...`, so nothing
  new is caught — but the target list is what a person reads to find out
  what is covered, and a check running invisibly is one nobody can audit
  without grepping for it.
- **A parity gate.** `make check` and CI must run the same set, and they
  did not: `make vet` ran four passes and CI ran one, so fourteen
  build-tagged files compiled only on a maintainer's machine. The two
  lists live in different files and you are only ever editing one of
  them, which is why this is a gate and not a habit. It compares by
  command rather than by target name, and separately requires every
  tagged vet pass to appear in both. Watched to fail in both directions.
- §17 has no open decision left: the parity gate closes the one that
  stood there. The staleness gate caught the status line still claiming
  it, which is what that half of the check is for — and then caught a bug
  in itself, because "No design decision is open" contains "decision is
  open", so a denial read as a claim. Both are covered now.
- `scripts/gates` is one registry: the usage text, the dispatch and the
  parity gate all read the same list, so they cannot drift from each
  other.

### Changed
- The README carries badges — CI, latest release, Go reference, licence —
  and no longer states a version in prose. The status line said v0.5.0
  five releases after v0.5.0, and the first fix was a gate to keep the
  copy correct; the better one was to delete the copy. A release badge
  shows the version, updates itself, and cannot be wrong.
- The staleness gate checks the version `docs/architecture.md` claims at
  the top, against the released tag or the changelog's newest heading.
  That document is kept because its claim is not a copy of anything: it
  says which phases are done and whether any design decision is open, and
  both halves were false on the day v1.0.0 shipped.
- Documentation corrections the gate could not see: `CLAUDE.md`'s map of
  where things go named 13 of 20 packages, missing `internal/redact` and
  every gate package; the definition of done described a `make check`
  without its licence check or schema diff and with one vet pass where
  there are four; a 0.9.5 entry named a file that stopped existing inside
  0.9.5; and §16's live-driver step counts predated the run before
  v1.0.0.
- The README explains logging in over SSH — where the callback port comes
  from, that it is percent-encoded in the printed URL, and how to forward
  it — and links `SECURITY.md` and `CONTRIBUTING.md`, which existed and
  were unreachable from the front page.
- `login --no-browser` says "(not opening a browser: --no-browser)"
  rather than "(could not open a browser automatically: --no-browser)".
  It is the documented path for logging in over SSH, so it should not
  report a flag the person just passed as a failure.
- §17 records an open decision: `make check` runs `go vet` four times and
  CI runs it once, so 14 build-tagged files compile only on a
  maintainer's machine.

## [1.0.0] - 2026-09-06

### Added
- A version promise, in the README. From here a tool removed or renamed,
  an argument that becomes required, or a change to what a tool returns
  is a major version; new tools and new optional arguments are minor.
  The schema diff in CI enforces it on every pull request.
- Two exclusions from that promise, named so they are decisions rather
  than surprises: the Developer Preview features, which are built on an
  API Google may change or withdraw while it is in preview, and the
  exact prose of a tool's text output, which is written for a model to
  read and will be reworded when a model reads it badly. What a tool
  returns as `structuredContent` is covered; the sentence around it is
  not.

### Changed
- 1.0.0 is a promise about compatibility, not new behaviour: no code
  changed from 0.9.5. The §16 gates that had guarded this version — use
  in anger, and an eval round with another client — are retired in the
  design document with the reasons, rather than left to lapse.

## [0.9.5] - 2026-09-06

### Changed
- The leak gate scans files that are not committed yet. It read the
  index alone, so a brand-new file was invisible to it until someone
  staged it — `make check` went green on a working tree carrying an
  address, and the check that says nothing identifying is committed had
  never looked at the thing about to be. Proved by planting one, which
  passed. It also fails rather than skips when `git ls-files` fails: a
  check that skips when it cannot read its input reports the same green
  as one that read everything.
- Three gates hold what used to be habit. A driver may not call
  `fmt.Print*`, and may not log a value that came back from a tool
  without passing it through the redactor — the sources of tool text are
  derived from the SDK call rather than listed, after a listed version
  turned out to be inert over the whole eval harness. Nothing in the
  command writes to a stream except through one boundary, which is what
  would have caught the three unmasked prints above without anyone
  finding them by hand. And the places a person is written are counted
  from the type declarations on both sides — a version that typed the
  field names said `Email` where the Drive type says `EmailAddress`, so
  it could not see the one function that renders an address.
- The coverage floor derives its own zero-statement exemptions from
  `go list`. Two entries had been added and removed by hand as files
  moved between packages; a package with no non-test Go files cannot be
  below a floor, and now says so itself.
- Every workflow sets `defaults: run: shell: bash`, and a gate fails one
  that does not. The Windows runner defaults to PowerShell, which read
  `-coverprofile=cov.out` as a file called `cov`; the fix had been three
  `shell: bash` lines on the steps that existed at the time, which is a
  rule nothing makes the next step follow. Set at workflow level because
  the runner hands every `run` block to a shell — so this is not a
  property of steps that look shell-ish, nor of jobs that look
  matrix-ish. Note an explicit bash is not the implicit default: GitHub
  runs it as `bash --noprofile --norc -eo pipefail`, so a command
  failing mid-pipeline now fails the step on Linux and macOS too.
- goreleaser is pinned to v2.18.1, up from v2.18.0, for its dependency
  security bump.
- The gitleaks the CI scan installs is named in the workflow as
  `GITLEAKS_VERSION`, and pre-commit installs the same version, with a
  test that fails if the two drift. `versionKeys` in the pins check had
  been naming `gitleaks-version`, which is not an input that action has
  ever had, so the one wrapper-installed tool the check did not cover
  was the one it appeared to cover.
### Security
- Six more places a real person or document reached an artifact people
  paste, found by reviewing the fix that closed the first ones. The live
  driver's cleanup logged the scratch document's URL as the last line of
  every run. `diff_revisions` renders `revision A → B` and only the
  first id had a rule, so the second survived. The startup log and
  `login` both printed an error carrying the client-secret path, six
  lines from where that was fixed for `doctor`, and `login` printed the
  full account address. The eval harness wrote a JSON artifact holding
  the document id, every tool argument and the full text of every tool
  result, and printed 400 raw characters of tool output on three
  failure paths — it had no redaction at all while the driver beside it
  redacted every line.
- Two more prints of a real document: the preview spike printed the
  scratch document's URL, and a live comment step printed a real comment
  id. Both were outside the two packages the first version of the print
  gate looked at.
- The redactor is `internal/redact`, so both drivers use the same rules
  and its tests run in every `make check` rather than only under a build
  tag. `doctor` also masks an address arriving inside an error nothing
  here formatted, which is how Google's 403 names an account.
- `doctor` and `status` no longer print the signed-in account's address,
  the title of the document they check, or its revision id, and a Google
  OAuth client id is removed from the client-secret path. The README
  asks people to paste `doctor` output into a bug report, so all of that
  was going into public issues. The account line keeps the domain and
  drops the local part, because the question it answers is "am I signed
  in as the right account?" and the domain is what answers it. The
  document check reports counts rather than values, so there is no value
  path for a later edit to widen. Nobody chose to print a client id: the
  Cloud console names the file it hands you after the client, so
  printing where the secret lives printed the id with it. Masking is
  applied to every line the command prints rather than to the one that
  formats a path: the path reached the output again inside errors other
  packages had formatted it into. The startup log line recording the
  resolved account is masked for the same reason. `doctor` still names
  the account the token actually belongs to, masked — that check is the
  only one that catches a stale profile or a `GDOCS_REFRESH_TOKEN`
  override, and dropping it answered the question with the unchecked
  half.
- The live driver's transcript redactor now covers people as well as
  documents. Ids, URLs and revisions have a shape and were already
  caught; a person's name has none, so names are caught by position —
  a closed, short set, because this project's renderers wrote every one
  of them — and an address is caught both ways. The redactor is
  `internal/redact`, carrying no build tag, so its tests run in every
  `make check` instead of only when someone runs a driver with
  credentials.
- `docs/security.md` said logs carry no document data, which was true and
  narrower than it read: `doctor` output and the driver transcript are
  different surfaces, and both are pasted into issues. It now covers all
  three.
- The agent-eval transcript no longer prints the document's URL. It is
  the other artifact people paste, and it had no redactor at all.
- A coverage-floor exemption naming a package `go list ./internal/...`
  does not return now fails the build. Such an entry reads as a
  considered decision and exempts nothing; two were stale, one of them
  because a build tag was removed and its stated reason quietly stopped
  being true.

## [0.9.4] - 2026-09-05

### Changed
- CI no longer cancels a superseded run **on `main`**. Cancelling one on
  a branch costs nothing, but cancelling on the default branch leaves a
  merged commit with no verdict, and whoever bisects later finds a green
  history with a hole in it.
- Two gates are Go rather than bash, and have tests of their own. The
  coverage floor and the staleness check were shell scripts; both had
  hand-written lists that fell behind in silence (the packages under the
  floor, the settings expected in the configuration document), and the
  staleness rule once failed on the release pull request it existed to
  guard. They are `devcheck coverage` and `devcheck staleness` now, they
  derive both lists from the code, they assert a floor on how much they
  read, and the coverage floor runs on every platform in CI instead of
  only on Linux. Turning it on for Windows immediately found two things
  nobody could have seen while it ran on Linux alone: the workflow's
  default shell there is PowerShell, which turned `-coverprofile=cov.out`
  into a file called `cov`, so the profile was never where the workflow
  said it was; and `internal/userconfig` sat at 78.9% on Windows against
  93% elsewhere, because the test for "there is nowhere to put a config"
  skipped there instead of clearing `%AppData%`, which is the same
  experiment. The test job runs under bash on all three runners now.
- A delete now has to name what it deletes twice. `delete_tab` takes
  `confirm_tab` and `delete_comment` takes `confirm_comment_id`, each
  repeating the target exactly, and the call is refused otherwise. Both
  tools were already unregistered unless `GDOCS_ENABLE_DESTRUCTIVE=true`;
  a registration gate is a deployer's decision made once, and this is the
  one a model has to make each time. Retyping an id is a different act
  from setting a boolean, which is as easy to supply as to omit. The
  field is optional in the schema and enforced by the server, because a
  required field would break every existing caller while a refusal cannot
  be skipped by a client in an auto-approve mode.
- `make check` runs `licenses` and `schema-diff` too. Both were available
  and neither was in the gate a person actually runs — the licence check
  matters for a binary other people install, and the schema diff only
  fails on breaking changes, so it costs nothing between releases. The
  whole set now takes about fifteen seconds.
- CI cancels a superseded run on the same ref, and every job in every
  workflow has a `timeout-minutes`. The default is six hours, which is
  not a bound.

## [0.9.3] - 2026-09-05

### Added
- The live driver sweeps every tool that accepts `dry_run` and checks the
  one thing a test double cannot: that the document's revision is
  unchanged afterwards. A fake accepts the batch whether or not the code
  meant to send it, so "nothing was sent" is only establishable against
  the real document. It also holds each rendering to its promise — it
  must say it was a dry run and name the operations it would perform —
  after a sibling server found three dry runs describing a request body
  beside a null field. The sweep fails when a tool gains `dry_run`
  without joining it, which it did on its first run.

### Fixed
- `[ambiguous]` meant two opposite things. A target matching several
  passages and a write whose outcome is unknown both came out as
  `[ambiguous]`, and they ask the caller to do opposite things — choose
  between candidates, or go and look at the document. A write with an
  unknown outcome is `[ambiguous_outcome]` now.

### Added
- The error vocabulary is one list that a test holds. It was documented
  as ten classes while the code emitted fifteen, four of them
  (`unknown`, `stale`, `unavailable`, `unsupported`) named in no
  document at all — the vocabulary lived in comments in two packages, so
  nothing could contradict it. `service.Classes` now carries every class
  with what it asks the reader to do next, and
  `TestClassVocabularyIsClosed` scans every `Errorf` in `internal/` and
  fails on a class that is not listed, a listed class nothing emits, or
  a scan that read too few files to be looking at anything.

## [0.9.2] - 2026-09-05

### Fixed
- Debug logs carried part of a document's identity after all. The
  security page promised "no document data at any level" in one
  paragraph and admitted "ids (truncated), revisions" in a table row two
  screens later, and the code did the second: six characters of the
  document id and the whole revision id on every fetch, and an id on the
  revision-conflict path. The bug form tells people a debug log is safe
  to attach, so the promise is the one that had to become true. Ids are
  gone from every line, the request path logs as `/v1/documents/…/x`,
  and `ShortID` is documented as being for a filename a person has to
  recognise, never for a log.
- The test that guaranteed this could not see two of the three leaks: it
  read the server's logger while the service wrote to its own, and it
  searched for the whole id rather than the prefix that was actually
  there. It now shares one logger, drives the conflict path, and looks
  for the id, its first six characters, and the revision id.

### Added
- A gate on the pins themselves. Two releases have been broken by a pin
  that was not one: `v0.8.0` published nothing because a SHA on
  `cosign-installer` pins the action and not the cosign it installs, and
  `goreleaser-action` was pinned by SHA while being asked for `~> v2`.
  A comment beside the value did not hold either of them shut, so
  `TestWorkflowsPinExactly` now fails on an action that is not a full
  commit SHA and on a tool version that is a range, a bare major or
  `latest` — checked against all four loose spellings, including the
  `~> v2.18.0` that survived a review in a sibling repository. It also
  fails when it finds no workflows or no versions, because a checker
  that reads nothing passes for the wrong reason.

### Changed
- The error fixture sends Google's two spellings of a reason —
  camelCase in the legacy `errors[]`, UPPER_SNAKE in the `ErrorInfo`
  detail — because a fake that sends one spelling in both places cannot
  catch a parser that prefers the wrong envelope. A sibling server had
  that bug live with a green suite. One assertion here was pinning the
  parser's preference rather than the contract, and now checks that the
  reason arrives in either spelling.
- docs/security.md says what the code does: the destructive-tool
  annotations are advisory and unregistration is the control, exports
  are no longer "a later phase", and the table gains the identifier
  scan, the credential-host allowlist and the OAuth timeout.

## [0.9.1] - 2026-09-05

### Added
- The error path is tested end to end. Google's documented error bodies
  now go through the whole stack — classification, wording, tool
  rendering — and the test asserts the sentence a model actually reads,
  not the class an internal function returned. Three of its cases fail
  against the code as it stood this morning.
- The identifier scan can read the whole history
  (`LEAKCHECK_HISTORY=1`), not just the files as they stand. A tree scan
  cannot see an identifier that was committed and edited out later,
  which is exactly the accident this repository has already had once.
  First run: 600 text blobs, nothing found.
- A `conflict` eval task: the model is handed a stale revision, and
  scored on whether it passes the guard, reports the refusal, and leaves
  the document alone rather than writing again without the guard. It is
  the one refusal the design exists for that a test can produce on
  demand.

## [0.9.0] - 2026-09-05

### Added
- The identifier scan hard rule 1 always needed: `internal/leakcheck` is
  a test over every tracked file that fails on an address at a domain
  somebody could own, a 21-digit account id, an OAuth client id, a
  user-content URL with an id in its path, and a Drive id that does not
  look invented. gitleaks cannot do this — none of those are secrets, so
  every rule passes them — and the rule had been enforced by remembering.
  Each rule is an allow-list: a deny-list naming the domain to watch for
  would itself be the disclosure.
- The live driver's coverage rule is a check. "Every registered tool is
  driven by a step, every op kind appears as an `op`" had been a comment
  since the Python driver, and it went unenforced through the port —
  which is how `style_columns`, `style_rows` and the named-range ops once
  spent months never running. `TestCoverage` reads the package's own
  source and fails when a tool or op has no step.
- Three eval tasks that score what a model does with a refusal
  (`not-found`, `preview-off-suggest`, `read-only`). Every other task
  scores a success, so the wording of an error was the one model-facing
  surface with no evidence behind it. Tasks can now set the server's
  environment, which is what puts the model in front of a refusal.

### Changed
- The debug-logging guarantee now drives every registered tool instead of
  two, and a new table of per-tool arguments is asserted complete, so a
  tool added tomorrow cannot quietly fall outside it.
- The coverage floor derives its package list from `go list ./internal/...`
  with a written-down exemption list. A package added under `internal/`
  used to be under no floor at all until someone remembered to edit the
  script. `internal/userconfig` was the package this caught: 75.4%, now
  93.0%, with its error paths tested.

### Fixed
- OAuth token refresh had no timeout. The context carried no HTTP client,
  so the oauth2 library used `http.DefaultClient`, which has none: a
  token endpoint that accepts the connection and never answers would hang
  the first tool call for as long as the process ran. The per-request
  timeout on the API client does not cover a refresh, because the refresh
  happens inside the token source. `GDOCS_HTTP_TIMEOUT` now bounds it,
  and the code exchange during `login` too.
- The release pinned `goreleaser-action` by commit SHA and then asked it
  for `~> v2`, so the tool that decides what the artifacts are floated
  within a major version. Pinned exactly, the same fix as
  `cosign-release` and `syft-version`: a SHA pins the action, never what
  the action installs.
- A throttled call was reported to the model as a permission error and
  never retried. Drive answers its per-user, per-project and sharing
  rate limits with **403**, not 429, and the reason string is the only
  thing that distinguishes them from "you may not" — which the mapping
  read after the status, so `userRateLimitExceeded` came out as
  `[forbidden]`, a class that tells a model to go looking for
  permissions to change. Those reasons now classify as `[rate_limited]`
  and back off on reads exactly like a 429; a write is still never
  repeated on them, since only 429 and 503 prove nothing was applied.
  The daily project quota classifies the same way but is not retried,
  because backing off does not free it. Both spellings of a reason are
  recognised — Drive's camelCase `userRateLimitExceeded` and the
  UPPER_SNAKE form a `google.rpc.ErrorInfo` detail would carry.
- A comment could be posted twice. The rule that a write is repeated only
  on 429 or 503 — the answers that prove nothing was applied — was written
  for document batches and tested through them, but the check named that
  one request kind, so Drive writes (adding a comment or a reply, editing
  one, resolving a thread) fell through it and were retried on any 5xx.
  A 500 after Drive had already created the comment left two. Drive
  writes now follow the same rule.
- Google's error reason now reaches the message. It was parsed, stored
  and dropped, so `storageQuotaExceeded` ("your Drive is full") and
  `downloadRestrictedForRevision` ("this revision cannot be downloaded")
  both arrived as a bare `[forbidden]` with nothing to act on.
- The live driver and the agent evals ran for the first time since they
  were ported to Go, and the driver held three faults of its own: it
  scrubbed document ids out of a result before parsing it, so the
  rich-link chip step sent Google a redacted URL and the chip was
  refused; it named an object with `object_id`, a key the tool's schema
  does not have; and it deleted the tab tree before the resource reads
  that address the tab nested inside it. Scrubbing now happens where the
  transcript is written rather than where a step reads a result, and the
  tab tree is deleted in a cleanup, which runs after the reads and also
  runs when a step in between gives up. The replace step swaps in a
  different image, cropped, so a replace that quietly did nothing can no
  longer pass. Both harnesses are green — 91 steps with the preview on
  and 88 with it off, every failure an intended refusal, and 13 of 13
  eval tasks.

## [0.8.1] - 2026-09-05

### Fixed
- Releases could not publish: signing failed with "create bundle file:
  open : no such file or directory". Pinning the actions moved
  `cosign-installer` to its v4, which installs cosign 3, and cosign 3
  dropped `--output-signature` and `--output-certificate` in favour of
  `--bundle`. The signature is now `checksums.txt.bundle`, carrying
  everything a verifier needs, and the README's `cosign verify-blob`
  matches. The versions those actions install are pinned too — a SHA on
  an installer pins the wrapper, not the tool.

## [0.8.0] - 2026-09-05

### Fixed
- The staleness gate blocked its own release pull request. It failed when
  `[Unreleased]` was empty, which is exactly what a release commit
  produces, and under the pull-request flow CI runs before the tag the
  commit is preparing can exist — so no release could ever go green.
  Entries under a version heading with no tag yet now count as
  documented. An undocumented change, or a heading whose tag already
  exists, is still refused.
### Added
- Per-call debug logging: with `GDOCS_LOG_LEVEL=debug` the server records
  which method and tool ran, whether it failed, and how long it took. It
  records nothing about the document — no ids, titles or text — so a
  debug log is safe to attach to a bug report, and a test fails the build
  if a log line ever carries document data. The bug form says so instead
  of asking reporters to audit their own logs.

### Changed
- The agent evals are Go: `go test -tags=evals ./internal/evals`, one
  subtest per task, replacing `scripts/evals/run.py`. `-run` selects a
  task and a failed check names itself instead of being counted. No
  Python remains in the repository.

- The live driver is Go: `go test -tags=live ./internal/livecheck` drives
  the binary over stdio through the MCP SDK's own client, replacing
  `scripts/live-drive.py`. Steps that must be refused now assert their
  refusal instead of printing it for a human to read, so a green run means
  the guards fired as well as the writes.
- The repository's gates no longer need Python. `make check` shelled out
  to `python3` twice — to list tool names and to diff two schema dumps —
  which made an interpreter an undeclared prerequisite of a Go project's
  own definition of done. Both now run `go run ./internal/devcheck`.

### Fixed
- The server exited non-zero when a client disconnected, which is how
  every session ends: the SDK reports a closed connection as JSON-RPC
  -32004 with the EOF only as message text, so `errors.Is(err, io.EOF)`
  never matched it. Hosts log a non-zero exit as a crash. The stdio smoke
  test never caught it because it slept before closing stdin, and the
  exit is clean when nothing is in flight; it now closes abruptly too.

## [0.7.0] - 2026-09-04

### Added
- Releases are verifiable, not just downloadable: build provenance
  attestations tie every archive to the workflow, commit and runner that
  produced it (`gh attestation verify`), `checksums.txt` is signed with a
  keyless Sigstore certificate (`cosign verify-blob`), and each archive
  ships an SBOM. Builds stamp the commit's timestamp rather than the
  build's, so rebuilding a tag gives byte-identical binaries. The README
  shows the verification commands.

## [0.6.2] - 2026-09-04

### Fixed
- A binary installed with `go install`, which is what the README tells
  people to run, reported its version as `dev` forever: the release
  version arrives through ldflags, and `go install` applies none. It now
  falls back to the module version Go records in the build info, so
  `--version`, the MCP handshake and the User-Agent all name the release.

### Changed
- README: the install section leads with `go install`, says where the
  binary lands, and shows how to verify a release archive against
  `checksums.txt`.

## [0.6.1] - 2026-09-04

### Fixed
- The test suite passes on Windows and macOS, not just Linux. CI's first
  run ever caught three Linux-only assumptions: an export directory
  hardcoded as `/tmp/exports` (not an absolute path on Windows, so the
  config rightly refused it), a token file asserted to be `0600` (Windows
  has no POSIX permission bits; its ACL is what protects the file), and
  golden files compared byte for byte after a checkout had rewritten them
  as CRLF. A `.gitattributes` now pins line endings to LF.

### Changed
- CI and the release workflow pin what they run: actions moved from
  checkout v4, setup-go v5, upload-artifact v4, goreleaser v6,
  golangci-lint v8 and gitleaks v2 to their current majors, `go-version:
  stable` became `go-version-file: go.mod` so CI uses the toolchain the
  code declares, and golangci-lint, govulncheck and go-licenses are on
  explicit versions instead of `@latest`, which made a green build
  unreproducible. Dependabot already proposes these weekly.
- A tag now publishes a real GitHub release rather than a draft, with the
  archives and `checksums.txt`. docs/development.md describes the whole
  process.

## [0.6.0] - 2026-09-03

### Added
- The rest of the writable style surface, so nothing the API accepts is
  missing: paragraph borders on all five edges with their padding,
  paragraph shading, end indent, content direction, spacing mode,
  keep-lines-together and widow and orphan control, on `format_document`'s
  `paragraph_style` and `layout_document`'s `named_style`; and on
  `edit_table`'s `style_cells`, all four cell borders and per-side
  padding. Borders take a shorthand — `1pt solid #cccccc`, or `none` —
  with the parts left out defaulting to 1pt solid black. All of it reads
  back: `get_document`'s named style lines and `read_document with_styles`
  report borders and shading, and a zero-width border reads as no border,
  which is what Google's empty border object means. Verified live against
  paragraphs, a named style and a table.
- Tab stops, `headingId` and a cell's row and column span are read and
  reported but never written: the discovery document marks them read-only.

## [0.5.0] - 2026-09-03

### Added
- `get_document` reports each tab's named style definitions — the font,
  size, colour, alignment and spacing that `NORMAL_TEXT`, `TITLE`,
  `SUBTITLE` and `HEADING_1` … `HEADING_6` give every paragraph carrying
  them — with how many paragraphs of that tab carry each, headers and
  footnotes included, since redefining a style changes the whole tab.
  Only the styles in use are listed; the others have no appearance in
  the tab to show, and `get_document` is the tool the model calls first.
  `layout_document`'s `named_style` op could redefine these but nothing
  read them back, so the model could neither see what it was about to
  change nor confirm it afterwards; the `export_document format: html`
  workaround in docs/development.md is no longer the only way.

- `page_break_before` on `format_document`'s `paragraph_style` op and on
  `layout_document`'s `named_style` op, so every paragraph property the
  server reports can also be written. `get_document` reported it from the
  day named styles were read; nothing could set it.

### Changed
- `format_document` and `get_document` now point at `layout_document`'s
  `named_style` op: one styles the passages you target, the other
  redefines the style every paragraph inherits. A second client (Claude
  Desktop) set a page break on each heading paragraph and reported that
  "the Docs API doesn't expose editing the named style itself" — it never
  found the tool that does.
- `doc.Paragraph` now carries its whole paragraph style (alignment, line
  spacing, space above and below, both indents, keep-with-next,
  page-break-before) in a `doc.ParagraphStyle` it shares with the new
  named style definitions, instead of the two fields the renderers
  happened to read. No output changes; `read_document with_styles`
  annotates the same alignment and indent as before.

### Fixed
- `manage_tabs action: move` with a `position` later than the tab's
  current one moved it a place short, and moving a tab to the very next
  position did nothing while reporting success. Google inserts the tab at
  the given index while it still occupies its old slot and removes it
  afterwards, so the index needs raising by one when a tab moves later
  within the same parent; moving it earlier, or under a different parent,
  was already right. Found by the end-to-end live driver, whose Phase 4
  section had been addressing the wrong tab as a result.

## [0.4.0] - 2026-09-03

### Added
- `layout_document`: `page` (page size, margins, background, landscape,
  where page numbering starts, first- and even-page headers and footers),
  `section` (the same for one section, plus 1–3 columns with an optional
  separating line and gap; a section's type is fixed by the break that
  made it, so `section` does not accept one), `section_break`, and
  `named_style` to
  redefine `NORMAL_TEXT`, `TITLE`, `SUBTITLE` or `HEADING_1` …
  `HEADING_6` for a whole tab. Lengths are in points.
- Named ranges: `create_named_range`, `delete_named_range` and
  `replace_named_range` on `edit_document`, and `named_range` as a
  target. Unlike a block handle, which is valid only for the revision it
  came from, Google keeps a named range on its text across edits, so it
  is the way to come back to a passage in a later call. A replace
  overwrites every range the name covers, so the overwrite guard is shown
  what all of them hold; forgetting a name destroys nothing and is not
  guarded.
- `edit_table` ops `style_columns` (a fixed width in points, or an even
  share of the table) and `style_rows` (least height, keep a row off a
  page break). Repeating a header row stays with `pin_header_rows`: the
  API refuses `TableRowStyle.tableHeader` even though its schema lists
  it.
- `insert_object` gains `action: replace` to swap an image's source in
  place, and `action: delete` to remove an object by id — including a
  floating image, which no text range covers and no `edit_document`
  delete could reach. Deleting an inline object goes through the
  overwrite guard like any other deletion of its range, minus the object
  the op names, so `force` is on this tool too.
- Comment mode words the new ops as proposals. The ones that change a
  whole tab — `page`, `named_style`, `replace_image`, and deleting a
  floating object — say that they cannot be posted as a comment on a
  passage and name `direct`, instead of anchoring somewhere arbitrary.
- `reply_comment` gains `action: edit` to rewrite a comment or one of its
  replies, and `review_suggestion` gains `discard` to remove a suggestion
  outright. Google allows either only to the author.
- `get_document` reports, per tab, the page setup (naming US Letter, A4
  and US Legal), the floating objects with their ids, and the named
  ranges — everything a read of the text cannot show.

### Changed
- `read_document` shows block handles (`[p12]`) and heading ids by
  default; pass `with_handles: false` to drop them. The evals had the
  model asking for them on three of four reads, and an edit targets a
  handle, so the flag cost a round trip more often than it saved tokens.
- `edit_table` accepts several changes to one table's grid in one call,
  and its ops now read strictly in order: once an op changes a table's
  grid, the ops that follow it on that table are applied in a batch of
  their own against a fresh read, so their row, column and cell numbers
  mean the grid as it is by then. `insert_rows` followed by
  `set_cells r2c3` therefore writes the cell in the grid the insertion
  left, where before the two shared a batch and the cell meant the old
  grid. A dry run lists the held-back ops under `followups` and says it
  cannot resolve them yet; a number below one or a malformed cell name
  in one of them still refuses the call before anything is written.
  Comment mode is unchanged: nothing is applied, so every op stays in
  the proposals.
- Internal, with no change to what a tool returns: every result's text is
  shaped in one place. `get_document`'s moved out of the tool layer into
  the service, and the comment listing and the one-line thread summary
  under a read now come from one renderer model (`render.Thread`).

## [0.3.0] - 2026-09-03

### Added
- Resources: `gdocs://<id>` (every tab's body as markdown under one
  budget), `gdocs://<id>/outline` and `gdocs://<id>/tabs/<tab>`, for
  clients that attach a document whole; `--dump-schemas` lists the
  resource templates next to the tools.
- `make bench` and a synthetic large-document generator
  (`internal/doc/doctest.Large`) for the numbers below.
- Agent evals (`scripts/evals/run.py`): thirteen tasks through Claude
  Code headless against scratch documents, scored on the end state and
  the tool-call trace; the design document records the run.

### Changed
- Content written where a paragraph holds only whitespace fills that
  paragraph wherever the insertion lands, not just on the follow-up
  path: appending into a fresh header, footer or footnote replaces the
  blank line and takes the content's own paragraph style.
- Read tools (`get_document`, `get_outline`, `read_document`,
  `find_in_document`, `search_documents`, `export_document`,
  `list_suggestions`, `list_comments`, `list_revisions`, `diff_revisions`)
  return a text block only: Claude Code shows the model only a result's
  structured form when one is present, which made every read look like
  metadata. The scope, revision, block count and continuation now come
  in a header comment the service builds. Write results keep both forms
  and their JSON now carries the rendered `preview`.
- Dry runs list what a second batch will do (`followups`), and the
  content of new headers, footers, footnotes and data-filled tables lands
  through one follow-up edit against the refetched document instead of
  three separate paths; a segment can be named by its id (`segment:
  kix.…`) as the raw format shows it.
- One op-kind registry in the planner drives validation, compile order,
  the guard, the overlap check and the tools' allowed-op lists; the
  comment footer and the raw read format are rendered by the renderer;
  `edit_document`'s result text comes from the service.
- Large documents: what several operations need from one fetch is now
  derived once and shared (handle memory, comment threads, a searchable
  text index and an anchor index per segment); handles and cells are
  looked up through maps; list numbers are assigned at parse time;
  outline and statistics count words without building strings. On a
  150-page fixture a cached section read went from 14.5 ms to 0.1 ms, a
  text-targeted dry run from 61 ms to 20 ms, and locating 300 comments by
  quoted text from 384 ms to 38 ms.

### Fixed
- An insertion never deletes what it lands next to: a paragraph is
  treated as blank, and so filled, only when every run in it is text.
  One holding an image, a footnote reference, a chip or a break is left
  alone, as hard rule 4 requires.
- `search_documents` prints the `page_token` to paginate with; it used
  to say more results existed and name the token only in the JSON.
- A `format: raw` read that hits the budget says so, after the JSON
  array, and names the handle to continue from.
- A footnote inserted with content no longer starts with a blank line:
  Google creates the footnote with a paragraph holding one space, and the
  content now replaces it.
- Content written into an empty paragraph (a new header, footer,
  footnote or tab, or an append into an empty last paragraph) takes the
  content's own paragraph styles; a heading no longer comes out as normal
  text there.

## [0.2.0] - 2026-09-03

### Added
- Phase 2: comments on both backends: `list_comments` (full threads with
  replies, resolved and deleted state, located by the preview's anchors or
  by quoted text), `add_comment` (pinned to a target with Developer
  Preview, quoted otherwise), `reply_comment` (reply, resolve, reopen) and
  the gated `delete_comment`; `read_document` gains `include_comments`
  markers.
- Version history: `list_revisions`, `diff_revisions` (unified diff of
  Google's markdown or text export between two revisions) and
  `read_document` at an old `revision`.
- `edit_table`: insert tables (filled from a data grid in a second batch),
  set cells by minimal diff, insert and delete rows and columns, merge and
  unmerge, cell styling and pinned header rows, with the same modes,
  guard and dry run as text edits.
- `insert_object`: inline images from public URLs, person chips, rich-link
  chips and date chips.
- `manage_tabs` (add with content, rename, move, nest) and the gated
  `delete_tab`; `edit_document` gains `delete_header` and `delete_footer`.
- Raw client calls for replies, comment deletion, revisions and revision
  export through `files.download`; request builders for every new op.

### Changed
- One block-range resolver serves reads and writes, so a stale handle is
  refused on reads as well; comment threads are looked up once per fetch
  and shared by the guard, reads and listings.
- Unsupported markdown in content is reported as `[unsupported]` rather
  than `[invalid]`.

### Fixed
- Minimal-diff replacements line up with the index space when the
  paragraph holds chips, images, footnote references or breaks (offsets
  used to drift past them); `find_in_document` offsets and context too.
- Inserting inline into a heading or a list item keeps the paragraph's
  style and bullet instead of demoting it to normal text.
- `replace_all` and `merge_cells` go through the overwrite guard;
  `delete_header`/`delete_footer` are refused up front in suggest mode
  (the API rejects them there); lists are created after the content ops
  that would shift them.
- `expect_revision` is re-checked after a revision conflict; suggestion
  review plans against a fresh read; a failed comment lookup refuses a
  direct edit instead of leaving the guard blind.
- Handles are checked against the last read on every path: an unknown
  handle or a document never read in this session is `[unknown]`, and a
  read served from the cache refreshes the memory. Edits that change the
  block count warn that later handles moved.
- `within` cannot point at another tab's section or a foreign segment's
  block; cell targets carry the cell's own tab and segment; replacing an
  empty section body inserts a paragraph instead of gluing text onto the
  next heading.
- Comment markers land at the right offset when several sit in one run;
  multi-range comment anchors cover their whole span; tables with a
  merged header row render as valid markdown; boundary whitespace in
  styled runs is kept; objects in table cells are escaped.
- A table inserted with data is found by its predicted handle even when
  earlier ops in the batch shifted it; deleting the only top-level tab is
  refused before the API call; old-revision reads no longer report a
  Drive revision id as the concurrency token; diff line counts match the
  printed hunks.
- Dry runs list the request kinds instead of dumping raw requests with
  UTF-16 indices; `logout` revokes the stored token rather than an
  environment override and no longer creates a profile for a name that
  never existed.
- The integration-tagged preview spike compiles again and `make vet`
  checks it.

## [0.1.0] - 2026-09-03

### Added
- Phase 1: `edit_document` (insert, append, replace by minimal diff,
  delete, replace_all, page breaks, footnotes, headers, footers) and
  `format_document` (text style, paragraph style, bullets, clear) with
  `mode: suggest | direct | comment`, dry runs, revision guards with one
  automatic re-plan, and an overwrite guard that refuses direct deletion
  of ranges holding comments, suggestions, images or footnotes unless
  forced. Targets are exact text (normalised), stable heading ids,
  handles checked against the last read, or cells.
- `find_in_document`, `search_documents`, `create_document`,
  `export_document`, `list_suggestions`, `review_suggestion`.
- Markdown fragment parser (goldmark) that refuses constructs the Docs
  API cannot express; Drive search, export and comment calls; own Docs
  wire types replacing the generated client.
- Phase 0: `login`, `logout`, `status`, `doctor` commands; loopback OAuth
  with PKCE; keyring storage with file fallback; env-first configuration.
- Read tools: `get_document`, `get_outline`, `read_document` with
  markdown, text and raw formats, section and handle scoping, output
  budgets with continuation, style annotations, and CriticMarkup for
  pending suggestions.
- Raw REST client for the Docs and Drive APIs with retries, rate limits
  and typed error classes; own wire types instead of the generated client.
- Design document (`docs/architecture.md`) with the evidence log.
