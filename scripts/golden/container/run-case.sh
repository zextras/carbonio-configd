#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Run one golden case against the real legacy zmconfigd (as zextras).
#   run-case.sh <case>
# case.txt: "base: <name>" (inputs shared by cases, test/golden/base/<name>)
# and "mode: forced|daemon". In forced mode every cycle-N/sections lists the
# sections handed to "zmconfigd <sections>"; in daemon mode the cycles are
# consecutive iterations of one daemon process.
set -euo pipefail
. /golden/bin/lib.sh

case=$1
cdir=/golden/in/cases/$case
mode=$(sed -n 's/^mode: *//p' "$cdir/case.txt")
rm -rf "/golden/out/$case"
mkdir -p "/golden/out/$case"

/golden/bin/ldap-reset.sh
# Every case starts from the pristine templates.
restore_tree() { # <installed dir> <pristine copy>
	# Files the previous case generated are removed; the packaged files that
	# the run may have rewritten are copied back (root-owned packaged files
	# are never touched by zmconfigd and stay as they are).
	(cd "$1" && find . -type f | sort) >"$STATE/now.txt"
	(cd "$2" && find . -type f | sort) >"$STATE/was.txt"
	(cd "$1" && comm -23 "$STATE/now.txt" "$STATE/was.txt" | xargs -r rm -f)
	cp -a "$2/." "$1/" 2>/dev/null || true
}
restore_tree "$ZM/conf" "$STATE/pristine/conf"
restore_tree "$ZM/common/conf" "$STATE/pristine/common-conf"
restore_tree "$ZM/data/spamassassin/localrules" "$STATE/pristine/localrules"

cycles=$(find "$cdir" -maxdepth 1 -type d -name 'cycle-*' | wc -l)
cd "$ZM"

case $mode in
forced)
	for n in $(seq "$cycles"); do
		/golden/bin/cycle.sh prepare "$case" "$n"
		# shellcheck disable=SC2046
		"$ZM/libexec/zmconfigd" $(cat "$cdir/cycle-$n/sections")
		/golden/bin/cycle.sh collect "$case" "$n"
	done
	;;
daemon)
	"$ZM/bin/zmpython" /golden/bin/legacy_cycles.py "$case" "$cycles"
	;;
*) echo "unknown mode $mode" >&2; exit 1 ;;
esac
