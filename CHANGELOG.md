# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

### Added

- The bundle gate holds what the manifest says about ITSELF. `$schema`
  and `support` were not decoded at all, so no check could read them —
  and `$schema` named `main`, a branch upstream can amend under a
  document that claims to conform to it. The filename pins the FORMAT;
  the ref pins the BYTES, and only one of the two was pinned.

  Four claims: `$schema` is upstream's published path at a ref that
  cannot move — a full release tag or a commit SHA — the version in that
  URL equals `manifest_version`, that version is not below the one this
  repository has checked, and a `support` URL says where a failing
  install is reported. The ref rule is an allow-list over the whole URL,
  because refusing the branch names `main`, `master` and `HEAD` passes a
  branch called anything else, a partial tag like `v2.1` that upstream
  re-points as it releases, and the right filename served by somebody who
  is not upstream.

  The floor is the claim the other three structurally cannot make: they
  hold the manifest against itself, and 0.2 beside a 0.2 schema is stale
  and entirely self-consistent. Checked against the published schemas —
  v0.2, v0.3 and v0.4 are served and v0.5 is not, and 0.4's only change
  is a `uv` value in the `server.type` enum, which a `binary` server
  gains nothing from.

- A **Claude Desktop bundle** (`.mcpb`) on every release, and the MCP
  registry entry that points at it. This server shipped archives and
  nothing else, so installing it meant hand-editing a config file and it
  could not appear in the registry at all — the registry's `mcpb` package
  type needs a bundle.

  The bundle carries a macOS universal binary, a Windows one, both Linux
  architectures and a launcher that picks between them from `uname -m`
  and **execs** it — not a call, because the server talks MCP over that
  process's stdio and a shell left in the middle would own the pipes. On
  an unknown architecture it writes to stderr, never stdout, where a line
  of English would corrupt the JSON-RPC stream before the first request
  completes.

  It is packed in the universal binary's post hook — the one point where
  every binary exists and `checksums.txt` has not been written — and
  named in **both** `checksum.extra_files` and `release.extra_files`.
  Both, or it ships unsigned, or is hashed and never published, and
  neither looks any different on the release page.

- `make mcpb`, which holds the manifest against the bundle the packer
  stages: every path the manifest names must be a file going in, every
  `${user_config.x}` must be declared, every platform must spawn the file
  staged FOR it, and the Linux launcher must choose between the packer's
  own names. A schema catches none of those — each produces a bundle that
  installs and then does nothing.

- `gates registry-publish`, which builds the registry entry from the
  release's **own `checksums.txt`**, so the hash describes the bytes that
  were published. It runs from its own workflow with `id-token: write`
  and `contents: read` and nothing else, and `mcp-publisher` is verified
  with cosign before it is unpacked. A prerelease tag skips it: an entry
  cannot be taken back.

### Fixed

- The release stamped the binary's version from `{{ .Tag }}`, so in a
  `--snapshot` rehearsal the binary disagreed with everything else. It
  comes from `{{ .Version }}` now, which is the same string on a real tag
  and lets the bundle's version be checked in all five places before one.

### Added
- The `pins` gate classifies every action, and an unknown one fails it.
  The gate could only ever check the versions that were *written*; an
  action that installs a tool and names no version at all is an absence,
  and nothing could see it. That is not hypothetical — the Pipedrive
  server's release failed on exactly this shape, with
  `sigstore/cosign-installer` pinned by SHA and no `cosign-release`, so
  the job took whatever cosign was newest and that cosign had changed its
  default signing format. `download-syft` had the same hole one step
  below it. **A SHA pins the wrapper, not the tool.**

  This repository was not affected — it pins both — but nothing held
  that. Every action is now in one of two tables, the installers with the
  input that pins each one's tool and the actions that install nothing
  with the reason, and an action in neither fails the gate, because being
  unclassified is the state that let the other two through.

  Watched failing on all three shapes before being trusted:
  `cosign-release` removed, `syft-version` removed, and an unclassified
  installer added.

## [1.2.0] - 2026-09-15

