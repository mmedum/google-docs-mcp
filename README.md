# google-docs-mcp

[![CI](https://github.com/mmedum/google-docs-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-docs-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-docs-mcp?sort=semver)](https://github.com/mmedum/google-docs-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-docs-mcp.svg)](https://pkg.go.dev/github.com/mmedum/google-docs-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-docs-mcp)](./LICENSE)

Google Docs as MCP tools. Read, edit, suggest and comment on documents from Claude or any MCP client.

A single Go binary that speaks [Model Context Protocol](https://modelcontextprotocol.io)
over stdio. It runs as a subprocess of your client, on your own machine,
against your own Google account. There is no server to host, no shared
deployment and no service account: you create a Google OAuth client, log
in once, and the refresh token stays in your OS keyring.

It works inside a document the way a careful colleague does — reads it at
the right granularity, edits in place without damaging what surrounds the
edit, proposes changes as suggestions, comments on passages, and handles
tables, tabs, headers, footnotes and formatting.

## Why google-docs-mcp

Existing servers hand the model raw UTF-16 indices, convert markdown in
ways that silently corrupt documents, and anchor comments through the
Drive API where they never render inline. This server keeps index math on
the server, addresses content by exact text and stable heading ids, edits
by minimal diff, refuses to overwrite anchored content, and uses the Docs
API's suggestion mode where the project is enrolled. The reasoning and
evidence are in [`docs/architecture.md`](docs/architecture.md).

Reading, searching, creating, exporting, editing with minimal diffs in
suggest, direct or comment mode, formatting, reviewing suggestions,
comment threads, revision history and diffs, tables, tabs, headers,
footers, footnotes, images, chips, named ranges, page and section layout,
named styles, `gdocs://` resources, large-document performance and agent
evals are all in. Every GA member of the Docs API's `Request` union is
emitted; §16 of the architecture says which fields those tools expose and
which they deliberately do not.

## Install

```bash
go install github.com/mmedum/google-docs-mcp/cmd/google-docs-mcp@latest
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
   - **Internal** if your account is in a Google Workspace organisation.
     Tokens then never expire.
   - **External** with publishing status **Testing** for a consumer
     account. Add yourself as a test user. Google expires refresh tokens
     for such apps after 7 days, so you will run `login` weekly.
4. Add scopes (Google Auth Platform → Data Access): `.../auth/documents`,
   `.../auth/documents.readonly`, `.../auth/drive`, `.../auth/drive.readonly`.

   All four, though one login never asks for more than two of them: a
   normal login requests `documents` and `drive`, and `GDOCS_READ_ONLY=true`
   requests `documents.readonly` and `drive.readonly` instead. The consent
   screen lists what the client may ask for, so it has to cover both, and
   a scope it has not been given is refused at the moment somebody first
   tries read-only mode. Google grants only what is requested, so listing
   the read-only pair costs a normal login nothing.
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

The document is optional: `doctor` on its own checks credentials, scopes
and API reachability, and reads the document when given one.

`login` opens a browser, completes Google's desktop OAuth flow on a
loopback port, and stores the refresh token in your OS keyring (Secret
Service, Keychain or Credential Manager), falling back to a 0600 file
with a warning when no keyring is available. `doctor` checks every step
and tells you exactly what is missing.

### Logging in over SSH

The callback goes to the *remote* host's loopback address and your
browser is local, so the port has to be forwarded. It is drawn at random
and appears only in the URL `login` prints, percent-encoded as
`127.0.0.1%3A<port>`:

```bash
google-docs-mcp login --no-browser
```

Read the port out of that URL, forward it from a second local terminal,
then open the URL in your own browser:

```bash
ssh -N -L <port>:127.0.0.1:<port> you@remote-host
```

If `ssh` says `bind: Address already in use`, stop the login with Ctrl-C
and start it again to draw a different port. `--timeout` sets how long
`login` waits; the default is five minutes.

### Optional: Developer Preview

Suggestion mode (`mode: suggest`), comments anchored to a text range, and
accepting or rejecting suggestions use Docs API features that are in the
[Google Workspace Developer Preview Program](https://developers.google.com/workspace/preview).
Apply with the form on that page, giving your Cloud project id. Once
enabled for your project, set `GDOCS_PREVIEW=true`. The programme terms
allow use inside your own organisation; do not offer a preview-enabled
deployment to people outside it.

**These features sit outside the version promise below.** They are the
one part of this server built on an API Google may change or withdraw
while it is in preview, and a change there is not something this project
can absorb without changing behaviour. Everything reachable with
`GDOCS_PREVIEW` unset follows semver as stated; the preview-gated
features follow Google's preview programme, and if it moves, they move.

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
| `search_documents` | Locate documents by title or content, owner, or modification date. |
| `export_document` | Google's own md, txt, html inline; pdf, docx, odt, rtf, epub as files under `GDOCS_EXPORT_DIR`. |
| `create_document` | New document, optionally with markdown content. |
| `edit_document` | Atomic batch of `insert`, `append`, `replace` (minimal diff), `delete`, `replace_all`, `insert_break`, `insert_footnote`, `create_header`, `create_footer`, `delete_header`, `delete_footer`, `create_named_range`, `delete_named_range`, `replace_named_range`. Targets are exact text, `heading_id`, handles, cells, or a named range that survives later edits. `mode: suggest`, `direct` or `comment`; `dry_run`; `expect_revision`; `force`. |
| `format_document` | `text_style`, `paragraph_style`, `bullets`, `clear_formatting` on the same targets, same modes. |
| `list_suggestions` | Pending suggested edits with ids, text and handles, including the formatting-only ones that add and remove nothing. |
| `review_suggestion` | Accept, reject or discard suggestions by id or all (Developer Preview). |
| `list_comments` | Comment threads with every reply, resolved and deleted state, quoted text and the block they sit on. |
| `add_comment` | Comment on a passage (pinned with Developer Preview, quoted otherwise) or on the document. |
| `reply_comment` | Reply to, resolve, reopen a thread, or rewrite a comment or reply of your own. |
| `list_revisions` | Version history: revision ids, times, authors. |
| `diff_revisions` | Unified diff of Google's markdown or text export between two revisions. `read_document` reads an old `revision` whole. |
| `edit_table` | `insert_table` (with a data grid), `set_cells` (minimal diff per cell), `insert_rows`, `delete_rows`, `insert_columns`, `delete_columns`, `merge_cells`, `unmerge_cells`, `style_cells`, `style_columns` (fixed or even widths), `style_rows` (least height, page-break behaviour), `pin_header_rows`. Same modes and guard as text edits. |
| `insert_object` | Insert an inline image from a public URL, a person chip, a rich-link chip or a date chip at a location; replace an image's source in place; or delete an object, including a floating image no text range covers. |
| `layout_document` | `page` (size, margins, background, landscape, page numbering, first/even-page headers), `section` (the same for one section, plus 1–3 columns), `section_break`, and `named_style` to redefine `NORMAL_TEXT`, `TITLE`, `SUBTITLE` or `HEADING_1` … `HEADING_6` for a whole tab. |
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
came from and re-checked on use), a table cell, or a named range, which
is the one anchor that outlives an edit: Google moves it with the text it
covers, so `create_named_range` now and `target: {named_range: …}` in a
later call reach the same passage. New content is written as markdown. A
`replace` is applied as the smallest diff between the old and new text, so
untouched words keep their formatting and anchored comments.

Tables are named by handle (`tbl1`) and cells as `r2c3`; a table that
gets a data grid is inserted empty and filled in a second batch once it
exists. Table ops read in order: once one changes the grid, the ops after
it on that table are applied in a batch of their own, so their row,
column and cell numbers mean the grid as it is by then. Old revisions are
read and diffed through Google's export, so they have no handles.

## Safety

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

## How it works

```
MCP client ──stdio──► google-docs-mcp
                       ├── tools     one handler per tool; shapes the reply
                       ├── service   orchestration and scope resolution
                       ├── plan      write planning and all the index math
                       ├── doc       model, handles, sections
                       ├── render    markdown, text, outline
                       ├── gapi      raw REST client for Docs and Drive
                       └── auth      refresh token → access token
```

Index math never leaves the server: the model addresses content by exact
text, `heading_id` or handle, and `internal/plan` turns that into the
UTF-16 offsets the Docs API wants. The wire types in `internal/gdocs` are
this project's own, held to Google's published discovery document by a
gate, rather than the generated client.

## Getting help

`google-docs-mcp doctor` checks credentials, scopes and API reachability
and names what is missing; most first-run trouble is an API that was
never enabled or a consent screen without you on it. If that does not
explain it, [open an issue](https://github.com/mmedum/google-docs-mcp/issues)
— the bug form asks for the `doctor` output and the version.

Never paste a document id or URL, document content, a
`client_secret.json` or a token into an issue; describe the shape of the
document instead. Security problems go through
[`SECURITY.md`](SECURITY.md), privately.

## Versioning

From v1.0.0, semver as you would expect: a tool removed or renamed, an
argument that becomes required, or a change to what a tool returns is a
major version. New tools and new optional arguments are minor. The
schema diff in CI is what enforces it, and it runs on every pull
request — the tool surface cannot change without the diff naming it.

The two exclusions, both stated so they are decisions rather than
surprises: the Developer Preview features above, and the exact prose of
a tool's text output, which is written for a model to read and will be
reworded when a model reads it badly. The `structuredContent` a tool
returns is covered; the sentence wrapped around it is not.

Each release's notes are the matching section of
[`CHANGELOG.md`](CHANGELOG.md); a change needing you to act — a new
scope, another login, a different command in your client config — is
marked **Breaking:** there.

## Development

```bash
make build     # the binary
make test      # race detector, coverage floor
make check     # everything CI runs
```

`make check` is the definition of done: formatting, `go vet` under every
build tag, golangci-lint, race tests with a per-package coverage floor,
`govulncheck`, a licence check, a secret scan, an API-coverage gate and
an API-fields gate that fail when a Google API method or field has no
verdict on it, a schema diff against the released tool surface, a stdio
smoke test, and a staleness gate that fails when this README, the docs or
the changelog drift from the code.

Test fixtures are synthetic. Never add content, ids or URLs from real
documents. `make bench` measures the large-document paths;
`go test -tags=evals ./internal/evals` runs the agent evals against your
own account (see the package comment).

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — the design, the
  request flow, the evidence log behind every convention, and the
  decisions a contributor should not undo.
- [`docs/configuration.md`](docs/configuration.md) — every environment
  variable and flag.
- [`docs/security.md`](docs/security.md) — the threat model: what reaches
  a log, what `doctor` prints, what the drivers write. Each claim is held
  by a test.

## Contributing

Questions and bugs go in
[issues](https://github.com/mmedum/google-docs-mcp/issues); pull requests
are welcome. [`CONTRIBUTING.md`](CONTRIBUTING.md) covers the branch and
review flow, and `make check` is what has to pass.

## Security

[`SECURITY.md`](SECURITY.md) says what is in scope and how to report a
vulnerability privately. Do not open a public issue for one.

## Code of conduct

[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
