# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

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

### Fixed
- `manage_tabs action: move` with a `position` later than the tab's
  current one moved it a place short, and moving a tab to the very next
  position did nothing while reporting success. Google inserts the tab at
  the given index while it still occupies its old slot and removes it
  afterwards, so the index needs raising by one when a tab moves later
  within the same parent; moving it earlier, or under a different parent,
  was already right. Found by the end-to-end live driver, whose Phase 4
  section had been addressing the wrong tab as a result.

### Changed
- `doc.Paragraph` now carries its whole paragraph style (alignment, line
  spacing, space above and below, both indents, keep-with-next,
  page-break-before) in a `doc.ParagraphStyle` it shares with the new
  named style definitions, instead of the two fields the renderers
  happened to read. No output changes; `read_document with_styles`
  annotates the same alignment and indent as before.

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
