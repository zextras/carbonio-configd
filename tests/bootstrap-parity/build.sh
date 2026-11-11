#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Build the parity image (Ubuntu 24.04 + Carbonio CE packages, no bootstrap).
#
#   ./build.sh [--no-cache]
#
# Environment: PARITY_IMAGE (default localhost/carbonio-parity:noble)
set -euo pipefail
cd "$(dirname "$0")"
IMAGE="${PARITY_IMAGE:-localhost/carbonio-parity:noble}"
start=$(date +%s)
podman build "$@" --pull=newer -t "${IMAGE}" -f Containerfile .
echo "built ${IMAGE} in $(($(date +%s) - start))s"
