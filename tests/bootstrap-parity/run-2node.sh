#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# 2-node scenarios; node 1 is always the single-node scenario (LDAP master + MTA +
# proxy + store). Both containers sit on one podman network, resolve each other
# through /etc/hosts.
#
#   --scenario store    (default) node 2 = second store (carbonio-appserver) using the LDAP of node 1
#                       needs ./build.sh --store;  default DIR out/2node-<mode>
#   --scenario replica  node 2 = carbonio-directory-server only, LDAPREPLICATIONTYPE=replica;
#                       between the nodes `zmldapenablereplica` is run on the master (as the
#                       docs require); after node 2 the script waits until its contextCSN equals
#                       the master's. Needs ./build.sh --ldap;  default DIR out/replica-<mode>
#
#   ./run-2node.sh legacy                       [--scenario S] [--out DIR] [--keep]
#   ./run-2node.sh configd /path/to/carbonio-bootstrap     [--scenario S] [--out DIR] [--keep]
#
# Needs ./build.sh and the image of the scenario.
# Output: DIR/node1 (taken after node 2 joined), DIR/node2, DIR.timing; replica scenario
# also DIR/node2-synced (slapd of node 2 running, serving the same DNs as the master) and DIR.sync.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

MODE="${1:?usage: run-2node.sh legacy|configd [BIN] [options]}"
shift
BIN=""
if [[ ${MODE} == configd ]]; then
  BIN="${1:?usage: run-2node.sh configd /path/to/carbonio-bootstrap [options]}"
  shift
  [[ -x ${BIN} ]] || die "not an executable file: ${BIN}"
elif [[ ${MODE} != legacy ]]; then
  die "mode must be legacy or configd"
fi
OUT=""
KEEP=0
SCENARIO=store
while (($#)); do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --scenario) SCENARIO="$2"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done
case "${SCENARIO}" in
  store)
    OUT="${OUT:-${PARITY_DIR}/out/2node-${MODE}}"
    NODE2_IMAGE="${PARITY_STORE_IMAGE:-localhost/carbonio-parity:noble-store}"
    NODE2_CONFIG="${PARITY_DIR}/config/store-node.config"
    ;;
  replica)
    OUT="${OUT:-${PARITY_DIR}/out/replica-${MODE}}"
    NODE2_IMAGE="${PARITY_LDAP_IMAGE:-localhost/carbonio-parity:noble-ldap}"
    NODE2_CONFIG="${PARITY_DIR}/config/replica-node.config"
    ;;
  *) die "scenario must be store or replica" ;;
esac

NET=parity-net
N1=parity-${MODE}-n1 FQDN1=mail.parity.test IP1=10.89.77.10
N2=parity-${MODE}-n2 FQDN2=mail2.parity.test IP2=10.89.77.11
podman network exists "${NET}" || podman network create --subnet 10.89.77.0/24 "${NET}" >/dev/null
# Each node writes its own "IP FQDN short" line; the peer is added as extra line
# (a duplicate own line makes slapd bind the same address twice: EADDRINUSE).
PEER1="${IP2} ${FQDN2} ${FQDN2%%.*}"
PEER2="${IP1} ${FQDN1} ${FQDN1%%.*}"

bootstrap() { # NAME CONFIG_IN_CONTAINER
  if [[ ${MODE} == legacy ]]; then
    podman exec "$1" /usr/bin/carbonio-bootstrap -c /opt/product.config
  else
    podman exec "$1" /opt/zextras/libexec/setup run \
      --legacy-config /opt/product.config --non-interactive
  fi
}

t0=$(date +%s)
start_container "${N1}" "${FQDN1}" "${PARITY_IMAGE}" --network "${NET}" --ip "${IP1}"
start_container "${N2}" "${FQDN2}" "${NODE2_IMAGE}" --network "${NET}" --ip "${IP2}"
prepare_container "${N1}" "${FQDN1}" "${PARITY_DIR}/config/single-node.config" "${PEER1}"
prepare_container "${N2}" "${FQDN2}" "${NODE2_CONFIG}" "${PEER2}"
if [[ ${MODE} == configd ]]; then
  for n in "${N1}" "${N2}"; do
    podman cp "${BIN}" "${n}:/opt/zextras/libexec/setup"
    podman exec "${n}" chmod 0755 /opt/zextras/libexec/setup
  done
fi

mkdir -p "${OUT}"
t1=$(date +%s)
log "node 1 (${FQDN1}) bootstrap"
bootstrap "${N1}" >"${OUT}.node1.bootstrap.log" 2>&1 || die "node 1 bootstrap failed, see ${OUT}.node1.bootstrap.log"
t2=$(date +%s)
n1_seconds=$((t2 - t1))
log "node 1 done in ${n1_seconds}s"
if [[ ${SCENARIO} == replica ]]; then
  log "enabling replication on the master (zmldapenablereplica)"
  podman exec "${N1}" /opt/zextras/libexec/zmldapenablereplica >"${OUT}.enablereplica.log" 2>&1 ||
    die "zmldapenablereplica on node 1 failed, see ${OUT}.enablereplica.log"
fi
t2=$(date +%s)
log "node 2 (${FQDN2}) bootstrap"
bootstrap "${N2}" >"${OUT}.node2.bootstrap.log" 2>&1 || die "node 2 bootstrap failed, see ${OUT}.node2.bootstrap.log"
t3=$(date +%s)
log "node 2 done in $((t3 - t2))s"
take_snapshot "${N1}" "${OUT}/node1"
take_snapshot "${N2}" "${OUT}/node2"
if [[ ${SCENARIO} == replica ]]; then
  # The snapshots above are the state the bootstrap leaves (the legacy one leaves
  # slapd of the replica stopped). Now prove that the replica replicates: make sure
  # its slapd runs, wait until it serves the same DNs below cn=zimbra as the master
  # and snapshot it again as node2-synced.
  dns() { podman exec "$1" bash -c 'pw=$(runuser -u zextras -- /opt/zextras/bin/zmlocalconfig -s -m nokey zimbra_ldap_password);
    /opt/zextras/common/bin/ldapsearch -LLL -o ldif-wrap=no -x -H ldapi://%2Frun%2Fcarbonio%2Frun%2Fldapi/ -D uid=zimbra,cn=admins,cn=zimbra -w "$pw" -b cn=zimbra -s sub "(objectClass=*)" dn | grep ^dn: | LC_ALL=C sort' 2>/dev/null; }
  podman exec "${N2}" systemctl is-active --quiet carbonio-openldap.service ||
    { log "slapd of node 2 is not running after the bootstrap; starting it"; podman exec "${N2}" systemctl start carbonio-openldap.service; }
  synced=no
  for ((i = 0; i < 180; i++)); do
    d1="$(dns "${N1}")"
    if [[ -n ${d1} && ${d1} == "$(dns "${N2}")" ]]; then synced=yes; break; fi
    sleep 1
  done
  log "replica serves the DNs of the master below cn=zimbra: ${synced} after ${i}s"
  printf 'replica_synced=%s\nreplica_sync_wait_seconds=%s\nmaster_dns=%s\n' "${synced}" "${i}" "$(wc -l <<<"${d1}")" >"${OUT}.sync"
  take_snapshot "${N2}" "${OUT}/node2-synced"
fi
printf 'node1_seconds=%s\nnode2_seconds=%s\n' "${n1_seconds}" "$((t3 - t2))" >"${OUT}.timing"
if ((!KEEP)); then
  podman rm -f "${N1}" "${N2}" >/dev/null
fi
log "total $(($(date +%s) - t0))s"
