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
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_document","arguments":{"document":"1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"}}}'
  sleep 1
} | timeout 20 "$BIN" > "$TMP/out.jsonl"
grep -q '"id":1' "$TMP/out.jsonl" || { echo "no initialize response"; cat "$TMP/out.jsonl"; exit 1; }
grep -q '"name":"read_document"' "$TMP/out.jsonl" || { echo "read_document missing from tools/list"; exit 1; }
grep '"id":3' "$TMP/out.jsonl" | grep -q '"isError":true' || { echo "tool call without credentials should be a tool error"; cat "$TMP/out.jsonl"; exit 1; }
grep '"id":3' "$TMP/out.jsonl" | grep -q '\[auth\]' || { echo "tool error should carry the [auth] class"; exit 1; }
echo "stdio smoke ok"
