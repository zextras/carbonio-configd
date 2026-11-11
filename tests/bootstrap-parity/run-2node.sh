#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# 2-node variant: node 1 = the single-node scenario (LDAP master + MTA + proxy +
# store), node 2 = a second store (carbonio-appserver) that joins the LDAP of node 1.
# Both containers sit on one podman network, resolve each other through /etc/hosts.
#
#   ./run-2node.sh legacy                       [--out DIR] [--keep]
#   ./run-2node.sh configd /path/to/configd     [--out DIR] [--keep]
#
# Needs both images: ./build.sh && ./build.sh --store
# Output: DIR/node1 (taken after node 2 joined), DIR/node2, DIR.timing. Default DIR out/2node-<mode>.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

MODE="${1:?usage: run-2node.sh legacy|configd [BIN] [options]}"
shift
BIN=""
if [[ ${MODE} == configd ]]; then
  BIN="${1:?usage: run-2node.sh configd /path/to/configd [options]}"
  shift
  [[ -x ${BIN} ]] || die "not an executable file: ${BIN}"
elif [[ ${MODE} != legacy ]]; then
  die "mode must be legacy or configd"
fi
OUT="${PARITY_DIR}/out/2node-${MODE}"
KEEP=0
while (($#)); do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done

NET=parity-net
N1=parity-${MODE}-n1 FQDN1=mail.parity.test IP1=10.89.77.10
N2=parity-${MODE}-n2 FQDN2=mail2.parity.test IP2=10.89.77.11
STORE_IMAGE="${PARITY_STORE_IMAGE:-localhost/carbonio-parity:noble-store}"
podman network exists "${NET}" || podman network create --subnet 10.89.77.0/24 "${NET}" >/dev/null
# Each node writes its own "IP FQDN short" line; the peer is added as extra line
# (a duplicate own line makes slapd bind the same address twice: EADDRINUSE).
PEER1="${IP2} ${FQDN2} ${FQDN2%%.*}"
PEER2="${IP1} ${FQDN1} ${FQDN1%%.*}"

bootstrap() { # NAME CONFIG_IN_CONTAINER
  if [[ ${MODE} == legacy ]]; then
    podman exec "$1" /usr/bin/carbonio-bootstrap -c /opt/product.config
  else
    podman exec "$1" /opt/zextras/bin/configd bootstrap run \
      --legacy-config /opt/product.config --non-interactive
  fi
}

t0=$(date +%s)
start_container "${N1}" "${FQDN1}" "${PARITY_IMAGE}" --network "${NET}" --ip "${IP1}"
start_container "${N2}" "${FQDN2}" "${STORE_IMAGE}" --network "${NET}" --ip "${IP2}"
prepare_container "${N1}" "${FQDN1}" "${PARITY_DIR}/config/single-node.config" "${PEER1}"
prepare_container "${N2}" "${FQDN2}" "${PARITY_DIR}/config/store-node.config" "${PEER2}"
if [[ ${MODE} == configd ]]; then
  for n in "${N1}" "${N2}"; do
    podman cp "${BIN}" "${n}:/opt/zextras/bin/configd"
    podman exec "${n}" chmod 0755 /opt/zextras/bin/configd
  done
fi

mkdir -p "${OUT}"
t1=$(date +%s)
log "node 1 (${FQDN1}) bootstrap"
bootstrap "${N1}" >"${OUT}.node1.bootstrap.log" 2>&1 || die "node 1 bootstrap failed, see ${OUT}.node1.bootstrap.log"
t2=$(date +%s)
log "node 1 done in $((t2 - t1))s; node 2 (${FQDN2}) bootstrap"
bootstrap "${N2}" >"${OUT}.node2.bootstrap.log" 2>&1 || die "node 2 bootstrap failed, see ${OUT}.node2.bootstrap.log"
t3=$(date +%s)
log "node 2 done in $((t3 - t2))s"

take_snapshot "${N1}" "${OUT}/node1"
take_snapshot "${N2}" "${OUT}/node2"
printf 'node1_seconds=%s\nnode2_seconds=%s\n' "$((t2 - t1))" "$((t3 - t2))" >"${OUT}.timing"
if ((!KEEP)); then
  podman rm -f "${N1}" "${N2}" >/dev/null
fi
log "total $(($(date +%s) - t0))s"
