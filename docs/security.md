# Security model

The threat model and the invariants the code holds.
[`SECURITY.md`](../SECURITY.md) says how to report a vulnerability.

It applies to one binary, a subprocess of your MCP client on your
machine, with your own OAuth client and token, against Google's APIs
only (D-01, D-11). It has no listener except while `login`
waits, no telemetry and no update check.

## Trust boundaries

| Boundary | Crosses | Assumption |
|---|---|---|
| MCP client ↔ this server | An OS pipe | Both run as the same OS user, unauthenticated. Stdout carries only protocol frames (D-03). |
| This server ↔ Google | TLS | API requests carry the token only to `googleapis.com` and its subdomains, `*.googleusercontent.com` and `docs.google.com` (D-11). Sign-in and the token refresh use the endpoints the OAuth client file names. |
| This server ↔ disk | The file system | The config directory holds the profile and, without a keyring, the token file (D-67). Exports go only under `GDOCS_EXPORT_DIR` (D-24). |
| Browser ↔ the login listener | The loopback interface, on a random port | Alive only while `login` waits. PKCE and `state` are checked on the callback (D-66). |
| Document authors ↔ the model | Every text, comment and suggestion a tool returns | Written by other people, sometimes to steer an assistant. |

## What is worth stealing

- **The refresh token.** The `documents` and `drive` scopes read and
  change every document the account can reach (D-68). It lives in the
  OS keyring where there is one, and in an owner-only file where there
  is not (D-67).
- **Document content.** It passes through tool results to your client,
  and to disk only under `GDOCS_EXPORT_DIR`.
- **The documents themselves.** A direct edit changes what other people
  wrote, and a deleted tab or comment does not come back through this
  server.

## Adversaries

**A collaborator writing to steer your assistant.** The server cannot
stop a model being persuaded by a document's text or comments, so it
limits what persuasion reaches. The delete tools are absent unless the
deployer turns them on (D-54). A direct edit never deletes anchored
content unforced (D-28), and the write mode is the person's (D-29).
`review_suggestion` names each suggestion it reviews, so none proposed
after the list was read is reviewed unseen (D-40). No tool changes the
server's settings (D-59).

**A malicious MCP client, or a persuaded model driving an honest one.**
It can call any registered tool as you, so registration is the control.
The repeated target of a delete and `force` are witnesses the model
writes, never your approval (D-56).

**Another person editing the same document.** An edit between the read
and the write is caught by the revision guard (D-30), and work unsaved
in an open editor is never written past (D-44).

**A co-resident process.** It cannot finish a login without the PKCE
verifier, and the token file is readable by your account alone.

## Invariants the code holds

A test or a gate holds each of these. A way to break one is a
vulnerability.

- **Stdout carries only JSON-RPC.** forbidigo, the smoke test and
  `TestNothingPrintsOutsideTheBoundary` hold it (D-03).
- **API credentials go only to Google's hosts, over HTTPS.**
  `TestDefaultAllowlistSendsCredentialsOnlyOverHTTPSToGoogle` holds it,
  a revision export's URL from a response body included (D-11).
- **Logs carry no document id, revision, title, text or query, at any
  level.** `TestDebugLogsCarryNoDocumentData` and
  `TestTransportErrorsDoNotCarryTheURL` hold it (D-04).
- **`doctor`, `status` and the startup line mask the account to its
  domain and drop a client id.** `TestDoctorOutputHidesWhatItKnows`,
  `TestMaskClientID` and `TestTheCollectorMasksTheAccount` hold it
  (D-05).
- **A direct edit never deletes a comment anchor, a suggestion, an
  object or a footnote reference unless forced, and no write mode is
  substituted for another.** `TestGuardBlocksDirect`,
  `TestReplaceAllAndMergeAreGuarded` and
  `TestSuggestModeRefusesUnsupportedRequests` hold it (D-28, D-29).
- **Every write is guarded by the revision it was planned on, and one
  that may have landed is never repeated.** `TestDryRunAndConflictRetry`,
  `TestWriteRetriesOnlyOn429` and `TestWriteNetworkFailureIsAmbiguous`
  hold it, and the integration test `TestRawStaleRevision` checks
  Google's answer (D-30, D-61).
