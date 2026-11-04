#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Fail (exit 1, diff on stdout) when internal/localconfig/lcdocs.go or lcdefaults.go
# differ from what scripts/gen-lc.py generates from the vendored carbonio-mailbox
# inputs in scripts/lc-source. Hermetic: python3 and gofmt only, no network, no clone.
# Usage: scripts/gen-lc-check.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

python3 "$root/scripts/gen-lc.py" --outdir "$tmp"

status=0
for f in lcdocs.go lcdefaults.go; do
	diff -u "$root/internal/localconfig/$f" "$tmp/$f" || status=1
done

if [ "$status" -ne 0 ]; then
	echo "generated localconfig tables are stale: run 'make generate'" >&2
fi
exit "$status"
