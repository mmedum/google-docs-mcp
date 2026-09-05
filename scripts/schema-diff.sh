#!/usr/bin/env bash
# Compares the tool schemas of the current build with the last tag's. A
# removed tool, a removed field, or a new required field is breaking.
set -euo pipefail
BIN=${1:-./google-docs-mcp}
LAST=$(git describe --tags --abbrev=0 2>/dev/null || true)
"$BIN" --dump-schemas > schemas.json
if [ -z "$LAST" ]; then
  echo "no previous tag; schemas.json written"
  exit 0
fi
TMP=$(mktemp -d)
trap 'rm -r -f "$TMP"' EXIT
git worktree add -q "$TMP/src" "$LAST"
(cd "$TMP/src" && go build -o "$TMP/old" ./cmd/google-docs-mcp)
git worktree remove -f "$TMP/src"
"$TMP/old" --dump-schemas > "$TMP/old.json"
go run ./internal/devcheck schema-diff "$TMP/old.json" schemas.json
