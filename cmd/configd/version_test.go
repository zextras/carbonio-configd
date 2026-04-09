// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestGetVersionInfo(t *testing.T) {
	vi := GetVersionInfo()

	// When running under go test, ReadBuildInfo() is available
	// and returns valid info. The version for test binaries is typically "(devel)".
	if vi.Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestPrintVersion(t *testing.T) {
	// Just verify it doesn't panic
	PrintVersion()
}