### Added
- `status --json` prints the same state as one JSON object on stdout, so
  a script can read whether this server is authorised instead of parsing
  the text written for a person. `credentials.resolved` is the field to
  branch on; `schema_version` changes only when a field is removed or its
  meaning changes, never when one is added.

  The design is an outside contributor's, taken from the Drive server
  where it landed first, and the reason is a fault this repository has
  already caused: a label moved under a release — `refresh token:` became
  `token store:` — and a check written against the old one started
  reading "not authorised" for an account that was fine. It fails
  silently, and the usual response to "not authorised" is to run `login`,
  which asks a person for consent they already gave.

  One collector, two renderers, so the two cannot drift: the text output
  is byte-identical to what the released binary prints, which is asserted
  by diffing them rather than by reading.

  **The account is masked at the collector, not on the way out.** The
  text path redacts inside `outf`; the JSON encoder does not go through
  it, so a struct written straight to the stream would have carried the
  address in full. Held by a test that drives the collector and was
  watched failing with the redaction removed.

## [1.1.4] - 2026-09-14

### Fixed
- The export directory has to exist. Only a relative path was refused before, so
  an absolute one with a typo in it was accepted at startup and failed
  much later, at the moment somebody tried to move a file — a long way
  from the setting that caused it. `GDOCS_EXPORT_DIR` is now checked for being an
  absolute path, existing, and being a directory, and each failure names
  the setting. Unset is still allowed and still means the feature is off;
  that is a decision, not a mistake.

## [1.1.3] - 2026-09-14

### Added
- `forbidigo` holds the rule that stdout carries only MCP JSON-RPC
  frames. That rule is in this repository's CLAUDE.md and in the MCP
  specification — the stdio transport says the server **MUST NOT** write
  anything to stdout that is not a valid MCP message, and **MAY** log to
  stderr — and until now nothing enforced it. Verified by injection
  rather than by reading: a `fmt.Println` added to `internal/service/`
  passed the entire `make check`, in all four servers. A stray print
  there corrupts the JSON-RPC stream, and the failure a person sees is a
  client that silently stops working.

  It is configuration rather than a new gate, because golangci-lint
  already runs here and `forbidigo` already does this job. Two patterns,
  `^fmt\.Print.*$` and `^os\.Stdout$`, with `analyze-types: true` so
  that an aliased import still matches and so that stdout is caught as a
  *destination*: after the writers were threaded through there are far
  fewer `fmt.Print*` calls and many `fmt.Fprint*`, and
  `fmt.Fprintf(os.Stdout, …)` corrupts the stream identically. Both
  spellings are held, and both were injected and watched to fail.

  The process's streams are now named in exactly one place — `main`,
  which carries the single `//nolint:forbidigo` and a sentence saying
  why. `scripts/` is excluded by path: it is maintainer tooling run at a
  terminal, not the server.

  A hand-written gate was drafted first and thrown away. It would have
  needed a path constant, which is how the existing print check came to
  read one file (`const file = "main.go"`) out of a whole server, and a
  floor on files read so it could not pass by reading nothing. Neither
  problem exists in a linter that is handed the package list.
- The release page carries the release notes. `gates release-notes`
  prints the `CHANGELOG.md` section matching the tag and the release
  workflow passes it to goreleaser with `--release-notes`, so what a
  person wrote for a release is what a reader of the release sees. It
  replaces a machine list of full commit SHAs that included the
  `Release X.Y.Z` commit itself and matched the changelog nowhere. A tag
  whose section is missing or empty fails the release rather than
  publishing one that says nothing.

  The command is the sibling servers' one, not a new one: this repository
  had no version of it, and the standard's rule is to copy the existing
  answer rather than invent another. The compare-link footer stop came
  with it, and is worth keeping — the footer follows the oldest section
  with no heading in between, so without it the oldest release's notes
  end in a block of links.

  **Do not reach for `changelog.disable` to stop the generated list.** It
  is read in the changelog pipe's `Skip`, which runs before `Run`, so
  `ctx.ReleaseNotes` is never assigned and the file named by
  `--release-notes` is never opened: the body collapses to the footer
  with nothing above it. That is not hypothetical — a sibling carries
  `disable: true` and passes `--release-notes` in the same workflow, and
  its release page has shown a footer and nothing else ever since. The
  `changelog:` block is deleted here instead.

  `release.footer` stays in `.goreleaser.yaml` and still works:
  `internal/pipe/release/body.go` wraps `ReleaseNotes` in
  `release.header` and `release.footer` on every path, `--release-notes`
  included. The pair the changelog pipe's early return skips is the
  `--release-header` and `--release-footer` *flags*, a different thing
  with a similar name — and getting that backwards first is why this is
  written down. Read out of goreleaser v2.18.1; the documentation does
  not cover the interaction.