- **Unsaved editor work in a Markdown file is never written past, `force`
  included.** `TestMarkdownFileWaitsForTheEditor` and
  `TestMarkdownFileKeepsASaveThatSlipsIn` hold it (D-44).
- **The delete tools are absent without `GDOCS_ENABLE_DESTRUCTIVE`, and
  each call repeats its target.** `TestListToolsAndSchemas`,
  `TestDeletesNeedTheTargetRepeated` and
  `TestDeleteCommentHoldsItsTextToTheWitness` hold it (D-54, D-56).
- **Read-only mode registers the reads alone and asks for read-only
  scopes.** `TestListToolsAndSchemas` and `TestScopesAgree` hold it
  (D-55).
- **The server asks the person nothing, the annotations are exact, and
  no tool takes or writes its settings.**
  `TestOnlyToolsThatAddSayTheyAreNotDestructive`,
  `TestNoToolTakesAServerSetting` and
  `TestNoToolCanWriteTheServersSettings` hold it, with forbidigo
  (D-52, D-57, D-59).
- **Exports stay in `GDOCS_EXPORT_DIR`, never over a file or through a
  link.** `TestAnExportNeverOverwrites` and
  `TestAnExportNeverWritesThroughALink` hold it (D-24).
- **A login checks `state` and uses PKCE.**
  `TestLoginRejectsBadStateAndDenial` holds it (D-66).
- **The token file is owner-only, with a warning, and the environment's
  token overrides both stores.** `TestFileFallbackWhenKeyringBroken` and
  `TestEnvWins` hold it (D-67).
- **Every HTTP call has a deadline, the token refresh included, and
  every read a budget.** `TestTokenRefreshIsBounded` and
  `TestBudgetAndContinue` hold it (D-63, D-18). A regex runs on RE2, in
  linear time (D-34).
- **Nothing in the repository identifies a real document, account or
  organization.** `make leaks` and gitleaks hold it (D-06). The live
  driver and the evals print only through the redactor, which
  `make transcript` holds (D-07).

## Not a vulnerability

Do not report these as vulnerabilities. Open an ordinary issue if one
surprises you.

- **The client did not prompt.** Annotations are hints, and a client
  can run a tool with nobody approving. A delete tool registered while
  its switch is off is a vulnerability.
- **What runs as you can act as you**, the token included.
- **The scope reaches every document the account can.** `drive.file`
  reaches only files the app created or the person opened through
  Google's Picker, which a stdio server cannot show.
- **A tool result is not masked.** It is your data, returned to you.
- **A model following instructions in a document.** That is the client's
  concern. Content that makes the server itself break an invariant is a
  vulnerability.
- **Someone else's code.** A flaw in Google's APIs goes to Google, and
  one in a dependency to that project.

## Dependency vulnerabilities

`govulncheck` runs in `make check` and in CI, and fails when this
server's code can reach a vulnerable symbol. A CVE in a dependency is a
vulnerability here only then. It is fixed in a release whose CHANGELOG
entry names the CVE or GHSA; otherwise the dependency is updated in the
normal course.

## What you are responsible for

- **Your own OAuth client.** Use the client file the Cloud console gave
  you, since sign-in sends the code and the token to the endpoints it
  names. [`troubleshooting.md`](troubleshooting.md#tasks) says how to
  rotate the token, revoke access and handle a suspected exposure.
- **The switches you turn on.** `GDOCS_ENABLE_DESTRUCTIVE` registers
  deletes, so leave it off for a client that runs with nobody watching.
  `GDOCS_EXPORT_DIR` is a directory a model can write into. Use
  read-only mode, logged in again, where reading is enough.
- **Your client's approvals.** Give the deleting and reviewing tools an
  `ask` rule, as the README shows.
- **Where the token lives.** Keep a keyring available, and
  `GDOCS_REFRESH_TOKEN` out of shared shells and logs. On Windows the
  token file has no POSIX permission bits and takes the ACL of your
  profile directory, so prefer Credential Manager there.
- **What you paste.** `status`, `doctor` and a debug log are built to be
  safe (D-04, D-05). A token, `token.json`, a client JSON and a tool
  result are not.
