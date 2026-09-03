# google-docs-mcp

A production-grade [Model Context Protocol](https://modelcontextprotocol.io)
server for Google Docs, written in Go. It lets Claude Code, Claude Desktop
and any other MCP client work inside a Google Doc the way a careful
colleague does: read it at the right granularity, edit it in place without
damaging what is around the edit, propose changes as suggestions, comment
on passages, and handle tables, tabs, headers, footnotes and formatting.

Single static binary. Per-user OAuth against your own Google account, in
your own Google Cloud project. Nothing leaves your machine except calls to
Google's APIs.

**Status: Phase 3 done (v0.3.0).** Reading, searching, creating,
exporting, editing with minimal diffs in suggest, direct or comment mode,
formatting, reviewing suggestions, comment threads, revision history and
diffs, tables, tabs, headers, footers, footnotes, images, chips,
`gdocs://` resources, large-document performance and agent evals are in;
see [docs/architecture.md](docs/architecture.md) for the design and what
the evals found.

## Why another Google Docs MCP

Existing servers hand the model raw UTF-16 indices, convert markdown in
ways that silently corrupt documents, and anchor comments through the
Drive API where they never render inline. This server keeps index math on
the server, addresses content by exact text and stable heading ids, edits
by minimal diff, refuses to overwrite anchored content, and uses the Docs
API's suggestion mode where the project is enrolled. The reasoning and
evidence are in [docs/architecture.md](docs/architecture.md).

## Install

Download a binary from the releases page, or:

```
go install github.com/mmedum/google-docs-mcp/cmd/google-docs-mcp@latest
```

## Set up Google (once per person)

Every deployer uses their own Google Cloud project and OAuth client. There
is no shared app and nothing to verify with Google.

1. Create or pick a Google Cloud project.
2. Enable the **Google Docs API** and the **Google Drive API**
   (APIs & Services → Library).
3. Configure the OAuth consent screen (Google Auth Platform → Audience):
   - **Internal** if your account is in a Google Workspace organisation.
     Tokens then never expire.
   - **External** with publishing status **Testing** for a consumer
     account. Add yourself as a test user. Google expires refresh tokens
     for such apps after 7 days, so you will run `login` weekly.
4. Add scopes (Google Auth Platform → Data Access): `.../auth/documents`,
   `.../auth/documents.readonly`, `.../auth/drive`, `.../auth/drive.readonly`.
5. Create an OAuth client (Google Auth Platform → Clients) of type
   **Desktop app**, download its JSON, and store it as
   `~/.config/google-docs-mcp/client_secret.json` (Linux; the
   `google-docs-mcp` folder under your OS config directory elsewhere).
   Keep it out of any repository.
6. Run:

```
google-docs-mcp login
google-docs-mcp doctor https://docs.google.com/document/d/<some doc you can open>/edit
```

`login` opens a browser, completes Google's desktop OAuth flow on a
loopback port, and stores the refresh token in your OS keyring (Secret
Service, Keychain or Credential Manager), falling back to a 0600 file
with a warning when no keyring is available. `doctor` checks every step
and tells you exactly what is missing.

### Optional: Developer Preview

Suggestion mode (`mode: suggest`), comments anchored to a text range, and
accepting or rejecting suggestions use Docs API features that are in the
[Google Workspace Developer Preview Program](https://developers.google.com/workspace/preview).
Apply with the form on that page, giving your Cloud project id. Once
enabled for your project, set `GDOCS_PREVIEW=true`. The programme terms
allow use inside your own organisation; do not offer a preview-enabled
deployment to people outside it.

## Connect a client

Claude Code:

```
claude mcp add --transport stdio google-docs -- google-docs-mcp
```

Claude Desktop (`claude_desktop_config.json`) or Cursor (`mcp.json`):

```json
{
  "mcpServers": {
    "google-docs": {
      "command": "/absolute/path/to/google-docs-mcp",
      "env": { "GDOCS_LOG_LEVEL": "info" }
    }
  }
}
```

All settings are environment variables; see
[docs/configuration.md](docs/configuration.md). Nothing needs to be set
for the defaults.

## Tools

| Tool | What it does |
|---|---|
| `get_document` | Title, tabs, revision id, owner, last change, counts, and this server's capabilities (available write modes, default). Cheap; call it first. |
| `get_outline` | Heading tree per tab with stable `heading_id`s, block handles, and section sizes. |
| `read_document` | Scoped, budgeted read as markdown, plain text, or raw Docs JSON. Scope by `heading_id`, heading text, handle range, tab, or header/footer/footnote. Options show handles, styles, pending suggestions as `{++inserted++}` / `{--deleted--}`, and comment markers `{>>c:id<<}`. |
| `find_in_document` | Text or regex search returning handles, offsets and context. |
| `search_documents` | Locate documents by title or content, owner, or modification date. |
| `export_document` | Google's own md, txt, html inline; pdf, docx, odt, rtf, epub as files under `GDOCS_EXPORT_DIR`. |
| `create_document` | New document, optionally with markdown content. |
| `edit_document` | Atomic batch of `insert`, `append`, `replace` (minimal diff), `delete`, `replace_all`, `insert_break`, `insert_footnote`, `create_header`, `create_footer`, `delete_header`, `delete_footer`. Targets are exact text, `heading_id`, handles or cells. `mode: suggest`, `direct` or `comment`; `dry_run`; `expect_revision`; `force`. |
| `format_document` | `text_style`, `paragraph_style`, `bullets`, `clear_formatting` on the same targets, same modes. |
| `list_suggestions` | Pending suggested edits with ids, text and handles. |
| `review_suggestion` | Accept or reject suggestions by id or all (Developer Preview). |
| `list_comments` | Comment threads with every reply, resolved and deleted state, quoted text and the block they sit on. |
| `add_comment` | Comment on a passage (pinned with Developer Preview, quoted otherwise) or on the document. |
| `reply_comment` | Reply to, resolve or reopen a thread. |
| `list_revisions` | Version history: revision ids, times, authors. |
| `diff_revisions` | Unified diff of Google's markdown or text export between two revisions. `read_document` reads an old `revision` whole. |
| `edit_table` | `insert_table` (with a data grid), `set_cells` (minimal diff per cell), `insert_rows`, `delete_rows`, `insert_columns`, `delete_columns`, `merge_cells`, `unmerge_cells`, `style_cells`, `pin_header_rows`. Same modes and guard as text edits. |
| `insert_object` | Inline image from a public URL, person chip, rich-link chip, or date chip at a location. |
| `manage_tabs` | Add (with content), rename, move or nest tabs. Always direct: the API cannot suggest tab changes. |

Two more tools register only with `GDOCS_ENABLE_DESTRUCTIVE=true`:
`delete_comment` (a thread or one reply) and `delete_tab` (a tab with its
content and child tabs). Both carry the destructive annotation and ask the
client to involve the person.

Documents are identified by id or any `docs.google.com` URL.

### Resources

Clients that attach context as MCP resources can read a document without
a tool call. Each is markdown with no handles, suggestions or comments;
the read tools are for scoped, budgeted reads.

- `gdocs://<id>` — every tab's body, cut at 400000 characters with a
  note saying where `read_document` can continue.
- `gdocs://<id>/outline` — the heading tree `get_outline` returns.
- `gdocs://<id>/tabs/<tab>` — one tab's body; `tab` is an id, title or
  number.

### How edits are addressed

The model never sees index numbers. A target is exact text quoted from a
read (curly quotes, dashes and spacing are normalised; it must occur
once, or `occurrence` / `within` disambiguates), a whole section by its
stable `heading_id`, a block by handle (`p12`, valid for the revision it
came from and re-checked on use), or a table cell. New content is written
as markdown. A `replace` is applied as the smallest diff between the old
and new text, so untouched words keep their formatting and anchored
comments.

Tables are named by handle (`tbl1`) and cells as `r2c3`; a table that
gets a data grid is inserted empty and filled in a second batch once it
exists. Old revisions are read and diffed through Google's export, so they
have no handles.

## Safety model

- Read tools are annotated read-only. Write tools take a `mode` chosen by
  the person: `suggest` (tracked change), `direct`, or `comment` (the
  proposal is posted as a comment and nothing is edited). Every write is
  guarded by the revision it was planned against; a concurrent edit is
  re-planned once, then refused.
- Direct edits never delete a range that holds a comment anchor, a
  pending suggestion, an image or a footnote unless explicitly forced.
- Destructive tools (deleting comments or tabs) are not registered unless
  `GDOCS_ENABLE_DESTRUCTIVE=true`. `GDOCS_READ_ONLY=true` registers only
  read tools and requests read-only scopes.
- The server talks only to Google (`googleapis.com`, and `docs.google.com`
  for the revision export links Google's API returns). No telemetry. Logs
  (stderr) never contain document text.
- Refresh tokens live in the OS keyring; `logout` revokes and deletes.

See [docs/security.md](docs/security.md).

## Development

```
make check      # gofmt, vet, golangci-lint, tests with coverage floor, govulncheck, stdio smoke
make build
./google-docs-mcp --dump-schemas
```

Test fixtures are synthetic. Never add content, ids or URLs from real
documents; gitleaks runs in pre-commit and CI. `make bench` measures the
large-document paths; `scripts/evals/run.py` runs the agent evals
against your own account (see its header).

## Licence

Apache-2.0.
