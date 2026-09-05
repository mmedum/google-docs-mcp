#!/usr/bin/env bash
# Fails when the documentation drifts from the code:
#  - README's tool table must list exactly the registered tools;
#  - docs/configuration.md must mention every GDOCS_* variable config.go defines;
#  - CHANGELOG.md must have content under [Unreleased] when source changed since the last tag;
#  - docs/architecture.md must not claim "no code yet" once code exists.
set -euo pipefail
BIN=${1:-./google-docs-mcp}
SCHEMAS=$(mktemp)
trap 'rm -f "$SCHEMAS"' EXIT
fail=0

"$BIN" --dump-schemas > "$SCHEMAS"
tools=$(go run ./internal/devcheck tool-names "$SCHEMAS")
readme_tools=$(grep -oE '^\| `[a-z_]+` \|' README.md | tr -d '`| ' | sort)
if [ "$(echo "$tools" | sort)" != "$readme_tools" ]; then
  echo "README tool table differs from registered tools:"
  diff <(echo "$tools" | sort) <(echo "$readme_tools") || true
  fail=1
fi

for var in $(grep -oE '"[A-Z_]+"' internal/config/config.go | grep -oE '[A-Z][A-Z_]+' | sort -u); do
  case "$var" in PT|PROFILE|LOG_LEVEL|LOG_FORMAT|PREVIEW|DEFAULT_WRITE_MODE|READ_ONLY|ENABLE_DESTRUCTIVE|EXPORT_DIR|HTTP_TIMEOUT|CLIENT_SECRET) ;; *) continue ;; esac
  [ "$var" = PT ] && continue
  if ! grep -q "GDOCS_$var" docs/configuration.md; then
    echo "docs/configuration.md does not document GDOCS_$var"
    fail=1
  fi
done

if grep -qi "no code yet" docs/architecture.md; then
  echo "docs/architecture.md still says 'no code yet'"
  fail=1
fi

last=$(git describe --tags --abbrev=0 2>/dev/null || true)
range=${last:+$last..HEAD}
if git diff --quiet ${range:-HEAD~1} -- '*.go' 2>/dev/null; then
  :
else
  unreleased=$(awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -c '^- ' || true)
  # A release commit moves the entries from [Unreleased] under the version
  # about to be tagged, so [Unreleased] is legitimately empty. That
  # section counts as documentation until its tag exists — without this,
  # a release pull request can never go green, because CI runs before the
  # tag it is preparing.
  pending_version=$(grep -m1 -oE '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | tr -d '#[] ' || true)
  pending=0
  if [ -n "$pending_version" ] && ! git rev-parse -q --verify "refs/tags/v$pending_version" >/dev/null; then
    pending=$(awk -v v="## [$pending_version]" 'index($0, v)==1{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -c '^- ' || true)
  fi
  if [ "$unreleased" -eq 0 ] && [ "$pending" -eq 0 ]; then
    echo "source changed since ${last:-the previous commit} but CHANGELOG.md documents nothing new"
    echo "put the entries under [Unreleased], or under the version heading this release is about to tag"
    fail=1
  fi
fi

[ $fail = 0 ] && echo "staleness check ok"
exit $fail
