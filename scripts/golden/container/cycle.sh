#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Per-cycle hooks of the legacy runner.
#   cycle.sh prepare <case> <n>   apply cycle n's inputs, reset the recorders
#   cycle.sh collect <case> <n>   save what the legacy run produced
set -euo pipefail
. /golden/bin/lib.sh

what=$1 case=$2 n=$3
IN=/golden/in
OUT=/golden/out/$case/cycle-$n
cdir=$IN/cases/$case
cyc=$cdir/cycle-$n
base=$IN/base/$(sed -n 's/^base: *//p' "$cdir/case.txt")

ldif_apply() {
	local f
	for f in "$@"; do
		ldapmodify -a -x -H "$LDAP_URL" -D cn=config -w "$LDAP_PW" -f "$f" >/dev/null
	done
}

case $what in
prepare)
	if [ "$n" = 1 ]; then
		ldif_apply "$base"/ldif/*.ldif
		cp "$base/localconfig.xml" "$ZM/conf/localconfig.xml"
	fi
	if [ -d "$cyc/ldif" ]; then ldif_apply "$cyc"/ldif/*.ldif; fi
	if [ -f "$cyc/localconfig.xml" ]; then cp "$cyc/localconfig.xml" "$ZM/conf/localconfig.xml"; fi
	# Whatever has a later mtime than this marker after the run was written by
	# the legacy code, even when its content did not change.
	touch "$STATE/marker"
	sleep 0.1
	: >"$STATE/exec.jsonl"
	;;
collect)
	mkdir -p "$OUT/files"
	cd "$ZM"
	find conf common/conf data/spamassassin/localrules -type f -newer "$STATE/marker" ! -path conf/localconfig.xml | sort >"$STATE/written.txt"
	: >"$OUT/modes.txt"
	while read -r f; do
		mkdir -p "$OUT/files/$(dirname "$f")"
		cp "$f" "$OUT/files/$f"
		printf '%s %s\n' "$f" "$(stat -c %a "$f")" >>"$OUT/modes.txt"
	done <"$STATE/written.txt"
	cp "$STATE/exec.jsonl" "$OUT/exec.jsonl"
	;;
esac
