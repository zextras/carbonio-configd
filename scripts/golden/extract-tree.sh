#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Refresh test/golden/tree (the legacy install tree the Go run reads) from the
# image the expected outputs are generated with.
#
#   scripts/golden/extract-tree.sh
#
# Run it when GOLDEN_IMAGE changes, then run scripts/golden/regen.sh.
set -euo pipefail

IMAGE=${GOLDEN_IMAGE:-docker.io/carbonio/devel:202610290800-ce-single}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
TREE=$ROOT/test/golden/tree
NAME=golden-extract-$$
trap 'podman rm -f "$NAME" >/dev/null 2>&1 || true' EXIT

podman run -d --name "$NAME" --user root --entrypoint /bin/sleep "$IMAGE" 600 >/dev/null
rm -rf "$TREE"
mkdir -p "$TREE/conf" "$TREE/common/conf" "$TREE/seed" "$TREE/mailbox"

cp_from() { podman cp "$NAME:/opt/zextras/$1" "$TREE/$2"; }
cp_from conf/zmconfigd.cf conf/zmconfigd.cf
cp_from conf/zmconfigd conf/zmconfigd
mkdir -p "$TREE/conf/nginx" "$TREE/conf/attrs"
cp_from conf/nginx/templates conf/nginx/templates
cp_from conf/attrs/attrs.xml conf/attrs/attrs.xml
cp_from conf/sasl2 conf/sasl2
for f in $(podman exec "$NAME" sh -c 'cd /opt/zextras/conf && ls *.in | grep -v zmssl'); do
	cp_from "conf/$f" "conf/$f"
done
for f in master.cf.in tag_as_foreign.re.in tag_as_originating.re.in; do
	cp_from "common/conf/$f" "common/conf/$f"
done
# The directory seed the product package loads into an empty directory.
SEED=common/etc/openldap/zimbra
cp_from $SEED/carbonio.ldif seed/carbonio.ldif
for f in zimbra_globalconfig zimbra_defaultcos zimbra_defaultexternalcos mimehandlers; do
	cp_from "$SEED/$f.ldif" "seed/$f.ldif"
done
# Public package-default password hashes are not vendored.
grep -v '^userPassword: {SSHA}' "$TREE/seed/carbonio.ldif" >"$TREE/seed/carbonio.ldif.new"
mv "$TREE/seed/carbonio.ldif.new" "$TREE/seed/carbonio.ldif"
podman exec "$NAME" python3 -c 'import sys, zipfile
sys.stdout.write(zipfile.ZipFile("/opt/zextras/mailbox/jars/mailbox.jar").read("com/zimbra/cs/util/buildInfo.properties").decode())' \
	>"$TREE/mailbox/buildInfo.properties"
# Installed files carry no SPDX header; neither does the vendored copy.
echo ">> tree refreshed from $IMAGE"
