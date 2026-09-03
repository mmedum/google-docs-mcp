# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

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
