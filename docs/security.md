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
  a bug report. A test fails the build if that stops being true.
- Scopes: `documents` and `drive` (or their read-only variants with
  `GDOCS_READ_ONLY`). `drive` is required to reach documents the app did
  not create; the narrower `drive.file` cannot.
- `logout` revokes the token at Google and deletes it locally.

## What can go wrong and what limits it

| Risk | Mitigation |
|---|---|
| The model edits the wrong passage | Targets are exact text, stable heading ids, or handles checked against the revision they came from. Every write is guarded by `requiredRevisionId`; a concurrent edit is re-planned once, then refused. |
| Content anchored to comments, suggestions, images or footnotes is destroyed | Direct edits refuse to delete such ranges unless `force` is passed; `suggest` and `comment` modes delete nothing. |
| Destructive actions | Delete tools are unregistered unless `GDOCS_ENABLE_DESTRUCTIVE=true`, carry `destructiveHint`, and request user interaction. |
| Runaway output | Reads are budgeted (`max_chars`, default 20 000) and cut at block boundaries. |
| Regex denial of service | Go's RE2 engine, linear time. |
| Secrets in the repository | gitleaks in pre-commit and CI with rules for Google client ids, secrets and refresh tokens; fixtures are synthetic. |
| Document content in logs | Logs carry ids (truncated), revisions, counts and latencies, never text. |
| Arbitrary file writes | Exports (later phase) are confined to `GDOCS_EXPORT_DIR`. |

## Reporting

See [SECURITY.md](../SECURITY.md).
