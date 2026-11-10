# Architecture

How `google-docs-mcp` is put together, and the decisions a contributor
should not undo without a better reason than the one that put them here.
Part 1 is the overview. Part 2, from [Decisions](#decisions), is one
entry per decision, cited elsewhere by its D-ID. What was verified about
Google lives in the comment at the code that depends on it, with the
reference linked and the integration test or live step that checks it
named.

## Context

`google-docs-mcp` is a Model Context Protocol server for Google Docs.
One Go binary runs as a subprocess of an MCP client and speaks MCP over
stdio. It holds one person's OAuth refresh token and calls the Google
Docs API and the Drive API over raw REST, with wire types of its own
(D-01, D-08). Nothing is hosted.

The parties around the server:

- **The person** creates their own Cloud project and Desktop OAuth
  client, signs in once with `google-docs-mcp login`, and every call
  acts as them (D-01, D-66). They choose how a change lands: as a
  suggestion, a direct edit or a comment (D-29).
- **The deployer** decides what the server may do, through `GDOCS_*`
  settings the client passes at start: read-only mode, the delete tools,
  the default write mode, the export directory and the profile (D-24,
  D-54, D-55, D-69, D-70). The deployer is usually the person, setting
  up their own client.
- **The client** approves each call by its own rules and draws its
  prompt from the tool annotations. The server asks the person nothing
  itself (D-52, D-57).
- **The model** chooses the calls and their arguments, and never sees an
  index (D-12). It can be wrong, so every write is guarded against
  deleting what it did not mean to, and no tool lets it change the
  server's settings (D-28, D-59).
- **Other people** edit the same document. Their work arrives between a
  read and a write, and their unsaved work in an open editor is never
  written past (D-30, D-44).

The server sends a credential only to Google, over HTTPS: the
`googleapis.com` hosts, `docs.google.com`, and Google's content hosts
under `googleusercontent.com`. Every URL is checked with its port before
the token is attached, and there is no telemetry (D-11).

It works on one document and everything inside it, its history included
(D-02). That covers reading, finding, creating, exporting, editing as
suggestions, directly or as comments, formatting, tables, tabs, headers,
footers, footnotes, images, chips, named ranges, layout, comment
threads, suggestions and revisions. A document may be a Google Doc, a
Markdown file or a Word file. A Google Doc is read and written through
the Docs API. A Markdown file in Drive, which Docs opens in place, is
read and written as its own text (D-42). A Word file is read as a Google
Doc and written as the file it is (D-49).

The platform has gaps nothing here can close, so nothing here promises
to close them. The Docs API cannot insert a chart or a table of
contents, write an equation or a drawing, or fetch an image from a
private address. It reads an old revision only as an export, never as
structure. A comment made through Drive cannot be turned into a range or
shown inline in the editor.

It is not a Drive client (D-02), a hosted service (D-01), or a faithful
converter between markdown and a document (D-26).

It exists because the hard part of an AI working in a document is not
the plumbing. Other servers make the model count UTF-16 indices,
overwrite what they edit, and post comments that pin to nothing. This
one keeps the index arithmetic on the server, edits by minimal diff,
pins comments where Google allows, and lets the person choose how each
change lands (D-12, D-27, D-29, D-38).

## Quality goals

In priority order:

1. **Nothing in a document is destroyed by accident.** A replace is a
   minimal diff, so what is anchored to unchanged text survives. A
   direct edit that would delete a comment anchor, a suggestion, an
   object or a footnote reference is refused unless forced. Suggest and
   comment modes delete nothing, and the delete tools exist only when
   the deployer enables them (D-27, D-28, D-29, D-54).
2. **Edits land where they were meant to, against the document as it was
   read.** The model addresses content by exact text, heading id, handle
   or named range, never by index. Every write is planned against one
   revision and guarded by it (D-12, D-13, D-14, D-15, D-30). The unit
   tests run against fakes that answer as Google documents, and the live
   driver is what checks the beliefs those fakes encode (D-80, D-83).
3. **The person decides how a change lands, and the server never decides
   for them.** Suggest, direct or comment, with a configured default and
   no silent substitution, on a Google Doc, a Markdown file or a Word
   file (D-17, D-29).
4. **Results say what happened.** A write reports what it touched and
   the new revision. A write that may have landed is
   `[ambiguous_outcome]` and is never repeated, and a later batch that
   fails is reported, not rolled back (D-30, D-31, D-61).
5. **The stream, the logs and the repository stay clean, and the surface
   is a contract.** Stdout carries frames only, logs carry no document
   data, transcripts are redacted, the repository holds nothing real,
   and a released tool or field keeps working (D-03, D-04, D-06, D-07,
   D-77).

## Codemap

- `cmd/google-docs-mcp` is the program. With no subcommand it serves;
  `login`, `logout`, `status` (text or `--json`), `doctor`, `--version`
  and `--dump-schemas` are the rest. It names stdout and stderr once and
  passes them down (D-03), masks the account in what `doctor` and
  `status` print (D-05), and serves without credentials (D-71).
- `internal/auth` runs Google's desktop flow on a loopback port with
  PKCE, and holds the scope sets for full and read-only use. Its token
  source refreshes on a bounded client of its own. It also inspects and
  revokes a grant (D-63, D-66, D-68).
- `internal/config` reads the `GDOCS_*` environment and the bound flags,
  and validates them once at start. `config.Vars` is the registry of
  every variable the server reads, which the staleness and `mcpb` gates
  read. A removed setting still turned on is refused (D-53, D-69, D-70).
- `internal/credentials` resolves the refresh token from the
  environment, the OS keyring, or an owner-only file the server warns
  about (D-67).
- `internal/doc` is the parsed document model: tabs, segments, blocks
  and runs with their UTF-16 indices, handles, sections derived from
  headings, the anchored-content index, suggestion states read as
  properties, and normalized text search. It has no network (D-12, D-14,
  D-16, D-21).
- `internal/doc/doctest` loads the synthetic fixture that tests in
  several packages share, and builds `Large`, a long generated document
  for the benchmarks (D-06).
- `internal/evals` holds the agent evals, behind the `evals` build tag
  and run by hand: Claude Code headless against a scratch document,
  scored on the end state and the trace (D-84).
- `internal/gapi` is the raw REST client for Docs and Drive. It holds
  what every request shares: the host allowlist with the port, compact
  JSON, the read, write and Drive-write limiters, deadlines, retries by
  kind with `[ambiguous_outcome]` for a write that may have landed, the
  classes of Google's failures read by reason, long-running exports and
  whole-file uploads. Nothing here imports MCP. Its integration tests,
  behind the `integration` build tag, probe Google directly (D-11, D-61,
  D-62).
- `internal/gdocs` holds the hand-written wire types for the Docs API,
  every `suggested*` field included. It imports nothing from this
  repository (D-08, D-09).
- `internal/livecheck` is the live driver, behind the `live` build tag
  and run by `make live`. Its untagged tests check that every line the
  drivers print goes through the redactor (D-07, D-83).
- `internal/markdown` turns markdown the model writes into a neutral
  fragment, and refuses what the Docs API cannot express (D-26).
- `internal/mdfile` works on a Markdown file as text: the editor's
  comment markup parsed out and kept, exact-text find, edits that never
  move a marker, the outline by slug, and the block merge for working
  copies. It has no network (D-43, D-47).
- `internal/ooxml` edits a Word file as it is stored: paragraphs lined
  up with the Docs model, splices of `word/document.xml`, tracked
  changes and Word comments (D-49, D-51).
- `internal/plan` compiles ops and fragments into `batchUpdate`
  requests: the minimal diff, the overwrite guard, ordering, lists,
  tables, layout and the registry of op kinds. It owns every piece of
  index arithmetic on the write path and has no network (D-27, D-28,
  D-33).
- `internal/redact` masks ids, addresses and names in what the drivers
  write and in what `doctor` and `status` print. It has no build tag, so
  its tests run in every `make check` (D-05, D-07).
- `internal/render` writes every word a caller reads: markdown, plain
  text, outlines and raw bytes, CriticMarkup and comment marks, budgets
  and continuations, and the unified diff (D-18, D-20, D-22).
- `internal/server` wires the MCP SDK: the server instructions, the
  `gdocs://` resource templates, per-call debug logging, and the schema
  dump through an in-memory session (D-04, D-76, D-79).
- `internal/service` is where the rules live: the fetch cache and the
  comments-view fallback, target resolution and handle memory, the write
  path and its rounds, comments on both backends, suggestions, history,
  export, search, and the Markdown and Word write protocols. It is where
  a failure becomes a `[class]` tool error (D-17, D-30, D-31, D-38,
  D-44, D-50, D-60, D-64).
- `internal/tools` registers the MCP tools, each with its annotations,
  under the read-only and delete switches. Handlers validate input, call
  the service and shape the reply (D-19, D-54, D-55, D-57, D-59).
- `internal/userconfig` stores non-secret per-profile state: the client
  file's path, the account, and where the token went (D-70).
- `internal/version` holds the version the build stamps in, with the
  module's build info as a fallback.

Dependencies run one way. `tools` calls `service`, `service` calls
`gapi`, and `gapi` decodes into `gdocs`. `service` also uses `plan`,
`doc`, `render`, `markdown`, `mdfile` and `ooxml`; `plan` uses `doc` and
`markdown`; `render`, `mdfile` and `ooxml` use `doc`. Nothing under
`internal/gapi` imports MCP.

Beside the packages, `scripts/gates` is every check the repository runs
on itself, written in Go behind one registry (D-10, D-85), and
`scripts/internal/livecover` reads the live driver's step names for the
gates that check them. `testdata`
holds the synthetic fixture, the renderers' goldens, and the API
snapshots with their verdicts (D-06, D-09). `packaging/mcpb` holds the
Claude Desktop bundle's manifest and launcher (D-93).

## Request flow

### A read

A client calls `read_document` with a heading. The service asks Drive
for the file's type once per document, so a Markdown or Word file takes
its own path (D-42, D-49). A read of the same document within five
seconds of the last one reuses it (D-64). Otherwise `documents.get` runs
with every tab, in the inline suggestions view and with the comments
view, and asks again without the view when Google refuses it (D-16,
D-17). `gapi` checks the host, takes a token from the read limiter,
applies the per-attempt deadline, and retries a transient failure with
backoff (D-11, D-61, D-63).

`doc` parses the response into tabs, segments and blocks, and the handle
memory records what each handle points at (D-14). The scope resolves to
a range of blocks, and `render` writes markdown under the budget, each
block behind its handle, ending with where to continue (D-18). With
`include_suggestions` a read of the synthetic fixture shows:

```
[p2] # Background {h.bg}

[p3] Revenue grew {--a lot--}{>>s:s1<<}{++**substantially**++}{>>s:s1<<} in Q3.

[p4]

[p5] - First point
[p6]     - Nested point
```

The tool returns that text as its one content block, with the revision
in a header line (D-19, D-20).

### A guarded write

A client calls `edit_document` to replace a sentence in direct mode. The
service reads the document fresh, checks `expect_revision` if one is
given, and resolves every target before anything is sent; one that fails
fails the call (D-13, D-30). `plan` takes the minimal diff of the old
text against the new (D-27) and checks every range it would delete
against the anchor index. A comment anchor, a suggestion, an object or a
footnote reference inside one refuses the op `[blocked]` unless forced
(D-28). The rest compiles to requests: inserted markdown becomes text,
styles, lists and empty slots for tables and images, ordered by
descending index so that earlier requests do not move later ones (D-33).
A dry run stops here and reports what would be sent (D-35).

`batchUpdate` carries `requiredRevisionId`, and `writeMode: SUGGEST` in
suggest mode (D-29). A conflict reads again and plans again, once
(D-30). Tables and images in the content, and ops after a table's grid
changes, follow in later batches, each against a fresh read at the
revision the last reply gave (D-31, D-32). The reply carries the new
revision, the ops applied, previews and warnings, as text and JSON
(D-19). A 5xx or a network failure on the batch is
`[ambiguous_outcome]`, and the server never sends it again (D-61).

### A write to a Markdown or Word file

The Docs API reads a Markdown file and a Word file and refuses every
write to either, so Drive's type for the file picks another path (D-42,
D-49). The service reads the file's bytes with Drive's head revision and
checksum, together with the live editing session's revision and comments
for a Markdown file, or the Docs model for a Word file. A person's work
in an open editor that has not reached the file yet makes the write
wait, then refuse (D-44).

The change is planned in `mdfile` or `ooxml` under the same overwrite
guard. A Markdown file's comment markup is kept byte for byte, and a
suggestion on a Word file becomes a tracked change (D-43, D-51). Just
before the upload the head revision, the checksum and the live revision
are read again, and the write plans again if any moved. The file is
uploaded whole as its own type, once, or not at all when nothing changed
(D-45). Then the server finds the revision its upload replaced. If that
is not the one it planned on, someone's save landed in between. On a
Markdown file the change is made again on top of that save where it
still fits. Otherwise the save is put back and the write is `[conflict]`
(D-44). A Word write then waits for Docs to read the revision it
uploaded, so the next read shows it (D-50).

## Invariants

- Stdout carries only MCP JSON-RPC frames (D-03).
- No log line carries a document id, a revision, a title, text or a
  query (D-04).
- Nothing in the repository comes from a real document, account or
  deployment (D-06).
- Nothing the live driver or the evals print reaches a transcript
  unredacted (D-07).
- No generated Google client is imported (D-08), and every published
  method and modeled field has a written verdict (D-09).
- A credential goes only to Google's hosts over HTTPS, checked with the
  port (D-11).
- The model never sees or computes an index (D-12).
- A handle is checked against the revision it came from (D-14).
- A direct edit never deletes anchored content unless forced (D-28).
- The server never substitutes one write mode for another (D-29).
- Every write to a Google Doc is guarded by the revision it was planned
  on (D-30).
- A write is repeated only on 429, and one that may have landed is never
  repeated (D-61).
- Unsaved work in an open editor is never written past, `force` included
  (D-44).
- A write never forges or ends a comment in a Markdown file's markup
  (D-43).
- The server asks the person nothing (D-52).
- The delete tools exist only when the deployer enables them (D-54), and
  each repeats its target (D-56).
- No tool takes or writes one of the server's settings (D-59).
- A released tool, argument or output field keeps working until a major
  release (D-77).
- `make check` and CI run the same gates (D-85).

## Cross-cutting concerns

**Errors.** Every tool error is a tool result in the form
`[class] message`, and the class tells the caller what to do next. The
vocabulary is closed and declared in `service.Classes` in
`internal/service/service.go`, with what each class asks the reader to
do. `gapi.Classes` is the client's part of it. `make classes` fails on a
class the code emits and the list lacks, and on one the list holds and
nothing emits. `ambiguous`, a target matching several things, and
`ambiguous_outcome`, a write that may have landed, ask for opposite
things. Google's reason is read before its status (D-60, D-62).

**Concurrency with people.** A revision guards every write to a Google
Doc (D-30). Drive offers no precondition on an upload, so a write to a
Markdown or Word file checks, writes, verifies and repairs. The live
session and the Docs model say when a person's work has not reached the
file yet (D-44, D-50).

**Comments on two backends.** Docs pins a comment to a range where the
comments view answers, and Drive carries every thread operation. A
Markdown file's editor comments live in its bytes, and a Word file's in
its XML. One located thread list per fetch serves the guard, reads with
comments and the listing (D-38, D-39, D-43, D-51, D-65).

**Budgets.** Every read is scoped and budgeted and says where to
continue (D-18). The resources share one budget across a document's tabs
(D-76), and a regex replace stops at 500 matches (D-34).

**Configuration and registration.** The environment is the source, a
flag mirrors each variable, and a profile holds one account (D-69,
D-70). Read-only mode and the delete switch decide what registers, and
read-only mode also narrows the scopes `login` asks for (D-54, D-55,
D-68). A removed setting that restricted something is refused at start
(D-53). The full list is in [configuration.md](configuration.md).

**Reliability.** Limiters keep reads, document writes and Drive writes
below Google's quotas. Every attempt has a deadline, the token refresh
included, and retries follow the kind of request (D-61, D-63). A
five-second cache coalesces reads and never decides correctness, and
everything derived from one fetch is derived once (D-64, D-65).

**Logging.** `slog` writes to stderr, and a line carries the method, the
tool, the outcome and the duration (D-03, D-04). `doctor`, `status` and
the startup line mask the account (D-05). The threat model is in
[security.md](security.md).

