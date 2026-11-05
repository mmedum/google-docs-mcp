# google-docs-mcp

[![CI](https://github.com/mmedum/google-docs-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-docs-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-docs-mcp?sort=semver)](https://github.com/mmedum/google-docs-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-docs-mcp/v3.svg)](https://pkg.go.dev/github.com/mmedum/google-docs-mcp/v3)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-docs-mcp)](./LICENSE)

Google Docs as MCP tools. Read, edit, suggest and comment on documents from Claude or any MCP client.

A single Go binary that speaks [Model Context Protocol](https://modelcontextprotocol.io)
over stdio. It runs as a subprocess of your client, on your own machine,
against your own Google account. There is no server to host, no shared
deployment and no service account: you create a Google OAuth client, log
in once, and the refresh token stays in your OS keyring.

It is for a person with a Google account who wants an assistant to work
inside their documents the way a careful colleague does. It reads a
document at the right granularity, edits in place without damaging what
surrounds the edit, proposes changes as suggestions, and comments on
passages. It handles tables, tabs, headers, footnotes and formatting,
and Markdown and Word files Docs opens in Drive. Folders, sharing and
moving files belong to a Drive server such as google-drive-mcp.

## Goals and non-goals

Goals:

- **Nothing in a document is destroyed by accident.** A replace changes
  only the words that differ. A direct edit that would delete a comment
  anchor, a suggestion, an image or a footnote is refused unless forced,
  and suggest and comment modes delete nothing.
- **Edits land where they were meant to.** The model addresses content
  by exact text, heading id, block handle or named range, never by an
  index. Every write is checked against the revision it was planned on.
- **You decide how a change lands.** Suggest, direct or comment, with a
  default you set; the server never swaps one for another.
- **Results say what happened.** A write reports what it touched and the
  new revision. A write that may have landed says so rather than being
  sent twice.
- **Your documents stay yours.** Every call runs as the signed-in
  account, only to Google's hosts, with no telemetry. Logs never carry a
  document id, title or text.

Non-goals:

- **Folders, moves, sharing, trash and copies.** The scope is one
  document and everything inside it, its history included.
- **A hosted service.** It speaks stdio only.
- **Round-tripping a document through Markdown.** Markdown is for new
  content. What the Docs API cannot express is refused, naming the
  construct, rather than approximated.
- **What the Docs API cannot do**: insert a chart or a table of
  contents, write an equation or a drawing, read an old revision's
  structure rather than its export, or fetch an image from a private
  address.
- **Asking you before a call.** Your client approves each call; the
  server asks nothing.

## Why google-docs-mcp

Other servers hand the model raw UTF-16 indices, convert Markdown in
ways that silently corrupt documents, and anchor comments through the
Drive API, where they never render inline. This server keeps index math
on the server, addresses content by exact text and stable heading ids,
edits by minimal diff, refuses to overwrite anchored content, and uses
the Docs API's suggestion mode and pinned comments. Every GA request of
the Docs API is emitted. The reasoning, one decision at a time, is in
[`docs/architecture.md`](docs/architecture.md).

## Install

```bash
go install github.com/mmedum/google-docs-mcp/v3/cmd/google-docs-mcp@latest
```

That puts `google-docs-mcp` in `$(go env GOPATH)/bin`, which is the path
to give your MCP client. Or take a signed archive from the
[latest release](https://github.com/mmedum/google-docs-mcp/releases/latest)
— Linux, macOS and Windows, on amd64 and arm64 — and verify it before you
run it:

```bash
tar xzf google-docs-mcp_*_linux_amd64.tar.gz
sha256sum -c checksums.txt --ignore-missing

# The checksum file is signed with a keyless Sigstore certificate tied to
# the release workflow's identity. The bundle carries both.
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/mmedum/google-docs-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# And the archive itself carries build provenance.
gh attestation verify google-docs-mcp_*_linux_amd64.tar.gz --repo mmedum/google-docs-mcp
```

Every archive also ships an SBOM (`.sbom.json`), so you can see what is
inside a binary you did not build. `go install` needs none of this: the
module proxy and `sum.golang.org` verify the source before it is built.
`google-docs-mcp --version` reports the release it came from either way.

## Set up Google

Every deployer uses their own Google Cloud project and OAuth client. There
is no shared app and nothing to verify with Google.

1. Create or pick a Google Cloud project.
2. Enable the **Google Docs API** and the **Google Drive API**
   (APIs & Services → Library).
3. Configure the OAuth consent screen (Google Auth Platform → Audience):
   - **Internal** if your account is in a Google Workspace organization.
     Tokens then never expire.
   - **External** with publishing status **Testing** for a consumer
     account. Add yourself as a test user. Google expires refresh tokens
     for such apps after 7 days, so you will run `login` weekly.
4. On an **External** app, add the scopes (Google Auth Platform → Data
   Access): `.../auth/documents`, `.../auth/documents.readonly`,
   `.../auth/drive`, `.../auth/drive.readonly`. Google asks for the list
   only from apps used by people outside your Workspace organization, so
   an **Internal** app can skip this step. Listing them there anyway does
   no harm.

   One login asks for one pair of them: a normal login requests
   `documents` and `drive`, and `GDOCS_READ_ONLY=true` requests
   `documents.readonly` and `drive.readonly` instead. An External app's
   consent screen lists what the client may ask for, so it has to cover
   both pairs, and a scope it has not been given is refused at the
   moment somebody first tries read-only mode. Google grants only what
   is requested, so listing the read-only pair costs a normal login
   nothing.
5. Create an OAuth client (Google Auth Platform → Clients) of type
   **Desktop app**, download its JSON, and store it as
   `~/.config/google-docs-mcp/client_secret.json` (Linux; the
   `google-docs-mcp` folder under your OS config directory elsewhere).
   Keep it out of any repository.
6. Run:

```bash
google-docs-mcp login
google-docs-mcp doctor https://docs.google.com/document/d/<some doc you can open>/edit
```

`login` opens a browser, completes Google's desktop OAuth flow on a
loopback port, and stores the refresh token in your OS keyring (Secret
Service, Keychain or Credential Manager), falling back to a 0600 file
with a warning when no keyring is available. Over SSH, the callback port
has to be forwarded;
[troubleshooting](docs/troubleshooting.md#login-failed-auth-timed-out-waiting-for-the-browser)
says how.

`doctor` checks the credentials, the granted scopes and the APIs, and
reads the document when given one. When a check fails,
[docs/troubleshooting.md](docs/troubleshooting.md) says what it means.

### Markdown files

Docs opens a Markdown file stored in Drive (`.md`, typed `text/markdown`)
and edits it in place, leaving it a Markdown file. The Docs API reads such
a file but refuses every write to it, and nothing writes through an open
editor's session: the only write is an upload of the whole file, and an
upload makes an open editor reload. So there are two ways to work on one.

**Together, live: a working copy.** `create_document` with `from_file`
makes a Google Doc from the file. You and Claude work in it as in any
Google Doc: edits merge with your typing, suggestion mode shows tracked
changes, comments sit on their passage. `write_markdown_file` then writes
what changed in the copy back into the file, block by block. The rest of
the file keeps its own Markdown, its front matter and the comments Docs
keeps in it. A change to a block that was also changed in the file since
the copy was made is a conflict, refused unless `force` keeps the file's
version of those.

**Directly, on the file.** `get_document` says `kind: markdown_file`,
reads return the file's own Markdown, and edits are minimal changes to
that text, uploaded once per call:

- Targets are exact text, `heading_id` (a slug such as `next-steps`, from
  `get_outline`) or `heading`. Lines (`L12`) stand where a Google Doc has
  handles, and a long read continues from one.
- `edit_document` takes `insert`, `append`, `replace`, `delete` and
  `replace_all`, with content written as given. Formatting, tables,
  objects, layout and tabs are refused: write them as Markdown.
- `mode: direct` changes the text. `mode: comment` and `add_comment`
  write a comment into the file under your account's name, the way the
  editor writes its own; the editor shows it, marked "From imported
  document", and keeps it when you save. Its id changes each time the
  file is written, until someone saves the file in the editor.
  `reply_comment` replies the same way. Resolving cannot be done
  through the file (written into it, a resolve makes Google fail to read
  the file), so you resolve in the editor. A Markdown file takes no
  suggestions.
- The comments Docs keeps in the file are left out of reads, never
  touched by an edit, and listed by `list_comments` along with comments
  made through Drive's API. Content that holds comment markup is refused.
- The guard refuses a direct edit that changes commented text, an image
  or a footnote reference unless forced, as in a Google Doc.
- **Writing while someone has the file open.** Drive has no way to make
  an upload conditional, and an open editor holds work the file does not
  show yet. Before writing, the server compares the editor's live
  session with the file: if a comment, reply or resolve has not reached
  the file, it waits a moment, then refuses, `force` or not: that work
  is in no revision yet. Just before the upload it
  checks again. After it, it checks the file's history: if someone's
  save landed in between, the save is kept and the change made again on
  top of it.
- When a person saves the file in the Docs editor, Docs rewrites all of
  it in its own Markdown style and turns front matter into a heading. A
  read warns about front matter; `list_revisions` and `read_document`
  with `revision` reach the earlier text.
- `search_documents` finds Markdown files with `kind: markdown_file` or
  `any`; its default is Google Docs only. `create_document` with `kind:
  markdown_file` makes a new one.

### Word files

Docs opens a Word file stored in Drive (`.docx`) and edits it in place,
leaving it a Word file. The Docs API reads one, its tracked changes as
suggestions and its comments as comments, and refuses every write to it.
So this server writes the file itself: `get_document` says `kind:
word_file`, reads work as on a Google Doc, handles included, and an edit
is made in the file's XML and uploaded whole through Drive.

- `edit_document` takes `insert`, `append`, `replace`, `delete` and
  `replace_all` on the body's text, addressed as on a Google Doc;
  `replace_all` leaves a match in a header, footer or footnote, with a
  warning. Text
  replaced inside a paragraph changes by its minimal difference and keeps
  its look; content of several paragraphs goes between paragraphs or
  replaces whole ones. Headings take the file's own heading styles, and
  lists continue the list they are placed against, at a level
  `list_level` can set.
- `mode: suggest` writes Word tracked changes under your account's name,
  which Docs shows as suggestions and Word as tracked changes.
  `mode: direct` changes the text. `mode: comment` and `add_comment`
  write Word comments. `list_comments` lists them; replying to, resolving
  or deleting one is done in the Docs editor.
- Formatting, tables, images, layout, tabs and reviewing suggestions are
  refused, saying so: save the file as a Google Doc (File > Save as Google
  Docs) for those.
- The guard is a Google Doc's: a direct edit that deletes commented text,
  a suggestion, an image or a footnote reference is refused unless forced.
- **Writing while someone has the file open.** Each write checks that
  Docs and the file still agree on every paragraph it reaches, waits and
  then refuses when they do not, checks again just before the upload, and
  afterwards checks the file's history: a save that landed in between is
  put back and the write not applied. An editor that has the file open
  reloads it. The write then waits until Docs has read the new revision.

## Connect a client

Claude Code:

```bash
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

Claude Desktop rewrites `claude_desktop_config.json` from its own state
while it runs, dropping edits made behind its back: quit it fully, then
edit, then start it. It also loads tool definitions lazily, so give it a
moment before expecting the tools in a chat.

Claude Desktop can instead open the `.mcpb` bundle from a release, with
no config file to edit. It asks for the same settings: the OAuth client
JSON, which may be left empty to use the one `login` recorded, the
profile, read-only, the default write mode, deleting comments and tabs
(`enable_destructive`) and the export folder (`export_dir`).
Deleting stays off unless you turn it on, and binary exports stay off
until you pick a folder. This server asks nobody before a call. Claude
Desktop approves each tool, with Allow once or Always allow, and a tool
you always allow then runs unasked, so leave `enable_destructive` off
unless you need its tools. The bundle covers macOS and Linux on both
architectures, and Windows with its amd64 binary, which Windows on arm64
runs under emulation. It does **not** log you in: install the binary as
well, run `google-docs-mcp login` once, and the bundle picks up the same
credentials.

All settings are environment variables; see
[`docs/configuration.md`](docs/configuration.md). Nothing needs to be set
for the defaults.

## Tools

| Tool | What it does |
|---|---|
| `get_document` | Title, tabs, revision id, owner, last change, counts, and this server's capabilities (available write modes, default). Per tab it also reports the page setup, the floating objects, the named ranges and the named style definitions its paragraphs carry — everything a read of the text cannot show. Cheap; call it first. |
| `get_outline` | Heading tree per tab with stable `heading_id`s, block handles, and section sizes. |
| `read_document` | Scoped, budgeted read as markdown, plain text, or raw Docs JSON. Scope by `heading_id`, heading text, handle range, tab, or header/footer/footnote. Block handles come with the text unless `with_handles` is false; options add styles, pending suggestions as `{++inserted++}` / `{--deleted--}` / `{==restyled==}`, and comment markers `{>>c:id<<}`. |
| `find_in_document` | Text or regex search returning handles, offsets and context. |
| `search_documents` | Locate documents by title or content, owner, or modification date; `kind` adds Markdown files. |
| `export_document` | Google's own md, txt, html inline; pdf, docx, odt, rtf, epub as new files under `GDOCS_EXPORT_DIR`, never over one already there. |
| `create_document` | New document, optionally with markdown content; `kind: markdown_file` makes a `.md` file, `from_file` a working copy of one. |
| `edit_document` | One batch of `insert`, `append`, `replace` (minimal diff), `delete`, `replace_all` (text or RE2 regex), `insert_break`, `insert_footnote`, `create_header`, `create_footer`, `delete_header`, `delete_footer`, `create_named_range`, `delete_named_range`, `replace_named_range`. Targets are exact text, `heading_id`, handles, cells, or a named range that survives later edits. Markdown content may hold tables and images; later batches place them once the text has landed, and a list in it continues the list item it is placed against unless `list_level` sets its level. `mode: suggest`, `direct` or `comment`; `dry_run`; `expect_revision`; `force`. |
| `format_document` | `text_style`, `paragraph_style`, `bullets`, `clear_formatting` on the same targets, same modes. A link goes to a URL, to a heading in the same tab (`#h.…`, from `get_outline`) or to a tab (`#tab=t.…`); markdown content takes the same forms. |
| `list_suggestions` | Pending suggested edits with ids, text and handles, including the formatting-only ones that add and remove nothing, and the replies on each one's thread. |
| `review_suggestion` | Accept, reject or discard suggestions, each by the id `list_suggestions` shows. |
| `list_comments` | Comment threads with every reply, resolved and deleted state, who each is assigned to, quoted text and the block they sit on. |
| `add_comment` | Comment on a passage (pinned to it) or on the document; on a Markdown file, written into the file. |
| `reply_comment` | Reply to, resolve, reopen a thread, or rewrite a comment or reply of your own. Also replies on a suggested edit's thread. |
| `list_revisions` | Version history: revision ids, times, authors. |
| `diff_revisions` | Unified diff of Google's markdown or text export between two revisions. `read_document` reads an old `revision` whole. |
| `edit_table` | `insert_table` (with a data grid), `set_cells` (minimal diff per cell), `insert_rows`, `delete_rows`, `insert_columns`, `delete_columns`, `merge_cells`, `unmerge_cells`, `style_cells`, `style_columns` (fixed or even widths), `style_rows` (least height, page-break behavior), `pin_header_rows`. Same modes and guard as text edits. |
| `insert_object` | Insert an inline image from a public URL, a person chip, a rich-link chip or a date chip at a location; replace an image's source in place; or delete an object, including a floating image no text range covers. |
| `layout_document` | `page` (size, margins, background, landscape, page numbering, first/even-page headers, pageless), `section` (the same for one section, plus 1–3 columns), `section_break`, and `named_style` to redefine `NORMAL_TEXT`, `TITLE`, `SUBTITLE` or `HEADING_1` … `HEADING_6` for a whole tab. |
| `manage_tabs` | Add (with content), rename, move or nest tabs. Always direct: the API cannot suggest tab changes. |
| `write_markdown_file` | Write a working copy back into its Markdown file, carrying only what changed in the copy; conflicts with changes made in the file since are refused unless forced, and changes in the Docs editor not saved yet are never written past; `dry_run`. |

The delete tools register only with `GDOCS_ENABLE_DESTRUCTIVE=true`:
`delete_comment` (a thread or one reply) and `delete_tab` (a tab with its
content and child tabs). Each carries the destructive annotation, and
each call must repeat its target in `confirm_comment_id` or `confirm_tab`.
`delete_comment` also takes `text_start`, how the text it deletes begins
as `list_comments` shows it, so your client's prompt shows which comment
goes; the server reads the comment and refuses `[stale]` when it differs.

Documents are identified by id, any `docs.google.com` URL, or a Markdown
file's `drive.google.com/file/d/…` URL.

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
read (curly quotes, dashes and spacing are normalized; it must occur
once, or `occurrence` / `within` disambiguates), a whole section by its
stable `heading_id`, a block by handle (`p12`, valid for the revision it
came from and re-checked on use), a table cell, or a named range, which
is the one anchor that outlives an edit: Google moves it with the text it
covers, so `create_named_range` in one call and
`target: {named_range: …}` in a later one reach the same passage. New
content is written as markdown. A `replace` is applied as the smallest
diff between the old and new text, so untouched words keep their
formatting and anchored comments.

Tables are named by handle (`tbl1`) and cells as `r2c3`; a table that
gets a data grid is inserted empty and filled in a second batch once it
exists. Table ops read in order: once one changes the grid, the ops after
it on that table are applied in a batch of their own, so their row,
column and cell numbers mean the grid as it is by then. Old revisions are
read and diffed through Google's export, so they have no handles.

## Safety

- Read tools are annotated read-only. The edits, `edit_document`,
  `format_document`, `edit_table`, `insert_object` and
  `layout_document`, take a `mode` chosen by the person: `suggest`
  (tracked change), `direct`, or `comment` (the proposal is posted as a
  comment and nothing is edited). Each is guarded by the revision it was
  planned against; a concurrent edit is re-planned once, then refused.
- Direct edits never delete a range that holds a comment anchor, a
  pending suggestion, an image or a footnote unless explicitly forced.
- Destructive tools (deleting comments or tabs) are not registered unless
  `GDOCS_ENABLE_DESTRUCTIVE=true`. `GDOCS_READ_ONLY=true` registers only
  read tools and requests read-only scopes.
- **Your MCP client approves each call; the server asks you nothing.**
  claude.ai and Codex prompt for every tool marked destructive: the
  deletes, and every write that changes or removes what is there, such as
  `edit_document`, `format_document` or `reply_comment`. Only
  `create_document`, `add_comment` and `export_document`, which add, are
  not marked. VS Code prompts for every tool that is not read-only:
  every write, and `export_document`, which writes a local file. In
  Claude Code, list the calls you want to approve one by one under
  `permissions.ask` in your settings, with the name you gave the server
  in place of `google-docs`. For example, the deletes, reviewing other
  people's suggestions, and writing a Markdown file back:

  ```json
  {
    "permissions": {
      "ask": [
        "mcp__google-docs__delete_comment", "mcp__google-docs__delete_tab",
        "mcp__google-docs__review_suggestion", "mcp__google-docs__write_markdown_file"
      ]
    }
  }
  ```

  A client that runs with nobody watching can make every write its
  settings register, so leave `GDOCS_ENABLE_DESTRUCTIVE` off for one.
- `dry_run` previews a write and sends nothing. `edit_document`,
  `format_document`, `edit_table`, `insert_object`, `layout_document`,
  `review_suggestion`, `write_markdown_file`, `add_comment` and the
  delete tools take it.
- The server talks only to Google: `googleapis.com` and its subdomains,
  `*.googleusercontent.com`, and `docs.google.com` for the export links
  Google's API returns. No telemetry. Logs (stderr) never carry a
  document id, revision, title or text, at any level.
- Refresh tokens live in the OS keyring; `logout` revokes and deletes.

[docs/security.md](docs/security.md) has the threat model.

## Getting help

Run `google-docs-mcp doctor` first. It checks the credentials, the
granted scopes and the APIs, and names what is missing; most first-run
trouble is an API that was never enabled or a consent screen without
you on it. [docs/troubleshooting.md](docs/troubleshooting.md) covers
each failure.

`google-docs-mcp status --json` prints the setup as one JSON object,
without contacting Google, for a script that has to decide whether this
server is authorized before starting it; the shape is in
[docs/configuration.md](docs/configuration.md#reading-the-setup-from-a-script).

Then open an [issue](https://github.com/mmedum/google-docs-mcp/issues)
with what
[troubleshooting](docs/troubleshooting.md#reporting-a-problem) lists.
**Never paste a document id or URL, document content, a client JSON, a
token or a tool result**; describe the shape of the document instead.
Security problems go through [`SECURITY.md`](SECURITY.md), privately.

## Versioning

Tool names, their arguments and the shape of their output are stable
within a major version. A tool removed or renamed, an argument that
becomes required, or a change to what a tool returns is a major
version; new tools and new optional arguments are minor. The schema
diff in CI holds this on every pull request, so the tool surface cannot
change without the diff naming it.

The exact prose of a tool's text output is not covered: it is written
for a model to read, and is reworded when a model reads it badly. The
`structuredContent` a tool returns is covered.

A change that needs you to do something, such as a new scope, another
`login` or a different command in your client config, is marked
**Breaking:** in [CHANGELOG.md](CHANGELOG.md), which is what the release
notes are made from.

## Documentation

- [docs/configuration.md](docs/configuration.md): every setting and
  flag, where state is stored, and reading the setup from a script.
- [docs/troubleshooting.md](docs/troubleshooting.md): symptoms and their
  fixes, then rotating the token, revoking access and handling a
  suspected exposure.
- [docs/security.md](docs/security.md): the threat model.
- [docs/architecture.md](docs/architecture.md): how it works, and the
  design decisions a contributor should not undo.
- [docs/release.md](docs/release.md): how a release is cut and checked.
- [CONTRIBUTING.md](CONTRIBUTING.md): the gates, the live run and the
  review flow.
- [CHANGELOG.md](CHANGELOG.md): what each release changed.

## Contributing

Questions and bugs go in
[issues](https://github.com/mmedum/google-docs-mcp/issues); pull requests
are welcome. [`CONTRIBUTING.md`](CONTRIBUTING.md) covers building,
the gates and the pull request flow, and `make check` is what has to
pass.

## Security

[`SECURITY.md`](SECURITY.md) says how to report a vulnerability. Please
do not open a public issue for one.

## Code of conduct

[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
