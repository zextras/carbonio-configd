#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Run `carbonio-bootstrap run` non-interactively in a fresh container of the
# parity image (same image, same config as run-legacy.sh) and snapshot it.
#
#   ./run-configd.sh /path/to/carbonio-bootstrap [--out DIR] [--name NAME] [--keep] [--config FILE]
#
# The binary must be a linux/amd64 build (e.g. `make build` of the repo).
# Defaults: --out out/configd  --name parity-configd
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

BIN="${1:?usage: run-configd.sh /path/to/carbonio-bootstrap [options]}"
shift
[[ -x ${BIN} ]] || die "not an executable file: ${BIN}"
OUT="${PARITY_DIR}/out/configd"
NAME=parity-configd
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
podman cp "${BIN}" "${NAME}:/opt/zextras/libexec/setup"
podman exec "${NAME}" chmod 0755 /opt/zextras/libexec/setup
t1=$(date +%s)
log "container ready in $((t1 - t0))s; running carbonio-bootstrap run"

mkdir -p "${OUT}"
rc=0
podman exec "${NAME}" /opt/zextras/libexec/setup run \
  --legacy-config /opt/product.config --non-interactive \
  >"${OUT}.bootstrap.log" 2>&1 || rc=$?
t2=$(date +%s)
log "carbonio-bootstrap exit=${rc} duration=$((t2 - t1))s (log: ${OUT}.bootstrap.log)"
[[ ${rc} -eq 0 ]] || die "carbonio-bootstrap failed (container ${NAME} kept)"

take_snapshot "${NAME}" "${OUT}"
printf 'bootstrap_seconds=%s\nexit=%s\n' "$((t2 - t1))" "${rc}" >"${OUT}.timing"
((KEEP)) || podman rm -f "${NAME}" >/dev/null
log "total $(($(date +%s) - t0))s"
