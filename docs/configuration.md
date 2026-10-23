# Configuration

Everything is an environment variable, because MCP clients pass `command`,
`args` and `env` to a stdio server and nothing else. Each variable also
has a flag with the same name (`GDOCS_LOG_LEVEL` ↔ `--log-level`); a flag
given explicitly wins over the environment.

| Variable | Default | Meaning |
|---|---|---|
| `GDOCS_PROFILE` | `default` | Named profile. Profiles keep separate client secrets, tokens and account. Use one per Google account. |
| `GDOCS_CLIENT_SECRET` | profile setting, else `<config dir>/client_secret.json` | Path to the Desktop-app OAuth client JSON. |
| `GDOCS_REFRESH_TOKEN` | unset | Overrides the stored token (CI, automation). |
| `GDOCS_CONFIG_DIR` | OS config dir + `google-docs-mcp` | Where profiles live. |
| `GDOCS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. Logs go to stderr. |
| `GDOCS_LOG_FORMAT` | `text` | `text` or `json`. |
| `GDOCS_PREVIEW` | unset | Deprecated and ignored; setting it logs a warning. Comments and suggestions are generally available. |
| `GDOCS_DEFAULT_WRITE_MODE` | `suggest` | `suggest`, `direct` or `comment`. |
| `GDOCS_READ_ONLY` | `false` | Register only read tools; `login` requests read-only scopes. |
| `GDOCS_ENABLE_DESTRUCTIVE` | `false` | Register destructive tools (delete comment, delete tab). |
| `GDOCS_EXPORT_DIR` | unset | Absolute directory binary exports may be written to. Unset disables them. An export never overwrites: a name already taken gains a counter. |
| `GDOCS_HTTP_TIMEOUT` | `60s` | Per-request timeout for Google API calls (1s–10m). |

## Profiles and files

Default profile, Linux paths shown (`os.UserConfigDir()` elsewhere):

```
~/.config/google-docs-mcp/
  client_secret.json   your OAuth Desktop client (you put it here)
  config.json          non-secret: client secret path, account email, token location
  token.json           only when the keyring is unavailable (mode 0600)
  profiles/<name>/     the same files for a named profile
```

The refresh token is stored in the OS keyring under service
`google-docs-mcp`, account = profile name.

## Commands

```
google-docs-mcp login [--no-browser] [--timeout 5m] [--read-only] [--client-secret PATH]
google-docs-mcp logout
google-docs-mcp status
google-docs-mcp doctor [document id or URL]
google-docs-mcp --version
google-docs-mcp --dump-schemas
```

`doctor` checks credentials, the token exchange, granted scopes, the Drive
API, and, given a document, the Docs API and whether Google answers it with
comments included, which suggestion mode and pinned comments need.

## Startup behavior

The server always starts. If no credentials are stored or the token is
rejected, it logs a warning and every tool returns an `[auth]` error that
says to run `login`. Exiting instead would make the client show "failed to
connect" without the model ever learning why.

## Approving calls

The server asks the person nothing; the MCP client approves each call.
The README's Safety section says what each client prompts for, and gives
Claude Code `permissions.ask` rules.

### Removed settings

`GDOCS_REQUIRE_PROMPT` went with the server's question in 3.0.0, and its
flag `--require-prompt` is gone, so a flag left in a client's arguments
stops the server from starting. The variable is still read, only to
refuse it: set to anything but `false`, its old default, the server will
not start, because ignoring it would quietly allow what it was set to
stop.

| Variable | Still accepted | Instead |
|---|---|---|
| `GDOCS_REQUIRE_PROMPT` | `false`, or unset | Leave `GDOCS_ENABLE_DESTRUCTIVE` off for a client that runs with nobody watching. |
