#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Compare internal/bootstrap/setup/testdata/attrs-allowlist.tsv with the
# attribute definitions of Carbonio's attrs.xml. The snapshot holds, for each
# attribute bootstrap writes to LDAP itself (provAttrList in
# internal/bootstrap/setup/prov_attrs.go), the fields that decide whether that
# is safe: its callback, type, limits, and whether it is immutable or
# deprecated. A difference means the attribute changed upstream: read the
# callback again before updating the snapshot and the allowlist.
#
# attrs.xml is shipped by carbonio-directory-server
# (src/main/resources/conf/attrs/attrs.xml, installed as
# /opt/zextras/conf/attrs/attrs.xml by carbonio-common-appserver-conf).
#
# Usage: scripts/check-attrs-drift.sh [--update] SOURCE
#   SOURCE  a checkout of carbonio-directory-server (or any directory that
#           holds an attrs.xml / zimbra-attrs.xml), the path of an attrs.xml,
#           or the http(s) URL of one (raw file, e.g. the raw.githubusercontent.com
#           URL of attrs.xml).
#   --update  rewrite the snapshot from SOURCE instead of comparing; a row with
#           only a name is filled in, so a new attribute is added by adding its
#           name to the snapshot.
#
# Exit 0 when the snapshot matches, 1 (diff on stdout) when it does not, 2 on
# a usage or input error. Not run by CI. Needs python3 (and curl for a URL).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
snapshot="$root/internal/bootstrap/setup/testdata/attrs-allowlist.tsv"

update=0
if [ "${1:-}" = "--update" ]; then
	update=1
	shift
fi

if [ $# -ne 1 ]; then
	echo "usage: $0 [--update] SOURCE (checkout path, attrs.xml path or URL)" >&2
	exit 2
fi

src="$1"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

case "$src" in
http://* | https://*)
	curl -fsSL "$src" -o "$tmp/attrs.xml"
	xml="$tmp/attrs.xml"
	;;
*)
	if [ -d "$src" ]; then
		xml=""
		for rel in src/main/resources/conf/attrs/attrs.xml store/conf/attrs/zimbra-attrs.xml; do
			if [ -f "$src/$rel" ]; then
				xml="$src/$rel"
				break
			fi
		done
		if [ -z "$xml" ]; then
			xml="$(find "$src" \( -name attrs.xml -o -name zimbra-attrs.xml \) -not -path '*/target/*' -not -path '*/node_modules/*' | head -n 1)"
		fi
		if [ -z "$xml" ]; then
			echo "no attrs.xml under $src" >&2
			exit 2
		fi
	elif [ -f "$src" ]; then
		xml="$src"
	else
		echo "$src is neither a directory, a file nor a URL" >&2
		exit 2
	fi
	;;
esac

python3 - "$xml" "$snapshot" "$tmp/generated.tsv" <<'PY'
import sys
import xml.etree.ElementTree as ET

xml_path, snapshot_path, out_path = sys.argv[1:4]

HEADER = ["name", "type", "callback", "min", "max", "value", "immutable", "deprecated"]

with open(snapshot_path, encoding="utf-8") as f:
    lines = [l.rstrip("\n") for l in f if l.strip() and not l.startswith("#")]
names = [l.split("\t")[0] for l in lines[1:]]

attrs = {}
for el in ET.parse(xml_path).getroot().iter("attr"):
    attrs[el.get("name", "").lower()] = el

rows = ["\t".join(HEADER)]
missing = []
for name in names:
    el = attrs.get(name.lower())
    if el is None:
        missing.append(name)
        continue
    rows.append(
        "\t".join(
            [
                el.get("name"),
                el.get("type", ""),
                el.get("callback", ""),
                el.get("min", ""),
                el.get("max", ""),
                el.get("value", ""),
                el.get("immutable", ""),
                el.get("deprecatedSince", ""),
            ]
        )
    )

if missing:
    print("not in attrs.xml: " + ", ".join(missing), file=sys.stderr)
    sys.exit(1)

with open(out_path, "w", encoding="utf-8") as f:
    f.write("\n".join(rows) + "\n")
PY

if [ "$update" -eq 1 ]; then
	cp "$tmp/generated.tsv" "$snapshot"
	echo "updated $snapshot"
	exit 0
fi

status=0
diff -u "$snapshot" "$tmp/generated.tsv" || status=1
if [ "$status" -ne 0 ]; then
	echo "the attribute definitions changed: review the callbacks, then run '$0 --update $src'" >&2
fi
exit "$status"
