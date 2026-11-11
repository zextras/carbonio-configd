#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Diff two snapshot directories (default: golden single-node vs out/configd).
#
#   ./diff.sh [GOLDEN_DIR [ACTUAL_DIR]]
#
# Exit status: 0 identical, 1 differences, 2 usage/missing input.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
a="${1:-${here}/golden/single-node}"
b="${2:-${here}/out/configd}"
for d in "${a}" "${b}"; do
  [[ -d ${d} ]] || { echo "not a directory: ${d}" >&2; exit 2; }
done

rc=0
mapfile -t files < <({ ls -1 "${a}"; ls -1 "${b}"; } | sort -u)
for f in "${files[@]}"; do
  if [[ ! -e ${a}/${f} ]]; then
    echo "=== ${f}: only in ${b}"; rc=1
  elif [[ ! -e ${b}/${f} ]]; then
    echo "=== ${f}: only in ${a}"; rc=1
  elif ! diff -u --label "a/${f}" --label "b/${f}" "${a}/${f}" "${b}/${f}"; then
    echo "=== ${f}: DIFFERENT"; rc=1
  fi
done
exit "${rc}"
