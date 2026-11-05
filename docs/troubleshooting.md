# Troubleshooting

What to do when something does not work, then the maintenance tasks.
Setting up in the first place is the README's
[Set up Google](../README.md#set-up-google). Start with these two
commands:

```bash
google-docs-mcp status
google-docs-mcp doctor https://docs.google.com/document/d/<a document you can open>/edit
```

`status` prints the profile, the config directory, the account masked to
its domain, the client secret's path, the token store, the scopes and
the settings, without contacting Google. `doctor` prints the same, then
checks that credentials are found, the refresh token exchange, the
granted scopes and the Drive API. Given a document, it also reads it
through the Docs API and says whether Google answers it with the
comments view. A failed check prints `✘`, the check's name and the
reason. The document is optional.

Run both with the profile and environment your MCP client gives the
server, since each checks what it is given.

## `OAuth client JSON not found at …`

`login` stops with this before opening a browser.

**Cause:** no OAuth client JSON sits at the profile's default path, and
none was named.

**Fix (resolves it):** download the Desktop app client's JSON from the
Cloud console. Put it at the path printed, or pass
`--client-secret <path>`.

## `auth: client secret JSON is not a Desktop app client (expected an "installed" section)`

`login` stops with this.

**Cause:** the file is not a Desktop app client. Sign-in returns to a
loopback port chosen at run time, which only a Desktop app client
accepts (D-66).

**Fix (resolves it):** create an OAuth client of type **Desktop app**
in the same project, download its JSON, and run
`google-docs-mcp login --client-secret <path>`.

## `login failed: auth: timed out waiting for the browser`

**Cause:** the browser never reached the callback. Over SSH, the
callback goes to the remote host's loopback address while the browser
runs on your machine. Otherwise Google showed an error page rather than
returning to the callback.

**Fix (resolves it):** over SSH, run `google-docs-mcp login --no-browser`
on the remote host. The port is drawn at random and appears only in the
URL it prints, percent-encoded as `127.0.0.1%3A<port>`. Read the port
from that URL, and forward it from a second local terminal:

```bash
ssh -N -L <port>:127.0.0.1:<port> you@remote-host
```

Then open the URL in your local browser. If `ssh` says
`bind: Address already in use`, stop `login` with Ctrl-C and start it
again to draw another port. `--timeout` sets how long `login` waits.

For an error page, read it. An External app in **Testing** signs in
only the accounts listed as test users.

## `login failed: auth: Google returned no refresh token; …`

**Cause:** Google answered the sign-in without a refresh token, and the
server cannot call Google later without one.

**Fix (resolves it):** remove the app at
[Google account permissions](https://myaccount.google.com/permissions),
then run `google-docs-mcp login` again.

## `` [auth] Google rejected the stored credentials; run `google-docs-mcp login` and try again ``

Every tool answers this, and the server keeps running, so the model
reads why (D-71). `doctor` fails `credentials found` or
`refresh token exchange`.

**Cause:** this profile has no usable token. Nobody logged in under it,
the client starts the server with another profile or config directory
than your shell uses, or Google refuses the token. An **External** app
in **Testing** gets
[refresh tokens that expire after 7 days](https://developers.google.com/identity/protocols/oauth2#expiration),
so a personal account signs in again about once a week. The token also
dies when the app's access is removed or the OAuth client is deleted.

**Fix (resolves it):** run `google-docs-mcp login` with the same
`GDOCS_PROFILE` and `GDOCS_CONFIG_DIR` as the client's entry. A
Workspace account with an **Internal** consent screen gets refresh
tokens that do not expire.

## `` [forbidden] the stored credentials lack a required OAuth scope; re-run `google-docs-mcp login` ``

`doctor` fails `granted scopes` with `missing …`, and `login` warns
`Google granted fewer scopes than requested`.

**Cause:** a scope was never granted. A box was left unticked at
sign-in, an External app's consent screen lacks the scope, or
`GDOCS_READ_ONLY` changed after the login, which changes the set
(D-68). A token never gains a scope.

**Fix (resolves it):** on an External app, add the scope to the consent
screen, as the README's setup step lists. Then run `login` again under
the configuration the client uses, and tick every box.

## `doctor` fails `Drive API (about.get)` or `Docs API (documents.get)`

**Cause:** the Google Drive API or the Google Docs API is not enabled in
the project that owns the OAuth client. A document check also fails
when the document is not shared with this account.

**Fix (resolves it):** enable both APIs under **APIs & Services →
Library**, then run `doctor` again. For a document, open it in the
browser as the same account.

## `[unavailable] Google refused the comments view for this document, so suggestion mode is unavailable; …`

`doctor` with that document prints `• Google refused the comments view
for this document`, and `get_document` says the same.

**Cause:** every read asks Google for the comments view, which suggestion
mode and pinned comments need. Google refused it for this document, so
the server read it again without the view (D-17).

**Fix:** none from this server. Reads still work.

**Workaround:** use `mode: direct` or `mode: comment`. A comment is then
posted through Drive, quoting the text rather than pinned to it, and the
result says so (D-38).

## `[unavailable] binary exports need GDOCS_EXPORT_DIR to be set to a directory; …`

`export_document` answers this for pdf, docx, odt, rtf and epub.

**Cause:** `GDOCS_EXPORT_DIR` is unset, which turns binary exports off
(D-24).

**Fix (resolves it):** set `GDOCS_EXPORT_DIR` to an absolute path in
the client's entry for this server, and restart the client.

**Workaround:** md, txt and html come back inline with no directory.

## `[conflict] the file is being worked on in the Docs editor: … has not reached the file yet, …`

A write to a Markdown file answers this.

**Cause:** someone has the file open in the Docs editor, and a comment,
reply or resolve made there is not saved into the file yet. A write
would lose it, so the server waits, then refuses, `force` or not (D-44).

**Fix (resolves it):** call again once the editor has saved, or once
the person closes the file.

**Workaround:** work in a Google Doc copy with `create_document`
`from_file`, then write it back with `write_markdown_file` (D-47).

## `[conflict] the document changed twice while planning this edit; re-read and try again`

**Cause:** someone else edited the document between the read and the
write. The server re-planned the write once against the new revision,
and the document changed again (D-30).

**Fix (resolves it):** read the passage again and send the edit again.

## `[ambiguous_outcome] the write may have been applied; read the … before trying again`

**Cause:** Google failed or timed out on a write, so the server cannot
know whether it landed. Only a 429 is repeated (D-61).

**Fix (resolves it):** read the document, or `list_comments`, and see
whether the change is there before sending it again.

**Impact:** repeating an insert or a comment that landed makes it twice.

## `[rate_limited] Google's rate limit was hit; wait a moment and retry`

**Cause:** the account or the Cloud project spent its quota for the
moment, and the server's retries were refused too (D-61). Another
program on the same account is the usual cause.

**Fix:** wait a minute and retry. A refusal that says the project's
**daily** quota is spent cannot succeed until the quota resets.

## `config: invalid: …`

The server and its subcommands refuse to start.

**Cause:** a setting has a value the server does not take, such as a
profile name it refuses, a relative `GDOCS_EXPORT_DIR` or a timeout out
of range. The message names the setting and what it takes (D-69). A
relative `GDOCS_CONFIG_DIR` stops it the same way, with
`userconfig: GDOCS_CONFIG_DIR … must be an absolute path`.

**Fix (resolves it):** correct the value in the client's entry, using
[configuration.md](configuration.md), and restart the client.

## `config: invalid: GDOCS_REQUIRE_PROMPT was removed: …`

The server and its subcommands refuse to start. The flag
`--require-prompt` stops them too, with
`flag provided but not defined: -require-prompt`.

**Cause:** `GDOCS_REQUIRE_PROMPT` is set to anything but `false`, or the
flag is in the client's arguments. The server asks the person nothing,
and the client approves each call (D-52, D-53). Ignoring the setting
would quietly allow what it was set to stop.

**Fix (resolves it):** unset it, and take the flag out of the arguments.
For a client that runs with nobody watching, leave
`GDOCS_ENABLE_DESTRUCTIVE` off, and list the writes you want to approve
under the client's ask rules, as the README shows.

## `warning: keyring unavailable (…); refresh token saved in plaintext at … (mode 0600)`

Each later use warns `using plaintext token file …` while the keyring
stays out of reach.

**Cause:** the OS keyring could not be reached at login, such as on a
headless Linux machine with no Secret Service running (D-67).

**Fix (resolves it):** start a keyring, or log in from a desktop
session, and run `login` again. The token moves to the keyring and the
file is deleted.

**Impact:** the file is a live credential. On Windows it has no POSIX
permission bits and takes the ACL of your profile directory. Keep it
out of backups that leave the machine, and remove it with `logout`, so
the token is revoked.

## A tool you expect is missing

**Cause:** registration follows the settings the server started with,
not the call. `delete_comment` and `delete_tab` register only with
`GDOCS_ENABLE_DESTRUCTIVE=true` (D-54), and `GDOCS_READ_ONLY=true`
registers the reads alone (D-55).

**Fix (resolves it):** set the variable in the client's entry for this
server, not in your shell, and restart the client. After turning
read-only off, run `login` again for the write scopes.

## Tasks

### Rotating the token

```bash
google-docs-mcp login
```

The new token replaces the old one in the same store.

### Revoking access

```bash
google-docs-mcp logout
```

This revokes the token at Google and deletes the local copy. If Google
cannot be reached, `logout` warns and deletes the local copy anyway;
remove the app at
[Google account permissions](https://myaccount.google.com/permissions)
to finish. `logout` cannot revoke or remove `GDOCS_REFRESH_TOKEN`, and
says so when it is set.

### Suspected exposure

If a refresh token may have leaked, such as in a copied home directory,
a shared machine or a `token.json` in a backup:

1. Run `google-docs-mcp logout` on any machine that still has it.
2. If you cannot, remove the app's access at
   [Google account permissions](https://myaccount.google.com/permissions).
3. Run `google-docs-mcp login` on the machines that should keep
   working.

To replace the OAuth client as well, create a new Desktop app client,
run `login --client-secret <new path>`, and delete the old client.

### Moving to another machine

Do not copy the token. Install the binary, copy the OAuth client JSON,
and run `login`. A keyring entry does not move, and a copied
`token.json` is a second live copy of a credential.

### Two accounts on one machine

Profiles keep them apart (D-70):

```bash
GDOCS_PROFILE=work google-docs-mcp login --client-secret ./work_client.json
GDOCS_PROFILE=personal google-docs-mcp login --client-secret ./personal_client.json
```

Each profile has its own client secret, token and account. Point each
client entry at its profile with the same variable.

### Reporting a problem

Open an [issue](https://github.com/mmedum/google-docs-mcp/issues) with:

- the output of `google-docs-mcp doctor` and `google-docs-mcp --version`;
- how you installed it and which client runs it;
- a debug log, from `GDOCS_LOG_LEVEL=debug`, which carries no document
  id, revision, title, text or query at any level (D-04).

`doctor` masks the account to its domain and replaces the document you
named with `<ref>` (D-05). Never paste a document id or URL, document
content, a client JSON, a token or a tool result; describe the
document's shape instead. A suspected vulnerability goes through
[`SECURITY.md`](../SECURITY.md), privately.
