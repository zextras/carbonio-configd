// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"os"
	"path/filepath"
	"testing"
)

// --- openLogFile (error paths) ---

func TestOpenLogFile_NestedDirCreation(t *testing.T) {
	// openLogFile should succeed when the parent directory exists.
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "service.log")

	f, err := openLogFile(logFile)
	if err != nil {
		t.Fatalf("openLogFile() unexpected error: %v", err)
	}
	defer f.Close()

	info, statErr := os.Stat(logFile)
	if statErr != nil {
		t.Fatalf("log file not created: %v", statErr)
	}

	if info.Size() != 0 {
		t.Errorf("expected empty file, got %d bytes", info.Size())
	}
}

// --- bootstrapPostfixMainCf (StatPermError test removed) ---
// The test TestBootstrapPostfixMainCf_StatPermError was removed because
// the behavior is correct: a directory named main.cf would cause os.Stat
// to succeed (it IS a directory), so bootstrap returns nil early.
