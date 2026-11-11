#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Run the LEGACY bootstrap (carbonio-bootstrap -> setup.pl) non-interactively
# in a fresh container of the parity image and snapshot the result.
#
#   ./run-legacy.sh [--out DIR] [--name NAME] [--keep] [--config FILE]
#
# Defaults: --out out/legacy  --name parity-legacy  (container removed unless --keep)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

OUT="${PARITY_DIR}/out/legacy"
NAME=parity-legacy
KEEP=0
while (($#)); do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --config) PARITY_CONFIG="$2"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done

t0=$(date +%s)
start_container "${NAME}"
prepare_container "${NAME}"
t1=$(date +%s)
log "container ready in $((t1 - t0))s; running carbonio-bootstrap -c /opt/product.config"

mkdir -p "${OUT}"
rc=0
# No tty: setup.pl -c is fully non-interactive.
podman exec "${NAME}" /usr/bin/carbonio-bootstrap -c /opt/product.config \
  >"${OUT}.bootstrap.log" 2>&1 || rc=$?
t2=$(date +%s)
log "legacy bootstrap exit=${rc} duration=$((t2 - t1))s (log: ${OUT}.bootstrap.log)"
[[ ${rc} -eq 0 ]] || { ((KEEP)) || true; die "legacy bootstrap failed (container ${NAME} kept)"; }

take_snapshot "${NAME}" "${OUT}"
podman cp "${NAME}:/opt/zextras/log/." "${OUT}.zextras-log/" 2>/dev/null || true
printf 'bootstrap_seconds=%s\nexit=%s\n' "$((t2 - t1))" "${rc}" >"${OUT}.timing"
((KEEP)) || podman rm -f "${NAME}" >/dev/null
log "total $(($(date +%s) - t0))s"
