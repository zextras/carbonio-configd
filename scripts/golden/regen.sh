#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Regenerate the golden expected outputs from the REAL legacy zmconfigd
# (Jython) and ProxyConfGen, running in a throwaway podman container.
#
#   scripts/golden/regen.sh [case ...]     (default: every case)
#
# The expected files are never produced by the Go code. See test/golden/README.md.
set -euo pipefail

IMAGE=${GOLDEN_IMAGE:-docker.io/carbonio/devel:202610290800-ce-single}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GOLDEN=$ROOT/test/golden
NAME=golden-legacy-$$
OUT=$(mktemp -d)

cleanup() {
	podman rm -f "$NAME" >/dev/null 2>&1 || true
	rm -rf "$OUT"
}
trap cleanup EXIT

if [ $# -gt 0 ]; then
	cases=("$@")
else
	mapfile -t cases < <(cd "$GOLDEN/cases" && printf '%s\n' */ | tr -d /)
fi

echo ">> starting $IMAGE as $NAME"
podman run -d --name "$NAME" --user root --entrypoint /bin/sleep "$IMAGE" 7200 >/dev/null
podman exec "$NAME" mkdir -p /golden/in /golden/out
podman cp "$ROOT/scripts/golden/container" "$NAME:/golden/bin"
podman cp "$GOLDEN/base" "$NAME:/golden/in/base"
podman cp "$GOLDEN/cases" "$NAME:/golden/in/cases"
podman exec "$NAME" bash -c 'chmod -R a+rX /golden/in /golden/bin && chmod 0755 /golden/bin/* && chown zextras:zextras /golden/out'
podman exec --user root "$NAME" /golden/bin/bootstrap.sh
podman exec --user zextras "$NAME" /golden/bin/ldap-init.sh

for c in "${cases[@]}"; do
	echo ">> legacy run: $c"
	podman exec --user zextras "$NAME" /golden/bin/run-case.sh "$c"
done

podman cp "$NAME:/golden/out/." "$OUT"
for c in "${cases[@]}"; do
	for cyc in "$OUT/$c"/cycle-*; do
		n=$(basename "$cyc")
		dest=$GOLDEN/cases/$c/$n/expected
		rm -rf "$dest"
		mv "$cyc" "$dest"
	done
done
echo ">> done: expected outputs updated for: ${cases[*]}"
