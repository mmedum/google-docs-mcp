# Changelog

All notable changes to this project are documented here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/). Tool removals, renames
and new required fields are breaking; the schema diff in CI flags them.

## [Unreleased]

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
