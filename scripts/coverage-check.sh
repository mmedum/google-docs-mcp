#!/usr/bin/env bash
# Enforces a statement-coverage floor per core package. The profile comes
# from -coverpkg=./internal/... so cross-package coverage counts; blocks
# then appear once per test binary and are de-duplicated here.
set -euo pipefail
PROFILE=${1:-cov.out}
MIN=${2:-80}
MODULE=github.com/mmedum/google-docs-mcp
fail=0
# The list is derived, not typed: a package added under internal/ is
# under the floor from its first commit. An exemption has to be written
# down here, which makes it a decision someone can see in review.
#
#   devcheck  a build-gate helper with no runtime path; the gates that
#             use it are what exercise it
#   doctest   fixtures for other packages' tests
#   gdocs     wire types: struct tags, no logic
#   leakcheck a test-only package: it scans the repository, it has no
#             statements of its own to cover
exempt="internal/devcheck internal/doc/doctest internal/gdocs internal/leakcheck"
packages=$(go list ./internal/... | sed "s|^$MODULE/||")
for pkg in $packages; do
  case " $exempt " in *" $pkg "*) continue ;; esac
  pct=$(awk -v p="$MODULE/$pkg/" 'NR>1 && index($1, p)==1 {
      if (!($1 in stmts)) stmts[$1]=$2;
      if ($3>0) hit[$1]=1 }
    END { for (k in stmts) { total+=stmts[k]; if (k in hit) cov+=stmts[k] }
          if (total) printf "%.1f", 100*cov/total; else print "0" }' "$PROFILE")
  printf '%-22s %6s%%\n' "$pkg" "$pct"
  if awk -v a="$pct" -v b="$MIN" 'BEGIN {exit !(a < b)}'; then fail=1; fi
done
[ $fail = 0 ] || { echo "coverage below ${MIN}% in at least one core package"; exit 1; }
