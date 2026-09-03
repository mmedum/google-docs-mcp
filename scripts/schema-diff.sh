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
python3 - "$TMP/old.json" schemas.json <<'PY'
import json, sys
old = {t["name"]: t for t in json.load(open(sys.argv[1]))["tools"]}
new = {t["name"]: t for t in json.load(open(sys.argv[2]))["tools"]}
breaking = []
for name, t in old.items():
    if name not in new:
        breaking.append(f"tool removed: {name}"); continue
    o_req = set(t["inputSchema"].get("required", [])); n_req = set(new[name]["inputSchema"].get("required", []))
    for f in n_req - o_req:
        breaking.append(f"{name}: new required field {f}")
    for f in t["inputSchema"].get("properties", {}):
        if f not in new[name]["inputSchema"].get("properties", {}):
            breaking.append(f"{name}: field removed {f}")
added = [n for n in new if n not in old]
print("added tools:", added or "none")
print("breaking changes:", breaking or "none")
sys.exit(1 if breaking else 0)
PY
