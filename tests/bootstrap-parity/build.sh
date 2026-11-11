#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Build a parity image (Ubuntu 24.04 + Carbonio CE packages, no bootstrap).
#
#   ./build.sh [--no-cache]                    single-node image (default package set)
#   ./build.sh --store [--no-cache]            2-node variant: application-server-only image
#   ./build.sh --ldap  [--no-cache]            LDAP-replica variant: directory-server-only image
#
# Environment: PARITY_IMAGE (default localhost/carbonio-parity:noble),
#              PARITY_STORE_IMAGE (default localhost/carbonio-parity:noble-store),
#              PARITY_LDAP_IMAGE (default localhost/carbonio-parity:noble-ldap),
#              PARITY_PACKAGES (override the package set, space separated)
set -euo pipefail
cd "$(dirname "$0")"
IMAGE="${PARITY_IMAGE:-localhost/carbonio-parity:noble}"
ARGS=()
if [[ ${1:-} == --store ]]; then
  shift
  IMAGE="${PARITY_STORE_IMAGE:-localhost/carbonio-parity:noble-store}"
  ARGS+=(--build-arg "CARBONIO_PACKAGES=${PARITY_PACKAGES:-carbonio-appserver carbonio-memcached}")
elif [[ ${1:-} == --ldap ]]; then
  shift
  IMAGE="${PARITY_LDAP_IMAGE:-localhost/carbonio-parity:noble-ldap}"
  ARGS+=(--build-arg "CARBONIO_PACKAGES=${PARITY_PACKAGES:-carbonio-directory-server}")
elif [[ -n ${PARITY_PACKAGES:-} ]]; then
  ARGS+=(--build-arg "CARBONIO_PACKAGES=${PARITY_PACKAGES}")
fi
start=$(date +%s)
podman build "$@" "${ARGS[@]}" --pull=newer -t "${IMAGE}" -f Containerfile .
echo "built ${IMAGE} in $(($(date +%s) - start))s"