- `CODE_OF_CONDUCT.md`, Contributor Covenant 3.0 — the one community
  health file GitHub's checklist names that this repository did not have.
  Reports go through GitHub's private security advisory flow rather than
  an address, because hard rule 1 keeps account emails out of the tree
  and the `leaks` gate enforces it.

### Changed
- `main` is one line and the dispatch lives in
  `run(args []string, stdout, stderr io.Writer) int`, the shape the
  sibling servers already had. `main` calls `os.Exit`, which no test
  survives, so nothing about the dispatch could be tested — and the
  unknown-command guard added in the previous change proved it: deleting
  the guard from `main` left every check green, because the test that
  came with it tested an extracted predicate rather than the behaviour.
  A test that cannot fail when the behaviour is removed is not holding
  the behaviour, which is the fault this repository's evidence log
  already records twice under a different name.

  The guard is now inline, as it is in the siblings, the predicate helper
  is gone, and `TestAnUnknownCommandIsReported` drives `run` and asserts
  the exit code, the message and that nothing reached stdout. Verified
  the only way that counts: neuter the condition so the file still
  compiles, watch the test go red, put it back. Deleting the guard
  outright is not that test — it leaves an import unused and fails the
  *build*, which looks like a red test and proves nothing.

  A second test holds the other direction, since a guard keyed on a
  leading dash is one typo from rejecting a real flag: `--version`,
  `-version`, `--dump-schemas`, `help`, `--help` and `-h` must all still
  reach their command.
- Release notes are published one heading level up. In `CHANGELOG.md` a
  version is an `##` and its change kinds are `###` underneath it; on the
  release page the version heading is gone, because GitHub renders the
  tag name as the page's `h1`. Published unaltered, the notes therefore
  started at `h3` directly under an `h1` — a skipped rank, which the
  W3C's heading guidance says to avoid. Confirmed by reading the rendered
  page rather than guessing: `h1 v2.0.4`, then `h3 Added`.

  So the section's headings are lifted one level on the way out, and the
  page reads `h1` then `h2` with nothing missing between. No wrapper
  heading was added: "Changelog" restates what the page obviously is, and
  repeating the version duplicates what GitHub already prints above it.
  Fenced code is left alone, since a `#` comment in a shell block is not
  a heading, and only `h3` and deeper are lifted, so a second `h1` can
  never be emitted.
- The README follows one skeleton, shared with the sibling servers and
  checked against GitHub's own README guidance, the community profile
  checklist and the standard-readme spec. What changed here: a short
  description under the 120 characters the spec asks for, `Why another Google Docs MCP` renamed to
  `Why google-docs-mcp`, so the four servers name that section the same
  way; `Reporting a problem` renamed to `Getting help` and moved out of the way of somebody
  installing, a `How it works` section, a `Documentation` section
  pointing at the three files under `docs/`, `Versioning` moved back with
  the other reference sections, and a tail of
  `Contributing → Security → Code of conduct → License`, each one line
  linking the file it names. `Licence` is now `License`: it names the
  `LICENSE` file and the `Apache-2.0` identifier, and British spelling
  stays in the prose.

