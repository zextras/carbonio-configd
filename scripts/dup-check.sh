#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Go duplication ratchet (local only, not wired into CI). Runs dupl (pinned in
# tools/go.mod) with -t 100 tokens over non-test, non-generated Go and compares
# the clone group count and the number of duplicated lines (union of the
# reported ranges) with go_groups and go_lines in scripts/dup-baseline.json.
# It fails when either increases. golangci-lint's dupl linter (default 150
# tokens) catches only larger clones; this check is stricter.
#
#   scripts/dup-check.sh           compare with the baseline
#   scripts/dup-check.sh --update  rewrite the baseline
set -euo pipefail
cd "$(dirname "$0")/.."

baseline=scripts/dup-baseline.json
threshold=${DUPL_THRESHOLD:-100}

report=$(git ls-files '*.go' |
	grep -v -e '_test\.go$' -e '^tools/' |
	{ xargs grep -L 'Code generated' || true; } |
	go tool -modfile=tools/go.mod dupl -files -t "$threshold")

groups=$(sed -n 's/^Found total \([0-9]*\) clone groups\./\1/p' <<<"$report")
groups=${groups:-0}
lines=$({ grep -E '^  .+:[0-9]+,[0-9]+$' <<<"$report" || true; } |
	sed -E 's/^  (.+):([0-9]+),([0-9]+)$/\1 \2 \3/' |
	sort -k1,1 -k2,2n |
	awk '{ if ($1 != f || $2 > e) { n += $3 - $2 + 1; f = $1; e = $3 } else if ($3 > e) { n += $3 - e; e = $3 } } END { print n + 0 }')

echo "dupl -t ${threshold}: ${groups} clone groups, ${lines} duplicated lines"

if [[ ${1:-} == --update ]]; then
	jq -n --argjson g "$groups" --argjson l "$lines" '{go_groups: $g, go_lines: $l}' >"$baseline"
	echo "baseline updated: $baseline"
	exit 0
fi

base_groups=$(jq .go_groups "$baseline")
base_lines=$(jq .go_lines "$baseline")
echo "baseline:    ${base_groups} clone groups, ${base_lines} duplicated lines"

if ((groups > base_groups || lines > base_lines)); then
	echo "FAIL: Go duplication increased. Remove the duplicate code; if the increase is deliberate, run 'make dup-baseline'." >&2
	printf '%s\n' "$report" >&2
	exit 1
fi

if ((groups < base_groups || lines < base_lines)); then
	echo "Duplication went down: run 'make dup-baseline' to lock it in."
fi
