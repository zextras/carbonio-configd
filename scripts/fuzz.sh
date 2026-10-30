#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Runs every native Go fuzz target (func FuzzXxx) for FUZZTIME each (local only,
# not wired into CI). `go test -fuzz` accepts one target per package per run,
# so targets are discovered with `go test -list` and run one at a time.
# `go test ./...` already runs each target's seed corpus and the crashers kept
# under testdata/fuzz/, so a found bug stays a regular failing test.
#
#   scripts/fuzz.sh                    all targets, FUZZTIME (default 30s) each
#   FUZZTIME=5m scripts/fuzz.sh        longer run
#   scripts/fuzz.sh ./internal/parser  only the targets of these packages
#
# Stops at the first failing target; the crasher is written by go test to
# <pkg>/testdata/fuzz/<FuzzName>/ and reproduced by plain `go test`.
set -euo pipefail
cd "$(dirname "$0")/.."

fuzztime=${FUZZTIME:-30s}
pkgs=("$@")
if [ "${#pkgs[@]}" -eq 0 ]; then
	pkgs=(./...)
fi

found=0
while read -r pkg; do
	while read -r target; do
		[[ $target == Fuzz* ]] || continue
		found=$((found + 1))
		echo "==> $pkg $target ($fuzztime)"
		go test "$pkg" -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime"
	done < <(go test "$pkg" -list '^Fuzz' 2>/dev/null)
done < <(go list "${pkgs[@]}")

if [ "$found" -eq 0 ]; then
	echo "fuzz: no Fuzz targets found" >&2
	exit 1
fi

echo "fuzz: ${found} targets ok"
