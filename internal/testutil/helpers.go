// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package testutil

import (
	"os"
	"testing"
)

// SkipIfRoot skips the test when running as root because operations such as
// chmod(0o000) have no effect for root and permission-denied error paths
// cannot be triggered.
func SkipIfRoot(t *testing.T) {
	t.Helper()

	if os.Getuid() == 0 {
		t.Skip("skipping permission-based test: running as root")
	}
}
