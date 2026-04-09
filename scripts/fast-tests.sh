#!/bin/bash

# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Run fast unit tests on affected packages only

set -e

# Get list of staged Go files
STAGED_GO_FILES=$(git diff --cached --name-only --diff-filter=ACM | grep '\.go$' || true)

if [ -z "$STAGED_GO_FILES" ]; then
    echo "No Go files staged, skipping tests"
    exit 0
fi

# Extract unique package paths
PACKAGES=$(echo "$STAGED_GO_FILES" | xargs -n1 dirname | sort -u | sed 's|^|./|' | tr '\n' ' ')

if [ -z "$PACKAGES" ]; then
    echo "No packages to test"
    exit 0
fi

echo "Running tests for packages: $PACKAGES"
go test -short -timeout=5s $PACKAGES