### Fixed
- A mistyped subcommand exits non-zero instead of starting the server.
  `flag` stops at the first non-flag argument and returns `nil`, so a
  stray word stayed in the argument list where nothing read it and the
  default path ran the server: `google-docs-mcp statsu` printed
  `serving MCP over stdio` and exited **0**. On a terminal that reads as
  a hang, since the server then blocks on stdin and says nothing.

  The exit code is the part that travels. Anything driving the binary — a
  setup script, a health check, an agent writing its own client config —
  takes 0 for "that worked", so a typo was indistinguishable from a
  correct invocation until the server turned out not to be there.

  A leading dash is the only thing separating a flag from a mistyped
  subcommand, so the guard is exactly that, and every documented
  invocation still reaches the server untouched. Contributed from
  outside, after running the servers side by side and noticing that two
  of the four already guarded this and two did not
  ([#58](https://github.com/mmedum/google-docs-mcp/pull/58)).

## [1.1.2] - 2026-09-13

### Added
- A `transcript` gate, which the two sibling servers had and this one
  did not. The live driver and the eval harness may put a value into
  their transcript only through a redacting helper, and the gate reads
  their syntax trees to say so — 81 writes, each a literal, a count, or
  through `shown`/`clip`/`redact.*`, with an allowlist carrying a reason
  per entry for the rest.

  It could not be copied from a sibling: theirs gate `os.Stdout`, because
  their drivers print, and these log through `testing.T`, so a copied
  gate would have had nothing to read and passed by construction. That is
  worse than no gate, because it reports a guarantee nobody is holding.

  Writing it found one: `s.t.Fatalf("harness step %s failed: %s", name,
  b.String())`, where `b` held the tool response — document text, logged
  whole, at exactly the moment somebody pastes the log into an issue.
  Every neighbouring line went through `clip`. Fixed, and the gate now
  refuses it: putting the call back fails, a new log line carrying the
  same value fails, and pointing the gate at a directory with no drivers
  in it fails rather than reporting nothing to do.

### Changed
- `--dump-schemas` emits the whole registrable surface, so the schema
  diff compares like with like. `delete_comment` and `delete_tab`
  register only under `GDOCS_ENABLE_DESTRUCTIVE`, and the dump carried a
  default build, so neither was in either side of the comparison and a
  removed field or a new required one on them was invisible with every
  check green. The decision now sits in `tools.FullSurface`, beside
  Register, which is the only place that can name every gate Register
  reads; a test enumerates the gate flags and fails if any combination
  registers a tool the full surface does not. A sibling server had the
  same fault and was fixed the same way, having first been fixed two
  other ways.
- The `classes` gate's Make target is called `classes`, like the gate and
  like the other three servers, instead of `gate-classes`. The parity
  gate's rename map is one entry shorter, which its own comment asks for.
- `golang.org/x/oauth2` is at v0.37.0 and `golang.org/x/time` at v0.16.0,
  what dependabot proposed, applied on top of the work above rather than
  merged from a branch predating it. oauth2 is not an ordinary dependency
  here — it is the token refresh — so `make check` passing is not the
  whole story: `doctor` was run against a real account and the refresh
  token exchange succeeded, which is the path no unit test reaches.

## [1.1.1] - 2026-09-13

### Fixed
- `--version` reports one spelling whichever way the binary was built.
  goreleaser stamps its own `{{.Version}}`, which has the leading `v`
  stripped, so a release archive said `1.1.0`; `go install` stamps
  nothing and the fallback reads `v1.1.0` out of the build info. The same
  release therefore reported two different strings depending on how
  somebody installed it, and anything parsing `--version` got a different
  answer per install method. Reported from outside by a reader comparing
  five servers side by side.
  The release stamp now carries the tag itself rather than goreleaser's
  v-stripped form, so the two sources agree at the source; the
  normalisation stays for a version passed by hand to `make`.
- `status` prints the same lines, in the same order, with the same
  labels as the three sibling servers, once a profile is configured (the
  not-yet-signed-in message still differs between them). They had drifted into four shapes
  — a version banner in three of them, `client secret` against
  `client json`, `read-only` against `read only`, four label widths — and
  the same reader found that too.
- `status` reports the account the same way in all four: the local part
  removed, the domain kept. The domain is the half a diagnosis uses —
  shared drives are a Workspace feature and a personal account cannot
  create one, so `@gmail.com` and a Workspace domain are two different
  sets of behaviour to explain — while the local part answers nothing.
  It is never an input to any command here, and this output is what the
  issue form asks people to paste. One server showed it in full, one
  masked the domain as well (which hid the useful half), and two sat in
  between.
- A permission failure no longer repeats the account it refused. Google
  names the account in the message of a 403, and that message was
  repeated verbatim into the error string — as was the whole response
  body when it was not an error envelope at all. That string reaches
  stderr, and the MCP stdio transport says a server may write logging
  there and clients "MAY capture, forward, or ignore" it, while the
  protocol's logging section says log messages MUST NOT carry personal
  identifying information. The local part is now masked where Google's
  text is parsed — one place, rather than at each print, so a print added
  later is safe without its author knowing the rule, and a writer wrapper
  could split an address across two Write calls and miss it. The domain
  is kept, because it is what says which account was refused.

## [1.1.0] - 2026-09-13

### Added
- An API-coverage gate. `make api-coverage` holds three things to each
  other: every method the Docs and Drive discovery documents publish, one
  hand-written verdict per method (`used`, naming the `internal/gapi`
  method that implements it, or `out` with a reason), and the client's own
  calls read out of the syntax. A capability Google adds now fails the
  build until somebody judges it, and a call with no row fails it too —
  67 methods published, 17 used. `make api-diff` refetches and rewrites
  the snapshot, reporting what is new, gone or moved; it is the only thing
  here that touches the network and deliberately not a gate, because a
  check that fails when Google is slow is one people learn to rerun until
  it passes. The snapshot it writes is what CI reads, so completeness is
  checked on every pull request rather than whenever somebody remembers.
  Watched failing four ways. The two-file split, and both traps it avoids,
  came from the chat server building the same gate first.
- An API-fields gate. `make api-fields` holds the wire types to the Docs
  discovery document the way `api-coverage` holds the client to its
  method list: the published schemas and properties in
  `testdata/api-fields.json`, one hand-written row per exception in
  `testdata/api-fields.tsv` (`out`, `extra` for a Developer Preview field
  public discovery does not publish, `alias` for a schema modelled under
  another name, `local` for a struct that models no published schema at
  all), and the modelled side read out of `internal/gdocs` with `go/ast`,
  promoting the tags of embedded structs. Three directions fail the
  build: a published property nothing models, a modelled field nothing
  publishes, and a struct that matches no schema and has no row. The
  third is what makes the set being compared part of the rule — rename
  `gdocs.SectionStyle` and the gate names the renamed type, where a
  floor under the number of schemas matched did not, because it sat at
  80 against a real 104. A row that has outlived what it describes — a
  property Google has withdrawn, a field the types have since grown —
  fails too, the way an `api-coverage` row does.

  It was written because the types had drifted 40 fields from the API
  without anything saying so — the suggestion field behind #46, and 39
  more the spike that found it went on to count. Judging those 39 against
  "if a tool writes a field, the types must carry it" added 16 of them:
  twelve `SectionStyle` fields, a table's column widths and a row's
  pinned-header flag, every one of which `layout_document` or `edit_table`
  could already set and no read could report. The other 23 are read-only
  detail with a reason each. `make api-diff` writes both snapshots, and
  the snapshot descends into any object a discovery document defines
  inline rather than as a `$ref`, recording it as a schema of its own
  named `Parent.property`. Docs v1 has none — every nested type there is
  a `$ref`, checked against the live document — but Drive v3 declares
  twenty-one, and reading only the top level is how the sibling server's
  `File.capabilities` went 46 fields unchecked. The descent is here so
  that omission cannot start being true.

### Changed
- `read_document format: raw` returns the bytes Google sent, not a
  re-encoding of this server's wire types. Its schema promises "Docs API
  JSON", and marshalling the types could only ever return the fields they
  model — so the one read whose job is to show what the API said was the
  least faithful read on the server. It dropped 39 published fields
  across nine types (`SectionStyle` alone missing all four margins,
  `columnProperties` and `pageNumberStart`), and it is how #46 came to be
  filed against the write path at all: the raw read had dropped the field
  that proved the write had worked. Elements now keep the bytes they were
  decoded from; a document built in Go, with no bytes to keep, still
  encodes from the types. Output stays compact, so `max_chars` still buys
  the same amount of document.
- Every request asks for compact JSON. Google indents by default, which
  on a 150-page document is 7.44 MB on the wire against 2.96 MB without
  it — 60% of the bytes for whitespace nothing reads, and, now that
  elements keep what they decoded from, retained indentation as well. The
  round trip costs 28 → 41 ms of decoding and 7.7 → 11.2 MB of heap for
  the kept bytes on a document that size, against 4.5 MB less to
  download. Those figures come from a prose-heavy document and are not a
  bound: an element's bytes are held again inside every ancestor's, so a
  deeply table-nested document retains its cells once per level. Asked for in the one place that builds an HTTP request, not
  in `documents.get`'s query where it began: the Drive half of this
  client — exports, comment threads, revisions — was still receiving
  indented JSON, and a call added later should not have to remember.
  Skipped when the request is not asking for JSON, because an export asks
  for bytes.
- A direct formatting change over a property a pending suggestion already
  sets now comes back with a warning naming the suggestion and the
  property. Google accepts such a request, replies with an empty result
  and sometimes applies nothing — verified live: bold over a suggested
  bold, and an alignment over the same suggested alignment, both leave
  the document unchanged, while a font size set to the value a suggestion
  names does land. The rule is inconsistent, so this warns rather than
  refusing; `ops_applied` alone could not tell anyone which had happened.

### Fixed
- Suggestions that only change formatting are no longer invisible. A
  suggested restyling inserts and deletes nothing — the API records it as
  a `suggestedTextStyleChanges` entry on the run — and `internal/gdocs`
  had no field for it, so every read reported the document as holding no
  suggestion: `list_suggestions` answered `0 pending suggestion(s)` in
  the same session where the write had just returned a suggestion id,
  `read_document format: raw` dropped the field while re-marshalling our
  own wire types, and `include_suggestions` and `with_styles` showed
  nothing. Reported as a silent write failure in #46; the write had
  worked all along, and the reading of it was what lied.

  All sixty-one `suggested*` fields the Docs discovery document publishes
  are now carried, across twenty-two types, so a suggestion this server
  cannot render is still one it cannot report as absent. `list_suggestions`
  gains a `format` kind, a `formats` field saying what a suggestion
  restyles (`text: bold`, `paragraph: alignment`) and a `restyled` field
  quoting the text it covers, the way `inserted` and `deleted` already
  quote theirs; a read marks one as
  CriticMarkup `{==text==}{>>s:<id> suggests bold<<}`, a highlight rather
  than an insertion, because nothing was added or removed. What a
  suggestion sets is read from its `*SuggestionState`, never from the
  style beside it: the API fills that with every inherited property, so
  trusting it reports nine changes where a person asked for one. A state
  that is present and names no property of its own is named by its stem:
  Google publishes `EmbeddedDrawingPropertiesSuggestionState` with no
  fields in it, so a suggested change to a drawing walks down to nothing
  and would be a suggestion the read denies exists — it comes back as
  `embeddedObject.embeddedDrawingProperties`. An empty property list
  still means what it says, a state that sets nothing, and is still not
  reported.
- A suggestion that adds or removes a blank line is reported in the
  direction it goes. `list_suggestions` decided insert from delete by
  looking at the quoted text, which is trimmed of the trailing newline
  for display — so a suggestion whose whole content is that newline
  quoted as nothing either way, and an inserted blank line was reported
  as a deletion. The kind now comes from which edit was recorded, and
  the quoted text is display only.
- A suggested restyling is reported once, not once per carrier. Google
  splits a text run wherever the existing style changes, so one suggested
  bold across a link or an already-italic word is recorded on three runs
  — `list_suggestions` read back `(text: bold) (text: bold) (text: bold)`
  and quoted only the first run's text. The carriers of one suggestion
  now merge in the model, so `formats` has one entry per target and
  `restyled` quotes the whole span; the sixty-character clip happens once
  over that span rather than once per piece.
- `with_styles` reports a pending change to a paragraph's own style. It
  printed the paragraph's committed alignment and said nothing about a
  suggestion to change it, while doing the right thing for a run — with
  CriticMarkup off there is no marker to carry the suggestion, so the
  annotation is the only place it can appear.
- A suggested restyling of a table row is reported once, not once per
  column. The model hangs a row's pending change on every cell so that a
  guard on any cell can see it, and the reader was emitting one per cell
  — invisible against a one-column fixture.
- A response without tabs content keeps its suggestions. The Docs API
  carries the same collections on the document itself when the caller
  did not ask for tabs, and the suggestion reader read only the tabs, so
  a list, an object or a document-style suggestion vanished on that
  response shape while the document-wide ones were reported twice on the
  other. Both shapes now go through one reading of what a tabless
  response describes, `gdocs.LegacyTab`, which is also what `Parse` uses
  — it was two lists before, and they disagreed in both directions.

## [1.0.1] - 2026-09-07

### Added
- `make leaks` refuses a build artifact. The rules are regexes over text,
  so the scan skipped any file holding a NUL byte — and an 8 MB `gates`
  binary, carrying 66 absolute paths from a maintainer's machine because
  it was built without `-trimpath`, passed the gate, `make check` and
  eight green CI checks. The history scan skipped it for the same reason.
  A binary is now refused if it starts with an executable's magic number
  or exceeds a megabyte, while it is still untracked, so it fails before
  `git add -A` can sweep it in. Two of the four servers in this family
  made the same mistake; the other has three of them on a public `main`.

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
  tagged vet pass to appear in both. Both files are read with their
  commented-out lines removed, and a gate counts as running in CI only
  where a `run:` step names it: matching the raw text meant that
  commenting out a step to unblock a red build left parity green, which
  is the divergence it exists to catch, reached by typing one `#`. The
  rule is a function over the two files' text, and its tests watch it
  fail in both directions and on each of the ways a step can be present
  without running.
- §17 has no open decision left: the parity gate closes the one that
  stood there. The staleness gate caught the status line still claiming
  it, which is what that half of the check is for — and then caught a bug
  in itself, because "No design decision is open" contains "decision is
  open", so a denial read as a claim. Both are covered now.
- `scripts/gates` is one registry: the usage text, the dispatch and the
  parity gate all read the same list, so they cannot drift from each
  other.

### Added
- The staleness gate holds the README's OAuth scope list to the scopes
  `login` actually requests, derived from `auth.Scopes` rather than typed
  out. The setup step is the one page a person follows exactly once, with
  no way to tell it was wrong until a login is refused, and no gate
  compared it with the code. All four sibling servers had a version of
  this: one listed four scopes and explained none, one listed a single
  scope where `login` can request five under feature flags, and one said
  "add the two scopes below" and then listed none at all, in a released
  README. A feature-gated scope is the one nobody notices, because it is
  absent from every run that does not use the feature. Watched failing in
  both directions — a scope dropped from the README, and one listed that
  the code never requests.

### Fixed
- A read-only server no longer advertises tools it does not register. The
  server instructions were one constant while `GDOCS_READ_ONLY=true` drops
  six groups of tools, so a read-only server opened by telling the model
  to "edit with edit_document and format_document" and then registered
  neither. They are built for the configuration now, and the read-only
  text says whose limit it is: a model that cannot tell "this server was
  started without writes" from "Google Docs cannot do that" reports the
  wrong one to the person who asked. Every test that read the
  instructions had built the default surface, which is why nothing caught
  it; the new one builds all three.
- Exported filenames. Three things in one sentence, two of them reported
  by the first outside person to export a document. A comma in the title
  became two spaces, because each run of unsafe characters was replaced
  with a space and the space after the comma was safe and survived. A
  shortened id ended in U+2026, which reads well in prose and badly in a
  name somebody has to type, tab complete or move between filesystems.
  And the 80-character limit was a byte slice, so a title in a language
  whose characters are not one byte each was cut through the middle of a
  rune — unreported, because catching it needs a long title nobody here
  had exported.
- The README lists four OAuth scopes and never said why, so the first
  outside reader to check what `login` actually requests found two of them
  and concluded the other two were dead. They are not: a normal login asks
  for `documents` and `drive`, and `GDOCS_READ_ONLY=true` asks for the
  `.readonly` pair instead, so a consent screen has to carry both or
  read-only mode is refused the first time somebody tries it. The
  instruction was right and unexplained, which for a setup step is the
  same as wrong.
- The parity gate called a gate missing when a `run: |` block ran it.
  Requiring the command on the `run:` line itself was safe here — nothing
  in the workflow is a block scalar — and wrong in general: it fails
  closed, so nothing slips through, but the first multi-line step
  somebody writes reports a gate CI plainly runs as absent, and a gate
  that cries wolf is a gate that gets edited out. Found by the drive
  server, which hit it while porting the same rule.
- The error-class gate could not see a duplicate. It compacted
  `service.Classes` as it stood, and `slices.Compact` only removes runs
  of adjacent equals, so a class written twice anywhere but next to
  itself passed — in an unsorted literal, which is every case that would
  actually happen. It sorts a copy first, and the test fails against the
  old version.
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
