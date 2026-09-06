# Architecture — google-docs-mcp

**Status:** v0.9.1 (2026-09-05). All design decisions are resolved (§17).
Phases 0, 1 and 2 are implemented: auth, raw client, model, renderer,
reads, search, create, export, editing with minimal diffs in all three
modes, formatting, suggestion review, comment threads on both backends,
revision history and diffs, tables, tabs, headers, footers, footnotes,
images and chips; §16 lists what Phase 3 adds. Every convention here was
checked against primary sources; §18 lists what was confirmed, refuted,
and changed.

## 1. Mission and scope

A production-grade, Go, stdio MCP server that lets Claude work **inside a
Google Doc** the way a careful colleague does: read it at the right
granularity, edit it in place without destroying anything around the
edit, propose changes as suggestions rather than overwrite, comment on
specific passages, and handle tables, tabs, headers, footnotes and
formatting. Single binary, per-user OAuth against the user's own Google
account, Workspace or consumer.

**The repository is self-contained and meant to be distributed.** Every
deployer creates their own Google Cloud project and OAuth client; nothing
deployer-specific is baked into the code, the repository, or the release
artifacts (§13).

**Scope is one document, every capability.** In: locating a document by
title, creating one, and everything that happens inside it, including its
history (revisions, suggestions, comment threads). Out (**decided**):
folder management, moving files, sharing, trashing, copying.

### Why build it (research summary, verified 2026-09-02)

- **Google's official Docs MCP** (`docsmcp.googleapis.com`, Developer
  Preview) exposes two tools, `read_doc` and `update_doc`, that pass the
  raw `documents.get` JSON and raw `batchUpdate` requests straight
  through. It solves the plumbing and none of the hard part.
