#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Fail when internal/localconfig/lcdocs.go or lcdefaults.go differ from what
# scripts/gen-lc.py generates from the pinned carbonio-mailbox tag.
# Usage: scripts/gen-lc-check.sh [MAILBOX_REPO]
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
repo="${1:-$HOME/zextras/carbonio-mailbox}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

python3 "$root/scripts/gen-lc.py" --mailbox-repo "$repo" --outdir "$tmp"

status=0
for f in lcdocs.go lcdefaults.go; do
	diff -u "$root/internal/localconfig/$f" "$tmp/$f" || status=1
done

if [ "$status" -ne 0 ]; then
	echo "generated localconfig tables are stale: run 'make generate'" >&2
fi
exit "$status"
