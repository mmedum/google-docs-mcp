# Configuration

Everything is an environment variable, because MCP clients pass `command`,
`args` and `env` to a stdio server and nothing else. Each variable also
has a flag with the same name (`GDOCS_LOG_LEVEL` ↔ `--log-level`); a flag
given explicitly wins over the environment. A variable set to spaces
alone is unset, as an empty one is, and the spaces around a value are
dropped.

The Claude Desktop bundle asks for `GDOCS_CLIENT_SECRET`, `GDOCS_PROFILE`,
`GDOCS_READ_ONLY`, `GDOCS_ENABLE_DESTRUCTIVE`, `GDOCS_DEFAULT_WRITE_MODE`
and `GDOCS_EXPORT_DIR`, each under its name without `GDOCS_`, lowercased.
A field left empty or off does what the variable left unset does. Leave
the OAuth client field empty to use the client `login` recorded for the
profile; a file picked there must be that same client, since the refresh
token belongs to it.

| Variable | Default | Meaning |
|---|---|---|
| `GDOCS_PROFILE` | `default` | Named profile. Profiles keep separate client secrets, tokens and account. Use one per Google account. A name is up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit; capitals are read as lowercase, and any other name stops the server at start. |
| `GDOCS_CLIENT_SECRET` | profile setting, else `<config dir>/client_secret.json` | Path to the Desktop-app OAuth client JSON. Unset or empty, the server uses the client `login` recorded for the profile. |
| `GDOCS_REFRESH_TOKEN` | unset | Overrides the stored token (CI, automation). |
| `GDOCS_CONFIG_DIR` | OS config dir + `google-docs-mcp` | Absolute directory profiles live in. A relative one stops the server at start. |
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
google-docs-mcp status [--json]
google-docs-mcp doctor [document id or URL]
google-docs-mcp --version
google-docs-mcp --dump-schemas
```

[troubleshooting.md](troubleshooting.md) says what `status` and `doctor`
print, and what each failed check means.

## Reading the setup from a script

`google-docs-mcp status --json` prints the same state as `status`, as one
JSON object on stdout, without contacting Google. It is for a script
that needs to know whether this server is authorized before starting
it. `credentials.resolved` is the field to branch on. `schema_version`
changes only when a field is removed or its meaning changes, and the
account is masked to its domain, as the text output masks it (D-05). A
label in the text output may be reworded in any release; the object may
not.

## Approving calls

The server asks the person nothing; the MCP client approves each call
(D-52). The README's Safety section says what each client prompts for,
and gives Claude Code `permissions.ask` rules.

## Removed settings

A removed setting that restricted something is still read, only to
refuse it: set to anything but its old default, the server will not
start, and its flag is gone (D-53). Ignoring it would quietly allow what
it was set to stop. [troubleshooting.md](troubleshooting.md) says what
to do when one stops the server.

| Variable | Still accepted | Instead |
|---|---|---|
| `GDOCS_REQUIRE_PROMPT` | `false`, or unset | Leave `GDOCS_ENABLE_DESTRUCTIVE` off for a client that runs with nobody watching. |