- **The best open-source servers** (taylorwilsdon/google_workspace_mcp,
  a-bonus/google-docs-mcp, piotr-agier/google-drive-mcp) make the model
  compute UTF-16 indices itself, hand-roll markdown converters that
  silently corrupt documents (a-bonus #149), anchor comments through the
  Drive API where they never render inline (a-bonus #134), and none use
  `writeControl`, suggestion mode, or return the ranges they changed.
- **The API moved in our favour in July 2026.** The Docs API now has
  `writeControl.writeMode: SUGGEST` (every request in the batch becomes a
  suggested edit), `insertComment` anchored to a real `Range`,
  `acceptSuggestion`/`rejectSuggestion`, and a `commentsViewMode` on
  `documents.get`. All of it is **Developer Preview** (§10). No
  open-source server has built on it yet.

### Non-goals

- Not a general Drive client.
- Not multi-tenant hosted. Stdio only (**decided**); the composition root
  stays transport-agnostic so this can change later.
- Not a WYSIWYG fidelity guarantee for markdown import. We convert what
  we can prove and refuse the rest loudly (§7.4).

## 2. Hard constraints from the platform

| Constraint (verified against official docs) | Consequence |
|---|---|
| Indices are **UTF-16 code units**, per segment (body / header / footer / footnote each start at 0), shift after every mutation, and are only valid for the revision you read. | The model never sees or computes indices. Server owns index math; Go strings are UTF-8 so every offset goes through `utf16` conversion. |
| `batchUpdate` is **atomic** and requests apply in order. | One batch per tool call; requests sorted by descending index. |
| `writeControl.requiredRevisionId` → 400 if the doc changed; the response returns the new revision id. | Every write is guarded by the revision it was planned against. |
| Inserted text "will match the text immediately before the insertion index"; a newline copies the paragraph style "including lists and bullets" from the current paragraph. | Minimal-diff edits inherit surrounding formatting for free; the compiler sets explicit styles only where the content asks for them. |
| Tabs: `includeTabsContent=true` returns `document.tabs[]`. Requests without `tabId` hit the first tab, except `replaceAllText` and named-range requests, which default to **all tabs**. | Always read with tabs; always set `tabId` or explicit `tabsCriteria`. |
| Only `suggestionsViewMode=SUGGESTIONS_INLINE` yields indices valid for a later `batchUpdate`. | The canonical index space is the inline view. |
| Every heading paragraph carries a stable, read-only `headingId`. | Sections are addressed by `headingId`. |
| Deleting a range removes whatever is anchored inside it: comment anchors, suggestions, inline objects. Suggested deletions leave the text in place until accepted. | Overwrite guard (§7.3); suggestion mode is the safe default. |
| Quotas: 300 reads/min/user, 60 writes/min/user (Docs). | Client-side token buckets below those limits; backoff honouring `Retry-After`. |
| Scopes: `documents` is *sensitive*, `drive` is *restricted*, `drive.file` cannot reach documents the app didn't create or open. | We need `documents` + `drive`. Fine for a per-user OAuth app that each deployer owns; no central app, no Google verification. |
| An **External** OAuth app in **Testing** gets 7-day refresh tokens. The rule is defined only for External apps; an **Internal** (Workspace) consent screen is exempt by construction. | The setup guide covers both: Internal for Workspace organisations, Testing with weekly re-login for consumer accounts (§10). |
| Images: `insertInlineImage` needs a publicly fetchable URL. | URL only; no upload path. |
| Preview features are absent from the discovery doc and from `google.golang.org/api/docs/v1`, and importing that module pulls in gRPC, OpenTelemetry and the cloud auth stack. | Thin raw REST client with our own wire types in `internal/gdocs`; the generated module is not a dependency (§5). |
| Claude Code truncates tool results above 25 000 tokens and warns at 10 000. | Reads are scoped and budgeted by default, with continuation handles. |

### What the API cannot do (so we don't promise it)

Equations and drawings (read-only), charts, inserting a table of
contents, reading the *structure* of an old revision (only exports),
turning Drive-API comment anchors into ranges (opaque), rendering
Drive-API comments inline in the UI, suggestion mode and anchored
comments **without** preview enrolment, images from private URLs.

## 3. Requirements distilled from other servers' failures

1. Semantic targets resolved server-side; the model never touches
   indices (taylorwilsdon #1030).
2. Writes return what they touched and the new revision (#1031).
3. Section-scoped, token-budgeted reads; never drop empty paragraphs
   from a view that claims to be complete (#638, #1085, #1084).
4. Markdown conversion tested per construct, failing loudly on what it
   can't express (a-bonus #149).
5. Tabs honoured on every write (piotr-agier #114).
6. Strict, boring JSON Schemas: flat structs with an `op`/`action` enum,
   no `oneOf`, no vendor keywords, no dots in tool names.
7. Comments that anchor (preview `insertComment`), Drive API as the
   documented degraded fallback.
8. Suggestion mode as a first-class write mode, plus accept/reject.
9. Optimistic concurrency on every write.
10. Dry run on every write.

## 4. Core design bets

1. **Edit, never overwrite.** A `replace` is compiled as the minimal
   diff between the existing text and the new text, so unchanged spans,
   their formatting, and anything anchored to them (comments,
   suggestions, inline objects) survive. A planner guard refuses to
   delete a range that contains comment anchors or pending suggestions
   unless the call is in suggest mode or explicitly forced. Google's
   version history records every batch under the OAuth user, so
   provenance is preserved by construction.
2. **The user chooses how changes land.** Every write takes
   `mode: suggest | direct | comment`. `suggest` makes the batch a set of
   tracked changes for a human to accept (preview). `direct` edits the
   text. `comment` does not touch the text at all: the proposed change is
   posted as a comment anchored to the passage, which works without
   preview. The default is configured per deployment
   (`GDOCS_DEFAULT_WRITE_MODE`) and overridden per call when the person
   asks for it; the server never silently substitutes one mode for
   another.
3. **Exact text first.** The primary target is an exact substring that
   must match once, with `occurrence` and a scope to disambiguate. This
   is the contract Anthropic's editor tool, Claude Code's Edit, Notion's
   MCP and the Aider/OpenAI edit benchmarks converge on. Prose
   normalisation (smart quotes, NBSP, whitespace runs) makes it robust.
4. **Stable IDs where Google gives them, short handles where it doesn't.**
   Headings by `headingId`; other blocks by ordinal handles (`p12`,
   `tbl3`) that the server remembers from the last read and re-checks.
   Handles are shown in the outline and find results, opt-in on full
   reads (they cost about a quarter more tokens).
5. **One plan, one batch, one revision guard.** Fetch → resolve → compile
   → sort → `batchUpdate` with `requiredRevisionId` → re-fetch → report.
6. **Markdown for new content; in-place ops for existing content.**
   Markdown is how Claude expresses text it writes. It is never used to
   round-trip someone else's document: existing content is edited by
   range, styled by explicit format ops, and read with style annotations
   on request. `text` and `raw` views exist alongside markdown.
7. **History is a first-class read.** Revisions, diffs between
   revisions, suggestion lists, and full comment threads (including
   resolved and replies) are Phase 2, not polish.
8. **Raw REST with our own wire types.** Responses decode into
   `internal/gdocs` structs that mirror the API's JSON; requests are
   built by us, GA and preview alike, and sent with an `oauth2`
   transport. The generated `google.golang.org/api` module is not used:
   it brings gRPC, OpenTelemetry and the cloud auth stack into a binary
   that only needs JSON.

## 5. Module layout

```
cmd/google-docs-mcp/      main: login / logout / status / doctor subcommands, server default,
                          --version, --dump-schemas
internal/config/          GDOCS_* env with flags bound to the same names; typed enums; validated at start
internal/credentials/     refresh token: OS keyring → 0600 file under os.UserConfigDir() with a logged
                          warning (gh pattern) → GDOCS_REFRESH_TOKEN env override
internal/userconfig/      non-secret pointer file: client_secret path, account email, preview flag
internal/auth/            loopback OAuth (127.0.0.1:<random>, PKCE), scope sets (full / readonly)
internal/gdocs/           Docs API wire types (the JSON we read), no dependencies
internal/gapi/            raw REST client: client.go (retry, limiter, slog), docs.go (get/batchUpdate),
                          drive.go (about, files; later comments, revisions, export), errors.go
                          (APIError + sentinel classes). No MCP imports.
internal/doc/             model: parse docs.Document → Tree; UTF-16 index math; handles; Target → Range
                          resolution; normalised text search; section derivation; anchored-content index
internal/render/          Tree → markdown / text / outline; style annotations; CriticMarkup for
                          suggestions; comment markers; budget + continuation
internal/markdown/        goldmark AST → Fragment (neutral block/inline IR); unsupported-construct errors
internal/plan/            ops + Fragment → batchUpdate requests: minimal diff, overwrite guard, compile,
                          order, tables (multi-batch), format, dry-run diff
internal/service/         orchestration: fetch → resolve → plan → apply → refetch; comments (two backends);
                          suggestions; revisions/diff; export; search; per-document handle memory
internal/server/          SDK wiring; schema dump via an in-memory client session
internal/tools/           errors.go, then one file per area: read.go, edit.go, format.go, table.go,
                          objects.go, tabs.go, comments.go, suggestions.go, history.go, export.go, drive.go
internal/version/
testdata/                 synthetic fixtures only (§14) + golden outputs
docs/  scripts/  audit/
```

Dependencies, all pinned: `modelcontextprotocol/go-sdk` v1.7.0 (with
`google/jsonschema-go`), `golang.org/x/oauth2`, `zalando/go-keyring`,
`golang.org/x/time/rate`; Phase 1 adds `yuin/goldmark` and a diff
library (`sergi/go-diff`). Nothing else.

## 6. Document model (`internal/doc`)

```
Document
  Tabs[]              id, title, index, parent, nesting; first tab is the default
    Segments          body + headers{} + footers{} + footnotes{} (each its own index space)
      Blocks[]        ordinal, handle, kind, start/end (UTF-16), style
        Paragraph     namedStyle, headingId, bullet (listId, nesting), alignment,
                      Runs[] (text, TextStyle, suggestion insert/delete ids), inline objects, footnote refs
        Table         rows × cols, Cell[r][c] → nested Blocks, merged spans
        SectionBreak  section style
        TOC           rendered read-only
  Anchors             every comment range (preview) / quoted-text match (GA), suggestion range,
                      inline object and footnote reference, indexed by segment range
  Revision            revisionId of the fetch this tree came from
```

- Built from `documents.get?includeTabsContent=true&suggestionsViewMode=
  SUGGESTIONS_INLINE` (+ `commentsViewMode=COMMENTS_VIEW_MODE_INCLUDED`
  when preview is on). The only index space we compute against.
- Offsets the model sees are Unicode code points; the server converts to
  UTF-16 at the boundary.
- **Handles**: `p<n>` paragraphs, `tbl<n>` tables (cells `tbl3:r2c3`),
  `sb<n>` section breaks, ordinal within the segment; prefixed outside the
  first tab's body (`tab2/p12`, `header/p1`, `footnote3/p1`). Headings
  also carry `heading_id`.
- **Handle memory**: per document, the service keeps the revision id and
  `handle → normalised text` of the last read in this process. Unchanged
  revision → exact; changed revision → re-locate by stored text (unique
  match required) → else `[stale]`. An unknown handle is `[unknown]` with
  "read the section first". The stateless fingerprinted alternative
  (`p12-7f3a`) is uglier and has no evidence behind it (§18).
- **Sections** are derived: a heading owns everything up to the next
  heading of the same or higher level in the same segment.

## 7. Addressing, reading, writing

### 7.1 Target (shared by every tool that points at content)

```
Target {
  text:        "exact substring"; occurrence?: 1; within?: <heading_id | handle>
  heading_id:  "h.abc123"; include_heading?: true
  heading:     "Background"; heading_level?: 2; occurrence?: 1; include_heading?: true
  handle:      "p12"
  handles:     { from: "p12", to: "p15" }
  cell:        "tbl3:r2c3"
  named_range: "key finding"          (a name, or the id a read reports)
  tab?:        <tab id or title>      (default: first tab)
  segment?:    "body" | "header" | "footer" | "footnote:<n>"   (default: body)
}
Location { at: "start" | "end" | "before" | "after", of?: Target }
```

A named range is the one target that outlives an edit: Google moves it
with the text it covers, so `create_named_range` now and `named_range` in
a later call reach the same passage, where a handle is only valid for the
revision it came from. A name several ranges share is refused as a target
(the id names one), and `get_document` lists both.

Text matching normalises curly quotes, NBSP and whitespace runs on both
sides. Errors carry the fix: `[ambiguous] "Q3" matches 4 times in the
body; add occurrence or within`, `[stale] p12 was "…" when read and no
longer exists; re-read the section`.

### 7.2 Read path

- `get_outline` → tabs, heading tree with `heading_id`, handles, block
  counts, sizes. Cheap; called first.
- `read_document` → `scope` (tab / heading_id / heading / handle range /
  whole), `format` (`markdown` default, `text`, `raw`), `with_handles`
  (default true; pass false to drop the prefixes), `with_styles`
  (default false: annotates what a run sets on itself, e.g.
  `{font: Arial 11pt, color: #c00}`, so other people's formatting is
  visible before it is changed. Google reports only explicitly set run
  properties, so this is exactly the deviation from what the paragraph
  inherits — and the corollary is that inherited formatting is invisible
  in a read: a heading whose `HEADING_2` definition is blue and centred
  reads as plain markdown, which is what `get_document`'s named style
  lines are for, once per tab instead of once per heading. Verified live),
  `include_suggestions` (CriticMarkup `{++ins++}` / `{--del--}` with
  `{>>s:<id> by author<<}`), `include_comments` (`{>>c:<id><<}` markers
  plus a thread list), `max_chars` (default 20 000), returns
  `revision_id` and `continue_from` when truncated.

  ```
  [p3] ## Background {h.k2x9}
  [p4] Revenue grew {++substantially++}{--a lot--} in Q3. {>>c:AAAAB<<}
  [p5]
  [p6] - first bullet
  [tbl1] | 3×4 table; cells tbl1:r1c1 … |
  ```

- `get_document` → identity, Drive metadata, counts, capabilities, and
  per tab what no read of the text shows: page setup, floating objects,
  named ranges, and the named style definitions (`named style HEADING_2
  (3 paragraph(s)): Arial 16pt, bold, color #1a73e8, space above 18pt`).
  Only the styles a paragraph of that tab carries are reported: the
  others have no appearance in the tab to show, and this is the tool the
  model calls first. The count covers every segment, headers and
  footnotes included, because redefining a style changes the whole tab —
  a wider base than `Stats.Paragraphs`, which counts bodies. Every value
  the response carries is named, including `START` and 100%: a named
  style inherits from the tab's `NORMAL_TEXT`, not from the API's
  defaults, so single spacing is news when the body text is not.
  Without this read `layout_document`'s `named_style` op was write-only:
  it redefines a style for a whole tab, and nothing read the definition
  back.
- `find_in_document` → plain or regex (RE2) search → handles,
  code-point offsets, ±80 chars of context.
- `export_document` → pdf / docx / md / html / txt via Drive export; text
  formats inline (budgeted), binary formats only under `GDOCS_EXPORT_DIR`.

### 7.3 Write path (`internal/plan`)

Every write tool takes ordered `ops[]`, `mode` (`suggest` | `direct` |
`comment`; default from `GDOCS_DEFAULT_WRITE_MODE`), `dry_run`, optional
`expect_revision`, and `force` (default false).

**Modes.** `direct` applies the compiled requests. `suggest` applies the
same requests with `writeMode: SUGGEST` (preview); an explicit
`mode: suggest` without preview is `[unavailable] suggestion mode needs
Developer Preview enrolment; use comment or direct`. `comment` compiles
each op into a comment on the resolved range instead of a mutation:
`replace` → "Proposed change:" plus the new text (with a word-level diff
summary for long ranges), `delete` → "Proposed deletion", `insert` →
anchored to the neighbouring block with "Insert after this:", format ops
→ "Proposed formatting: Heading 2". With preview the comment is anchored
by `insertComment`; without it, it is a Drive comment carrying the quoted
text (§8). Nothing in the document changes in `comment` mode, so the
guard below only reports.

1. Fetch the tree (fresh). If `expect_revision` differs → `[conflict]`.
2. Resolve every target to `Range{segment, tab, start, end}`. Any failure
   fails the whole call before any API write.
3. **Minimal diff.** For `replace`, diff the existing text of the range
   against the new text at word granularity (character fallback for short
   ranges) and emit `deleteContentRange` + `insertText` only for changed
   hunks. Inserted hunks inherit the style of the preceding text
   (documented API behaviour); explicit markdown emphasis in the new
   content is applied on top. When the replacement changes paragraph
   structure (a paragraph becomes three bullets), the planner falls back
   to whole-range replacement and says so.
4. **Overwrite guard.** Every range scheduled for deletion is checked
   against the anchor index. In `direct` mode, a range containing comment
   anchors, pending suggestions, inline objects or footnote references is
   refused: `[blocked] range contains 2 comment anchors (c:AAAAB, c:AAAAC)
   and 1 suggestion; use mode: suggest or comment, narrow the target, or
   pass force: true`. The person stays in control: Claude passes `force`
   only when they have chosen direct editing knowing what is inside the
   range. In `suggest` and `comment` modes nothing is deleted until a
   human accepts, so the guard only reports.
5. Compile the rest: markdown → Fragment → `insertText`,
   `updateParagraphStyle`, `updateTextStyle` per run,
   `createParagraphBullets` (compiled last; the API strips the nesting
   tabs), `insertTable`. Order by descending start index per segment;
   overlapping ops rejected up front.
6. `batchUpdate` with `writeControl.requiredRevisionId` (and
   `writeMode: SUGGEST`). On revision conflict: re-fetch, re-resolve,
   re-plan, retry once; a second conflict is `[conflict]`.
7. Re-fetch; return `{ revision_id, mode, ops_applied, changes: [{op,
   handles, preview}], suggestion_ids, warnings }`.

Multi-batch ops (only `insert_table` with data): insert the empty table,
re-fetch, fill cells. If the fill fails the empty table remains and the
response says so.

Dry run returns the resolved targets, the guard report, the kinds of
request that would be sent (not the requests themselves: they carry
UTF-16 indices, which the model never sees), the proposals in comment
mode, and a rendered view of the region. Nothing is sent.

### 7.4 Markdown coverage

Headings 1–6, paragraphs, bold/italic/strikethrough/inline code, links,
bullet and numbered lists (nested), hard breaks; task-list checkboxes are
dropped and their text kept. Fenced code → Courier-styled paragraphs.
Refused with `[unsupported] <construct> at line N`: images (use
`insert_object`), tables in content (use `edit_table`), HTML, block
quotes, horizontal rules.

What markdown cannot say goes through `format_document`: fonts, sizes,
colours, alignment, spacing, indents, named styles on existing text,
bullet presets, clearing formatting.

## 8. Collaboration and history

**Comments, two backends behind one tool surface.**

| | Preview (Docs API) | GA (Drive API v3) |
|---|---|---|
| list | `documents.get` with `commentsViewMode` → threads plus the tab's `commentAnchors` map (anchor id → ranges) → handles | `comments.list` (replies, resolved state, `includeDeleted` opt-in) → `quotedFileContent`; server matches the quote to a block, best effort |
| add | `insertComment` with a Range → anchored in the UI | `comments.create` with `quotedFileContent` → **unanchored** in the UI (stated in the description and in `warnings`) |
| reply / resolve / reopen | `replies.create` with `action` (GA works for every deployment; the preview's `addCommentReply` adds nothing for replies) | `replies.create` with `action` |
| delete | gated, `comments.delete` / `replies.delete` | gated |

`list_comments` always lists through the Drive API, which carries every
reply with author, time and action, resolved and deleted state for every
deployment, and locates each thread through the preview anchors when the
fetch carried them, else by its quoted text (a quote that matches once).
Resolved threads are included by default, deleted ones on request.
Resolution is reversible (`reopen`); deletion is gated. The guard, reads
with `include_comments` and the listing share one located thread list
per fetch.

**Suggestions.** `list_suggestions` renders pending insert/delete/style
suggestions with author and handles (GA). `review_suggestion`
(`action: accept | reject`, ids or `all`) and `mode: suggest` need
preview. In SUGGEST mode the API refuses `AddDocumentTab`,
`CreateNamedRange`, `DeleteFooter`, `DeleteHeader`, `DeleteNamedRange`,
`DeleteTab`, `UpdateDocumentTabProperties`, `UpdateTableColumnProperties`,
and cannot suggest document-format or header/footer settings; the planner
rejects those ops up front with the reason.

**Revisions.** `list_revisions` (Drive: id, time, last modifying user;
Drive may omit older revisions of busy documents); `diff_revisions`
exports `text/markdown` or `text/plain` at two revisions through
`files.download` with `revisionId` (a long-running operation whose
content URL must stay on a Google host; `files.export` takes no revision)
and returns a line-level unified diff, budgeted at hunk boundaries.
`read_document` accepts `revision` for a markdown or text view of an old
revision (export path; no handles, no scoping). Drive revision ids are a
different id space from the Docs `revision_id` concurrency token; the tool
descriptions say so. Google keeps version history automatically for every
batch the server sends; restoring an old revision is an explicit `replace`
of the body from an export, never implicit.

## 9. Tool surface

snake_case verb–noun, no dots. Claude Code prefixes `mcp__<server>__`.
"Gated" = registered only with `GDOCS_ENABLE_DESTRUCTIVE=1`; gated tools
also set `_meta["anthropic/requiresUserInteraction"]`. `GDOCS_READ_ONLY=1`
registers only readOnly rows and requests readonly scopes.

| Tool | Purpose | Annotations | Phase |
|---|---|---|---|
| `search_documents` | Locate a Doc by title or content (Drive search restricted to Docs); returns id, title, modified, owner | readOnly | 1 |
| `get_document` | Title, tabs, revision, owner, counts, capabilities (preview on/off, available write modes, configured default); per tab the page setup, floating objects, named ranges and the named style definitions its paragraphs carry | readOnly, idempotent | 0, 4 |
| `get_outline` | Heading tree with `heading_id`, handles, sizes | readOnly | 0 |
| `read_document` | Scoped, budgeted markdown/text/raw view; `with_styles`; `revision_id` | readOnly | 0 |
| `find_in_document` | Text/regex search → handles + context | readOnly | 1 |
| `export_document` | pdf/docx/md/html/txt | readOnly | 1 |
| `create_document` | Title, optional markdown body | — | 1 |
| `edit_document` | ops: `insert`, `append`, `replace`, `delete`, `replace_all`, `insert_break`, `insert_footnote`, `create_header`, `create_footer`, `delete_header`, `delete_footer`, `create_named_range`, `delete_named_range`, `replace_named_range`; mode / dry_run / expect_revision / force | destructive=false*, idempotent=false | 1, 2, 4 |
| `format_document` | ops: `text_style`, `paragraph_style`, `bullets`, `clear_formatting` | — | 1 |
| `list_suggestions` | Pending suggestions with handles and authors | readOnly | 1 |
| `review_suggestion` | accept / reject / discard (preview; discard is author-only) | — | 1, 4 |
| `list_comments` | Full threads: replies, resolved, deleted, quoted text, handles | readOnly | 2 |
| `add_comment` | Anchored to a Target (preview) or quoted (Drive); no target = document-level | — | 2 |
| `reply_comment` | `action: reply \| resolve \| reopen \| edit` (edit rewrites a comment or one reply, author-only) | — | 2, 4 |
| `delete_comment` | Gated; a thread or one reply | destructive | 2 |
| `list_revisions`, `diff_revisions` | History; `read_document` takes `revision` | readOnly | 2 |
| `edit_table` | ops: `insert_table`, `set_cells`, `insert_rows`, `delete_rows`, `insert_columns`, `delete_columns`, `merge_cells`, `unmerge_cells`, `style_cells`, `style_columns`, `style_rows`, `pin_header_rows`; a grid change puts the ops after it on that table in their own batch | — | 2, 4 |
| `insert_object` | `action: insert \| replace \| delete`; insert takes `kind: image \| person \| rich_link \| date` at a Location, replace swaps an image's source, delete removes an object by id (the only way to remove a floating one) | — | 2, 4 |
| `layout_document` | ops: `page`, `section`, `section_break`, `named_style` | — | 4 |
| `manage_tabs` | `action: add \| rename \| move`; always direct (the API refuses tab requests in SUGGEST mode) | — | 2 |
| `delete_tab` | Gated; child tabs go with it | destructive | 2 |

\* `edit_document` changes text by design; it is not gated. The layers
this server controls are the write mode the person chose, the overwrite
guard, `dry_run`, read-only mode, and not registering the destructive
tools at all. Client-side approval is **not** one of them: whether a call
is put to the person is the client's decision and its permission mode, so
a host running in an auto-approve mode may invoke any registered tool
without asking. `_meta["anthropic/requiresUserInteraction"]` is a signal
a client may act on, not a control — the spec says clients treat tool
annotations as untrusted, and a server cannot make its own hint binding.
Design as though every registered tool can be called unattended.
`replace_all` always carries explicit `tabsCriteria`.

**Resources** (Phase 3). Three templates, all `text/markdown`, for
clients that attach a document as context instead of calling a tool:
`gdocs://{document}` (every tab's body under one 400 000-character
budget, tabs separated by `<!-- tab N "title" -->`, a closing comment
naming where `read_document` continues), `gdocs://{document}/outline`
(the `get_outline` text) and `gdocs://{document}/tabs/{tab}`. They carry
no handles, suggestions or comments; the read tools remain the way to
scope, budget and target. No static resource list (that would be a Drive
listing) and no subscriptions (Google offers no push; polling would spend
the read quota). A document that does not exist is the spec's
resource-not-found error; other failures carry the `[class] message`.

Results: a client may show the model a result's text **or** its
structured form (Claude Code 2.1 shows only `structuredContent` when
both are present; the spec makes the text the backwards-compatible
form). So read tools (`get_document`, `get_outline`, `read_document`,
`find_in_document`, `search_documents`, `export_document`,
`list_suggestions`, `list_comments`, `list_revisions`,
`diff_revisions`) return a **text block only**, with the revision,
block count and continuation in a header comment; write tools return
both, and their JSON carries everything their text does, the rendered
preview included. Measured in the Phase 3 evals (§14).

## 10. Auth, enrolment, config, process model

- **Google account and Cloud project.** Each deployer runs this server
  under a Google Cloud project they own. The project may be shared with
  other tools the deployer runs, but this server always gets its own
  OAuth client so its tokens can be revoked independently. Setup,
  documented in the README and verified by `doctor`: create or pick the
  project; enable the Google
  Docs API and Google Drive API; configure the OAuth consent screen
  (**Internal** for a Workspace organisation, which avoids the 7-day
  token expiry; **External + Testing** with the user added as a test user
  for consumer accounts, with weekly `login`); add the four scopes; create
  a **Desktop app** OAuth client; download its `client_secret.json`; run
  `google-docs-mcp login --client-secret <path>`.
- **Developer Preview enrolment** (for suggestion mode, anchored
  comments, accept/reject; **decided: yes** for the maintainer's project;
  every other deployer enrols their own). Per Google's programme page:
  apply with the application form linked from
  https://developers.google.com/workspace/preview, providing "your Google
  Workspace account and Google Cloud project information"; access is
  granted "through your Google Cloud project(s)" and by adding the
  account to a programme Google Group; "the whole process should be done
  within a couple of days"; the programme "provides access to all the
  features", not per feature. The terms allow use inside the enrolling
  organisation and forbid granting "end users access, outside my domain
  or company" to applications built on pre-GA APIs. Publishing this
  server's source is not that; anyone else would need their own enrolled
  project, and the README must say so.
- **OAuth flow**: Google's documented desktop flow, loopback
  `127.0.0.1:<random port>` with PKCE (OOB has been blocked since 2023).
- **Refresh token storage**, in the order `gh` uses: OS keyring; on error
  (no session bus, no secret service, headless) a 0600 file under
  `os.UserConfigDir()/google-docs-mcp/` with a stderr warning;
  `GDOCS_REFRESH_TOKEN` env overrides both. Backends: Secret Service on
  Linux, Keychain on macOS, Credential Manager on Windows.
- Scope sets: `full` = `documents` + `drive`; `readonly` =
  `documents.readonly` + `drive.readonly`. A missing-scope 403 becomes
  `[forbidden] missing scope …; re-run google-docs-mcp login`.
- **Config**: `GDOCS_*` env is the source of truth; each setting also has
  a flag bound to the same name. Settings: `GDOCS_LOG_LEVEL`,
  `GDOCS_LOG_FORMAT`, `GDOCS_PREVIEW`, `GDOCS_DEFAULT_WRITE_MODE`
  (`suggest` | `direct` | `comment`; if set to `suggest` without preview
  the server refuses to start rather than downgrade), `GDOCS_READ_ONLY`,
  `GDOCS_ENABLE_DESTRUCTIVE`, `GDOCS_EXPORT_DIR`, `GDOCS_HTTP_TIMEOUT`,
  `GDOCS_PROFILE` (named config directory, so a person with two Google
  accounts runs two server entries). Operational flags: `--version`,
  `--dump-schemas`.
- **Startup**: refresh the access token once (the scope-agnostic
  credential check). On failure log to stderr and **keep serving**; every
  tool then returns `[auth] … run google-docs-mcp login`. `doctor` does the
  full interactive check: token age, consent type, scopes, preview
  enrolment (a `documents.get` with `commentsViewMode` on a doc id you
  pass), quota headroom.
- **Transport**: stdio (**decided**).

## 11. Reliability

- Retries: reads retry on 429/5xx/network with exponential backoff and
  jitter, cap 30 s, max 5, honouring `Retry-After`. `batchUpdate` retries
  only on 429/503 received before any response bytes; an unknown
  outcome is `[ambiguous_outcome] the edit may have been applied;
  re-read` — a different word from `[ambiguous]`, which means the target
  matched several things. One asks the caller to choose, the other to go
  and look.
- Rate limits: per-process token buckets (reads 4/s burst 20, writes
  0.8/s burst 5).
- Timeouts: per-call context from the MCP request; `GDOCS_HTTP_TIMEOUT`
  default 60 s, 120 s for exports.
- Caching: none for correctness; a 5-second coalescing cache keyed by
  (docId, revisionId). Writes always re-fetch.
- Logging: `slog` to stderr, text or JSON. Stdout carries only JSON-RPC.
- Performance (Phase 3). Everything derived from one fetch is derived
  once: the handle memory, the located comment threads, and per segment
  a searchable text (every paragraph's normalised characters in one
  string, so a text target or a quoted comment is one `strings.Index`
  pass; a match maps back to indices through a byte offset per
  paragraph, not per rune) and a sorted anchor list. Handles and cells
  are map lookups, list numbers are assigned at parse time, and word
  counts never build strings. `make bench` over `doctest.Large` (about
  150 pages: 6 400 body blocks, 130 tables, 300 comments, 200
  suggestions, 100 footnotes), one machine, 2026-09-03:

  | per fetch | | per call, document cached | |
  |---|---|---|---|
  | JSON decode | 43 ms | section read | 0.10 ms |
  | parse | 11 ms | text target | 0.07 ms |
  | locate 300 quoted comments | 43 ms | `find_in_document` | 3.3 ms |
  | locate 300 anchored comments | 6.8 ms | outline | 4.2 ms |
  | two-op dry run | 22 ms | whole body as markdown | 8.0 ms |
  | handle memory | 0.00002 ms | budgeted read (20 k chars) | 0.19 ms |

  Measured against the same fixture before the work, in one session: a
  cached section read was 145× slower, the outline 4×, a text target
  40×, `find_in_document` 15×, locating quoted comments 10×, a dry run
  3×. What remains per write is the fetch itself.

## 12. Confidentiality, security, safety

**Nothing internal leaves the owner's machine or enters the repository**
(**decided**).

- The repository never contains organisation names, document ids or
  URLs, account emails, Cloud project ids, OAuth client ids or secrets,
  or content from real documents. There is no default project or client
  baked in; the binary is useless until a deployer supplies their own.
  The repository shares no code with other projects. Fixtures under `testdata/` are synthetic, generated
  by a script from lorem text, never recorded from real documents.
  gitleaks runs in pre-commit and CI, as in the sibling repos, with rules
  for Google doc-id and client-id shapes added.
- The server talks only to Google: `*.googleapis.com`, plus the
  `docs.google.com` export URL that `files.download` hands back for an
  old revision (checked against an allowlist before credentials are
  sent). No telemetry, no crash reporting, no update checks.
- Logs never contain document text, comment text, titles, or search
  queries at `info`; at `debug` they are redacted to lengths and hashes.
  Request ids and revision ids are logged; document ids are logged
  truncated.
- Exports are written only under `GDOCS_EXPORT_DIR`, path-cleaned, no
  symlink following. Nothing else touches the filesystem except the
  credential store and userconfig.
- Destructive tools are not registered unless
  `GDOCS_ENABLE_DESTRUCTIVE=1`; annotations are hints the client may not
  trust (spec), which is why the gate is server-side.
- The refresh token lives in the keyring or a 0600 file (warned); the
  client secret file is user-owned and never logged. `logout` deletes and
  revokes.
- Regex search is Go `regexp` (RE2, linear time). Inputs are validated
  before any API call; validation failures are tool errors
  (`isError: true`), which the SDK produces from a returned `error`. The
  `[class]` prefix is our convention; the actionable message is what the
  guidance asks for.

## 13. Distribution and setup

The server is published for other people to run against their own Google
accounts. That sets these requirements:

- **Artifacts.** goreleaser builds for linux, macOS and Windows on amd64
  and arm64, with checksums; `go install
  github.com/mmedum/google-docs-mcp/cmd/google-docs-mcp@latest` as the
  second path. A Docker image is not a v1 target: the loopback OAuth flow
  and the keyring both assume the user's desktop. Homebrew tap later.
- **Client configuration** documented for Claude Code (`claude mcp add
  --transport stdio google-docs -e GDOCS_… -- google-docs-mcp`), Claude
  Desktop (`claude_desktop_config.json`) and Cursor (`mcp.json`). All
  three pass only `command`, `args` and `env`, which is why config is
  env-first.
- **Setup guide** in the README, per-deployer, in the order `doctor`
  checks it: Cloud project → APIs → consent screen (Internal vs Testing)
  → Desktop OAuth client → `login` → `doctor`. Preview enrolment is a
  separate, optional section that states the programme terms: use inside
  your own organisation only, enrol your own project.
- **Versioning.** Semantic versions; `CHANGELOG.md` in Keep a Changelog
  form; the schema-dump diff in CI classifies tool removals, renames, and
  required-field additions as breaking (major after 1.0, minor before).
- **Documentation set.** README (setup, tool catalogue, safety model),
  `docs/architecture.md` (this file), `docs/configuration.md`,
  `docs/security.md` (threat model, scopes, what is stored where),
  `CONTRIBUTING.md`, `SECURITY.md` (reporting), `CHANGELOG.md`. Licence:
  Apache-2.0 (already in the repository).
- **Support matrix.** Keyring backends per platform with the file
  fallback; `os.UserConfigDir()` paths; Windows paths in export-dir
  handling; CI runs the unit suite on all three platforms.
- **Nothing deployer-specific ships.** No client id, no project id, no
  account, no document ids, no telemetry endpoint (§12).

## 14. Testing

- **Unit, table-driven, no network**: parser on synthetic fixtures
  (multi-tab, tables, nested lists, headers/footnotes, suggestions,
  emoji/surrogate pairs, combining marks, empty paragraphs); index-math
  property tests; minimal-diff tests (unchanged spans keep their
  offsets); overwrite-guard tests; renderer and planner golden files;
  markdown fragment tests per construct plus the refusal list; text
  normalisation cases.
- **Schema-dump gate**: `--dump-schemas` via an in-memory client
  session; CI diffs against the previous tag.
- **Integration** (`//go:build integration`, `GDOCS_TEST_FOLDER_ID` in a
  scratch folder, never a real working document): creates a document per
  test, applies ops, reads back, asserts, trashes. Includes the preview
  checks (suggest mode visible in the UI, anchored comments), run
  manually until enrolment exists in CI.
- **Stdio smoke**: initialise, list tools, `get_document` on a scratch id.
- **Agent evals** (`internal/evals`, `-tags=evals`, Phase 3): thirteen tasks
  through Claude Code headless (`claude -p` with only this server's
  tools), each against a scratch document the harness seeds through the
  server, scored on the end state read back through the server and on
  the tool-call trace. Run 2026-09-03 with the default model: 13/13
  pass, 2–6 tool calls per task, about $3 in total. What they found:
  Claude Code shows the model only a result's `structuredContent` when
  one is present, so reads looked like metadata until the read tools
  went text-only (the four affected tasks fell from 17, 7, 8 and 22 tool
  calls to 4, 2, 4 and 4); a new footnote's paragraph holds a space, so
  the follow-up now replaces it instead of appending after it; every
  task starts with Claude Code's own tool lookup (`ToolSearch
  select:mcp__gdocs__…`) and no task called a tool that does not exist,
  so the snake_case verb_noun names are found by name; the model asked
  for `with_handles` on three of four reads and never for `format:
  text` once reads were visible, targeted by text and by handle equally,
  used no dry runs for one-op edits, and stopped at the overwrite guard
  and explained it instead of forcing. Re-run 2026-09-05 as the Go port
  (`internal/evals`, `-tags=evals`): 13/13 again, $2.90, 2–8 calls a
  task, with the same shape of trace. Four tasks were then added that
  score a refusal rather than a success — a document that is not there,
  a suggestion asked for with the preview off, a write against a
  read-only server, and an edit against a revision the document has
  left — because the wording of an error was otherwise the one
  model-facing surface with no evidence behind it. Seventeen tasks;
  16/16 when the first three landed, at $3.38, and the conflict task
  scored 5/5 on its first run in three calls. The
  refusals steer: told suggestion mode is unavailable the model used
  comment mode and said so, and told `[not_found]` it quoted the class
  back and stopped in two calls rather than hunting.
- **Live driver** (`internal/livecheck`, `-tags=live`): every tool and
  every op kind against one scratch document, through the binary over
  stdio as a client drives it, with the steps that must be refused
  asserting their refusal. Manual, never CI. Run 2026-09-05 as the Go
  port: 91 steps with `GDOCS_PREVIEW=true`, 88 with it off, every
  failure an intended refusal. `TestCoverage` in the same package makes
  the coverage rule a check rather than a comment: it reads the
  package's own source and fails when a registered tool has no step or
  an op kind never appears as an `op`.
- **Identifier scan** (`internal/leakcheck`): a test over every tracked
  file, enforcing §12's rule that nothing identifying anybody is
  committed. gitleaks cannot: a document id, an address, a client id and
  a 21-digit subject are not secrets. Every rule is an allow-list, since
  a deny-list naming what to look for would be the disclosure.
- **Surface completeness**: `server.toolArgs` holds one call's arguments
  for every registered tool, and `TestEveryToolHasArgs` fails when a tool
  has no entry. The debug-logging guarantee drives that whole table, so
  it cannot shrink to the tools someone thought of when it was written.

## 15. Confirmed decisions and their consequences

| Decision | Consequence in the design |
|---|---|
| Deployer-owned Cloud project (may be shared with other tools) with a dedicated OAuth client; the repository is isolated and distributed for other people | §10, §13; setup guide covers Workspace (Internal) and consumer (Testing) accounts; no baked-in identifiers |
| Enrol in Developer Preview | Spike A first; `mode: suggest` default; anchored comments; accept/reject |
| Edits happen live in shared documents; never overwrite; full history | Minimal-diff replace, overwrite guard, history tools in Phase 2, comment threads complete by default |
| The person chooses how changes land: suggestion, direct edit, or comment | `mode` on every write with a configured default; `comment` mode works without preview; no silent downgrades |
| Paragraph handles: short labels remembered by the server (option A) | `p12`-style handles with per-document handle memory (§6) |
| Markdown for new content only; existing content edited in place | `with_styles` reads, format ops, no whole-document round trips |
| One document, every capability; no folder/move/share/trash/copy | Tool table trimmed; `search_documents` is locate-only |
| Stdio only | No HTTP auth design |
| Nothing internal in the repo or in logs | §12 |

## 16. Delivery phases

Each phase ends in a tagged release and waits for an explicit "go".

**Phase 0 — skeleton and spikes (v0.0.1).** Scaffolding and gates
(Makefile, golangci, govulncheck, go-licenses, gitleaks, goreleaser, CI).
`login/logout/status/doctor`, raw client, parser, renderer,
`get_document`, `get_outline`, `read_document`. Spikes:

- **A. Preview**: done 2026-09-03 (see §18). Suggestion mode, anchored
  comments, the comments view and reject all work, and the owner
  confirmed in the Docs UI that the suggestion shows as a tracked change
  and the comment is pinned to its sentence.
- **B. Index math and minimal diff**: golden set for emoji, nested
  bullets, tables, footnotes; hunk-level replace keeps comment anchors.
- **C. Markdown fidelity**: whether Drive `files.create` converts
  `text/markdown` (the upload guide says yes, the import table doesn't
  list it), and a construct matrix for `create_document`.

**Phase 1 — core editing (v0.1.0).** `edit_document` with minimal diff,
the guard and all three modes (`comment` via the Drive backend until
preview lands), `format_document`, `find_in_document`,
`search_documents`, `create_document`, `export_document`,
`list_suggestions`, `review_suggestion`; revision guard, dry run, suggest
mode if A passed.

**Phase 2 — collaboration, history, structure (v0.2.0).** Done
2026-09-03: comments (both backends), revisions and diff, tables, tabs,
headers/footers/footnotes, images and chips. A table inserted with data is
filled in a second batch once it exists (found by the index the insertion
named); the tab that `add` creates gets its content the same way.

**Phase 3 — evals and polish (v0.3.0 → v1.0.0).** Done 2026-09-03:
resources (`gdocs://<id>`), performance on large documents, the §17a
cleanups, and the agent evals with the two fixes they forced (§14).

**Phase 4 — the rest of the API surface (v0.4.0).** Done 2026-09-03. The
`Request` union was diffed against what the planner emits (the union
taken from the discovery document, `docs.googleapis.com/$discovery/rest?version=v1`,
not from the reference page's prose), and everything missing was added.
All 40 GA members are now emitted. Of the eight Developer Preview members
beside them the server emits four — `insertComment`, `acceptSuggestion`,
`rejectSuggestion`, `deleteSuggestion` — and deliberately not the other
four (`addCommentReply`, `updateCommentPost`, `deleteComment`,
`deleteCommentReply`), because every thread operation goes through the
Drive API, which is GA and serves deployments without preview enrolment.
What was added:

- **Page and section layout.** A new `layout_document` tool: `page`
  (`updateDocumentStyle`), `section` (`updateSectionStyle`),
  `section_break` (`insertSectionBreak`) and `named_style`
  (`updateNamedStyle`). Layout is its own tool rather than more ops on
  `format_document`, which styles one passage: the two have almost no
  arguments in common, and a flat op struct holding both would double
  that schema. Lengths are points everywhere, and `get_document` names
  the standard page sizes on the way back so the model need not
  recognise 612×792.
- **Column and row sizing.** `edit_table` gains `style_columns`
  (`updateTableColumnProperties`, fixed or evenly distributed) and
  `style_rows` (`updateTableRowStyle`: least height and page-break
  behaviour, but not `tableHeader`, which the API refuses — see §18).
  `pin_header_rows` remains the way to repeat a header row.
- **Images.** `insert_object` gains `action: replace` (`replaceImage`)
  and `action: delete`. Delete is where the model of an object matters:
  an inline object lives in a run, so it goes with a range delete the
  overwrite guard can inspect (minus its own image anchor, which the op
  names); a floating one has no range at all and goes by id
  (`deletePositionedObject`). Before this a floating image could not be
  removed through the server.
- **Named ranges.** `create_named_range`, `delete_named_range` and
  `replace_named_range` on `edit_document`, plus `named_range` as a
  Target (§7.1). This is the one durable anchor the API offers, and it
  answers what handles cannot: coming back to a passage in a later call.
  `replace_named_range` overwrites every range the name covers, so the
  guard is shown what all of them hold; `delete_named_range` forgets the
  name and leaves the text, so it is not guarded.
- **Comments and suggestions.** `reply_comment` gains `action: edit`
  (Drive `comments.update` / `replies.update`), and `review_suggestion`
  gains `discard` (`deleteSuggestion`, preview). The Docs preview
  equivalents of the comment edits were refused in favour of the Drive
  path, per the existing rule that thread operations use one backend.

**What "everything" means here.** Every GA member of the `Request` union
is emitted, and every field of those requests that the API accepts on a
write. Text style: bold, italic, underline, strikethrough, small caps,
baseline, size, family, both colours, links. Paragraph style: named
style, alignment, content direction, spacing mode, line spacing, space
above and below, all three indents, keep-with-next, keep-lines-together,
widow and orphan control, page break before, shading, and all five
borders. Table cell style: background, content alignment, per-side
padding and all four borders. Column and row properties as in Phase 4.

What is left out is only what the **discovery document** marks read-only,
so no client can set it: `tabStops` and `headingId` on a paragraph,
`rowSpan` and `columnSpan` on a cell (a merge is what changes those), and
`TableRowStyle.tableHeader`, which the schema lists and the API refuses
(§18). Those are read and reported, never sent.

Borders take a shorthand — `1pt solid #cccccc`, `none` to clear, tokens
in any order, defaulting to 1pt solid black for the parts left out —
because a flat field per part would be fifteen schema entries for
paragraph borders alone and twelve more for cells. A width of zero reads
back as no border, which is what Google means by the empty border object
it returns for an edge that is not drawn.

v1.0.0 waits for use in anger and a further eval round with another
client.

## 17. Open decisions

None. Everything in §15 is decided; the next step is Phase 0 (§16), which
begins only on an explicit go.

## 17a. Deferred cleanups

Findings from the Phase 1 review that were judged right but too wide to
apply at once, kept here so they are not forgotten. Done in Phase 2: one
block-range resolver (`resolveBlocks`) serves `ResolveScope` and
`ResolveTarget`, so handles are validated against the last read on both
paths; comment threads are located once per `Fetched` and shared by the
guard, `read_document include_comments` and `list_comments`. Done after
v0.3.0, and nothing is left open:

- **Several grid changes on one table per call.** `chainStructural`
  splits a call: everything goes in the first batch until an op changes a
  table's grid, and from then on every op addressed in that table's rows,
  columns or cells waits for a batch of its own against a fresh read. So
  `insert_rows` then `set_cells r2c3` writes the cell the caller means,
  in the grid the insertion left. A dry run lists the held-back ops the
  way it lists other follow-ups and says it cannot resolve them, since
  the grid they name does not exist yet; what does not depend on that
  grid (a number below one, a malformed cell name) is still refused
  before anything is written. The planner keeps its one-change-per-table
  check as an assertion that the service split correctly. Comment mode
  does not split, because every op must reach the proposals.
- **`list_comments` text.** `render.Thread` is now the renderer's model
  of a comment thread, and `render.Mark` is a located one; the full
  listing and the one-line summary under a read come from the same code,
  and the service adds only the count line above it.
- **`get_document` text.** `Info.text()` in the service; nothing is
  shaped in the tool layer any more.
- **One read-side paragraph-style type.** `doc.ParagraphStyle` holds
  alignment, line spacing, space above and below, both indents,
  keep-with-next and page-break-before; `doc.Paragraph` and
  `doc.NamedStyleDef` embed it, and `parseParagraphStyle` fills it for
  both, so a paragraph now carries everything its style says rather than
  the two fields the renderer happened to need. This is also what
  answering "does this run deviate from its named style?" will need —
  the gap docs/development.md describes. Embedding keeps `p.Alignment`
  and the rest reading as before at every call site.

## 18. Evidence log: conventions checked, changed, or rejected

Verified 2026-09-02/03 against primary sources. "Inherited" means the
convention was carried over from the author's earlier MCP servers and was
checked rather than assumed.

| Convention | Verdict | Effect |
|---|---|---|
| Exact-text targets (Anthropic text editor tool; Claude Code Edit; Notion MCP `update_content`; Aider: removing line numbers took GPT-4 Turbo from 20% to 61%; OpenAI `apply_patch`) | Confirmed | `text` is the primary target; normalisation added (SWE-Edit brittleness). |
| Google `headingId` is stable | Confirmed (Docs API reference) | Sections by `heading_id`. |
| Inserted text inherits the preceding text's style; newline copies paragraph style incl. bullets | Confirmed (InsertTextRequest reference) | Minimal-diff replace is safe for formatting. |
| Fingerprinted block handles | No evidence; Anthropic advises against cryptic ids | Plain ordinals + server memory; open in §17. |
| Per-block handle prefix on every read | Confirmed costly (≈ +27% tokens for `[b12-7f3a] `, ≈ +15% for `[p12] `) | Opt-in. |
| Markdown in/out | Mixed (Anthropic: measure it; Google added md export for this use) | Markdown for new content; in-place ops for existing; evaluated in Phase 3. |
| CriticMarkup for suggestions | Confirmed convention (MultiMarkdown-6); used by the adeu docx MCP | Kept. |
| Preview programme terms | Confirmed (programme FAQ) | In-organisation use allowed; README states others need their own enrolment. |
| Preview features work as documented (spike A, 2026-09-03, live against a scratch document in an enrolled project) | Confirmed: `commentsViewMode=COMMENTS_VIEW_MODE_INCLUDED` accepted; `writeMode: SUGGEST` returns `suggestionResponses[].createdSuggestionIds` and the inline view carries the id on the run; `insertComment` with a `range` returns a `commentThread`; `rejectSuggestion` removes the suggestion. Response shapes differ from the reference page: a comment thread has `commentId`, `anchorId`, `headPost{postId, content, contentHtml, author{displayName, me, user}, createTime, updateTime, commentAction}`, `status` (OPEN) and `plainTextQuote`, with **no range**; a suggestion thread has `suggestionId`, `headPost`, `status`, `summaryText` ("Add: …") and `summaryHtml`, also without a range. | Phase 2 maps comments to blocks by `plainTextQuote` (plus `anchorId` when the UI exposes it) and maps suggestions to ranges through the inline run ids, not through the thread objects. The `comments`/`suggestions` keys are absent until one exists. |
| Keyring with env-only fallback (inherited) | Refuted as precedent: `gh` falls back to a 0600 file; `gcloud` uses plaintext files; go-keyring has no fallback | Keyring → file (warned) → env. |
| Loopback + PKCE OAuth (inherited) | Confirmed (Google native-app guide; OOB blocked since 2023-01-31) | Kept. |
| 7-day refresh tokens only for sensitive scopes (my claim) | Refuted: any External app in Testing | Internal consent screen. |
| Env-only configuration (inherited) | Mixed: all three clients pass only `env`; github-mcp-server binds flags and env | Env source of truth plus bound flags. |
| Server-side read-only mode and destructive gating (inherited) | Confirmed (github-mcp-server; spec: annotations untrusted) | Kept; `requiresUserInteraction` on gated tools. |
| `requiresUserInteraction` makes a client prompt before running a gated tool, so it counts as a safety layer (implied by listing "per-call approval" among the layers) | Refuted: the hint is advisory. The spec already says clients treat tool annotations as untrusted, and a host in an auto-approve permission mode runs a registered tool without prompting — which is the only way the hint could have been load-bearing | §9 no longer lists client approval as a layer. What the server controls is what it registers and what it refuses, so a tool that must not run unattended must be unregistered, not annotated. |
| snake_case verb_noun names (inherited) | Mixed: spec allows more; GitHub mixes styles; Anthropic says measure. Measured in the Phase 3 evals: 13 tasks, 15 tool lookups by name, no call to a tool that does not exist | Kept. |
| Per-block handle prefix opt-in (§7.2) | Evals: the model asked for `with_handles` on three of four reads and targeted by handle as often as by text | Flipped to default on (the owner's call, 2026-09-03): the flag is a per-read cost of about 15%, the round trip it saves is a whole read, and an edit targets handles. `with_handles: false` drops them. |
| A blank paragraph is one whose text is empty (my first fill rule) | Refuted in review: a paragraph holding only an image, a footnote reference or a page-number field also renders as no text, and filling it would delete that content with no guard (an insertion never populates anchors) | Blank means every run is text and the text is whitespace. |
| A new footnote starts empty (my assumption) | Refuted live: `createFootnote` yields a paragraph holding one space, so an append after it left a blank line the model then deleted | The follow-up replaces a blank first paragraph. |
| `[class] message` errors (inherited) | Project choice; spec requires only `isError` | Kept as convention. |
| Parallel tool registry for `--dump-schemas` (inherited) | Refuted as necessary: in-memory client session lists wire schemas | Dropped. |
| go-sdk leaves `Content` alone with an object `Out` | Confirmed in `mcp/server.go` v1.7.0 | Both forms reach the wire. |
| Clients show the model the text block as well as `structuredContent` (my assumption behind "markdown plus a small `Out`") | Refuted by the agent evals (2026-09-03): Claude Code 2.1.259 shows only `structuredContent` when present, so scoped reads looked like metadata; the model said so ("returned only metadata … so I fell back to the whole-document markdown resource") and probed `find_in_document` with `.+` to see text | Read tools return text only (go-sdk emits neither output schema nor structured content for an `any` output); write tools' JSON includes the preview. |
| Exit on failed startup probe (inherited) | No guidance; GitHub server doesn't probe; Claude Code shows "failed to connect" and the model never sees why | Keep serving with `[auth]` errors; `doctor` for humans. |
| `cmd/` + `internal/`, no `pkg/` (inherited) | Confirmed (go.dev; Russ Cox) | Kept. |
| `dry_run` on write tools (inherited) | Mixed: community precedent; not a substitute for client approval | Kept; safety rests on guard, gating, approval. |
| Smart chips cannot be inserted through the API (my assumption) | Refuted (Request reference, 2026-07-07): `insertPerson`, `insertRichLink`, `insertDate` are GA members of the request union | `insert_object` covers images and all three chip kinds. |
| `tabId` sits inside `Location`, `Range` and `TableCellLocation` (my assumption) | Mostly confirmed: `TableCellLocation` has none, the tab rides on `tableStartLocation`; `deleteHeader`/`deleteFooter`/`deletePositionedObject` take a top-level `tabId` | Builders match; `CellLoc` carries the table location. |
| `insertTable` leaves an empty paragraph before the table (my assumption) | Refuted: a newline is inserted at the index and the table starts at index + 1, so a mid-paragraph index splits the paragraph | Block-boundary insertions use the previous paragraph's last index so the table follows the block; the leftover newline becomes the paragraph after the table. |
| Preview comment threads carry no range (spike A) | Refined: the range lives in `DocumentTab.commentAnchors[anchorId].ranges` | Threads are located through the anchor map first, quoted text second. |
| Reply, resolve and reopen through the preview (`addCommentReply`, `commentAction`) | Confirmed to exist, but `replies.create` with `action: resolve \| reopen` is GA and serves every deployment | One Drive path for thread operations; the preview is used only where it adds anchoring. |
| Drive comment anchors written by third parties are positioned by the editor (a-bonus #134 hope) | Refuted (manage-comments guide: "Google Workspace editor apps treat these comments as un-anchored comments") | Drive-backend comments quote the text and say they are unanchored. |
| An old revision can be exported with `files.export?revisionId` (my assumption) | Refuted: `files.export` takes only `mimeType`; `files.download` (POST, long-running operation) takes `revisionId` and `mimeType` for Docs | `ExportRevision` polls the operation and fetches the content URL, refusing non-Google hosts. |
| `revisions.list` is complete for Docs (my assumption) | Refuted: "might be incomplete for files with a large revision history, including frequently edited Google Docs" | `list_revisions` says so in its output and description. |
| `deleteTab` fails when the tab has children (my assumption) | Refuted: child tabs are deleted with it | `delete_tab` warns; a document keeps at least one tab. |
| `comments.*` need the `fields` parameter (Drive guide) | Confirmed for comments (an omitted `fields` is an error); the replies pages list no parameters | Sent on every call. |
| `TableRowStyle.tableHeader` can be set, since it is in the schema | Refuted live (2026-09-03): `updateTableRowStyle` answers `400 INVALID_ARGUMENT: Unallowed field: tableHeader`, though `minRowHeight` and `preventOverflow` in the same request are accepted | `style_rows` carries neither the field nor a flag for it; `pin_header_rows` is how a header row is set, and the op's "changes nothing" message says so. A schema field is not proof the field mask accepts it. |
| `DocumentStyle.background` takes the colour object `colorJSON` builds, as `TextStyle.foregroundColor` does (my assumption) | Refuted (discovery document): `foregroundColor` is an `OptionalColor` — `{color: {rgbColor}}` — but `background` is a `Background`, whose own `color` field is that `OptionalColor`, so the payload nests one level deeper | `background` is built as `{"color": <OptionalColor>}`; caught in review before any live call, since a wrong shape fails the whole atomic batch. |
| A section's type can be changed on an existing section | Refuted (discovery document): `SectionStyle.sectionType` is **"Output only"**; only `insertSectionBreak` sets it | `section` neither sends nor accepts `section_type`; passing it is refused with the reason, and the type is chosen by the `section_break` that made the section. |
| A field mask names only the leaves it changes (as `updateTextStyle` does) | Refined for `updateNamedStyle`: the reference says "to update the text style to bold, set `fields` to include `text_style` **and** `text_style.bold`" — the mask is rooted at the named style, so the parent counts too. **Confirmed live** (2026-09-03) through the HTML export: after redefining `HEADING_2`, both headings render as `<h2 style="…color:#1a73e8;font-size:18pt;padding-top:20pt…">` without either being styled individually | The mask carries `textStyle` and `paragraphStyle` beside each leaf. A redefined named style is invisible to `with_styles` (it moves the paragraph default too), so `get_document` reports the definitions themselves (§7.2) and `export_document format: html` checks how they resolve — see docs/development.md. |
| `TableRowStyle.minHeight` and `SectionType: ONE_COLUMN \| TWO_COLUMN \| THREE_COLUMN` (a summary of the reference page) | Refuted against the discovery document (`docs.googleapis.com/$discovery/rest?version=v1`, 2026-09-03): the field is `minRowHeight` (with `preventOverflow` and `tableHeader` beside it), and `insertSectionBreak` takes only `CONTINUOUS` or `NEXT_PAGE` — columns are `SectionStyle.columnProperties` | Wire names taken from the discovery document, not from a prose summary; columns are set on the section, not by its type. |
| `updateCommentPost`, `deleteComment`, `deleteCommentReply`, `deleteSuggestion` in the Docs API | Confirmed present but **Developer Preview only** (Request reference); Drive `comments.update` and `replies.update` are GA | Editing a comment goes through Drive like every other thread operation; only `deleteSuggestion`, which has no Drive equivalent, is preview-gated. |
| `deleteSuggestion` is the same as `rejectSuggestion` (my assumption) | Refuted: reject declines a suggestion and any editor may; delete removes it and returns 403 to anyone but its author | Exposed as a third action, `discard`, with the author rule in its description. |
| A named range can serve as a durable target where a handle cannot | Confirmed (NamedRange reference: Google keeps the range with its content across edits) | `named_range` is a Target (§7.1); a name several ranges share is refused as a target, since a target must mean one range. |
| A fragment inserted into an empty paragraph keeps that paragraph's style (Phase 1 `Inline` rule) | Refuted by the follow-up path: a new header, footer, footnote or tab starts as one empty paragraph, so `# Title` content came out as normal text; an inline insertion into a non-empty paragraph must still keep its style | `FragmentOptions.Fill`: an empty paragraph takes the fragment's style. |
| Per-paragraph text matching is fast enough (my assumption) | Refuted on the 150-page fixture: rebuilding normalised units per paragraph per search made a text target 22 ms and 300 quoted comments 384 ms; one normalised string per segment with `strings.Index` and a unit-offset table brought them to 0.6 ms and 38 ms | `Fetched.text` (§11). |
| `TabProperties.index` is the tab's 0-based position among its siblings, so a requested 1-based position converts with `-1` (the API reference, applied to both add and move) | **Refuted for `move`, live 2026-09-03.** For `add` the index is 0-based as documented, but a move inserts the tab at that index with the tab *still in its old slot* and removes it afterwards, so moving one later lands it a place short, and moving it to the very next position does nothing while reporting success. Four probes: 3→1 gave 1; 1→3 gave 2; 2→3 gave 2; 4→2 gave 2 | `moveTabRequest` raises the index by one when the tab is moving later within the same parent (`siblingIndex`). Moving earlier, and moving under a different parent, are unchanged. The bug hid because every earlier test moved a tab to position 1, where the two readings agree. |
| `read_document with_styles` shows a run's formatting | Confirmed, and sharpened live 2026-09-03: Google returns only the properties a run sets itself, so the annotation already *is* the deviation from what the run inherits. A read of a document whose `HEADING_2` had just been redefined blue, centred and single-spaced annotated nothing at all | No per-run comparison to build (an earlier plan of mine, dropped). Inherited formatting is reported once per tab by `get_document` instead of once per paragraph. |
| Giving the MCP SDK a logger logs whole JSON-RPC frames, so debug logging leaks documents (my assumption, and the reason the logger was attached only at debug) | Refuted by reading go-sdk v1.7.0, 2026-09-05: the SDK logs at exactly two call sites — a transport-level `jsonrpc2 internal error` and one handler warning — and never a frame. The conditional stays anyway, because at info the SDK adds two lines of session chatter per session to every client's log file | Per-call logging is ours: `logCalls` middleware records method, tool name, outcome and duration at debug, which is [OWASP's "when, where, who and what"](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) without the payload — its exclusion list covers tokens and personal data, and a person's document is both. `TestDebugLogsCarryNoDocumentData` fails the build if a log line ever carries the fixture's id, title or text, so the bug form can ask for a debug log without asking the reporter to audit it. A handler wrapper to floor the SDK's own level was written and thrown away: twenty lines to suppress two, where a conditional already existed. |
| `errors.Is(err, io.EOF)` catches the end of a stdio session | Refuted, reproduced 2026-09-05: the SDK reports a closed connection as JSON-RPC **-32004** with the EOF only as message text (`server is closing: EOF`), so `errors.Is` never matched and the process exited **1** on an ordinary client disconnect — which a host reports as a crash. The smoke test could not see it because it slept before closing stdin, and with the sleep the exit is 0 | `clientWentAway` matches `jsonrpc.Error` codes -32004 and -32003; `jsonrpc.Error` is a public alias of the SDK's internal wire type, so the code is comparable without sniffing message text. The smoke test closes stdin abruptly as a second case, and fails against the previous binary. |
| Scrubbing a result where it is read keeps the transcript clean (the live driver's port to Go) | Refuted on the port's first run, 2026-09-05: the driver's `call` returned the scrubbed text, so a step that parsed the document URL out of `get_document` fed `https://docs.google.com/document/d/<scratch>/edit` back to `insertRichLink` and Google answered `400 The URL is invalid`. The Python it was ported from scrubbed in the print helper, so the fault arrived with the port | Scrubbing lives in `shown`, the one helper every transcript line goes through; a step parses the untouched text. A redaction on the read path corrupts whatever the caller feeds back. |
| The host allowlist's port handling is incidental — `googleHost` could take `url.URL.Hostname()` and drop the port as noise | Checked 2026-09-05 after a sibling server hit the other side of it: it is passed `Host`, port and all, so `docs.googleapis.com:8443` matches nothing and the request is refused. Fail-closed is the right direction here and not a formality — `ExportRevision` follows the `downloadUri` out of an operation's **response body** with the access token attached, so this check decides whether a credential leaves the machine | Pinned: the test covers ports on allowed hosts, and the comment says not to switch to `Hostname()`. Where a check that governs token egress is going to be wrong, it should be wrong towards refusing. |
| HTTP status is enough to classify a Google error; the reason string refines it at most (the mapping's shape since Phase 0 — status first, one reason-based exception for scopes) | Refuted 2026-09-05 against the [Drive error guide](https://developers.google.com/workspace/drive/api/guides/handle-errors), after the google-drive-mcp session hit the same class of bug from the other side: Drive answers throttling with **403** — `rateLimitExceeded`, `userRateLimitExceeded`, `sharingRateLimitExceeded`, `dailyLimitExceeded` — and prescribes exponential backoff for it. Reading the status first made those `[forbidden]`, so a throttled read was never retried and the model was told to go looking for permissions that do not exist. The same page lists 403 reasons on our own surface that mean "cannot", not "may not": `downloadRestrictedForRevision`, `fileNotExportable`, `storageQuotaExceeded`, `appNotAuthorizedToFile` | `throttled` classifies the four quota reasons as `ErrRateLimited` before the generic 403, and `once` makes them transient so reads back off (writes still repeat only on 429 or 503, which prove nothing was applied). Every reason now appears in `APIError.Error()`, so a refusal the classifier cannot improve on still reaches the model with Google's own word for it. No reason has been *observed* live on this surface — the change is what the documented reasons mean, not a bug seen in the wild. |
| The write-retry rule ("only 429 and 503, which prove nothing was applied") covers every write | Refuted in review 2026-09-05: the guard tested `k == kindWrite`, and Drive writes carry their own kind (`kindDriveWrite`, added for a separate rate limiter), so creating a comment or a reply was retried on **any** 5xx — a 500 arriving after Drive had created the comment left two. The classification change above would have widened it to throttling 403s as well. Neither was ever observed; the test that would have caught it existed only for document batches | The guard is `k != kindRead`, and the new test drives `CreateComment` through 403/500/503/429 (it fails against the old guard on exactly the 403 and the 500). A rule stated for "writes" is tested through every kind of write. |
| A security document is a description of the code (implicit in keeping one) | Refuted 2026-09-05 by auditing §12 and docs/security.md line by line against the code, after a sibling server found its own security page promising a deadline the code did not keep. Two claims here were false in opposite directions: the page said "logs carry ids (truncated), revisions" in one row and "no document data at any level" in another, and the code did the first — `ShortID` put six characters of the id and the whole revision id into debug lines, and the conflict path logged an id at info. The test that was supposed to hold the guarantee passed because it searched for the *whole* id, and because the service's logger was not the logger it read | Ids are gone from every log line, including the path (`/v1/documents/…/x`); `ShortID` is documented as filename-only. The test reads the service's logger too, drives the conflict path, and looks for the id, its first six characters, and the revision. It catches two leaks against the previous code. Where the two rows disagreed, the stronger one is now true rather than the weaker one being written down. |
| A comment beside a version keeps it pinned (the fix after the cosign 3 failure) | Refuted twice over: this repository had `goreleaser-action` pinned by SHA and asked for `~> v2`, and a sibling narrowed the same value to `~> v2.18.0` and recorded that in its evidence log **as the fix**, with a comment saying which half of the pin mattered. A `~>` value resolves to the highest match, so both still floated | `TestWorkflowsPinExactly` in `internal/devcheck`: every action a full commit SHA, every tool version exactly one version, and a failure when it finds no workflows or no versions at all. Verified against `~> v2`, `~> v2.18.0`, `latest` and `v2`. A rule a comment cannot hold is a rule that needs a test. |
| Gates are small enough to live in shell scripts | Refuted by their own failures, 2026-09-05: the coverage floor's package list was hand-written and silently stopped covering a new package; the staleness rule failed on the release pull request it guards; and the coverage floor ran only on Linux in CI because the other runners' bash could not be relied on. A sibling server had already moved its gates into one tested Go program, and its two gate bugs this week were the same two shapes | `internal/devcheck` holds them, with tests: derived lists rather than typed ones, a floor on how much each read before it reports nothing, and every platform in CI. Shell keeps only the stdio smoke and the schema-diff worktree driver, which are process plumbing. |
| The Windows runner runs the same commands as the others (implicit in a three-platform matrix) | Refuted 2026-09-05, the moment the coverage floor stopped being Linux-only: the default shell there is PowerShell, and it turned `-coverprofile=cov.out` into a file named `cov`. Every Windows run for as long as the matrix has existed produced a profile under a name no step referred to, and nothing noticed because the only step that read it was skipped on Windows. The same run also showed `internal/userconfig` at 78.9% there against 93% elsewhere, from a test that skipped rather than clearing `%AppData%` | `shell: bash` on the test job, so one line means one thing on three runners; the userconfig test runs everywhere. A platform in the matrix that no gate reads is a platform nobody is testing. |
| The error classes are a closed vocabulary of ten, stated in §9 and in a comment on `gapi.Class` (the design's oldest model-facing claim) | Refuted 2026-09-05 while comparing this server's conventions with two siblings': the code emitted **fifteen**, four of which — `unknown`, `stale`, `unavailable`, `unsupported` — appeared in no document, and `ambiguous` carried two unrelated meanings, a target matching several things and a write whose outcome is unknown. Those two ask the caller to do opposite things. The vocabulary lived in comments in two packages, so nothing could disagree with it | `service.Classes` is the whole list with what each class asks the reader to do; `gapi.Classes` is the client's half and is asserted to be a subset; `TestClassVocabularyIsClosed` scans every `Errorf` literal in `internal/` and fails on a class that is not listed, on a listed class nothing emits, and on a scan that reads too few files to be looking at anything. The write-outcome class is `ambiguous_outcome` now. |
| The API client's `Timeout` bounds every HTTP call the server makes | Refuted 2026-09-05, from the google-chat-mcp session's review: a token refresh happens inside the oauth2 token source, on the client in the *context*, and nothing put one there — so it ran on `http.DefaultClient`, which has no timeout. A token endpoint that accepts and never answers would hang the first tool call for the life of the process. The signal context made it interruptible, not bounded | `auth.boundHTTP` puts a client carrying `GDOCS_HTTP_TIMEOUT` in the context for the refresh and for `login`'s code exchange; the test hangs a listener and fails against the version without the fix. |
| A SHA on an action pins the release (applied after the cosign 3 failure to `cosign-release` and `syft-version`) | Incomplete, found 2026-09-05: `goreleaser-action` was pinned by SHA and then asked for `~> v2`, so the tool that decides what the artifacts *are* still floated. The same blind spot existed in a sibling repository, so it was the sweep that was partial, not one line | `version: "v2.18.0"`. The rule is now stated as its own row: a SHA pins the wrapper, and every tool a wrapper installs needs its own version. |
| A hand-written list of packages under a coverage floor stays current | Refuted 2026-09-05: a package added under `internal/` was under no floor until someone edited the script, silently — the opposite of what a floor is for. Deriving the list from `go list ./internal/...` immediately caught `internal/userconfig` at 75.4%, whose error paths had never been tested | The list is derived and the exemptions are written down with reasons (a build helper, fixtures, wire types, a test-only package). userconfig is at 93.0%. |
| A test that drives a tool or two proves a guarantee about the server | Refuted for the logging guarantee: `TestDebugLogsCarryNoDocumentData` named the tools it drove, so a tool added later was exempt until someone remembered — the same hole the sibling repo found in its own copy of the test, and the same shape as the live driver's coverage rule and the write-retry guard. A guarantee stated over a set has to be tested over the set | `server.toolArgs` is one call's arguments per tool, `TestEveryToolHasArgs` keeps it complete, and the logging test drives all of it. Whether a call succeeds against the fake does not matter — a refusal is logged too, and a refusal is where a message is most tempted to quote the document. |
| Pinning every action to a full commit SHA pins the build | Refuted by the first release after doing it, 2026-09-05: `cosign-installer` and `sbom-action/download-syft` do not *do* something, they *install* something, and the SHA pins only the wrapper. The cosign it fetched moved to 3, where `--bundle` "has moved from optional to required" (v3.0.1 release notes, re-read 2026-09-06 after a sibling found this row overstated), and the release died at signing with "create bundle file: open : no such file or directory". The notes say nothing about `--output-signature` or `--output-certificate`; this row and `.goreleaser.yaml` both called them dropped, which was a claim neither had checked. The pin read as complete, which is what made it dangerous | `cosign-release` and `syft-version` are pinned beside the SHAs, and the signature is now a Sigstore bundle. Where an action installs a tool, the tool is a build dependency and needs its own pin. |
| Pinning actions to a major tag (`@v7`) is enough | Refuted by GitHub's hardening guide, verified 2026-09-05: "Pinning an action to a full-length commit SHA is currently the only way to use an action as an immutable release" — a tag is mutable by anyone who can write to that repository ([Security hardening for GitHub Actions](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions)) | Every action is pinned to a full SHA with the version in a trailing comment, which is also what OpenSSF Scorecard's Pinned-Dependencies check scores. Dependabot updates SHAs and keeps the comment in step. |
| Declaring `permissions` once at the top of a workflow is least privilege | Refined, same sources: Scorecard's Token-Permissions wants the top level read-only and write raised per job ([Scorecard checks](https://github.com/ossf/scorecard/blob/main/docs/checks.md)) | `release.yml` is `contents: read` at the top; the one job that publishes raises `contents: write`, `id-token: write` and `attestations: write`. CI was already read-only. |
| A checkout leaves its token on disk for later steps, harmlessly | Rejected: nothing in these workflows pushes with it, and GitHub's guide treats persisted credentials as avoidable exposure | `persist-credentials: false` on every checkout. |
| Tests and vulnerability scanning are enough static analysis | Refuted by Scorecard's SAST check, which names CodeQL explicitly | A `codeql` workflow runs on pushes, pull requests and weekly, so a rule added after a merge still reaches old code. |
| Signing artifacts is one decision | Split, and both halves are done for different readers: Sigstore keyless signatures over `checksums.txt` (a person verifying a download) and GitHub build provenance attestations (a machine checking that this workflow and commit produced the file). Scorecard's Signed-Releases looks for `.asc`, `.minisig` or `.intoto.jsonl` assets and will not see either, which is a limitation of the check, not of the release | Kept as is; the README documents `gh attestation verify` and `cosign verify-blob`. |
| Scorecard's Branch-Protection tier 5 (two reviewers, code-owner approval, admins included) is the target | Rejected for a single-maintainer repository: two reviewers cannot exist, and a rule that must be bypassed on every merge teaches people to bypass rules | Pull requests required with CI green as the gate; the review count is the part that does not apply. Revisit if the project gains maintainers. |
| `ParagraphStyle.tabStops` is a field the tools should expose (implied by "implement everything") | Refuted by the discovery document, 2026-09-03: "The list of tab stops is not inherited. This property is read-only." The same holds for `headingId` and a cell's `rowSpan`/`columnSpan` | Read and reported, never sent. §16 lists the read-only set, so "not implemented" is not confused with "not possible". |
| Resource templates with a shared prefix shadow each other (my worry) | Refuted in go-sdk v1.7.0: a template matches through an anchored RFC 6570 regexp in which `{var}` excludes `/`, so `gdocs://{document}` does not match `gdocs://id/outline`; an unmatched URI is resource-not-found (code -32602 since SEP-2164) | Three templates registered side by side; handlers parse the URI themselves. |
| Pinning `shell: bash` on the steps that need it fixes the Windows runner (applied 2026-09-05, after PowerShell turned `-coverprofile=cov.out` into a file called `cov`) | Incomplete, found 2026-09-06: a sibling repository took the coverage half of that finding and skipped the shell half, reasoning that a shell fix applies to a shell gate and its own gate was Go. Its Windows job then failed with `open cov.out: The system cannot find the file specified` — the mangling is on the `run` line, not on the gate. Verified against GitHub's workflow-syntax reference: `defaults.run` applies to every `run` step in the workflow, a job-level block overrides it and a step's own `shell` overrides both; none of them reach a `uses:` step; and an explicit bash runs as `bash --noprofile --norc -eo pipefail {0}` where the implicit non-Windows default is `bash -e {0}`, so pinning it also turns `pipefail` on for Linux and macOS | `defaults: run: shell: bash` at workflow level in all three workflows rather than per step: the runner hands every `run` block to a shell, so this is not a property of steps that look shell-ish, and a default set at that level is inherited by a job somebody adds later. `TestWorkflowsPinTheShell` asserts the block in every workflow and asserts a floor on how many files it read, because the block is unforgettable inside a file and entirely forgettable in the next one. A matrix job whose work happens inside a `uses:` step is outside what any of this promises. |
| The pins check covers every tool a wrapper installs, because `versionKeys` names them | Refuted 2026-09-06 while checking whether anything was out of date: the list carried `gitleaks-version`, which is not an input `gitleaks-action` has ever had — it reads `GITLEAKS_VERSION` from the environment instead. The key had never matched a line, so gitleaks was the one wrapper-installed tool the check did not cover, and the list read as though it did. The action's README settles the smaller question: its default is "a hard-coded version number" rather than `latest`, so the SHA did already pin the scanner — this is a hole in the *check*, not a floating dependency like the cosign one | `GITLEAKS_VERSION: "8.30.1"` named in `ci.yml` so which scanner ran is a line in the workflow rather than a constant inside a five-megabyte bundle, and the key corrected in `versionKeys`, which now matches five versions rather than four. The floor counts what was found rather than trusting the list, which is the only reason this was visible at all. |
| Redacting a transcript by shape covers what needs covering, and a hand-written table of rendered lines is a fair test of it | Refuted twice on 2026-09-06. First on scope: an id, a URL and an address have a shape, but a person's name has none, and `userLabel` renders an owner as `Name <address>` into `get_document`, `list_revisions` and `search_documents` — so the transcript carried the signed-in account's own name on every run. Names can only be caught by **position**, which works here because this project's own renderers wrote every one of them. Then on the test: the table of positions was typed rather than taken from the renderers, and had already drifted — it asserted that a comment line's trailing timestamp survives, when `threadLine` always appends the quote and body, so the rule preserving that timestamp could never fire on real output, and `list_suggestions` was a fifth position the redactor's own comment did not name | Five positions, each case in `internal/livecheck/scrub_test.go` taken from the renderer's format string. A person is redacted to end of line and the tail goes with them — a display name has no reliable terminator, and stopping at the first `(` to save a timestamp left the organisation in `Ann Petersen (Acme Corp)`. That cost is asserted in a test of its own rather than left as a remark. The redactor moved to a file with no build tag so `make check` runs its tests; the coverage exemption that hid it is gone, and an exemption naming a package `go list` does not return is now a test failure — which is how the stale `internal/evals` entry beside it was found. |
