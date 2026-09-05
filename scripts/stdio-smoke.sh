#!/usr/bin/env bash
# Drives the binary over stdio without credentials: initialize, list tools,
# and call a tool (which must answer with an [auth] tool error, not crash).
set -euo pipefail
BIN=${1:-./google-docs-mcp}
TMP=$(mktemp -d)
trap 'rm -r -f "$TMP"' EXIT
export GDOCS_CONFIG_DIR="$TMP/cfg" GDOCS_LOG_LEVEL=error
{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  echo '{"jsonrpc":"2.0","id":4,"method":"resources/templates/list"}'
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_document","arguments":{"document":"1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"}}}'
  sleep 1
} | timeout 20 "$BIN" > "$TMP/out.jsonl"

# Closing stdin the moment the last message is written is how a client
# actually goes away, and it is a different path: the server is still
# working when the pipe ends. It must still exit 0 — a non-zero exit here
# is reported as a crash by every host. The run above sleeps first, which
# hides this entirely.
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  | timeout 20 "$BIN" > /dev/null 2>"$TMP/abrupt.err" \
  || { echo "abrupt client disconnect should exit 0, got $?:"; cat "$TMP/abrupt.err"; exit 1; }
grep -q '"id":1' "$TMP/out.jsonl" || { echo "no initialize response"; cat "$TMP/out.jsonl"; exit 1; }
grep -q '"name":"read_document"' "$TMP/out.jsonl" || { echo "read_document missing from tools/list"; exit 1; }
grep '"id":3' "$TMP/out.jsonl" | grep -q '"isError":true' || { echo "tool call without credentials should be a tool error"; cat "$TMP/out.jsonl"; exit 1; }
grep '"id":3' "$TMP/out.jsonl" | grep -q '\[auth\]' || { echo "tool error should carry the [auth] class"; exit 1; }
grep '"id":4' "$TMP/out.jsonl" | grep -q '"uriTemplate":"gdocs://{document}"' || { echo "resource templates missing"; cat "$TMP/out.jsonl"; exit 1; }
echo "stdio smoke ok"