**Testing.** Unit tests run without a network on synthetic fixtures,
against fakes that answer with the errors Google documents (D-80). The
coverage floor holds every package `go list` finds under `internal/`,
each exemption with its reason (D-81). A guarantee stated over the tools
is tested over every registered tool (D-82). The integration tests in
`internal/gapi` probe Google directly and leave their scratch documents
behind. The live driver drives the built binary over stdio through every
tool and op kind before a release, and the agent evals score a model
doing real tasks (D-83, D-84). Green unit tests prove only what the
fakes believe; the live driver is what tests the belief. How to run both
is in [CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

**The surface as a contract.** `--dump-schemas` lists the tools as a
client sees them, the schema diff compares that dump with the last tag,
and a pull request that changes the surface names each changed item in a
commit footer (D-77, D-78, D-79).

**Documentation.** A decision lives once, in part 2 here. What was
verified about Google lives in the comment at the line that depends on
it: the behavior, the reference link, where the reference is wrong or
silent, and the integration test or live step that checks it. The docs
gate holds every D-ID citation to an entry, every Held by cell to
something that exists, and the codemap to the tree. The staleness gate
holds the README, [configuration.md](configuration.md) and the CHANGELOG
to the code.

**Distribution.** A release is built from a tag, signed for people and
attested for machines (D-90, D-91). Each release carries a Claude
Desktop bundle whose settings derive from the server's own registry,
every switch off by default (D-93, D-94). Releasing is in
[release.md](release.md), and fixing a symptom in
[troubleshooting.md](troubleshooting.md).

## Decisions

| ID | Decision | Status | Held by |
| --- | --- | --- | --- |
| D-01 | One stdio binary per person, signed in to their own Google account through their own OAuth client; nothing is hosted | accepted | smoke, review |
| D-02 | The scope is one document and everything inside it, its history included; folders, moves, sharing, trash and copies are out | accepted | review |
| D-03 | Stdout carries only MCP JSON-RPC frames, and logs go to stderr through slog | accepted | forbidigo, smoke, TestNothingPrintsOutsideTheBoundary, TestTheJSONIsExactlyOneValue |
| D-04 | Logs carry method, tool, outcome and duration, never an id, a revision, a title, text or a query | accepted | TestDebugLogsCarryNoDocumentData, TestTransportErrorsDoNotCarryTheURL, TestAPermissionDenialDoesNotRepeatTheAccount |
| D-05 | What doctor, status and the startup line print masks the account to its domain and drops a client id | accepted | TestDoctorOutputHidesWhatItKnows, TestMaskAddress, TestMaskClientID, TestTheCollectorMasksTheAccount |
| D-06 | Nothing from a real document, account or deployment enters the repository; fixtures are synthetic, and two scanners hold it | accepted | leaks, gitleaks, TestLeaksGate, TestRulesCatchPlantedIdentifiers, TestLeaksCatchesAPlantedAddress, TestLeaksCatchesAPlantedBinary, TestHistoryCarriesNoIdentifiers |
| D-07 | A transcript the live driver or the evals write goes through one redactor, which carries no build tag | accepted | transcript, TestDriversDoNotPrint, TestEveryPlaceAPersonIsWrittenIsKnown, TestTranscriptKeepsPeopleAndDocumentsOut, TestTranscriptLeavesNothingIdentifying, TestTranscriptJSON |
| D-08 | Own wire types over raw REST: no generated client, and no code from another project | accepted | depguard, api-fields |
| D-09 | Every method Google publishes, and every field of a type this server models, has a verdict | accepted | api-coverage, api-fields, TestAPICoverageGate, TestAPIFieldsGate, TestFieldProblems, TestAnAliasBringsASchemaIntoTheComparison |
| D-10 | Everything the repository runs on itself is Go, in one `scripts/gates` program | accepted | review |
| D-11 | The server talks only to Google's hosts over HTTPS, matched with the port before a credential is attached | accepted | TestDefaultAllowlistSendsCredentialsOnlyOverHTTPSToGoogle, TestExportRevision |
| D-12 | The model never sees or computes a UTF-16 index; the server converts at the boundary | accepted | TestUTF16, TestCompileFragmentUTF16, review |
| D-13 | Exact text is the primary target: normalized on both sides, matching once, narrowed by occurrence or within | accepted | TestResolveTargetText, TestNormalizeAndWords |
| D-14 | Headings are addressed by headingId and other blocks by short handles the server remembers per document and re-checks | accepted | TestHandleMemoryStaleness, TestStaleHandleOnWrite, TestTabLookupAndHandles, TestShiftedHandlesWarning |
| D-15 | A named range is the one target that outlives an edit, and a name several ranges share is refused as a target | accepted | TestNamedRangeTarget, TestNamedRangeOps, TestReplaceNamedRangeIsGuarded |
| D-16 | A document is read with every tab in the inline suggestions view, the one index space, and every write names its tab | accepted | TestGetDocumentDecodesAndSendsParams, review |
| D-17 | Every read asks for the comments view and, refused, asks again without it; that read decides pinning and suggest mode | accepted | TestFetchFallsBackWhenCommentsViewRefused, TestFetchDoesNotHideDocumentErrors, TestInfoCommentsViewRefused |
| D-18 | Reads are scoped and budgeted by default and say where to continue | accepted | TestBudgetAndContinue, TestReadWholeBudget, TestRawBudgetCutsAtElementBoundaries |
| D-19 | Read tools return a text block only; write tools return text and a structured result that carries everything the text does | accepted | TestReadToolsAreTextOnly |
| D-20 | Reads mark suggestions as CriticMarkup and comments as markers | accepted | TestCommentMarks, TestRestyleUsesHighlightNotInsertion, TestMarkdownGolden |
| D-21 | A suggestion is read from its suggestion state, never from the style beside it | accepted | TestSuggestedStyleReadsTheStateNotTheStyle, TestSuggestedInsertionTakesItsOwnStyle, TestEverySuggestionStateFieldIsNamedByItsTag, TestListSuggestionsSeesAFormattingSuggestion, live |
| D-22 | `format: raw` returns the bytes Google sent | accepted | TestRawReturnsTheAPIsBytes, TestRawFallsBackToTheWireTypes, TestCompactJSONOnEveryRequest |
| D-23 | get_document reports what no read of the text shows, the named style definitions included | accepted | TestInfo, TestNamedStyleDescription, TestNamedStyles |
| D-24 | A binary export is written only under GDOCS_EXPORT_DIR, created exclusively, never over a file or through a link | accepted | TestAnExportNeverOverwrites, TestAnExportNeverWritesThroughALink, TestFileName |
| D-25 | Markdown is for new content; existing content is edited in place and never round-tripped | accepted | review |
| D-26 | The markdown parser converts what it can prove and refuses the rest, naming the construct and its line | accepted | TestUnsupported, TestReadProseParsesBackAtRandom, TestReadTextWritesBackUnchanged, TestEscapesAndReferences |
| D-27 | A replace is the minimal word-level diff of the old text against the new | accepted | TestMinimalEdits, TestPlanReplaceMinimal, TestAlignedMinimalDiffPastObjects |
| D-28 | A direct edit that would delete comment anchors, suggestions, objects or footnote references is refused unless forced | accepted | TestGuard, TestGuardBlocksDirect, TestReplaceAllAndMergeAreGuarded, TestDeleteRowsGuardAndSegments |
| D-29 | The person chooses how changes land, suggest, direct or comment, and the server never substitutes one mode for another | accepted | TestEditSuggestAndModes, TestCommentMode, TestCommentModeProposals, TestSuggestModeRefusesUnsupportedRequests |
| D-30 | Every write is planned against one revision and guarded by it, re-planned once on a conflict, and reports what it touched | accepted | TestDryRunAndConflictRetry, TestSuggestRecheckedOnConflict, TestExpectRevisionRecheckedOnConflict, TestBatchUpdateBodyAndWriteControl, TestRawStaleRevision |
| D-31 | Content with tables or images is written in rounds, each against a fresh read at the revision the last reply gave | accepted | TestEmbedsPlacedInRounds, TestEmbedFailuresAreReported, TestEmbedRefusals, TestCompileFragmentSlotLimits, live |
| D-32 | Ops addressed in a table after a grid change wait for a batch of their own against a fresh read | accepted | TestGridChangesChain, TestAForcedOpInALaterBatchIsRefused |
| D-33 | A list item keeps a right inherited bullet, and the rest are rebuilt in windows that count levels from the item before | accepted | TestCompileFragmentListLevels, TestPlanListLevel, TestBulletsNoneKeepsHandIndents, live |
| D-34 | A regex replace is expanded by the server into one replace per match, found with RE2 within a paragraph | accepted | TestRegexReplaceAllReplacesEachMatch, TestRegexReplaceAllIsGuardedAndRefused, TestARegexMatchAcrossAChipIsRefused, TestAReplacementNamingAMissingGroupIsRefused |
| D-35 | A write that takes dry_run resolves, plans and reports, and sends nothing | accepted | TestDryRunAndConflictRetry, TestEmbedsDryRun, TestDryRunListsFollowups, live |
| D-36 | A direct formatting change over a property a pending suggestion sets is warned about, not refused | accepted | TestDirectFormatWarnsOnACollidingSuggestion, TestClearFormattingCollidesWithEverySuggestedProperty, live |
| D-37 | Every GA request of the Docs API and every field it accepts on a write is emitted; a read-only field is reported, never sent | accepted | api-coverage, api-fields, TestKindRegistryCoversEveryTool |
| D-38 | A comment is pinned through insertComment where the comments view answered, else posted through Drive quoting the text and said to be unanchored | accepted | TestAddComment, TestPreviewAnchorsLocateComments, TestListCommentsLocatesAndRenders |
| D-39 | Thread operations go through Drive's comment API, one backend; a reply on a suggestion's thread is the exception | accepted | TestReplyAndDeleteComment, TestReplyToASuggestionsThread, TestCommentEditAndSuggestionDiscard |
| D-40 | review_suggestion takes each suggestion by the id list_suggestions showed | accepted | TestReviewNamesEverySuggestion, TestAReviewNamesASuggestionOnce |
| D-41 | History is read through Drive revisions and exports at a revision; restoring is an explicit replace | accepted | TestListRevisions, TestDiffRevisionsAndReadRevision, TestExportRevision |
| D-42 | A Markdown file in Drive is a document, read from its bytes and written by an upload of the whole file | accepted | TestMarkdownFileReadIsItsText, TestMarkdownFileEditUploadsAMinimalChange, TestMarkdownFileUnderTheOlderType, TestMarkdownFileStopsAtTheDocsModel, live |
| D-43 | A Markdown file's comment markup is read out, kept byte for byte, and never forged or ended by a write | accepted | TestParseSeparatesTheMarkup, TestApplyKeepsMarkersWhereTheTextIs, TestApplyRefusesMarkup, FuzzParseApply |
| D-44 | A Markdown write checks the live session, re-checks before uploading and repairs from history; force never writes past unsaved editor work | accepted | TestMarkdownFileWaitsForTheEditor, TestMarkdownFilePlansAgainWhenTheEditorMoves, TestMarkdownFileKeepsASaveThatSlipsIn, TestMarkdownFileConcurrency, live |
| D-45 | A call uploads once, and not at all when nothing changed, as the file's own type, and pins no revision | accepted | TestMarkdownFileUploadsNothingForNoChange, TestUploadFileSendsTheBytesAsTheGivenType, TestMarkdownFileRetypedIsRefused |
| D-46 | A comment written into a Markdown file uses the editor's markup, and a resolve is never written | accepted | TestMarkdownFileComments, TestMarkdownFileAddComment, TestAddReply |
| D-47 | Working on a Markdown file together goes through a Google Doc working copy, merged back block by block | accepted | TestCreateWorkingCopy, TestWriteBackCarriesTheCopysChanges, TestWriteBackConflicts, TestWriteBackWithoutItsBase, FuzzMerge |
| D-48 | Search finds Google Docs by default, and kind reaches Markdown files | accepted | TestSearchKinds |
| D-49 | A Word file is read as a Google Doc and written as its own file, by splices of the bytes it read | accepted | TestWordEditDirect, TestBytesCopiesUntouchedParts, TestWordRefusesDocsOnly, FuzzEdit, live |
| D-50 | An op reaches a Word file's paragraphs by order, each checked to hold the text the Docs model shows | accepted | TestWordStale, TestWordMergedCells, TestWordCoveredCell |
| D-51 | On a Word file a suggestion is a tracked change and a comment a Word comment, under the account's name | accepted | TestReplaceTracked, TestComment, TestWordEditSuggest, TestWordComments |
| D-52 | The server asks the person nothing; the client approves each call | accepted | forbidigo, TestWriteToolsEndToEnd, live |
| D-53 | GDOCS_REQUIRE_PROMPT is refused at start unless off, and GDOCS_PREVIEW is ignored with a warning | accepted | TestTheRemovedRequirePromptIsRefusedUnlessOff, TestPreviewIsDeprecated |
| D-54 | The delete tools register only with GDOCS_ENABLE_DESTRUCTIVE | accepted | TestListToolsAndSchemas, TestFullSurfaceRegistersEverything |
| D-55 | Read-only mode registers the reads alone and asks for read-only scopes | accepted | TestListToolsAndSchemas, TestScopesAgree |
| D-56 | A delete repeats its target, and delete_comment how the text it deletes begins | accepted | TestDeletesNeedTheTargetRepeated, TestDeleteCommentHoldsItsTextToTheWitness |
| D-57 | destructiveHint is true on every write but the three that only add, and readOnlyHint is on the reads alone | accepted | TestOnlyToolsThatAddSayTheyAreNotDestructive |
| D-58 | No tool carries requiresUserInteraction | accepted | TestListToolsAndSchemas |
| D-59 | No tool takes one of the server's settings, and no code a tool reaches can write them | accepted | TestNoToolTakesAServerSetting, TestNoToolCanWriteTheServersSettings |
| D-60 | Errors are tool results, `[class] message`, from one closed vocabulary | accepted | classes, TestClassesGate, TestToolErrorsAreResultsNotProtocolErrors, TestGoogleErrorsReachTheModel |
| D-61 | Reads retry transient failures; a write repeats only on 429, and a 5xx or network failure on one is `[ambiguous_outcome]` | accepted | TestWriteRetriesOnlyOn429, TestDriveWriteRetriesOnlyOn429, TestDeleteAfterServerErrorIsNotRepeated, TestWriteNetworkFailureIsAmbiguous, TestReadRetriesThenSucceeds |
| D-62 | A Google error is classified by its reason before its status | accepted | TestErrorClassification, TestThrottling403RetriesOnRead, TestThrottlingReasonSpellings, TestErrorMessageCarriesTheReason |
| D-63 | Every HTTP call has a deadline, the token refresh and the login code exchange included | accepted | TestTokenRefreshIsBounded, TestContextCancellation |
| D-64 | A five-second cache coalesces fetches of a document, and a write always fetches again | accepted | TestFetchCacheAndHandles |
| D-65 | Everything derived from one fetch is derived once | accepted | TestLargeFixtureIsConsistent, bench, review |
| D-66 | Sign-in is Google's desktop flow on a loopback port with PKCE | accepted | TestLoginHappyPath, TestLoginRejectsBadStateAndDenial, TestLoginTimeoutAndCancel |
| D-67 | The refresh token goes to the OS keyring, else an owner-only file with a warning, and the environment overrides both | accepted | TestEnvWins, TestFileFallbackWhenKeyringBroken, TestKeyringSaveRemovesStaleFile |
| D-68 | The scopes are documents and drive, or their read-only pair | accepted | TestScopesAgree, review |
| D-69 | Configuration is the environment, each variable with a bound flag, read through config and validated once at start | accepted | staleness, TestEnvThenFlagPrecedence, TestEveryVariableTheCodeNamesIsInEnvVars, TestEveryReaderOfAVariableGoesThroughConfig, TestASettingOfSpacesIsUnset |
| D-70 | A profile per Google account, each with its own client, token and account | accepted | TestProfileRule, TestPathsAndRoundTrip |
| D-71 | The server always starts; without a working credential every tool answers `[auth]`, and doctor is the check for people | accepted | TestAuthFailureSurfacesAsAuthClass, smoke |
| D-72 | Tool names are snake_case verb_noun with no dots | accepted | review |
| D-73 | Schemas are flat structs with an op or action enum, with no unions and no vendor keywords | accepted | review |
| D-74 | Page and section layout is a tool of its own | accepted | TestLayoutToolEndToEnd, review |
| D-75 | A border is one shorthand value | accepted | TestBorderShorthand |
| D-76 | Three `gdocs://` resource templates carry a document as markdown; there is no static list and no subscription | accepted | TestResources |
| D-77 | The released tool surface is a contract the schema diff holds; the prose of a text output is not part of it | accepted | schema-diff, TestSchemaDiffBreaksOnALostOutputField, TestUndeclaredBreaks |
| D-78 | A change to the tool surface names each changed item in a commit footer | accepted | schema-ack, TestSchemaAckNeedsEveryItemNamed, TestARefusalNamesTheFooterThatFixesIt |
| D-79 | The schema dump is what an in-memory client session lists | accepted | TestDumpSchemas, TestDumpSchemasListsEverythingAClientIsTold |
| D-80 | Unit tests are network-free on synthetic fixtures, and a fake answers with the errors Google documents | accepted | review |
| D-81 | The coverage floor holds every package `go list` derives, and each exemption names its reason | accepted | cover, TestEveryExemptionNamesARealPackage, TestEveryExemptionHasAReason |
| D-82 | A guarantee stated over the tools is tested over every registered tool | accepted | TestEveryToolHasArgs, TestDebugLogsCarryNoDocumentData |
| D-83 | The live driver drives every tool and op kind through the binary over stdio; it is run by hand, never in CI | accepted | live, TestCoverage |
| D-84 | Agent evals score whether a model can do the job through the tools; they are run by hand | accepted | TestEvals, live |
| D-85 | `make check` and CI run the same gates | accepted | parity, TestParityGate, TestParityCatchesDivergence, TestParityHoldsMakeCheckToWhatAPushRuns, TestParityHoldsThePullRequestGates, TestParityHoldsAGateCallsArguments, TestParityHoldsTheTreesTagsToAVetPass, TestTheReleaseIsReleaseYMLAndWhatItCalls |
| D-86 | Every action is pinned to a full commit SHA, and every tool an action installs to one exact version | accepted | pins, TestPinsGate, TestUnpinnedTools, TestPinsNamesEachProblem, TestPinsRejectsEveryRangeForm, TestAToolDownloadedAtLatestIsNotPinned, TestReadersRefuseALineBreakTheyDoNotRead |
| D-87 | CI runs on Linux, macOS and Windows, and every `run` step in bash | accepted | pins, TestPinsShell, review |
| D-88 | A workflow is read-only at the top, raises write per job, and keeps no checkout token | accepted | review |
| D-89 | CodeQL runs on pushes, pull requests and weekly | accepted | review |
| D-90 | A tag is the whole release, and its notes are that version's CHANGELOG section | accepted | release-notes, TestReleaseNotesTakesOnlyItsOwnVersion, TestReleaseNotesRefusesAnEmptySection |
| D-91 | A release is signed for people and attested for machines | accepted | TestTheReleaseAttestsItsArchivesBundleAndChecksumFile, TestReleaseAttestProblems, review |
| D-92 | Changes reach main through a pull request with CI green; no second reviewer is required | accepted | review |
| D-93 | Each release carries a Claude Desktop bundle in the layout the sibling servers share, its settings derived from config.Vars | accepted | mcpb, TestBundleVarsAreDerivedFromTheSettings, TestThePackedBundleLayout, TestTheCommittedManifestIsValid |
| D-94 | A bundle install starts the server as an unconfigured start does, every setting passed by env | accepted | mcpb, TestTheConfigDiffNamesTheFieldsThatDiffer, TestTheBundleSettingsRefuseEachBreak, TestVarsDescribeWhatBuildDoes |
| D-95 | The server asks the person before a risky write, through form elicitation | rejected | — |

### D-01 One stdio binary per person, signed in to their own Google account through their own OAuth client; nothing is hosted

The server is one Go binary that a client starts as a subprocess and
talks MCP to over stdio. It holds one person's refresh token and calls
Google as that person, through an OAuth client that person created.
Nothing is hosted, and there is no shared deployment.

Each person creates their own Cloud project, enables the Docs API and
the Drive API, and creates a Desktop app OAuth client. Nothing
deployer-specific is built into the code, the repository or a release,
so a copy of the binary does nothing until someone supplies their own
client. The reason is reach. A `drive` token opens every document its
owner can open (D-68), and per-person sign-in keeps every call inside
that person's own access, with the token in their own keyring (D-67).

Rejected: a hosted, multi-tenant server, which would hold many people's
tokens; a Docker image, since the loopback sign-in (D-66) and the
keyring assume the person's desktop; Google's own Docs MCP server, whose
tools pass the raw `documents.get` JSON and `batchUpdate` requests
through and leave the index arithmetic to the model.
Cost: each deployer creates a Cloud project and a Desktop OAuth client
before the first `login`. Any External app in Testing gets refresh
tokens that expire after seven days, whatever its scopes, so a consumer
account signs in again each week
([token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)).
An Internal app in a Workspace organization is exempt.

### D-02 The scope is one document and everything inside it, its history included; folders, moves, sharing, trash and copies are out

The scope is one document and everything inside it: reading, finding,
creating, exporting, editing, formatting, tables, tabs, headers,
footers, footnotes, objects, chips, named ranges, layout, comment
threads, suggestions and revisions. Locating a document by title or
content is in scope too, because every task starts there. Folder
management, moves, sharing, trash and copies are out.

The hard part of working in a document is inside it: index arithmetic,
edits that keep what is anchored around them, and suggestions a person
reviews. A server that also organized files would split its tools
between two jobs, and a person who runs google-drive-mcp beside it would
get two tools for one act. This server uses the Drive API only where a
document needs it: search, comment threads, revisions, exports, and the
uploads that write a Markdown or Word file (D-39, D-41, D-42, D-49).

Rejected: a general Drive client; folder management, moves, sharing,
trash and copies, which are operations on a file rather than on a
document.
Cost: `search_documents` only locates a document. Moving, sharing or
copying one takes another tool, such as google-drive-mcp.

### D-03 Stdout carries only MCP JSON-RPC frames, and logs go to stderr through slog

Stdout carries only MCP JSON-RPC frames, and every log line goes to
stderr through `slog`. A client reads each line of stdout as a frame, so
one stray print breaks the session, often with no message a person can
read.

Only `main` names the process's streams. It passes stdout and stderr
down once, and every other package takes an `io.Writer` or a logger, so
nothing below `cmd/google-docs-mcp` reaches stdout by accident.
`forbidigo` refuses `fmt.Print*` and `os.Stdout`, and
`TestNothingPrintsOutsideTheBoundary` holds `main.go` to its few
writers. `status --json` writes exactly one JSON value. The `smoke` gate
drives the built binary over stdio twice, the second time closing stdin
the moment the last byte is written, because a client that goes away
mid-call must be an ordinary exit and not a crash.

Rejected: a logging library with a sink of its own, which writes
wherever it was built to; a log level switch, since one print at the
wrong level still breaks the stream.
Cost: every package below `main` takes its writer as a parameter.
`scripts/`, maintainer tooling run at a terminal, is outside the
`forbidigo` rule.

### D-04 Logs carry method, tool, outcome and duration, never an id, a revision, a title, text or a query

A log line carries the method, the tool name, the outcome and the
duration. It never carries a document id or any part of one, a revision
id, a title, a document's or a comment's text, or a search query, at any
level. Per-call logging is this server's own middleware, at debug.

A debug log is what a bug report asks for, and a person should be able
to attach one without auditing it. A document is both personal data and
possibly confidential, which
[OWASP's logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
keeps out of logs, and when, where, who and what need none of it. A
transport failure's error text is the whole URL, query included, so the
client replaces it with the operation, the path with its ids taken out,
and the cause before anything reads it.
`TestDebugLogsCarryNoDocumentData` drives every registered tool and the
conflict path, and fails on the fixture's id, its first six characters,
its revision, its title or its text (D-82).

Rejected: ids truncated at debug, which still name the document; a
handler wrapper to raise the SDK's own log level, twenty lines to
suppress two lines of session chatter that a conditional already keeps
out.
Cost: a log cannot say which document a failure concerned, so a report
names the tool and the time instead.

### D-05 What doctor, status and the startup line print masks the account to its domain and drops a client id

`doctor`, `status` and the startup log line show the signed-in account
as its domain alone, `…@example.com`. A client-secret path is printed
with any Google client id taken out of the file name. `doctor` reports
counts about the document it checks, never its title or revision, and
its errors are masked the same way.

Their output is what a bug report carries: the README asks people to
paste `doctor` output into a public issue, and the startup line is in
the log they attach. The domain stays because it answers the question a
person reads that line for, whether the work account or the personal one
is signed in. The client id goes because the Cloud console names the
file it hands out after the client, so printing where the secret lives
would print the id along with it. A failure to read the client file
quotes its path, which is why the errors pass through the same mask.

Rejected: dropping the domain as well, which leaves a person with two
accounts unable to tell which one is signed in; printing the
client-secret path as given.
Cost: the domain is printed, which says which organization the person
belongs to.

### D-06 Nothing from a real document, account or deployment enters the repository; fixtures are synthetic, and two scanners hold it

Nothing from a real document, account or deployment enters the
repository: no organization names, document ids or URLs, account
addresses, Cloud project ids, OAuth client ids or secrets, and no text
from a real document. Fixtures are synthetic. `testdata/sample.json` is
written by hand, and `doctest.Large` generates a long document from
lorem text. Nothing deployer-specific ships either, so the binary works
only with a client someone supplies (D-01).

The repository is public, and a pushed commit cannot be taken back. Two
scanners hold the rule. gitleaks, in the pre-commit hook on the staged
changes and in `make check` and CI on every commit HEAD reaches, catches
secrets: client secrets and refresh tokens. The `leaks` gate catches
identifiers, which are not secrets and which no secret scanner flags: an
address at a domain someone could own, a 21-digit account id, a client
id, a user-content URL or a Drive id that does not look invented. Every
rule is an allow-list. The gate reads untracked files too, so it fails
before `git add -A` sweeps one in. It refuses a compiled executable or a
binary over a megabyte, since no text rule can read one.

Rejected: a deny-list of what to look for, which would itself be the
disclosure; gitleaks alone, which cannot see a document id or an
address; scanning only the index; a marker comment that lets a binary
through.
Cost: the history scan runs only with
`LEAKCHECK_HISTORY=1 go test ./scripts/gates`, outside `make check`. A
binary over a megabyte cannot be committed.

### D-07 A transcript the live driver or the evals write goes through one redactor, which carries no build tag

Everything the live driver and the agent evals write to a transcript
goes through `internal/redact`, which masks document ids and URLs,
revision ids, addresses and people's names. The package carries no build
tag, so its tests run in every `make check`, while the drivers it serves
run only by hand.

Transcripts are pasted into issues and commit messages. An id, a URL and
an address have a shape, so a pattern finds them. A name has none, so a
person is caught by position, after the labels this project's renderers
write. A person chip prints a name with no label before it, so the
driver reads the account's display name before any step can print a
chip, and that name is replaced wherever it appears. Redaction happens
where a line is shown, never where a result is read: a step parses the
untouched text, since a scrubbed URL fed back to Google is refused. The
`transcript` gate, `TestDriversDoNotPrint` and
`TestEveryPlaceAPersonIsWrittenIsKnown` keep every printed line on that
one path.

Rejected: scrubbing a result where it is read, which corrupts what a
step feeds back; redacting by shape alone, since a name has none; a
typed table of positions, which drifts from the renderers; finding
person fields by following assignments, which spreads through names like
`User` until nearly every read counts.
Cost: a person is redacted to the end of the line, and what follows goes
with them, because a display name has no reliable end.

### D-08 Own wire types over raw REST: no generated client, and no code from another project

The server calls the Docs API and the Drive API over raw REST. Requests
are built by hand, and responses decode into wire types written in
`internal/gdocs` and `internal/gapi`. No generated Google client is
imported, and the repository shares no code with another project.

The generated `google.golang.org/api` module brings gRPC, OpenTelemetry
and the cloud auth stack into a binary that needs JSON over HTTPS,
`golang.org/x/oauth2` and a rate limiter. Hand-written types carry the
fields this server reads and sends, and hand-built requests keep every
call under the server's own control: the host check, the limiters, the
retries and the deadline (D-11, D-61, D-63). `depguard` refuses an
import of `google.golang.org/api`, with the reason in its message.

Rejected: the generated `google.golang.org/api` module, for the
dependencies it brings and the control it takes over each request.
Cost: hand-kept types drift from the API as Google adds fields, and a
field the types lack is dropped from every decode. The `api-fields` gate
holds them to the discovery document (D-09).

### D-09 Every method Google publishes, and every field of a type this server models, has a verdict

Every method the Docs API and the Drive API publish has a verdict, used
or left out with a reason, and every field of a type `internal/gdocs`
models is modeled or written off with a reason. The `api-coverage` gate
holds the methods to `testdata/api-methods.json` and
`testdata/api-coverage.tsv`. The `api-fields` gate holds the fields to
`testdata/api-fields.json` and `testdata/api-fields.tsv`.

A method nobody judged is a feature nobody chose to leave out. A
published field the types lack is dropped from every decode, so a read
cannot show it and a write cannot set it. Careful reading does not keep
hand-written types in step; a gate does. `api-fields` fails on a
published property nothing models, a modeled field nothing publishes,
and a struct that matches no published schema and has no row saying why.
It also fails on a row that does not match the types as they are. A
field is judged by one rule: if a tool writes it, the types carry it.

Rejected: a floor under the number of schemas matched, which a rename
stays above; the verb and path kept in the hand-written file; refetching
the discovery documents in CI, a check that fails when Google is slow.
Cost: `make api-diff` refetches the snapshot by hand, so a field Google
adds is seen only when someone runs it. The Drive types are inline
anonymous structs, which `api-fields` cannot match by name.

### D-10 Everything the repository runs on itself is Go, in one `scripts/gates` program

Every check the repository runs on itself is Go: one `scripts/gates`
program, behind one registry of commands. The Makefile and CI call it,
and the pre-commit hooks call a `make` target or `go`. There is no shell
script.

Checks written as scripts failed where nobody looked. A hand-written
package list stopped covering a package added later, a rule failed on
the very pull request it guards, and a check ran only on Linux because
the other runners' shells could not be relied on. A check in Go derives
its inputs from the code, holds a floor on how much it read before it
reports nothing wrong, and runs the same on the three platforms CI uses
(D-87). The one registry is what the usage text, the dispatch and the
`parity` gate read, so they cannot drift apart (D-85).

Rejected: shell scripts; a separate program for one job, such as release
notes, beside the one that already answers it (D-90).
Cost: a one-line check is a Go function. A workflow's `run:` step is
still a shell line, which is why every workflow pins bash (D-87).

### D-11 The server talks only to Google's hosts over HTTPS, matched with the port before a credential is attached

The server sends a credential only over HTTPS, and only to Google:
`googleapis.com` and its subdomains, `*.googleusercontent.com` and
`docs.google.com`. Every URL is checked before the token is attached,
matched on the host with its port, so `docs.googleapis.com:8443` matches
nothing and is refused. There is no telemetry, crash reporting or update
check.

The check decides whether a credential leaves the machine, and not every
URL is one the server built. An old revision's export follows a download
URL taken from a long-running operation's response body (D-41), and the
token goes with the request. A check on `url.URL.Hostname()` would drop
the port and pass a host it never meant to. Where a check that governs
where a token goes is wrong, it should be wrong toward refusing. The
allowlist governs the API client; sign-in and the token refresh go to
the endpoints the OAuth client file names (D-66).

Rejected: matching on `url.URL.Hostname()`, which drops the port;
telemetry, crash reporting and update checks, which would send something
somewhere else.
Cost: a Google host reached on an explicit port is refused.

### D-12 The model never sees or computes a UTF-16 index; the server converts at the boundary

The model never sees or computes a UTF-16 index. It addresses content by
exact text, heading id, handle or named range (D-13, D-14, D-15), and
the offsets it does see, in find results, are Unicode code points. The
server converts at the boundary, and `internal/plan` owns every piece of
index arithmetic on the write path.

The Docs API counts in UTF-16 code units. The body, each header, each
footer and each footnote is its own index space starting at zero, and
every index shifts after each change and holds only for the revision it
was read at
([document structure](https://developers.google.com/workspace/docs/api/concepts/structure)).
Go strings are UTF-8, and a model counts in neither. A server that
leaves the arithmetic to the model puts text one place off after an
emoji or an accented letter, and an index taken from a stale read lands
in the wrong place.

Rejected: the model computing indices, as other open-source Docs servers
have it.
Cost: a dry run lists the kinds of request a write would send, not the
requests, since those carry indices (D-35).

### D-13 Exact text is the primary target: normalized on both sides, matching once, narrowed by occurrence or within

An edit's primary target is an exact passage of text. It must match
once, after both sides are normalized: curly quotes, non-breaking spaces
and runs of whitespace compare equal to their plain forms. `occurrence`
picks one of several matches, and `within` narrows the search to a
section or a block. A text matching several places is `[ambiguous]`, and
the message lists where.

Other editing tools converge on exact text: Anthropic's text editor
tool, Claude Code's Edit, Notion's MCP `update_content` and OpenAI's
`apply_patch` each take an exact old string. Aider measured line numbers
in edit targets as harmful, and removing them took GPT-4 Turbo from 20%
to 61% on its benchmark. Exact text also states what the model believes
is there, so a passage that changed since the read fails to match rather
than being overwritten. The live step "direct edit" targets by exact
text.

Rejected: line numbers or positions in an edit target.
Cost: a passage that repeats needs `occurrence` or `within`, and a model
that quotes loosely gets `[not_found]`, with a pointer to
`find_in_document`.

### D-14 Headings are addressed by headingId and other blocks by short handles the server remembers per document and re-checks

A heading is addressed by its `headingId`, which Google keeps stable.
Every other block is addressed by a short handle: `p12` for a paragraph,
`tbl3` for a table and `tbl3:r2c3` for one of its cells, `sb1` for a
section break. Handles count from the start of their segment, with a
prefix outside the first tab's body, such as `tab2/p12` or `header1/p1`.
Reads show them by default, and `with_handles: false` drops them.

The server remembers, per document, the revision and the text of each
handle from the last read in this process. At the same revision a handle
resolves exactly. At a later one it is found again by its remembered
text, which must match once, or it is `[stale]`. A handle never read is
`[unknown]`, with a pointer to read the section first. One resolver
serves reads and writes, so both check handles alike. Handles are on by
default because the agent evals showed the model asking for them on most
reads and targeting by handle as often as by text. The live steps "read
without handles" and "read after create" read both ways.

Rejected: fingerprinted handles such as `p12-7f3a`, which are harder to
read and have no evidence behind them; handles off by default on full
reads.
Cost: a handle holds only for the revision it came from, and handles
cost about 15% more tokens per read.

### D-15 A named range is the one target that outlives an edit, and a name several ranges share is refused as a target

A named range is the one target that outlives an edit. Google keeps a
named range with the content it covers as the document changes
([NamedRange](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents#NamedRange)),
so a passage named in one call is reached by `named_range` in a later
one, where a handle holds only for its revision (D-14). `edit_document`
creates, deletes and replaces named ranges, and `get_document` lists
them.

A target must mean one range. A name that several ranges share is
refused as a target, and the range id `get_document` reports names one.
`replace_named_range` writes every range the name covers, so the
overwrite guard sees what all of them hold (D-28). `delete_named_range`
forgets the name and leaves the text, so it is not guarded. A name is
counted in UTF-16 units, as the API counts it. The live steps "edit
through the named range" and "replace the named range's content" use
one.

Rejected: picking the first of several ranges that share a name, which
edits a passage the caller may not have meant.
Cost: `replace_named_range` overwrites every range the name covers,
which the guard shows before a direct write lands.

### D-16 A document is read with every tab in the inline suggestions view, the one index space, and every write names its tab

A document is always read with every tab's content and in the inline
suggestions view, and every write names the tab it touches.
`documents.get` is called with `includeTabsContent=true` and
`suggestionsViewMode=SUGGESTIONS_INLINE`, and the inline view's indices
are the one index space this server computes against.

Only the inline view gives indices a later `batchUpdate` accepts
([suggestions](https://developers.google.com/workspace/docs/api/how-tos/suggestions)),
so planning on any other view puts a write at the wrong offsets once a
suggestion is pending. Tabs are where a document's content can hide.
Without `includeTabsContent` a read returns the first tab alone, and a
request without `tabId` reaches the first tab, except `replaceAllText`
and the named-range requests, which reach every tab
([tabs](https://developers.google.com/workspace/docs/api/how-tos/tabs)).
Servers that ignore tabs edit the wrong one. So every location and range
the planner builds carries its `tabId`. The live step "read with styles"
reads a change planned below pending suggestions back on its target.

Rejected: reading the first tab alone, which leaves content out; reading
in a view with suggestions accepted or hidden, whose indices a write
cannot use.
Cost: `replace_all` always sends explicit `tabsCriteria`, since
`replaceAllText` defaults to every tab, and a pending suggestion's text
is part of every read's index space.

### D-17 Every read asks for the comments view and, refused, asks again without it; that read decides pinning and suggest mode

Every read asks `documents.get` for the comments view,
`commentsViewMode=COMMENTS_VIEW_MODE_INCLUDED`. If Google refuses it as
invalid or forbidden, the read asks again without it, and a refusal
about the document itself fails both times. Whether the view came back
decides two things for that read. A comment is pinned through the Docs
API or posted through Drive (D-38), and suggestion mode is allowed or
refused `[unavailable]` (D-29).

Comments and suggestions in the Docs API are generally available
([release notes](https://developers.google.com/workspace/docs/release-notes)),
but they reached projects in a staged rollout, and the discovery
document labels the fields Developer Preview. So the server detects what
each project gets instead of trusting an announcement or a flag. A
project the feature has not reached might apply a `SUGGEST` batch as a
direct edit, the overwrite the person asked to avoid. The live steps
"suggest edit" and "add comment" use what the view opens.

Rejected: a setting that gates the features, since a deployer cannot
know what Google enabled for their project; joining the Developer
Preview; trusting the announcement, or the shapes appearing in the
discovery document, without detecting.
Cost: every read carries the document's comment threads, a share of each
`documents.get` nobody has measured. Issue #107 asks to re-check
general availability from a project that never joined the preview, and
to measure a read with and without the view.

### D-18 Reads are scoped and budgeted by default and say where to continue

Every read is scoped and budgeted by default, and says where to
continue. `read_document` takes a scope, a tab, a heading, a range of
handles or the whole body, and a character budget, `max_chars`, 20 000
by default and 400 000 at most. It cuts at a block boundary and returns
`continue_from`, which resumes there. `diff_revisions`,
`export_document`'s inline formats and the resources are budgeted the
same way (D-76).

Claude Code warns when a tool result passes 10 000 tokens and cuts it at
25 000 ([MCP output limits](https://code.claude.com/docs/en/mcp)). A cut
the server did not make leaves the model reading half a paragraph, with
no way to ask for the rest. A budget applied at a block boundary, with a
continuation, keeps every read whole and resumable. A view that claims
to be complete keeps every block, empty paragraphs included, because a
model edits what it was shown. The live step "a budgeted read" reads
one.

Rejected: dropping empty paragraphs from a view that claims to be
complete, which makes the read disagree with the document it describes.
Cost: a long document takes several reads, each a `documents.get` unless
the five-second cache answers it (D-64).

### D-19 Read tools return a text block only; write tools return text and a structured result that carries everything the text does

A read tool returns one text block and no structured result. A write
tool returns a text block and a structured result, and its JSON carries
everything the text does, the rendered preview included. The read tools
are `get_document`, `get_outline`, `read_document`, `find_in_document`,
`search_documents`, `export_document`, `list_suggestions`,
`list_comments`, `list_revisions` and `diff_revisions`. Each puts the
revision and any continuation in a header line.

A client may show the model a result's text or its structured form, not
both. Claude Code shows only `structuredContent` when a result has it,
so in the agent evals a scoped read with a small structured result
looked like metadata, and the model fell back to reading the whole
document. The MCP specification makes the text the backwards-compatible
form
([structured content](https://modelcontextprotocol.io/specification/2025-11-25/server/tools#structured-content)).
go-sdk emits neither an output schema nor structured content for a tool
whose output type is `any`, which is how a read declares itself. A
write's result is small, and its fields are what a caller acts on, so it
carries both forms.

Rejected: markdown plus a small structured result on reads, which Claude
Code shows as the structured part alone.
Cost: read tools carry no output schema, so a client cannot validate a
read's shape.

### D-20 Reads mark suggestions as CriticMarkup and comments as markers

A read marks pending suggestions in CriticMarkup and comments with
markers. With `include_suggestions`, an insertion reads `{++text++}` and
a deletion `{--text--}`, each followed by `{>>s:<id><<}`. A suggestion
that only restyles reads as a highlight,
`{==text==}{>>s:<id> suggests bold<<}`, because nothing was added or
removed. With `include_comments`, a commented passage ends in
`{>>c:<id><<}`, and its thread is listed below the content.

A model has to see what is pending before it edits around it, in the
text it reads, at the place it applies. CriticMarkup is an existing
plain-text convention for tracked changes, which MultiMarkdown 6 reads
and other document servers use, so the marks mean something without a
legend. The ids in the marks are what `review_suggestion` and
`reply_comment` take (D-40). The live steps "read with suggestions" and
"read the suggested table and image" read them back.

Rejected: showing pending suggestions and comments only through their
own tools, which leaves a read presenting text as if nothing were
pending; a markup of this server's own.
Cost: a read with marks is longer, and its text is not the document's
text until the marks are taken out, so both are off by default.

### D-21 A suggestion is read from its suggestion state, never from the style beside it

A pending suggestion is read from its suggestion state, never from the
style beside it. A suggestion that only restyles inserts and deletes
nothing, so it is found through the `suggested*Changes` maps on runs,
paragraphs and cells, not through the suggestion ids on inserted or
deleted text. What it changes comes from its `*SuggestionState` flags.
The wire types carry every `suggested*` field the discovery document
publishes.

The style in a suggested change is the style as it would be after
accepting, every property filled in, so reading it reports a change to
every property where a person asked for one
([SuggestedTextStyle](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents#SuggestedTextStyle)).
A suggested insertion also carries an entry under its own id, which says
the style the inserted text takes, its flags marking the properties it
inherits. The server applies that entry to the run's style and reports
only other suggestions' entries as restylings. The integration tests
TestRawSuggestedStyle, TestRawSuggestedReset and TestRawAcceptedView
check both readings, and the live step "list_suggestions" lists a
style-only suggestion.

Rejected: walking insertion and deletion ids alone, which cannot find a
restyling; reading the style, which reports every inherited property;
dropping an insertion's own entry, which hides the bold of text inserted
after bold text.
Cost: one case stays unexplained, a reset filed under a second
suggestion id. Issue #109 covers it, together with suggested
insertions in table cells and rows, which no test probes.

### D-22 `format: raw` returns the bytes Google sent

`read_document` with `format: raw` returns the JSON Google sent for each
element in scope, compacted, not the server's own types marshaled again.
Each structural element keeps the bytes it was decoded from, and an
element built in Go falls back to marshaling its type.

A raw read is the one read whose whole job is to show what Google said.
Marshaled from the wire types, it would show only the fields this server
models and silently drop the rest, so a field proving that a write had
worked would be invisible. Keeping the bytes ends that class of gap
rather than one instance of it, at the cost of one field, one method and
one branch. Every request asks for compact JSON, `prettyPrint=false`
([system parameters](https://cloud.google.com/apis/docs/system-parameters)),
which takes a long document's response to well under half its indented
size and pays for the retained bytes. The integration test
TestPrettyPrintFalseIsAccepted checks that, and the live step "read
format raw" reads one.

Rejected: modeling every field Google publishes so a marshal could show
it, which keeps a raw read one step behind each field Google adds.
Cost: an element's bytes are held again inside every ancestor's, so a
document nested deeply in tables retains its cells once per level of
nesting.

### D-23 get_document reports what no read of the text shows, the named style definitions included

`get_document` reports what no read of the text shows. Per tab, that is
the page setup, floating objects, named ranges, and the definition of
each named style a paragraph in the tab uses, every value named:
`named style HEADING_2 (3 paragraph(s)): Arial 16pt, bold, color #1a73e8, space above 18pt`.
Standard page sizes come back by name, so the model need not recognize
612×792.

Google reports only the properties a run or a paragraph sets itself, so
`with_styles` on a read shows exactly where text departs from what it
inherits. The other side of that is that inherited formatting is
invisible: a heading whose named style is blue and centered reads as
plain markdown. Without the definitions, redefining a named style with
`layout_document` would be a write nothing reads back. One line per
style per tab costs far less than a comparison on every run. A
definition's count covers every segment, headers and footnotes included,
since redefining a style changes the whole tab. The live step "read the
redefined HEADING_2 back" checks it.

Rejected: comparing each run against its named style on every read,
which repeats per paragraph what one line per tab says.
Cost: inherited formatting is invisible in a read of the text, so a
model that needs it calls `get_document`.

### D-24 A binary export is written only under GDOCS_EXPORT_DIR, created exclusively, never over a file or through a link

`export_document` returns `md`, `txt` and `html` inline, budgeted, and
writes `pdf`, `docx`, `odt`, `rtf` and `epub` as a file under
`GDOCS_EXPORT_DIR`, and only there. Each file is created exclusively,
with `O_EXCL`, so an export never replaces a file already there and
never writes through a symbolic link at its name. A name already taken
gains a counter, `-2` and up. With the directory unset, a format that
writes a file is refused `[unavailable]`.

The export is the one tool that writes to the person's disk, and a model
chooses its arguments. Cleaning the path keeps the name inside the
directory but not what the name points at, and an ordinary write
truncates whatever was there. Creating exclusively makes choosing the
name and creating the file one step. The file is new, so the tool is
annotated as a write that only adds (D-57). It stays registered in
read-only mode, since it changes nothing in Google (D-55).

Rejected: `os.WriteFile`, which truncates an earlier export and follows
a link; treating the export as read-only, which hides a local write from
a client's prompt.
Cost: after 100 taken names an export is `[blocked]` until earlier
exports are moved, and read-only mode still registers `export_document`.

### D-25 Markdown is for new content; existing content is edited in place and never round-tripped

Markdown is how the model writes new content: an insert, an append or a
replace takes markdown, and the server compiles it into requests.
Existing content is never rewritten through markdown. It is edited by
range, styled by explicit format ops, and read with style annotations on
request (D-23).

A Google Doc holds what markdown cannot say: fonts, colors, spacing,
comments anchored to a passage, suggestions, chips and objects.
Exporting someone else's document to markdown and writing it back would
drop all of it, while a minimal diff of the text a call replaces keeps
everything outside the change (D-27). A Markdown file in Drive is a
different case: its bytes are markdown, so a minimal edit of them is no
round trip (D-42). The live step "direct edit" inserts markdown content
beside a minimal-diff replace.

Rejected: round-tripping a document through markdown, which loses
everything markdown cannot express.
Cost: what markdown cannot say goes through `format_document`: fonts,
sizes, colors, alignment, spacing, indents, named styles on existing
text, bullet presets and clearing formatting.

### D-26 The markdown parser converts what it can prove and refuses the rest, naming the construct and its line

The markdown parser converts what it can prove the Docs API can express
and refuses the rest. A refusal is `[unsupported]`, naming the construct
and its line with a hint, before anything is written. The parser takes
headings, paragraphs, emphasis, inline code, links, nested lists, hard
breaks, fenced code, and tables and images between blocks (D-31). It
refuses HTML blocks, block quotes and horizontal rules, and an image
inside text or a list item, which `insert_object` places instead.

A converter that degrades silently corrupts a document in a way nobody
sees until later, and the person finds the damage after the write. A
refusal costs one more call, with the reason in hand. Every construct
has a test. Text read and written back is unchanged, which a randomized
test holds, because a read escapes exactly the characters that markdown
would take as markup.

Rejected: a guarantee of WYSIWYG fidelity, which no conversion between
the two formats can keep; a converter that drops what it cannot express
without saying so, as other open-source servers' converters do.
Cost: HTML, block quotes and rules are refused, and a task-list checkbox
is dropped while its text is kept.

### D-27 A replace is the minimal word-level diff of the old text against the new

A `replace` is compiled as the minimal diff of the old text against the
new. Only the changed hunks become `deleteContentRange` and `insertText`
requests, grouped to word-sized pieces by a semantic cleanup. Unchanged
spans keep their formatting and everything anchored to them: comments,
suggestions and objects. When the replacement changes the paragraph
structure, such as one paragraph becoming three list items, the planner
replaces the whole range, and the result says the op was not minimal.

A server that overwrites the range it edits deletes every comment
anchored inside it and every suggestion a person left there. Inserted
text takes the style of the text before it, and a newline copies the
paragraph's style "including lists and bullets"
([InsertTextRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#InsertTextRequest)).
So a minimal diff inherits the surrounding formatting with no style
requests at all. Google's version history records every batch under the
signed-in account, so what changed and who changed it stays visible. The
live step "direct edit" checks it.

Rejected: replacing the whole range, which loses what is anchored to
unchanged text.
Cost: a replace that changes paragraph structure falls back to the whole
range, and the overwrite guard then reports what that range holds
(D-28).

### D-28 A direct edit that would delete comment anchors, suggestions, objects or footnote references is refused unless forced

A direct edit that would delete a range holding comment anchors, pending
suggestions, inline objects or footnote references is refused
`[blocked]` unless the call passes `force`. The refusal names what is
inside, such as
`range contains 2 comment anchors (c:AAAAB, c:AAAAC) and 1 suggestion`,
and the ways forward: suggest or comment mode, a narrower target, or
`force`. A forced op's warnings name what it destroys, and a dry run
shows them first. In suggest and comment modes nothing is deleted until
a person accepts, so the guard only reports.

Deleting a range removes whatever is anchored inside it, and a model
replacing a sentence does not know that a colleague's comment sits on
it. The guard checks every range scheduled for deletion against the
anchors of the read the write was planned on, the minimal diff's hunks
included (D-27). The same guard holds a Markdown file's commented
passages and a Word file's comments (D-43, D-50). `force` is for a
person who chose direct editing knowing what the range holds.

Rejected: relying on suggest mode alone to protect what is anchored,
which leaves direct mode free to delete it; a guard that only warns,
which a model reads past.
Cost: `force` stays a boolean that a model can pass. Its warnings, and a
dry run before it, are what show what it destroys.

### D-29 The person chooses how changes land, suggest, direct or comment, and the server never substitutes one mode for another

Each edit takes `mode`: `suggest`, `direct` or `comment`. `suggest`
sends the batch with `writeMode: SUGGEST`, so each change is a
suggestion a person accepts or rejects. `direct` edits the text.
`comment` changes nothing and posts each op as a comment on the passage
it would change: a replace as "Proposed change:" with the new text, a
delete as "Proposed deletion". The deployer sets the default with
`GDOCS_DEFAULT_WRITE_MODE`, `suggest` unless set, and a call overrides
it when the person asks.

The person decides how a change lands, and a mode they did not choose is
a change they did not approve. So the server never substitutes one:
where a mode cannot work, it refuses and names the modes that can.
Suggest mode is `[unavailable]` on a read without the comments view
(D-17) and on a Markdown file, for which Docs offers no suggestions
(D-42). A request the API refuses in `SUGGEST` mode, such as adding a
tab, deleting a header or creating a named range, is refused for its op
before anything is sent. The live step "suggest edit" writes one.

Rejected: sending suggest where the comments view was refused, which a
project the feature has not reached might apply directly.
Cost: with the default `suggest`, a write to a Markdown file is refused
until the person chooses direct or comment, and `manage_tabs` always
writes directly.

### D-30 Every write is planned against one revision and guarded by it, re-planned once on a conflict, and reports what it touched

Every write is planned against one fresh read and sent with that read's
revision as `writeControl.requiredRevisionId`. `expect_revision`, when
given, is checked against the read before anything is sent. If someone
edits between the read and the write, Google refuses the batch, and the
server reads again, resolves every target again, plans again and sends
once more. A second conflict is `[conflict]`. A write reports the new
revision, the ops applied, previews of what changed and any warnings.

Google answers a stale `requiredRevisionId` with 400 INVALID_ARGUMENT,
"The required revision ID '…' does not match the latest revision."
([WriteControl](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/batchUpdate#writecontrol));
the reference documents only the 400. The integration test
TestRawStaleRevision checks it. A batch is atomic, and every target is
resolved before anything is sent, so a call fails whole or plans whole.
A model that gets back the revision and what changed can check its own
edit without reading the document again.

Rejected: writing without a revision guard, which lets a stale plan land
on text a person just changed; re-planning until a write lands, which
can loop against an active editor.
Cost: what can only follow a write, such as filling a table it inserted,
goes in later batches, each against a fresh read, each reported and none
rolled back (D-31, D-32).

### D-31 Content with tables or images is written in rounds, each against a fresh read at the revision the last reply gave

Content holding tables or images is written in rounds. The first batch
writes the text with an empty paragraph, a slot, where each table or
image goes. Later batches fill the slots one at a time, highest first,
each against a fresh read. That read must be at the revision the
previous batch's reply gave, and must still show the slot where the text
left it. Otherwise somebody else wrote in between, and the rest are
reported, not placed. Such content is the only op of its call, so
nothing else in the first batch moves the slots.

An image goes into its slot. A table goes in at the newline of the
paragraph before the slot, is filled, and a last batch deletes the empty
paragraphs it leaves, except where Docs needs one. The reference
documents only part of where `insertTable` puts a table
([InsertTableRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#InsertTableRequest)),
and the integration test TestRawTablesInContent checks the placement.
The live steps "content with a table and an image, suggested" and
"content with an image Google cannot fetch" drive both outcomes.

Rejected: placing every table and image by index arithmetic in one
batch, which rests on a formula for a table's length checked at a few
sizes only.
Cost: the rounds are not atomic. A round that fails leaves the text
written and its empty paragraph, with a warning naming the content's
line. One call holds at most 10 tables and images, a table at most 200
rows and 20 columns, and an image address at most 2 kB.

### D-32 Ops addressed in a table after a grid change wait for a batch of their own against a fresh read

After an op changes a table's grid, every later op in the same call
addressed in that table's rows, columns or cells waits for a batch of
its own, planned against a fresh read. So `insert_rows` then `set_cells`
on `r2c3` writes the cell the caller means, in the grid the insertion
left. `chainStructural` in `internal/service/followup.go` makes the
split before the ops are resolved, keyed by the table handle as written,
one round per grid change. A round that fails stops the rest, since they
were numbered for a grid that never came about. Comment mode does not
split: nothing is applied, and every op must reach the proposals. The
planner keeps its rule of one grid change per table per batch as a check
that the split was made. The live step "three grid changes on one table:
a batch each, renumbered between" drives it against Google.

Rejected: numbering a later op against the grid as read, which reaches
the wrong row once rows are inserted before it; refusing a second grid
change on one table, which leaves the caller to split the call by hand.
Cost: a dry run cannot resolve the held-back ops, since the grid they
name does not exist yet, so it lists them and says so. What does not
depend on that grid, a row numbered below one or a malformed cell name,
is still refused first. A forced op in a later batch is refused
`[blocked]`, because no dry run showed what it destroys, so it runs as a
call of its own.

### D-33 A list item keeps a right inherited bullet, and the rest are rebuilt in windows that count levels from the item before

An inserted list item that took the right bullet keeps it, and every
other run of list items is rebuilt in a window. A rebuild deletes the
bullets, clears the indent, writes a tab for each level above the
window's base and makes the list again. When the item before is deeper
than the window, the window takes in the items before it, at their own
levels, until one is not. The content's leading list continues the list
it is placed against, at its level, unless `list_level` names one.

The rules are Google's, and its references state them only in part. An
inserted paragraph takes the bullet of the paragraph at the insertion
point
([InsertTextRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#InsertTextRequest)).
`createParagraphBullets` skips a paragraph that already has one, and a
level changes only by deleting the bullet and making it again
([lists](https://developers.google.com/workspace/docs/api/how-tos/lists)).
A list counts its levels from the item just before it, which no
reference says. The integration tests TestRawBullets and
TestRawBulletsWindow check these, and the live steps "an item after a
nested one gains a child" and "back to the top after a nested item by
list_level" drive them. `internal/plan/lists.go` holds the details.

Rejected: leading tabs and `createParagraphBullets` alone, which leave
an item that already has a bullet at its old level; tabs read as
absolute levels, which a list counted from the item before cannot start.
Cost: `deleteParagraphBullets` leaves the removed level's indent, so
every bullet deletion clears the indent back to the named style, and an
item indented by hand gets its own indents put back.

### D-34 A regex replace is expanded by the server into one replace per match, found with RE2 within a paragraph

A `replace_all` with `regex` sends no `replaceAllText`. The server finds
the matches itself, with the RE2 matcher `find_in_document` uses, over
each paragraph's index-aligned text in one tab. It plans each match as a
replace, or as a delete where the replacement expands to nothing, so the
overwrite guard, suggest mode and the minimal diff apply to each match
as to any replace (D-27, D-28). A match never crosses a paragraph and
covers text only, never a chip, a break or an element the server does
not model. A table of contents is skipped, the pattern decides case, a
group the replacement names must exist, and no match at all is
`[not_found]`. The live steps "regex replace_all" and "read after the
regex replace" read Go's expansion back.

The reason is the guard, which has to see every range a replace touches.
Google does not document which regex flavor `searchByRegex` runs,
whether its replacement expands groups, or how a match treats a chip or
an image
([ReplaceAllTextRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#ReplaceAllTextRequest)).

Rejected: the API's `searchByRegex`, whose matches the guard could only
guess.
Cost: a match stays within one paragraph, and a call replaces at most
500 matches in a tab, refusing a wider pattern.

### D-35 A write that takes dry_run resolves, plans and reports, and sends nothing

A write that takes `dry_run` resolves every target, plans the change,
runs the guard and reports, and sends nothing to Google. The report
gives the changes it would make, the guard's warnings, the kinds of
request it would send, the proposals in comment mode, a rendered view of
the region and the later batches it would run. It names request kinds
rather than requests, because a request carries UTF-16 indices, which
the model never sees (D-12). A delete, a comment or a review names what
it would act on, after the same lookup and checks the write makes.

The tools that take it are `edit_document`, `format_document`,
`edit_table`, `insert_object`, `layout_document`, `review_suggestion`,
`write_markdown_file`, `add_comment`, `delete_comment` and `delete_tab`.
The live driver calls every tool whose schema takes `dry_run`, fails on
one it has no call for, and checks with the live step "revision after
the dry runs" that the document did not move.

Rejected: a dry run as a substitute for the client's approval, since the
model decides whether to send one (D-52).
Cost: a dry run never plans a later batch, whose grid or slots do not
exist yet (D-31, D-32). `create_document`, `reply_comment`,
`manage_tabs` and `export_document` take none, so they cannot be
previewed; issue #105 proposes `dry_run` on each, with a test that a
dry run sends nothing.

### D-36 A direct formatting change over a property a pending suggestion sets is warned about, not refused

In direct mode, a formatting change over a property that a pending
suggestion on the same range already sets comes back with a warning
naming the suggestion and the property. It is applied, not refused.
`guardRestyle` in `internal/plan/ops.go` makes the check.

Google's behavior here is not one rule, and the reference says nothing
of it
([UpdateTextStyleRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#UpdateTextStyleRequest)).
Bold set directly over a suggested bold leaves the run unbolded, and the
same holds for a paragraph alignment: the batch is accepted, the reply
is empty, and nothing changes. A font size set to exactly the size a
suggestion names does land, and so does an alignment to a value the
suggestion does not name. The integration tests
TestRawSuggestedStyleSameProperty and TestRawSuggestedStyleValue check
both sides, and the live step "direct change to the suggested property"
drives it. What the person needs either way is to learn that a
suggestion sits on the property they set, which `ops_applied` cannot
tell them.

Rejected: refusing the change, which would block the writes that land.
Cost: the warning fires on every collision, the ones that land included,
since the rule is not known. Issue #106 asks for more properties to
be probed until the rule is found and reported to Google, so the warning
can name only the changes Google drops.

### D-37 Every GA request of the Docs API and every field it accepts on a write is emitted; a read-only field is reported, never sent

The planner emits every generally available member of the Docs API's
`Request` union, and every field those requests accept on a write,
except the comment-thread requests Drive serves (D-39). The union and
its fields are taken from the discovery document,
`https://docs.googleapis.com/$discovery/rest?version=v1`, never from the
reference page's prose. A field the discovery document marks read-only
is read and reported, never sent: `tabStops` and `headingId` on a
paragraph, a cell's `rowSpan` and `columnSpan`, and
`SectionStyle.sectionType`, which only `insertSectionBreak` sets. The
live step "a section's type is read-only" checks that the server refuses
one.

A field in the schema is not proof the API takes it.
`updateTableRowStyle` answers `tableHeader` with 400 "Unallowed field:
tableHeader", which the reference does not say
([UpdateTableRowStyleRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#UpdateTableRowStyleRequest)),
so `pin_header_rows` is how a header row is set.
`testdata/api-coverage.tsv` holds a verdict for every published method,
`testdata/api-fields.tsv` for every published field of a modeled type
(D-09), and `TestKindRegistryCoversEveryTool` holds each op kind to its
tool.

Rejected: sending a field the discovery document marks read-only, which
fails the whole atomic batch; taking wire names from the reference
page's prose.
Cost: a request member or field Google adds is emitted only once someone
adds it, and `make api-diff`, which refetches the discovery documents,
runs by hand.

### D-38 A comment is pinned through insertComment where the comments view answered, else posted through Drive quoting the text and said to be unanchored

A comment on a passage is pinned to its range through the Docs API's
`insertComment` when the read that resolved the target carried the
comments view (D-17). Otherwise it goes through Drive's
`comments.create`, quoting the passage, and the result says it is
unanchored. A comment with no target is on the document as a whole and
goes through Drive. An assignee needs a pinned comment; without one, the
comment posts with a warning that it was not assigned.

Pinning is the point, because a comment on its passage is how a person
reviews a change in place
([InsertCommentRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#InsertCommentRequest)).
Drive cannot pin one in a Google Doc: "Google Workspace editor apps
treat these comments as un-anchored comments"
([manage comments](https://developers.google.com/workspace/drive/api/guides/manage-comments)).
The live steps "add comment" and "document-level comment" drive both
paths.

Rejected: anchoring through Drive's comment anchors, which the editor
does not position for another app; refusing a comment where the comments
view is refused, which leaves the person no comment at all.
Cost: a comment posted through Drive sits on the document, not on its
passage. Whether Google notifies an assignee, as the editor does, is not
established; issue #110 asks for a check with a second account and a
line in the tool description either way.

### D-39 Thread operations go through Drive's comment API, one backend; a reply on a suggestion's thread is the exception

Every thread operation goes through Drive's comment API: listing,
replying, resolving and reopening through `replies.create` with its
`action`, editing a comment or a reply, and deleting one.
`list_comments` always lists through Drive, which carries every reply
with its author, time and action, and the resolved and deleted state, on
every deployment. Each thread is located in the document through the
comments view's anchors when the read carried them, else by its quoted
text, matching once.

One backend gives every thread one behavior, whichever path posted it,
and Drive's calls are generally available
([replies.create](https://developers.google.com/workspace/drive/api/reference/rest/v3/replies/create)).
The live steps "reply", "resolve", "reopen" and "edit a comment" drive
them.

A reply on a suggestion's thread is the exception. Drive cannot see
those threads, so `reply_comment` with `suggestion_id` sends the Docs
API's `addCommentReply`, which needs the comments view. The live step
"reply on a suggestion's thread" drives it.

Rejected: the Docs API's own thread requests, `addCommentReply` on a
comment thread, `updateCommentPost`, `deleteComment` and
`deleteCommentReply`, which would give one act two paths.
Cost: a reply on a suggestion's thread takes plain replies only. Its
length limit is unmeasured, so it is held to the 2048 bytes
`insertComment` takes; issue #111 asks for the limit to be measured
the way the comment limits are.

### D-40 review_suggestion takes each suggestion by the id list_suggestions showed

`review_suggestion` takes each suggestion by the id `list_suggestions`
showed, and `ids` is required. The server reads the document fresh,
lists what is pending at that revision, and refuses an id that is not
pending as `[not_found]`. An id named twice is reviewed once, since
Google refuses a second review of one suggestion and that would fail the
whole batch. `expect_revision` pins the review to the revision the
caller read.

A client's prompt shows the call's arguments, so the ids are what a
person approving the call sees, and a suggestion proposed after the list
was read is never reviewed unseen. `accept` applies a suggestion and
`reject` declines it, as any editor may; `discard` removes it, and
Google allows only its author to
([DeleteSuggestionRequest](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/request#DeleteSuggestionRequest)).
The live steps "list the suggestions to accept" and "accept every
pending suggestion" pass the ids a listing showed.

Rejected: `all`, which would accept or reject suggestions nobody listed.
Cost: accepting every pending suggestion means listing them and passing
each id. `ids` is a required input, so relaxing it later is a schema
change a client must follow.

### D-41 History is read through Drive revisions and exports at a revision; restoring is an explicit replace

History is read through Drive. `list_revisions` lists a document's
revisions, newest first, with the time and who changed it.
`diff_revisions` exports Markdown or plain text at two revisions and
returns a line-level unified diff, budgeted at hunk boundaries.
`read_document` with `revision` reads one old revision as Google's
export of the whole document. Restoring an old revision is an explicit
`replace` of the body from an export, never a call of its own.

The Docs API reads only the document as it is, so an old revision's
structure cannot be read at all. Only an export reaches one, and only
Drive's `files.download` takes a `revisionId` for a Google Doc, since
`files.export` takes a MIME type alone
([files.download](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/download)).
The download is a long-running operation whose content URL must stay on
a Google host (D-11). The live steps "read an old revision" and "diff
revisions" drive it.

Rejected: an implicit restore, which would overwrite what people wrote
since without anyone choosing it.
Cost: Drive's list "might be incomplete for files with a large revision
history", busy Google Docs among them, and the result says so
([revisions.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/revisions/list)).
An old revision reads without handles or scoping, and Drive's revision
ids are a different space from the Docs `revision_id` a write is guarded
by.

### D-42 A Markdown file in Drive is a document, read from its bytes and written by an upload of the whole file

A Markdown file stored in Drive, which Google Docs opens and edits in
place, is a document here. The service asks Drive for a file's type once
per id, and `text/markdown` or the older `text/x-markdown` takes the
Markdown path; `get_document` says `kind: markdown_file`. Such a file is
read from its bytes and written by a Drive media upload of the whole
file. Its text is what the model reads, what an exact-text target
matches and what an upload rewrites. Its outline is its headings, each
named by a slug, and a long read continues from a line.

The Docs API reads such a file, comments view included, and refuses
every write to it, `SUGGEST` and `insertComment` included, with 400 "The
document must not be an Office file"
([batchUpdate](https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/batchUpdate)).
The integration tests TestRawMarkdownFile and TestRawMarkdownFollowups
check it. A minimal edit to the file's own bytes is no round trip
through Markdown (D-25); it is the minimal diff applied to text (D-27).

Rejected: the Docs API, which refuses every write to such a file;
refusing the file because Markdown is for new content only.
Cost: every upload makes an editor that has the file open reload. An op
only the Docs API performs, such as formatting, tables or tabs, is
`[unsupported]` before anything is sent. A file over 5 MB, Drive's
ceiling for a simple upload, is read but not written.

### D-43 A Markdown file's comment markup is read out, kept byte for byte, and never forged or ended by a write

A Markdown file's comments live in its bytes: a marker on each side of
the passage, `<!-- gdoc_comment_begin: c1 -->` and its end, and an entry
in a `gdoc_comments` YAML block that must end the file.
`internal/mdfile` parses that markup out and keeps it byte for byte. A
read shows the text without it, and every edit is applied to that
visible text under fixed rules. A marker is never deleted. Inside a
replaced range a begin marker moves before the new text and an end
marker after it. At a replaced range's edge a marker stays outside it,
and new text at an insertion point goes outside a comment that begins or
ends there. The result is parsed again and must carry the same markers
and the same block, or the write is refused.

No Google page documents the markup. Drive cannot see these comments,
which the integration test TestRawMarkdownComments checks. Content
holding the markup is refused `[invalid]`, so no write can forge or end
a comment, which the live step "forged comment markup" checks.
`FuzzParseApply` holds the rules.

Rejected: letting the model edit the raw bytes, where a replace could
delete or move a marker and unanchor a person's comment.
Cost: `list_comments` reads the Docs comments view as well as Drive's
list, since the two hold disjoint sets.

### D-44 A Markdown write checks the live session, re-checks before uploading and repairs from history; force never writes past unsaved editor work

Every write to a Markdown file checks, writes, verifies and repairs.
Before planning, it reads the file and, through a `fields` mask, the
live session's revision and comments. A comment, reply or resolve the
live session holds and the file does not is a person's unsaved work, so
the write waits three seconds and tries again, twice, then is
`[conflict]`. Just before the upload it reads the head revision, the
checksum and the live revision again, and plans again if any moved.
After the upload it finds in `revisions.list` the revision its upload
replaced. If that is not the one the plan was made on, a save landed in
between. The save is read from history and the change made again on top
of it, or, when the change does not fit, the save is put back and the
write is `[conflict]`.

Drive offers no precondition on an upload: it serves no ETag, ignores
`If-Match`, and moves `version` on metadata changes too
([v2 to v3](https://developers.google.com/workspace/drive/api/guides/v2-to-v3-reference)).
A person's work in the editor reaches the file only when the editor
saves, and an upload a few seconds after a save overwrites it. The
integration tests TestRawMarkdownAfterDocs and TestRawMarkdownWithPerson
check both.

Rejected: uploading while a person has the file open, unchecked; a Drive
precondition, which does not exist.
Cost: unsaved editor work holds a write through two three-second waits.
`force` never writes past it, since that work is in no Drive revision
yet and nothing could bring it back.

### D-45 A call uploads once, and not at all when nothing changed, as the file's own type, and pins no revision

A call to a Markdown file batches its ops into one upload, and a call
that changes nothing uploads nothing. The upload is sent as the file's
own type, `text/markdown` or `text/x-markdown`, and pins no revision.

Every upload is a Drive revision, identical bytes included, so one call
makes at most one. The media part's type becomes the file's: a part sent
as `text/plain` retypes a Markdown file, which the Docs API then
refuses, and the upload guide does not say an update retypes a file
([manage uploads](https://developers.google.com/workspace/drive/api/guides/manage-uploads)).
The integration tests TestRawMarkdownRewrite and TestRawMarkdownEdges
check both, and the live step "edit it" drives an upload. A pin on a
revision other than the head cannot be undone, Drive answering 400
`illegalKeepForeverModification`, and a file keeps at most 200 revisions
forever
([manage revisions](https://developers.google.com/workspace/drive/api/guides/manage-revisions)).

Rejected: pinning the revision a write replaces, which cannot be undone;
a `text/plain` media part, which retypes the file.
Cost: Drive keeps an unpinned revision at least 30 days unless the file
passes 100 revisions, so the way back from a write can expire. The
result names the revision before the write, which `diff_revisions`
reads.

### D-46 A comment written into a Markdown file uses the editor's markup, and a resolve is never written

`add_comment`, comment mode and a reply to a comment a Markdown file
keeps all write the markup the Docs editor writes: markers around the
passage and an entry under the signed-in account's name, its strings
YAML double-quoted. The editor shows such a comment, "From imported
document", and on a person's save keeps it and gives it a token, which
makes it Google's own. A resolve, a reopen or an edit is never written;
the person does those in the editor.

The markup is the only comment such a file can carry, since the Docs API
refuses `insertComment` on it and Drive cannot see the editor's comments
(D-42, D-43). `status: RESOLVED` written into the bytes makes every
`documents.get` on the file answer 500 "Internal error encountered."
until the bytes are put back. The integration tests
TestRawMarkdownWithPerson and TestRawMarkdownBuild check both. The live
steps "comment on a passage" and "reply in the file" check that the Docs
view lists what was written, and "resolve in the file" checks the
refusal. After writing markup the server waits for the live session to
list it, and puts the bytes back if the Docs API then cannot read the
file.

Rejected: writing `status: RESOLVED`, which breaks every read of the
file.
Cost: a comment written here needs a passage and cannot be assigned.
Until a person saves in the editor, its id changes each time the file is
written.

### D-47 Working on a Markdown file together goes through a Google Doc working copy, merged back block by block

For a person and the model to type in one Markdown file together, the
work goes through a Google Doc working copy. `create_document` with
`from_file` imports the file's text, without its front matter or comment
markup, into a Doc through Drive's Markdown import
([import](https://developers.google.com/workspace/drive/api/guides/manage-uploads#import-docs)).
It records the file and the revision it was made from in the Doc's
`appProperties`, which only this OAuth client sees
([properties](https://developers.google.com/workspace/drive/api/guides/properties)).
Everything the server does on a Google Doc then works live on the copy.

`write_markdown_file` exports the copy as Markdown and merges it into
the file by blocks: paragraphs, headings, list items, table rows and
fenced code. A block the copy did not change keeps the file's own text,
since Docs' export rewrites Markdown its own way. The result is written
as minimal edits under the guard, by the protocol of D-44. No API writes
through an editor's live session, and every upload makes an open editor
reload (D-42). The live steps "make a working copy", "the copy says what
it is" and "write the copy back" drive the round trip.

Rejected: writing through an open editor's session, which no API offers.
Cost: a block changed in both the copy and the file is a conflict,
refused unless `force` keeps the file's version. A base revision gone
from history needs `force` too. An image placed in the copy has no
address a Markdown file can use, so it is written empty with a warning.

### D-48 Search finds Google Docs by default, and kind reaches Markdown files

`search_documents` finds Google Docs by default. `kind: markdown_file`
reaches Markdown files, under either Drive type, and `kind: any` reaches
both. The query is Drive's search restricted by MIME type, with trashed
files left out, and it locates a document; everything else happens
through the other tools (D-02).

A person who syncs a code folder into Drive holds hundreds of
`README.md` files, which would bury the documents a search means. So
Markdown files are found when the model asks for them, and the tool
description says how. The live steps "search by title" and "search for
Markdown files" drive both kinds.

Rejected: Markdown files in the default results, where a synced code
folder drowns the documents.
Cost: a model looking for a Markdown file must pass `kind`. No kind
reaches a Word file, which is opened by its id or URL.

### D-49 A Word file is read as a Google Doc and written as its own file, by splices of the bytes it read

A Word file (`.docx`) in Drive is read as a Google Doc and written as
its own file. The Docs API reads one with handles, headings and the
comments view, its tracked changes as suggestions and its comments as
anchored comments, and `get_document` says `kind: word_file`. It refuses
every write to one with 400 "The document must not be an Office file",
which the integration test TestRawDocx checks. So `edit_document` and
the comment tools make their change in the file's XML and upload it
whole through Drive, under the protocol a Markdown file uses (D-44).

`internal/ooxml` never re-encodes the file. `encoding/xml` cannot write
a document back with its namespace prefixes as they were, so every edit
is a splice of `word/document.xml`, and a part not changed is copied
from the zip untouched. A heading takes the paragraph style the file
names "heading N", a name that is the same in every language where the
id is not. A list item takes `numPr`, continuing the list it is placed
against. `FuzzEdit` holds the splices, and the live step "word: read it
back" reads a write back through Docs.

Rejected: re-encoding with `encoding/xml`, which rewrites the namespace
prefixes; a Docs API write, which Google refuses.
Cost: formatting, tables, images, layout, tabs and reviewing suggestions
are `[unsupported]`, saying to save the file as a Google Doc. A comment
the file holds is replied to, resolved or deleted in the editor.

### D-50 An op reaches a Word file's paragraphs by order, each checked to hold the text the Docs model shows

An op on a Word file resolves against the Docs model as on a Google Doc,
handles, headings, exact text and the overwrite guard alike (D-28). It
then reaches the file's paragraphs by order. The Docs body's paragraphs,
tables' included, are the file's `w:p` elements in the same order, text
for text, which no reference states; the integration test
TestRawDocxRevision checks it. Each paragraph an op reaches is checked
to hold the same text in both, deleted text, tabs and a content
control's text counted as Docs counts them. A difference means Docs has
not read a revision yet, or a person's edit has not reached the file, so
the write waits and then refuses `[conflict]`.

A cell spanning columns is the exception. Docs keeps each cell the span
covers, empty, and the file has none, so those are passed over. A span
of rows is a cell in each of its rows on both sides, and Docs reads a
legacy `hMerge` as cells of their own. The integration test
TestRawDocxModel rereads the model `TestWordMergedCells` replays, and
the live step "a sentence changed" writes through the map.

Rejected: reaching a Word file's paragraphs by position alone,
unchecked, which writes into the wrong paragraph when Docs has read a
different revision than the file holds.
Cost: after an upload the write waits for Docs to read the new revision,
about four seconds, so the next read shows it.

### D-51 On a Word file a suggestion is a tracked change and a comment a Word comment, under the account's name

On a Word file a suggestion is a Word tracked change and a comment a
Word comment, both under the signed-in account's name. A suggested
insertion is `w:ins` and a deletion `w:del`, a new paragraph's mark
marked inserted and a deleted one's deleted. A comment is range markers
and a reference in the text, with its text in `comments.xml`, which a
file without one gets with its content type and relationship. Docs reads
a tracked change as a suggestion and a Word comment as an anchored
comment, so a suggest-mode or comment-mode write shows in Docs as it
does on a Google Doc. The integration test TestRawDocxRevision checks
both, and the live steps "word: a comment" and "word: list the comments"
drive a comment.

A change may not reach into a link from outside it, nor, in either mode,
into text a pending suggestion inserted or deleted. Written into the
suggestion, a direct change would go with it when it is accepted or
rejected. A tracked insertion goes inside a link, since Docs drops the
link's text when the link sits inside `w:ins`.

Rejected: reviewing a Word file's suggestions, which would go by
position, since Docs names a tracked change `suggestIdImport<uuid>_<n>`
with a uuid that changes on every revision.
Cost: `review_suggestion` refuses a Word file, saying to save it as a
Google Doc, so its tracked changes are reviewed in the editor.

### D-52 The server asks the person nothing; the client approves each call

The server asks the person nothing. Whether a call is put to a person is
the client's decision and its permission mode, and the server adds no
question of its own. `forbidigo` refuses the SDK's `Elicit` and the
input requests a result can carry, so no code can ask. The server tests
call every tool through a client that can be asked and fail any call
that asks, and the live driver does the same.

The MCP specification puts consent to each tool call on the host
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)).
What holds on every client is what the server controls: what it
registers (D-54, D-55), the retyped target (D-56), the write mode the
person chose (D-29), the overwrite guard and `force` (D-28),
`expect_revision` (D-30), a Markdown file's conflict and editor checks
(D-44), and `dry_run` (D-35). A client remembers an approval per tool,
never per argument, so a risky variant carried as an argument rides
along unseen once its tool is always allowed.

Rejected: asking the person through form elicitation (D-95);
`requiresUserInteraction` beside the question, two hard gates on one
call.
Cost: a host in an auto-approve mode may call any registered tool
without asking. So every registered tool is designed as callable
unattended, and `GDOCS_ENABLE_DESTRUCTIVE` stays off for a client nobody
watches.

### D-53 GDOCS_REQUIRE_PROMPT is refused at start unless off, and GDOCS_PREVIEW is ignored with a warning

`GDOCS_REQUIRE_PROMPT` is a removed setting. Set to anything but `false`
it is refused at start, naming what to do instead: leave
`GDOCS_ENABLE_DESTRUCTIVE` off for a client that runs with nobody
watching. `false`, its old default, and unset are accepted.
`GDOCS_PREVIEW` and `--preview` are accepted, ignored and logged as
deprecated, so a configuration that still sets them starts.

`GDOCS_REQUIRE_PROMPT` refused the writes that asked the person on a
client that could not ask, and the server asks nobody (D-52). Ignoring
it would quietly allow what its owner set it to stop. `GDOCS_PREVIEW`
gated comments and suggestions, which are generally available and
detected per read (D-17), so the switch decides nothing and restricts
nothing. `config.Vars` marks the removed setting, so the bundle gate
leaves it out of what the bundle asks for.
[configuration.md](configuration.md) lists both.

Rejected: ignoring `GDOCS_REQUIRE_PROMPT`, which would allow what it was
set to stop; refusing `GDOCS_PREVIEW`, which would stop a server whose
setting restricts nothing.
Cost: `--require-prompt` is not a flag, so one left in a client's
arguments stops the server at start.

### D-54 The delete tools register only with GDOCS_ENABLE_DESTRUCTIVE

`delete_tab` and `delete_comment` register only when the deployer sets
`GDOCS_ENABLE_DESTRUCTIVE=true`, and the service refuses a delete
without it too. Read-only mode leaves the switch nothing to register.
`--dump-schemas` dumps the full surface, the deletes included, so the
schema diff holds them like any other tool.

The control has to be the server's. Annotations are hints, and the MCP
specification tells clients to treat them as untrusted unless the server
is trusted
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)).
A client may approve every call of a tool once and never ask again, so a
tool that must not run unattended is one the server does not register. A
deleted tab's text stays in version history, but a deleted comment
cannot be restored.

Rejected: relying on `destructiveHint` or `requiresUserInteraction` to
make a client ask first, which clients treat as advisory.
Cost: the switch turns on both deletes for every call that server entry
takes, so it stays off for a client that runs with nobody watching.

### D-55 Read-only mode registers the reads alone and asks for read-only scopes

With `GDOCS_READ_ONLY=true` the server registers the reads,
`export_document` and the resources alone, and `login` asks for
`documents.readonly` and `drive.readonly`. The service refuses a write
as `[forbidden]` as well, so no path reaches one. `export_document`
stays registered because it writes a local file under `GDOCS_EXPORT_DIR`
and changes nothing in Google (D-24).

A read-only server is one a deployer can run for a client nobody
watches, or for a person who wants answers and no edits. A read-only
token makes that hold at Google too: a missed check in the server still
cannot write, because the grant does not allow it
([Docs scopes](https://developers.google.com/workspace/docs/api/auth)).
`TestListToolsAndSchemas` holds the registered set, and
`TestScopesAgree` holds the README's scope list to what `login` asks
for.

Rejected: registering the writes and refusing them per call, which
offers the model tools it can never use; full scopes in read-only mode,
which leave the grant wider than the server.
Cost: a token granted read-only cannot write, so turning read-only off
needs a fresh `login`. `export_document` registers in read-only mode, so
a read-only server still writes files under the export directory.

### D-56 A delete repeats its target, and delete_comment how the text it deletes begins

A delete names its target twice. `delete_tab` takes `confirm_tab` and
`delete_comment` takes `confirm_comment_id`, each repeating what the
call deletes, and refused when it does not match. `delete_comment` also
takes `text_start`: how the text it deletes begins, as `list_comments`
shows it, the thread's comment or the reply's. The comment is read
before the delete, a dry run included, and a `text_start` that is not
how the text begins is `[stale]`. It carries at least the first 20
characters, or the whole of a shorter text, with whitespace folded as
the listing folds it.

A client's prompt shows the call's arguments, so the repeated target and
the start of the text are what a person approving the call reads.
Retyping an id is a different act from setting a boolean, which a model
supplies as easily as it omits. The check lives in the server, the half
a client cannot skip as it can skip an annotation.

Rejected: a `confirm: true` boolean, a second breaking input for nothing
gained.
Cost: the model types the witness, so it shows the call meant this
target and is never the person's approval (D-52). A comment delete needs
a listing first, since the start of the text comes from `list_comments`.

### D-57 destructiveHint is true on every write but the three that only add, and readOnlyHint is on the reads alone

`destructiveHint` is true on every write except `create_document`,
`add_comment` and `export_document`, which only add: a new document, a
new comment, or a new file in the export directory. `readOnlyHint` is on
the reads alone, which change nothing. The writes marked destructive
that are not gated register without `GDOCS_ENABLE_DESTRUCTIVE` and take
no confirm; `delete_tab` and `delete_comment` say destructive too.

The MCP schema defines `destructiveHint: false` as "the tool performs
only additive updates", with true as the default
([ToolAnnotations](https://modelcontextprotocol.io/specification/2025-11-25/schema#toolannotations)).
An edit, a restyle, a resolved thread or a renamed tab changes what is
there, whatever the guard allows. Clients prompt by these hints:
claude.ai and Codex in its default mode by `destructiveHint`, VS Code on
every tool without `readOnlyHint`. `add_comment` stays false on a
Markdown or Word file, though the upload carries the whole file, because
the guard refuses it when the file moved since the read.
`TestOnlyToolsThatAddSayTheyAreNotDestructive` holds the set.

Rejected: `destructiveHint: false` on a write the overwrite guard keeps
from deleting more than was asked, which the schema's definition does
not allow.
Cost: claude.ai and Codex prompt for every edit, and VS Code for every
tool not marked read-only, `export_document` included.

### D-58 No tool carries requiresUserInteraction

No tool carries Claude Code's
`_meta["anthropic/requiresUserInteraction"]` mark. In Claude Code the
operator decides with `permissions.allow` and `permissions.ask` rules.
The README gives `ask` rules, and the staleness gate holds each tool
they name to a registered one.

Anthropic scopes the mark to tools whose permission prompt is itself the
point, such as a consent step, and Claude Code prompts for a marked tool
on every call, in every interactive mode
([Claude Code MCP](https://code.claude.com/docs/en/mcp)). Neither delete
here is that. Version history keeps a deleted tab's text. A deleted
comment cannot be restored, but it is one item of many, often deleted
many in a row, where a prompt on every call goes unread. Only Claude
Code reads the mark, so it is a hint, and the control is that the
deletes are not registered unless the deployer turns them on (D-54).

Rejected: the mark on the gated tools, a prompt per item; the mark sent
only to a client that declares no form elicitation, which a client
escapes by declaring its capabilities differently per request.
Cost: a deleted comment cannot be restored, and a Claude Code user who
wants a prompt before one adds the README's `ask` rule.

### D-59 No tool takes one of the server's settings, and no code a tool reaches can write them

No tool takes one of the server's settings as an input, nested ones
included, and no code a tool reaches can write them. The packages behind
the tools import none of `internal/userconfig`, `internal/credentials`
and `internal/auth`, which write the profile, the token and the sign-in,
and none sets an environment variable. `TestNoToolTakesAServerSetting`
reads the settings from the flags `config.Define` registers and fails on
a tool input of the same name. `TestNoToolCanWriteTheServersSettings`
follows the imports from `internal/tools` rather than a typed list.

Only the operator changes what the server may do, outside the
conversation. A model can be persuaded by what a document says, and a
call that could name its own export directory, default write mode or
destructive switch would move a limit the operator set. Holding the rule
by the import graph checks a package added later without anyone
remembering to.

Rejected: a tool input that adjusts a setting for one call, such as the
export directory, which a persuaded model could point anywhere.
Cost: changing a setting means restarting the server with a different
environment, from the client's configuration.

### D-60 Errors are tool results, `[class] message`, from one closed vocabulary

Every failure a tool reports is a tool result with `isError` set and the
text `[class] message`, never a JSON-RPC error. The class comes from one
closed vocabulary, `service.Classes` in `internal/service/service.go`,
where each class says what it asks the reader to do. `gapi.Classes` is
the client's half and is held to be a subset. The `classes` gate scans
every class literal in `internal/` and fails on a class not listed, on a
listed class nothing emits, and on a scan that read too little. The
message carries the fix, such as "add occurrence or within" or "re-read
the section".

The MCP specification asks only for `isError`, so that a model sees the
failure and can correct itself
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools));
the class is this server's convention. It tells the model what to do
before it reads the sentence. `ambiguous` and `ambiguous_outcome` are
different words because they ask for opposite things: one to choose
between candidates, the other to go and look what happened. The live
step "a stale revision" checks that a refusal reads `[conflict]`.

Rejected: a vocabulary kept in comments in two packages, which nothing
could check; one word for both meanings of `ambiguous`, which makes a
duplicate write look like a bad argument.
Cost: a class added to the code fails the build until it is listed with
what it asks the reader to do.

### D-61 Reads retry transient failures; a write repeats only on 429, and a 5xx or network failure on one is `[ambiguous_outcome]`

Reads retry a transient failure, a 429, a 5xx, a throttling 403 or a
network failure, with exponential backoff and full jitter, at most five
attempts and 30 seconds a wait, honoring `Retry-After`. A write repeats
only on 429, which proves nothing was applied. A 5xx or a network
failure on a write is `[ambiguous_outcome]`: the write may have been
applied, so read before trying again. The rule covers every kind of
write, Drive's comment writes and uploads as well as document batches.

Google's `code.proto` says of `UNAVAILABLE` that it is "not always safe
to retry non-idempotent operations"
([code.proto](https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto)).
A repeated `batchUpdate` that had landed fails its revision check and is
planned again, so an edit could apply twice, and `documents.create` and
`comments.create` have no guard and duplicate outright.
`[ambiguous_outcome]` is classed before the server or network error it
wraps, so neither wins. `TestWriteRetriesOnlyOn429` and
`TestDriveWriteRetriesOnlyOn429` hold both write paths.

Rejected: repeating a write on 503; a retry guard keyed to document
batches alone, under which a comment write repeats after a 500 and posts
twice.
Cost: a write Google failed with a 5xx is reported rather than retried,
so the model reads before trying again, a call more than a retry would
take.

### D-62 A Google error is classified by its reason before its status

A Google error is classified by its reason before its status. A 403
whose reason is `ACCESS_TOKEN_SCOPE_INSUFFICIENT` is a missing scope,
`[forbidden]` with a message to run `login` again. A 403 whose reason is
`rateLimitExceeded`, `userRateLimitExceeded` or
`sharingRateLimitExceeded` is `[rate_limited]` and backs off on a read.
`dailyLimitExceeded` is `[rate_limited]` without a retry, since waiting
does not refill a daily quota. Only then does a 403 mean `[forbidden]`.
Both spellings of a reason, camelCase in the legacy envelope and
UPPER_SNAKE in an `ErrorInfo` detail, fold to one key. Every reason
appears in the error text, so a refusal the classifier cannot improve on
still reaches the model in Google's own word.

Drive answers throttling with 403 rather than 429 and prescribes
exponential backoff for it
([handle errors](https://developers.google.com/workspace/drive/api/guides/handle-errors)).
Read by status first, a throttled read is `[forbidden]`, never retried,
and the model goes looking for a permission that is not missing. The
same guide lists 403 reasons that mean "cannot", such as
`fileNotExportable`, which stay `[forbidden]` with Google's reason
quoted.

Rejected: the status first, which makes throttling a permission error.
Cost: the throttling reasons come from Drive's guide, and nothing here
can make Google send one, so only the fakes test the mapping.

### D-63 Every HTTP call has a deadline, the token refresh and the login code exchange included

Every HTTP call the server makes runs under a deadline. Each attempt
against Google's APIs carries `GDOCS_HTTP_TIMEOUT`, 60 seconds unless
set. The token refresh and `login`'s code exchange run on a bounded
client of their own, and a revision export polls its operation a bounded
number of times.

The refresh happens inside the oauth2 token source, on whatever client
the context carries, not on the API client. With none there it runs on
`http.DefaultClient`, which has no timeout, so a token endpoint that
accepts a connection and never answers would hang the first tool call
for the life of the process. `auth.boundHTTP` puts a client with a
timeout in the context for both calls
([oauth2.HTTPClient](https://pkg.go.dev/golang.org/x/oauth2#HTTPClient)).
`TestTokenRefreshIsBounded` hangs a listener and fails without it, and
`TestContextCancellation` holds the per-attempt deadline.

Rejected: the signal context alone, which interrupts a hung call but
does not bound one.
Cost: an attempt that outlasts the deadline counts as a network failure,
so a read tries again and a write is `[ambiguous_outcome]` (D-61), even
when Google would have answered.

### D-64 A five-second cache coalesces fetches of a document, and a write always fetches again

Fetches of one document within five seconds share one read. The cache is
keyed by the document id, and an entry older than five seconds is
evicted on the next fetch. A write never plans from the cache: it
fetches the document again, and checks the caller's handles against the
read the caller saw (D-14).

The cache coalesces, and decides nothing. A model often reads an
outline, then a section, then finds a passage, all within seconds, and
three `documents.get` calls for one unchanged document spend the read
quota for nothing. Correctness comes from the revision guard on every
write (D-30), never from what a read remembers, so a stale cached read
can make a read late but cannot make a write wrong. A write also drops
the document's entry once it lands. `TestFetchCacheAndHandles` holds the
window: a read inside it is served from the cache, and one after it, or
after an invalidation, fetches again.

Rejected: caching for correctness, such as planning a write against a
cached read, which would write against a document somebody else may have
changed.
Cost: a read inside the window does not show a change somebody else made
a moment before it.

### D-65 Everything derived from one fetch is derived once

Everything this server derives from one fetch is derived once, on first
use, and shared by every caller of that fetch. That covers the handle
memory, the located comment threads, and per segment the searchable text
and the sorted list of what a deletion could destroy. The searchable
text is every paragraph's normalized characters in one string, so a text
target or a quoted comment is one substring search, and a match maps
back to indices through an offset per paragraph.

A call can resolve several targets, run the guard and render a read from
the same document. Built per use, each of those walks the document
again, and the cost grows with the document's size times the number of
uses. The overwrite guard, `read_document` with comments and
`list_comments` share one located thread list, so the three can never
disagree about where a comment is. `make bench` measures these paths
over `doctest.Large`, a generated document of about 150 pages, and
`TestLargeFixtureIsConsistent` holds that fixture to its own shape.

Rejected: matching text paragraph by paragraph, which rebuilt the
normalized text on every search and made locating quoted comments scale
with the comments times the paragraphs.
Cost: what remains per write is the fetch itself. `make bench` is not in
`make check`, so nothing fails when a path starts to scale with the
document's size again.

### D-66 Sign-in is Google's desktop flow on a loopback port with PKCE

`google-docs-mcp login` signs in through Google's flow for desktop apps.
It listens on 127.0.0.1 on a random port, sends the browser to Google
with a PKCE challenge, and exchanges the code that comes back for a
refresh token. The client file must be a Desktop app client; a web
client is refused.

Google documents this flow for installed applications and blocks the
out-of-band flow, which had the person paste a code back
([OAuth for installed apps](https://developers.google.com/identity/protocols/oauth2/native-app)).
The loopback redirect keeps the code on the person's machine, PKCE keeps
it useless to anything that intercepts it. `TestLoginHappyPath`,
`TestLoginRejectsBadStateAndDenial` and `TestLoginTimeoutAndCancel` hold
the flow, its state check and its bounded wait.

Rejected: the out-of-band flow, which Google blocks; a web client with a
hosted callback, which needs a server somebody runs (D-01).
Cost: over SSH the browser and the listener sit on different machines,
so the person forwards the port by hand. The `token_uri` and `auth_uri`
of the client file are used as given, falling back to Google's endpoints
only when missing, so a tampered file could send the code and the token
elsewhere. Issue #114 asks whether to pin both to Google's endpoints
or refuse any other host.

### D-67 The refresh token goes to the OS keyring, else an owner-only file with a warning, and the environment overrides both

The refresh token goes to the OS keyring: Secret Service on Linux,
Keychain on macOS, Credential Manager on Windows, under the profile's
name. When the keyring fails, as it does on a headless machine with no
session bus, the token goes to a file under the profile directory with
mode 0600, and the server warns on every use. `GDOCS_REFRESH_TOKEN`
overrides both, for automation.

A keyring is where a desktop keeps a secret, and the person's own login
unlocks it. A server that refused to run without one would not run on a
headless machine or in a container. Failing over to an owner-only file
with a warning is what `gh` does, and the warning keeps the weaker store
from passing unnoticed. `logout` revokes the token and deletes it from
both stores. `TestEnvWins`, `TestFileFallbackWhenKeyringBroken` and
`TestKeyringSaveRemovesStaleFile` hold the order.

Rejected: falling back to the environment alone, which leaves a headless
machine without a working `login`; a plaintext file with no warning.
Cost: on Windows the file has no POSIX mode bits, so it is protected
only by the access control it inherits from the person's profile
directory.

### D-68 The scopes are documents and drive, or their read-only pair

`login` asks for `documents` and `drive`, or `documents.readonly` and
`drive.readonly` in read-only mode (D-55). Nothing else is requested,
and the README lists the scopes `login` asks for.

The server works on any document the person names, created by anyone.
`drive.file` reaches only the files the app created or was handed
through a picker, so it cannot open a document somebody shared. `drive`
is what reaches every document the person can open, and what search,
comments, revisions and file uploads use
([Docs scopes](https://developers.google.com/workspace/docs/api/auth),
[Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)).
A token missing a scope makes Google answer 403, which the server
reports as `[forbidden]`, saying the credentials lack a scope and to run
`login` again. `TestScopesAgree` holds the README's scope list to the
code.

Rejected: `drive.file`, which cannot reach documents the app did not
create.
Cost: `documents` is a sensitive scope and `drive` a restricted one.
That is fine for a per-person app that is never published, and it is why
each person brings their own OAuth client (D-01).

### D-69 Configuration is the environment, each variable with a bound flag, read through config and validated once at start

Configuration is the environment. Every setting is a `GDOCS_*` variable
with a flag of the same name bound to it, and a flag given on the
command line wins. Every reader goes through `config.Lookup`, which
reads a value of spaces alone as unset, and validation runs once at
start. A relative `GDOCS_CONFIG_DIR` or `GDOCS_EXPORT_DIR` is refused
there, as is a value outside a setting's rule.

Claude Code, Claude Desktop and Cursor pass a stdio server only a
command, its arguments and its environment, so the environment is what
every client can set. A bound flag serves a person running the binary by
hand. One registry, `config.Vars`, records every variable `Define`
reads; the staleness gate holds docs/configuration.md to it and the
bundle gate derives its settings from it (D-93).
`TestEveryReaderOfAVariableGoesThroughConfig` refuses a read of a server
variable anywhere else, `TestEveryVariableTheCodeNamesIsInEnvVars`
refuses one the registry lacks, and `TestASettingOfSpacesIsUnset` holds
the spaces rule.

Rejected: configuration by environment alone, with no flag to set it
from a terminal; a configuration file, which no client passes.
Cost: a bad setting stops the server before it serves, so the client
shows it failing to connect and the reason is only on stderr.

### D-70 A profile per Google account, each with its own client, token and account

A profile is one Google account's configuration. `GDOCS_PROFILE` names
it, and each profile keeps its own OAuth client file path, refresh token
and account. The default profile lives in the server's configuration
directory, and any other under `profiles/<name>/` below it. The keyring
entry is filed under the profile's name.

A refresh token belongs to the OAuth client that issued it, and a token
reaches one account's documents. A person with a work and a personal
account runs two server entries, each with its own profile, so neither
call can reach the other account's documents by mistake. A profile name
is lowercase letters, digits, `-` and `_` (`config.ProfileRule`), since
it becomes a directory and a keyring account on every platform.
`TestProfileRule` and `TestPathsAndRoundTrip` hold the rule and the
layout.

Rejected: one server holding several accounts and choosing per call,
which would let a tool argument decide whose access a call uses (D-59).
Cost: two accounts are two server entries in the client's configuration,
each with its own `login`.

### D-71 The server always starts; without a working credential every tool answers `[auth]`, and doctor is the check for people

The server always starts. With no refresh token, a token Google rejects,
or a client file that will not load, it logs a warning to stderr and
serves anyway, and every tool answers `[auth]` saying to run
`google-docs-mcp login`. At start it checks the token once, off the
startup path. `doctor` is the check for people: it reports what
docs/configuration.md lists, the credentials, the token exchange, the
granted scopes, the Drive API and, given a document, the Docs API and
the comments view.

A server that exits on a failed credential check is shown by the client
as "failed to connect", and the model never learns why. Serving the
error keeps the reason where the model can read it and pass it on: one
command to run. `TestAuthFailureSurfacesAsAuthClass` holds the tool
side, and the smoke gate starts the binary with no credentials.

Rejected: exiting on a failed startup probe, which hides the reason from
the model.
Cost: a deployment with no working login looks healthy until the first
call. A bad setting still stops the server at start (D-69), since no
tool could answer for it.

### D-72 Tool names are snake_case verb_noun with no dots

Tool names are snake_case verb_noun with no dots: `get_document`,
`edit_table`, `review_suggestion`. Claude Code prefixes each with
`mcp__<server>__`, and the names stay readable after it.

The MCP specification allows more characters than this, and clients
differ in what they accept. A name made of lowercase words and
underscores is accepted everywhere, and a model finds a tool by the name
it would guess for the act, and the agent evals' traces show whether it
finds it (D-84). A name says what it acts on in Docs terms, so a person
running this server beside a Drive or Sheets server sees which one a
call goes to.

Rejected: dots or other separators in a name, which some clients refuse
and which make a prefixed name ambiguous.
Cost: no test checks the pattern, so a name is held to it in review. A
released name can only change in a major release (D-77).

### D-73 Schemas are flat structs with an op or action enum, with no unions and no vendor keywords

Every input schema is a flat struct. A tool that does several kinds of
thing takes an `op` or an `action` field naming the kind, with every
field of every kind beside it, and each field's description says which
kinds take it. No schema uses `oneOf`, `anyOf` or a vendor keyword.

Every client reads a flat object of plain types the same way. A union or
a vendor keyword is read differently from client to client, and the
servers before this one failed there. A flat struct is also what the
schema dump and the schema diff compare field by field (D-77). The
server checks per kind what the schema cannot say: a `replace_all`
without find text, an `insert_table` past its row or column limits, or a
`create_named_range` without a name is `[invalid]` before anything is
sent.

Rejected: `oneOf` unions per op, and vendor keywords, which not every
client reads.
Cost: a schema lists fields a given op ignores, and which fields an op
needs lives in the descriptions and the server's checks rather than in
the schema. No test checks the shape, so it is held in review.

### D-74 Page and section layout is a tool of its own

Page and section layout is the `layout_document` tool, apart from
`format_document`. Its ops are `page` (`updateDocumentStyle`), `section`
(`updateSectionStyle`), `section_break` (`insertSectionBreak`) and
`named_style` (`updateNamedStyle`). Lengths are points throughout, and
`get_document` names a standard page size on the way back, so a model
need not recognize 612×792 as US Letter.

`format_document` styles one passage: a target and the text or paragraph
properties to set. Layout acts on a whole tab or section, and shares
almost no argument with it. Flat schemas carry every field of every op
side by side (D-73), so the two in one tool would double that tool's
schema and offer a model page margins beside a passage's font. A named
style redefines a style for the whole tab, so it sits with layout, and
`get_document` reports the definitions back (D-23).
`TestLayoutToolEndToEnd` drives every op through the server.

Rejected: more ops on `format_document`, which would double its schema.
Cost: a change of a passage's look and a page's look are two tools, and
a model asked for both makes two calls.

### D-75 A border is one shorthand value

A border is one shorthand value: a width, a dash style and a color in
any order, such as `1pt solid #cccccc`, or `none` to clear it. A part
left out takes 1pt, solid or black. One value sets all four edges, and a
field per edge sets one. A read reports an edge with no width as no
border.

The Docs API draws an edge only when its width, dash style and color are
all set, so a caller who sets one part gets nothing drawn without the
defaults. Google answers an edge that is not drawn with an empty border
object, which a read reports as no border rather than as `0pt solid`. A
field per part of every edge would multiply the properties of
`format_document`, `edit_table` and `layout_document`, which a flat
schema pays for in every call (D-73). `plan.ParseBorder` reads the
value, and `TestBorderShorthand` holds its forms and refuses a malformed
one.

Rejected: a field per part of each edge, which multiplies the schema's
properties.
Cost: changing one part restates the others. Setting only a color on a
2pt dotted edge draws it 1pt and solid.

### D-76 Three `gdocs://` resource templates carry a document as markdown; there is no static list and no subscription

The `gdocs://` resource templates carry a document as markdown, for a
client that attaches a document rather than calling a tool:
`gdocs://{document}`, its `/outline`, and `/tabs/{tab}`. The whole
document is every tab's body in order, cut at the read tools' largest
budget. There is no static resource list and no subscription, and a
resource carries no handles, suggestions or comments.

A static list of resources would be a Drive listing, which belongs to a
Drive server (D-02). A subscription needs a push from Google, which the
Docs API does not offer, and polling for one would spend the read quota.
Targeting, budgets and handles are the read tools' job, so a resource is
the plain text a client can attach. go-sdk matches a template through an
anchored RFC 6570 expression in which `{document}` excludes `/`, so no
template shadows another
([resource.go](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/resource.go)).
A document that does not exist is the specification's resource-not-found
error, and the live driver reads all three templates and one naming a
document that does not exist. `TestResources` holds the templates.

Rejected: a static resource list, which would be a Drive listing;
subscriptions, which Google offers no push for.
Cost: a resource cannot be scoped, budgeted or targeted, so a model that
needs any of that calls `read_document`.

### D-77 The released tool surface is a contract the schema diff holds; the prose of a text output is not part of it

The released tool surface is a contract. Removing or renaming a tool or
a resource template, making an argument required, or removing an output
field is a breaking change, which only a major release may make. The
`structuredContent` a tool returns is covered; the prose of its text
output is not.

A client is written against the surface, and it breaks when the surface
moves without notice. `make schema-diff` builds the binary, dumps its
schemas (D-79) and compares them with the last tag's. A break fails it
unless the CHANGELOG declares it in a `**Breaking:**` entry naming the
item in backticks, under a version that raises the major. The text
output is written for a model, and is reworded when a model reads it
badly, so it stays outside the promise. The README states the promise
and the exclusion. `TestSchemaDiffBreaksOnALostOutputField` and
`TestUndeclaredBreaks` hold the diff.

Rejected: a stability promise gated on an outside user's report or on
evals through a second client, neither of which a check can hold.
Cost: an output field keeps its place until a major release, even one
that means nothing. Issue #108 tracks removing the deprecated
`preview` fields of `get_document`'s capabilities and `status --json` at
the next major.

### D-78 A change to the tool surface names each changed item in a commit footer

A pull request that changes the tool surface names each changed item in
a commit footer. A `SCHEMA-CHANGE:` footer names an item that changed
without breaking, and a `BREAKING CHANGE:` footer one that breaks.
Annotations count, since a flipped `destructiveHint` changes whether a
client asks the person. `schema-ack` compares the pull request's head
with its merge-base, and runs only on a pull request.

The schema diff (D-77) says what changed; a footer says somebody meant
it. A footer is read as Conventional Commits defines one
([1.0.0](https://www.conventionalcommits.org/en/v1.0.0/#specification)):
a value may wrap over lines, a line that starts another footer ends it,
and a `Token #value` footer counts toward the block but acknowledges
nothing. The names are the list before the value's first colon and
space, and a name is a whole token, so `add_comment` does not name
`add_comments`. `TestSchemaAckNeedsEveryItemNamed` and
`TestARefusalNamesTheFooterThatFixesIt` hold the rule and its message.

Rejected: a footer value read as one line, which drops a wrapped list; a
`Token #value` footer that acknowledges, which has no description to
tell from names.
Cost: a change to the surface fails CI until a commit it adds names
every changed item, so a fix-up commit may exist only to carry a footer.

### D-79 The schema dump is what an in-memory client session lists

`--dump-schemas` prints what a client is told: the tools, the resource
templates, the fixed resources and the prompts, as an MCP client session
lists them from a server in memory. The dump is of the whole registrable
surface, every switch on, beside the read-only server's instructions.

The SDK has no public way to enumerate what a server registered, and a
second registry kept for the dump would describe what someone remembered
to add rather than what the wire carries. Asking the server through a
real session gives the schemas exactly as the SDK serializes them. A
tool behind a switch can lose a field like any other, so the dump starts
the server under `tools.FullSurface`, which turns on every switch
`Register` reads. `TestDumpSchemas` and
`TestDumpSchemasListsEverythingAClientIsTold` hold the dump, and
`TestFullSurfaceRegistersEverything` holds that no combination of
switches registers a tool the full surface does not.

Rejected: a parallel tool registry for the dump, which can drift from
what is registered.
Cost: `FullSurface` must name every switch `Register` reads, and a
switch added without it fails `TestFullSurfaceRegistersEverything`.

### D-80 Unit tests are network-free on synthetic fixtures, and a fake answers with the errors Google documents

Unit tests touch no network. A document comes from the synthetic fixture
`testdata/sample.json`, loaded through `internal/doc/doctest`, or from
the generated `doctest.Large`. Google is a fake behind the `gapi`
interface or an `httptest` server, and a fake answers an error the way
Google documents it: the status, the reason string and the message.

The code branches on Google's reason, not its status. A 403
`userRateLimitExceeded` means slow down, and a 403
`storageQuotaExceeded` means the Drive is full
([Drive errors](https://developers.google.com/workspace/drive/api/guides/handle-errors)).
A fake with a plausible invented reason tests the invention, and one
that refuses with no reason agrees with a bug by omission, since it
cannot reach the branch that is missing. So a fake's error is copied
from Google's documentation or a recorded answer, with the source beside
it.

Rejected: an invented reason or message in a fake, which tests the
invention rather than Google.
Cost: a fake encodes a belief about Google, and green unit tests prove
only what the fakes believe. The integration tests and the live driver
are what check the belief (D-83).

### D-81 The coverage floor holds every package `go list` derives, and each exemption names its reason

Each package under `internal/` clears a statement-coverage floor on its
own, on Linux, macOS and Windows. The list of packages is what
`go list ./internal/...` returns, so a package is under the floor from
its first commit. A package is exempt only by name, with its reason:
`internal/doc/doctest` is fixtures and `internal/gdocs` is wire types.

An average lets a well-tested package hide an untested one. A
hand-written list omits a package silently, and the omission looks
exactly like a package that does not exist. An exemption naming a
package the list does not return is worse than none, since it reads as a
considered choice and exempts nothing. `make cover` runs the floor,
which `TestEveryExemptionNamesARealPackage` and
`TestEveryExemptionHasAReason` hold to the tree.

Rejected: a hand-written list of packages under the floor.
Cost: the tests measure `-coverpkg=./internal/...`, so `cmd/` is outside
the floor, and the program's own paths are held by the smoke gate and
its tests alone.

### D-82 A guarantee stated over the tools is tested over every registered tool

A guarantee stated over the tools is tested over every registered tool,
never over a list someone wrote. `toolArgs` in `internal/server`'s tests
holds one call's arguments for every tool, and `TestEveryToolHasArgs`
fails when a registered tool has no entry.
`TestDebugLogsCarryNoDocumentData` drives that whole table and fails if
a log line carries the fixture's id, title or text (D-04).

A test that names the tools it drives proves the guarantee for those
tools. A tool added later is exempt until somebody remembers, and
nothing says so. Driving the registered set makes a new tool part of the
guarantee the day it is registered. Whether a call succeeds against the
fake does not matter: a refusal is logged too, and a refusal is where a
message is most tempted to quote the document.

Rejected: a test that names the tools it drives, which shrinks to the
tools someone thought of.
Cost: adding a tool costs a `toolArgs` entry, a live step (D-83) and a
row in the README's tool table, which the staleness gate checks, and
each fails loudly when it is missing.

### D-83 The live driver drives every tool and op kind through the binary over stdio; it is run by hand, never in CI

`make live` drives the built binary over stdio, as a client does,
against the signed-in account: every registered tool and every op kind,
on scratch documents it creates. A step that must be refused asserts its
refusal, so a green run means the guards fired as well as the writes
landed. The driver is a client that can be asked, and fails the run if
the server asks anything (D-52). Every line it prints goes through the
redactor (D-07).

Unit tests run against fakes written from what Google is believed to do,
and only a run against Google tests the belief. `TestCoverage`, in the
same package, reads the driver's own source and fails when a registered
tool has no step or an op kind never appears as an `op`, so the rule is
a check rather than a comment. It is run by hand, never in CI: it writes
to a real account and spends its quota.

Rejected: the coverage rule as a comment in the driver, which nothing
holds.
Cost: a run writes to the person's own Drive, needs
`GDOCS_ENABLE_DESTRUCTIVE=true` for the delete steps, and leaves its
scratch documents behind for the person to delete.

### D-84 Agent evals score whether a model can do the job through the tools; they are run by hand

The agent evals score whether a model can do a job through these tools.
`TestEvals`, under the `evals` build tag in `internal/evals`, runs each
task as a subtest: it seeds a scratch document through the server, runs
Claude Code headless with this server's tools alone, and checks the end
state read back through the server and the trace of tool calls. Some
tasks score a refusal: what the model does with `[not_found]`, a
read-only server or a stale revision. They run by hand.

Unit tests and the live driver show the tools work; they cannot show a
model understands them. The evals measure what a model makes of a result
and an error message, the one surface written for a model. They are the
evidence behind read tools returning text alone (D-19), and the trace
shows whether the model forced past the guard or explained it. Each
trace goes through the redactor before it is written (D-07).

Rejected: running the evals in CI, which would need a signed-in account,
the `claude` CLI and model usage on every pull request.
Cost: each task spends model usage, so a change that reads worse to a
model can pass `make check` and wait for the next eval run to show it.

### D-85 `make check` and CI run the same gates

`make check` and the CI workflow run the same checks, and `make parity`
compares them by what they run. Every command a target in `make check`
runs, its Makefile variables expanded, is one a `ci.yml` job a push to
`main` runs with the same words, as the shell splits them, or one an
action there stands in for. The `pr` job does not count: a push to
`main` never runs it. A `ci.yml` line that runs `make` with targets runs
their recipes and those of what they depend on. A command beside a gate
call on its line, in a list or a pipeline, is compared on its own. Every
go command `ci.yml` runs is one `make check` runs.

The gate refuses a command in `make check` whose failure make ignores,
behind a `-` prefix or under `.IGNORE`: check would pass where CI, which
runs it bare, fails. `TestParityRefusesACommandWhoseFailureMakeIgnores`
holds it.

`scripts/gates` has one list of commands, and each says where it runs:
in both `make check` and CI, in CI on a pull request only, at release
time, or by hand from a Makefile target or a pre-commit hook. A release
command is called from `.goreleaser.yaml`, `release.yml` or a workflow
of this repository that `release.yml` calls. A gate both run takes the
same arguments in both, and an omitted binary is the one the gate
defaults to. A bare binary name is not `./name`. Every file of the
checkout behind a build tag, read from its `//go:build` line, is vetted
on both sides by a pass that compiles it, CI's in a job a push runs.
The reader does not read `if:` on a step or a job, so a command under a
condition counts as one CI runs.

The two lists live in different files, and a person edits one of them at
a time. When they drift, the build-tagged files compile only where
somebody runs `make check` by hand, and a gate that runs only in CI is
one nobody runs before pushing. `schema-ack` measures from a pull
request's merge-base, which only a pull request has, so it runs there
alone. `TestParityGate` and `TestParityCatchesDivergence` hold it, the
second by planting each drift. `TestParityHoldsMakeCheckToWhatAPushRuns`
holds the push jobs, `TestTheReleaseIsReleaseYMLAndWhatItCalls` the
release, and `TestParityHoldsThePullRequestGates` the exception.

Rejected: adding the missing `vet -tags=` lines to the workflow by hand,
which fixes one drift and leaves the mechanism; a list of gates kept
separately in the Makefile, the workflow and the usage text.
Cost: a command goes into both in the same words, or is excused in
`scripts/gates/parity.go` with the reason. The gate refuses an excuse
whose command is gone.

### D-86 Every action is pinned to a full commit SHA, and every tool an action installs to one exact version

Every action a workflow uses is pinned to a full commit SHA, with its
version in a trailing comment. Every tool a step installs is pinned to
one exact version beside the action that installs it, and a container
image by its digest. A tool named in a workflow, the Makefile, a command
the goreleaser config runs or a string in the Go under `scripts/` is
named at one version everywhere, a `module@version` on any line but a
comment included. The Makefile names a tool as one `module@version`
variable, and one no recipe runs with the go command pins nothing. A
recipe or a hook that names a known tool as a word runs it from the
PATH, and is refused.

setup-go pins Go by go.mod alone, since it reads a `go-version` beside
go.mod first. A version key is read in any case, as the runner reads an
input's name. A version given to a reusable workflow of this repository
is exact, or one context's value that the workflow holds where it uses
it. The goreleaser config is read by key path as the goreleaser version
`goreleaserChecked` names reads it, and the gate fails when
`release.yml` runs another.

A pre-commit hook runs its tool through a Makefile target, so the hooks'
config names no version: a remote hook's `rev:` or a `module@version`
there would be one of its own. The `pins` gate fails on a
tag, a branch, a range of any form, a tool fetched at `latest`, an
action it has not classified, and when it finds too few workflows or
versions to read. It fails on a line break its readers do not read: a
lone CR, NEL, U+2028 or U+2029 in a YAML file it reads, and a lone CR in
the Makefile.

A tag is mutable by anyone who can write to the action's repository, and
a full SHA is the only way to use an action as an immutable release
([GitHub's hardening guide](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions)).
A SHA pins the wrapper, not what it installs: an installer that fetches
the newest major changes what signing needs while the SHA stays put. A
`~>` range resolves to the highest match, so it floats too.
`TestUnpinnedTools` holds the action half, and `TestPinsNamesEachProblem`,
`TestPinsRejectsEveryRangeForm` and
`TestAToolDownloadedAtLatestIsNotPinned` the tool half.
`TestReadersRefuseALineBreakTheyDoNotRead` holds the line breaks.

Rejected: a major tag such as `@v7`, which its owner can move; a `~>`
range, which resolves to the newest match.
Cost: Dependabot proposes the bumps weekly, for Go modules and actions,
and each one goes through a pull request that CI judges. A tool's
version is bumped by hand, since Dependabot moves the action SHAs and
not the versions passed to them.

### D-87 CI runs on Linux, macOS and Windows, and every `run` step in bash

CI runs the build, the tests with the race detector and the coverage
floor on Linux, macOS and Windows. Every workflow sets bash as the shell
for every `run` step at the workflow level, and no job or step sets
another. The `pins` gate checks both (`TestPinsShell`).

A suite that passes on one machine has passed on one machine. The
Windows runner's default shell is PowerShell, which turns
`-coverprofile=cov.out` into a file called `cov`, so the same line means
different things on different runners. Set at the workflow level, the
shell reaches a job somebody adds later, where a per-step setting
reaches only the steps that remembered it. An explicit bash runs as
`bash --noprofile --norc -eo pipefail`, so a command failing in a
pipeline fails the step everywhere
([workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#defaultsrunshell)).

Rejected: `shell: bash` per step, which a new job or step forgets.
Cost: `pipefail` is on for Linux and macOS too, and the setting never
reaches a `uses:` step. No gate holds the platforms in the matrix:
`make parity` compares commands, not runners.

### D-88 A workflow is read-only at the top, raises write per job, and keeps no checkout token

Every workflow starts with read-only permissions, or none, at the top. A
job that needs to write raises that permission in its own block: the
release job raises `contents`, `id-token` and `attestations`, and CodeQL
raises `security-events`. Every checkout sets
`persist-credentials: false`.

A permission granted at the top reaches every job, including one added
later that needs none of it. Raising it per job keeps a compromised or
mistaken step to what its own job needed, which is what OpenSSF
Scorecard's Token-Permissions check asks for
([Scorecard checks](https://github.com/ossf/scorecard/blob/main/docs/checks.md#token-permissions)).
No workflow pushes with the checkout's token, so leaving it on disk for
later steps is exposure with no use.

Rejected: permissions declared once at the top of a workflow; a checkout
token left on disk for later steps.
Cost: a job that needs a write carries a `permissions` block of its own,
and no gate checks either rule, so both are held in review.

### D-89 CodeQL runs on pushes, pull requests and weekly

CodeQL analyzes the Go code on every push to `main`, on every pull
request, and weekly. The weekly run is what reaches code that has not
changed: a query CodeQL adds after a merge still runs over it.

Tests check what the code was written to do, and `govulncheck` checks
the dependencies against known vulnerabilities. Neither looks for a
class of defect in the code itself, such as an unchecked path or an
injection. Static analysis does, and OpenSSF Scorecard's SAST check
names CodeQL for it
([Scorecard checks](https://github.com/ossf/scorecard/blob/main/docs/checks.md#sast)).
The workflow starts read-only and raises `security-events` for the
analysis job alone (D-88).

Rejected: tests and vulnerability scanning as the whole of static
analysis.
Cost: every pull request waits for a CodeQL build and analysis beside
CI, and the weekly run reports on `main` with nobody's change in it.

### D-90 A tag is the whole release, and its notes are that version's CHANGELOG section

Pushing a `v*` tag is the whole release. The release workflow builds the
archives, the bundle and the checksums, signs and attests them (D-91),
and publishes a release that is not a draft. Its notes are that
version's section of `CHANGELOG.md`, which `gates release-notes` prints
and passes to goreleaser. A tag with no section, or an empty one, fails
the release. The steps are in [release.md](release.md).

A release that waits on a manual step goes missing when the step is
forgotten, and a draft nobody publishes is that. The CHANGELOG entry is
written for a person deciding whether to upgrade, where a list of commit
subjects is written for the code's authors. goreleaser keeps no
`changelog` block, because `changelog.disable` is read before the notes
file is opened and leaves the body empty
([changelog.go](https://github.com/goreleaser/goreleaser/blob/v2.18.1/internal/pipe/changelog/changelog.go)).
`TestReleaseNotesTakesOnlyItsOwnVersion` and
`TestReleaseNotesRefusesAnEmptySection` hold the notes.

Rejected: `changelog.disable`, which empties the release body; a draft
release, which waits for a step nobody may take.
Cost: tags are pushed one at a time, since GitHub drops the tag events
past the third in one push, and the workflow can be run by hand against
a tag when one is dropped.

### D-91 A release is signed for people and attested for machines

A release is signed for people and attested for machines. The checksum
file carries a keyless Sigstore signature, as a bundle tied to the
release workflow's identity, so a person can check a download with
`cosign verify-blob`. Every archive, the Claude Desktop bundle and the
checksum file carry a GitHub build provenance attestation, which
`gh attestation verify` checks against this workflow and commit, and
`TestTheReleaseAttestsItsArchivesBundleAndChecksumFile` holds its
`subject-path` to those files. The attest step runs in the `release.yml`
job that runs goreleaser release, whose token writes `id-token` and
`attestations`: another job holds no `dist/`, and without either scope
the step fails after the release is published.
`TestReleaseAttestProblems` holds both. Every archive also ships an
SBOM.

The two answer different questions. A signature over the checksums says
the files are the ones the release workflow published, with no private
key to hold or leak. Provenance says which workflow, commit and runner
built each file. The bundle is attested in its own right, since its row
in the checksum file is a claim about that file rather than about the
bundle. The README gives both commands.

Rejected: the asset formats OpenSSF Scorecard's Signed-Releases check
looks for, `.asc`, `.minisig` or `.intoto.jsonl`, which would add a key
or a format nobody here verifies with.
Cost: Scorecard's Signed-Releases check sees neither the Sigstore bundle
nor the attestations, so it scores the releases as unsigned.

### D-92 Changes reach main through a pull request with CI green; no second reviewer is required

Changes reach `main` through a pull request, with CI green on Linux,
macOS and Windows, the maintainer's own changes and release commits
included. No second reviewer's approval is required.

`main` is released code: every version on the releases page is built
from a tag on it. A pull request is where CI judges a change before it
lands, and a direct push skips that. A rule requiring a second reviewer
cannot be met by a repository with one maintainer, and a rule that has
to be bypassed on every merge teaches people to bypass rules. The rule
is set in GitHub's repository settings, and `CONTRIBUTING.md` says how a
change travels.

Rejected: OpenSSF Scorecard's Branch-Protection tier 5, two reviewers
and code-owner approval with administrators included, which a
single-maintainer repository cannot meet.
Cost: a change merges on its author's review and CI alone, so a defect
CI cannot see reaches `main` unless the author's own review catches it.

### D-93 Each release carries a Claude Desktop bundle in the layout the sibling servers share, its settings derived from config.Vars

Each release carries a Claude Desktop bundle, a `.mcpb`, in the layout
the sibling servers' bundles share, google-sheets-mcp's among them:
`server/google-docs-mcp-darwin` (a universal binary),
`server/google-docs-mcp.exe`, `server/google-docs-mcp-linux-amd64`,
`server/google-docs-mcp-linux-arm64` and `server/launch-linux.sh`, with
`LICENSE` and `README.md` at the root. What it asks for at install is
derived from `config.Vars`: every variable the server reads, less a
removed one and less each that `bundleLeftOut` names with its reason,
asked under the variable's name without `GDOCS_`, lowercased.

Opening a bundle installs the server and asks for its settings, where
the alternative is a hand-edited configuration file. A setting added to
the server is asked for until the manifest asks for it or a reason is
written, so the bundle cannot fall behind. `make mcpb` holds the
committed manifest to the files the packer stages and to the
specification, which requires a privacy policy of a server that sends
user data to a service
([MANIFEST.md](https://github.com/anthropics/mcpb/blob/v2.1.2/MANIFEST.md)).
The gate, the packer and the registry entry read the manifest through
one function, which refuses a key repeated in one object, case aside.

Rejected: a `tools` list, which hosts show for display only and which is
wrong for most installs, since read-only mode and the destructive switch
change what registers; `$schema` at a branch, which upstream can change
under the manifest.
Cost: Windows carries amd64 only, which Windows on arm64 runs under
emulation. Issue #113 covers the settings table that
docs/configuration.md and the README type by hand, which can drift from
`config.Vars`.

### D-94 A bundle install starts the server as an unconfigured start does, every setting passed by env

A bundle install starts the server exactly as an unconfigured start
does. Every switch is a boolean defaulting to false, and every other
setting defaults to `""`, which the server reads as unset. No default
holds a `${...}` reference. Every setting reaches the server through
`env`: `mcp_config.args` is `[]`, a platform override carries neither
`env` nor `args`, and every command starts with `${__dirname}/` and
names a staged program.

Claude Desktop approves each tool, and a person can set a tool to always
allow
([Claude connectors](https://claude.com/docs/connectors/getting-started)),
so a switch left off is the one guard a bundle install keeps.
`make mcpb` starts the server's own `config.Load` from the environment
the defaults give it and refuses any field that differs from an
unconfigured start, naming it. The reference host leaves a blank
optional field as the text `${user_config.x}` and replaces the base's
`env` and `args` with an override's
([config.ts](https://github.com/anthropics/mcpb/blob/v2.1.2/src/shared/config.ts#L118-L120)).
The profile and default write mode fields give the rules the server
refuses another value with. `TestTheConfigDiffNamesTheFieldsThatDiffer`,
`TestTheBundleSettingsRefuseEachBreak` and
`TestVarsDescribeWhatBuildDoes` hold it.

Rejected: an optional key without a default; `env` or `args` on a
platform override; a relative command; a switch defaulting to the text
`"false"`.
Cost: a person turns each switch on by hand after install. Issue #112
asks to install the bundle in Claude Desktop itself on each platform,
since the host's handling rests on the reference host and Claude's docs.

### D-95 The server asks the person before a risky write, through form elicitation

The server could confirm a risky write itself. Before a delete or a
forced write, it would put a form with no fields to the person through
MCP form elicitation, and write only on an accept. It is declined: the
server asks the person nothing (D-52).

The MCP specification puts consent to each tool call on the host, and
lets a client answer an elicitation "from the user or other sources"
([elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation)).
Clients answer such a question with nobody there: a Claude Code
`Elicitation` hook accepts it with no dialog, and Codex in full access
accepts a form with no fields by itself. Others never draw it, and no
client's permission rules match it. A client remembers an approval per
tool, never per argument, so a risky variant carried as an argument
rides along unseen once its tool is always allowed. People stop reading
prompts they see often. `forbidigo` refuses the SDK's `Elicit`.

Rejected: confirming writes through elicitation; Claude Code's
`requiresUserInteraction` mark sent per client beside the question; a
setting that refuses the asking writes on a client that cannot ask.
Cost: a client running with nobody watching makes every registered
write, so `GDOCS_ENABLE_DESTRUCTIVE` stays off for one, and a
configuration that still sets `GDOCS_REQUIRE_PROMPT` on is refused at
start (D-53).

Next ID: D-96
