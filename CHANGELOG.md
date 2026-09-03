# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

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
