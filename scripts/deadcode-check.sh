#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Dead-code check (local only, not wired into CI): reports functions that no
# main package reaches, using golang.org/x/tools/cmd/deadcode pinned in
# tools/go.mod. There is no allowlist; the expected result is zero findings.
#
# Build tags: profiling and tracing select alternative implementations
# (foo.go vs foo_stub.go), so one tag set alone would report the unused variant.
# deadcode runs once per tag combination (always with integration) and a
# function counts as dead only when every run that compiles its file reports it.
#
# It runs without -test: with -test every test function is a root, so a
# production function called only from tests would count as live. Such
# functions are reported on purpose: test-only helpers belong in _test.go files
# or in a test support package.
#
# Test support packages are skipped: non-main packages of this module that no
# production (non-test) package imports, e.g. internal/testutil and test.
#
# Usage: scripts/deadcode-check.sh

set -euo pipefail

cd "$(dirname "$0")/.."

if [ "$#" -ne 0 ]; then
	echo "usage: $0" >&2
	exit 2
fi

export LC_ALL=C

tmp=$(mktemp -d "${TMPDIR:-/tmp}/deadcode.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

module=$(go list -m)
tagsets=("integration" "integration profiling" "integration tracing" "integration profiling tracing")

# Test support packages, as module-relative directories (one per line): every
# non-main package that no non-test package imports, under any tag set.
for tags in "${tagsets[@]}"; do
	go list -tags "$tags" -f '{{.ImportPath}} {{.Name}} {{join .Imports " "}}' ./...
done | awk -v module="$module" '
	{
		pkgs[$1] = $2
		for (i = 3; i <= NF; i++) imported[$i] = 1
	}
	END {
		for (p in pkgs) {
			if (pkgs[p] == "main" || (p in imported)) continue
			print (p == module ? "." : substr(p, length(module) + 2))
		}
	}
' | sort -u >"$tmp/support"

runs=0
for tags in "${tagsets[@]}"; do
	runs=$((runs + 1))

	# Files compiled under this tag set, module-relative.
	go list -tags "$tags" -f '{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}' ./... |
		sed "s#^$PWD/##" | sort -u >"$tmp/files.$runs"

	go tool -modfile=tools/go.mod deadcode -tags "$tags" ./... >"$tmp/raw.$runs"

	awk '
		/^[[:space:]]*$/ { next }
		match($0, /^[^:]+:[0-9]+:[0-9]+: unreachable func: /) {
			loc = substr($0, 1, RLENGTH)
			sym = substr($0, RLENGTH + 1)
			sub(/:[0-9]+:[0-9]+: unreachable func: $/, "", loc)
			print loc "  " sym
			next
		}
		{ print "deadcode-check: unrecognised deadcode output line: " $0 > "/dev/stderr"; bad = 1 }
		END { exit bad }
	' "$tmp/raw.$runs" | sort -u >"$tmp/dead.$runs"
done

# Keep a finding only if every run compiling its file reported it, and its
# package is not a test support package.
awk -v runs="$runs" -v tmp="$tmp" '
	BEGIN {
		while ((getline d < (tmp "/support")) > 0) support[d] = 1
		for (r = 1; r <= runs; r++) {
			while ((getline f < (tmp "/files." r)) > 0) compiled[r, f] = 1
			while ((getline line < (tmp "/dead." r)) > 0) {
				dead[r, line] = 1
				all[line] = 1
			}
		}
		for (line in all) {
			split(line, parts, "  ")
			file = parts[1]
			dir = file
			if (!sub(/\/[^\/]*$/, "", dir)) dir = "."
			if (dir in support) continue
			keep = 1
			for (r = 1; r <= runs; r++) {
				if (((r, file) in compiled) && !((r, line) in dead)) { keep = 0; break }
			}
			if (keep) print line
		}
	}
' | sort >"$tmp/findings"

if [ -s "$tmp/findings" ]; then
	cat <<EOF
deadcode-check: unreachable code (no main package reaches it):

$(sed 's/^/  /' "$tmp/findings")

Delete it, or move test-only code into a _test.go file or a test support package.
EOF
	exit 1
fi

echo "deadcode-check: ok"
