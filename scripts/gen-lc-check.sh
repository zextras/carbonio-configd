#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Fail when internal/localconfig/lcdocs.go or lcdefaults.go differ from what
# scripts/gen-lc.py generates from the pinned carbonio-mailbox tag.
# Usage: scripts/gen-lc-check.sh [MAILBOX_REPO]
#
# When MAILBOX_REPO (default ~/zextras/carbonio-mailbox) is not a directory, as in
# CI, the pinned tag is shallow-cloned from MAILBOX_URL (the public GitHub repo
# by default) into a temp dir. That needs network, git and python3 only.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
repo="${1:-$HOME/zextras/carbonio-mailbox}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ ! -d "$repo" ]; then
	tag="$(cat "$root/scripts/lc-mailbox-tag")"
	url="${MAILBOX_URL:-https://github.com/zextras/carbonio-mailbox.git}"
	repo="$tmp/carbonio-mailbox"
	echo "gen-lc-check: cloning $url at $tag" >&2
	git clone --quiet -c advice.detachedHead=false --depth 1 --branch "$tag" "$url" "$repo"
fi

python3 "$root/scripts/gen-lc.py" --mailbox-repo "$repo" --outdir "$tmp"

status=0
for f in lcdocs.go lcdefaults.go; do
	diff -u "$root/internal/localconfig/$f" "$tmp/$f" || status=1
done

if [ "$status" -ne 0 ]; then
	echo "generated localconfig tables are stale: run 'make generate'" >&2
fi
exit "$status"
