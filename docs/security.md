# Security

## Trust boundaries

- **The person** runs the binary under their own account and approves
  tool calls in their MCP client.
- **The MCP client** (Claude Code, Claude Desktop, Cursor) speaks JSON-RPC
  over stdio. Stdout carries only protocol frames; logs go to stderr.
- **Google** is the only network peer: `docs.googleapis.com`,
  `www.googleapis.com` (Drive), `oauth2.googleapis.com` and
  `accounts.google.com`. No telemetry, crash reporting or update checks.

## Credentials

- OAuth Desktop-app client JSON is supplied by the deployer and read from
  disk; it is never logged or copied.
- The refresh token is stored in the OS keyring. When the keyring is
  unavailable (no Secret Service on Linux, headless), it is written to a
  file under the profile directory and a warning is printed. The server
  asks for mode 0600, which Unix enforces; Windows has no POSIX
  permission bits, so there the file is protected by the ACL it inherits
  from the user's profile directory and nothing narrows it further —
  prefer the keyring (Credential Manager) on that platform. The
  `GDOCS_REFRESH_TOKEN` variable overrides both for automation.
- **Logs carry no document data at any level.** The server's own lines
  are startup and shutdown facts; per-call debug logging records the
  method, the tool name, the outcome and the duration. Document ids,
  titles and text never reach a log, so a debug log is safe to attach to
  a bug report. A test fails the build if that stops being true — it
  drives every registered tool and the conflict path, and it looks for
  the id, the first six characters of the id, and the revision id, all
  three of which a log line carried until this claim was audited against
  the code in September 2026.
- **`doctor` and `status` output is what a bug report carries, so it is
  held to the same rule.** The promise above is about logs, and that is
  narrower than it reads: the README asks people to paste `doctor`
  output into an issue, which is a different surface, and until this was
  audited that surface printed the signed-in account's address twice,
  the title of whichever document was checked, and its revision id.
  Those are gone, and the same masking now covers every line `doctor`
  prints, errors included — the client-secret path reached the output a
  second time inside `auth: read client secret <path>`, three lines
  under the line that had been carefully masked, on the commonest
  failure there is. The startup log line that records the resolved
  account is masked too, since that log is the other thing a bug report
  carries. The account line keeps the domain and drops the local part,
  because the reason to read it is "am I signed in as the right
  account?" and for someone with a work and a personal account the
  domain is what answers that — keeping it is the deliberate half of the
  trade. The client-secret line prints the path, with a Google client id
  removed from the file name: nobody chose to print a client id, but the
  Cloud console names the file it hands you after the client, so
  printing where the secret lives printed the id along with it. The
  document check reports counts rather than values, so there is no value
  path for a later edit to widen.
- **The live driver's transcript is redacted before it is written**, for
  the same reason: it is pasted into issues and commit messages. Ids and
  URLs are caught by shape; a revision is caught by shape where it is
  written as `revision <id>` and by position in a revision listing,
  which writes the id bare. A person's name has no shape,
  so names are caught by position — the set is closed and short because
  this project's own renderers wrote every one of them — and an address
  is caught both ways, since `userLabel` renders a person as
  `Name <address>`. `internal/redact` carries no build tag,
  so the tests for it run in every `make check` rather than only when
  someone runs the driver with credentials.
- Scopes: `documents` and `drive` (or their read-only variants with
  `GDOCS_READ_ONLY`). `drive` is required to reach documents the app did
  not create; the narrower `drive.file` cannot.
- `logout` revokes the token at Google and deletes it locally.

## What can go wrong and what limits it

| Risk | Mitigation |
|---|---|
| The model edits the wrong passage | Targets are exact text, stable heading ids, or handles checked against the revision they came from. Every write is guarded by `requiredRevisionId`; a concurrent edit is re-planned once, then refused. |
| Content anchored to comments, suggestions, images or footnotes is destroyed | Direct edits refuse to delete such ranges unless `force` is passed; `suggest` and `comment` modes delete nothing. |
| Destructive actions | Delete tools are **unregistered** unless `GDOCS_ENABLE_DESTRUCTIVE=true`, and once registered each call must repeat its target — `confirm_tab`, `confirm_comment_id` — or the server refuses it. Registration is a deployer's decision made once; the repetition is a decision made every time, and it is checked in the server rather than declared in the schema, so a client in an auto-approve mode cannot skip it. They also carry `destructiveHint` and `requiresUserInteraction`, but both are advisory — the spec says clients treat tool annotations as untrusted, and a host in an auto-approve mode runs a registered tool without asking. What this server controls is what it registers and what it refuses; the hints are a courtesy to clients that honour them. |
| Runaway output | Reads are budgeted (`max_chars`, default 20 000) and cut at block boundaries. |
| Regex denial of service | Go's RE2 engine, linear time. |
| Secrets in the repository | gitleaks in pre-commit and CI with rules for Google client ids, secrets and refresh tokens; fixtures are synthetic. |
| Identifiers in the repository, which are not secrets and which no secret scanner flags | `make leaks` fails the build on an address at a domain someone could own, a 21-digit account id, an OAuth client id, a user-content URL carrying an id, or a Drive id that does not look invented. `LEAKCHECK_HISTORY=1 go test ./scripts/gates` runs the same rules over every blob ever committed, since a tree scan cannot see what was cleaned up in a later commit. |
| Document content in logs | Logs carry counts, durations, outcomes and method names. No ids — not even a prefix — no revisions, no titles, no text. `TestDebugLogsCarryNoDocumentData` drives every registered tool plus the conflict path and fails on any of them. |
| A credential sent somewhere it should not go | Every request URL is checked against an allowlist of Google hosts before the token is attached, matched on `url.URL.Host`, so a host carrying a port matches nothing and is refused. This is not a formality: `ExportRevision` follows a download URL taken from a response body. |
| A hung Google endpoint | Every API call carries `GDOCS_HTTP_TIMEOUT`, and so does the OAuth token refresh and the code exchange at login — the refresh happens inside the token source, on the client in its context, which is a separate place to put a deadline and was missing one. |
| Arbitrary file writes | Exports are confined to `GDOCS_EXPORT_DIR`, and a format that writes a file is refused when it is unset. |

## Reporting

See [SECURITY.md](../SECURITY.md).
