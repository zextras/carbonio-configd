#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Run the LEGACY bootstrap (carbonio-bootstrap -> setup.pl) non-interactively
# in a fresh container of the parity image and snapshot the result.
#
#   ./run-legacy.sh [--out DIR] [--name NAME] [--keep] [--config FILE]
#                   [--setup-pl FILE] [--setup-bin FILE] [--zmserverips FILE] [--trace]
#
#   --setup-pl FILE      copy FILE over /opt/zextras/libexec/setup.pl
#   --setup-bin FILE     install FILE as /opt/zextras/libexec/setup (carbonio-configd)
#   --zmserverips FILE   install FILE as /opt/zextras/libexec/zmserverips
#   --install SRC=DEST   copy SRC to DEST (mode 0755) in the container; repeatable: the wrappers
#                        of carbonio-core that exec the setup binary (zmcertmgr, zmproxyconfig...)
#   --trace              record every start of zmprov/zmjava/ProvUtil/RandomPassword/Perl
#                        zmserverips (trace-install.sh) into OUT.trace/
#
# Defaults: --out out/legacy  --name parity-legacy  (container removed unless --keep)
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

OUT="${PARITY_DIR}/out/legacy"
NAME=parity-legacy
KEEP=0
SETUP_PL=""
SETUP_BIN=""
ZMSERVERIPS=""
TRACE=0
INSTALLS=()
while (($#)); do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --config) PARITY_CONFIG="$2"; shift 2 ;;
    --setup-pl) SETUP_PL="$2"; shift 2 ;;
    --setup-bin) SETUP_BIN="$2"; shift 2 ;;
    --zmserverips) ZMSERVERIPS="$2"; shift 2 ;;
    --install) INSTALLS+=("$2"); shift 2 ;;
    --trace) TRACE=1; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done

t0=$(date +%s)
start_container "${NAME}"
prepare_container "${NAME}"
if [[ -n ${SETUP_PL} ]]; then
  podman cp "${SETUP_PL}" "${NAME}:/opt/zextras/libexec/setup.pl"
  podman exec "${NAME}" chmod 0755 /opt/zextras/libexec/setup.pl
fi
if [[ -n ${SETUP_BIN} ]]; then
  podman cp "${SETUP_BIN}" "${NAME}:/opt/zextras/libexec/setup"
  podman exec "${NAME}" chmod 0755 /opt/zextras/libexec/setup
fi
if [[ -n ${ZMSERVERIPS} ]]; then
  podman cp "${ZMSERVERIPS}" "${NAME}:/opt/zextras/libexec/zmserverips"
  podman exec "${NAME}" chmod 0755 /opt/zextras/libexec/zmserverips
fi
for spec in "${INSTALLS[@]}"; do
  podman cp "${spec%%=*}" "${NAME}:${spec#*=}"
  podman exec "${NAME}" chmod 0755 "${spec#*=}"
done
if ((TRACE)); then
  podman cp "${PARITY_DIR}/trace-install.sh" "${NAME}:/usr/local/bin/parity-trace-install"
  podman exec "${NAME}" bash /usr/local/bin/parity-trace-install
fi
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
podman exec "${NAME}" bash -c 'cat /tmp/zmsetup.*.log' >"${OUT}.zmsetup.log" 2>/dev/null || true
if ((TRACE)); then
  mkdir -p "${OUT}.trace"
  podman cp "${NAME}:/var/log/parity-trace.log" "${OUT}.trace/" 2>/dev/null || true
  podman cp "${NAME}:/var/log/parity-ps.log" "${OUT}.trace/" 2>/dev/null || true
fi
printf 'bootstrap_seconds=%s\nexit=%s\n' "$((t2 - t1))" "${rc}" >"${OUT}.timing"
((KEEP)) || podman rm -f "${NAME}" >/dev/null
log "total $(($(date +%s) - t0))s"
