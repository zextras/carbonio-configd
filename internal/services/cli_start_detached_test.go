// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStartDirect_DetachedMissingBinary verifies startDetached returns error for missing binary.
func TestStartDirect_DetachedMissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: filepath.Join(tmp, "nonexistent-binary"),
		Detached:   true,
		LogFile:    logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when detached binary is missing")
	}
}

// TestStartDetached_LogFileOpenError exercises the os.OpenFile error path.
func TestStartDetached_LogFileOpenError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/bin/true",
		LogFile:    "/nonexistent-dir-xyz/test.log",
	}

	err := startDetached(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when log file cannot be opened")
	}
}

// TestStartDetached_LogOpenFails verifies startDetached returns error when log cannot be opened.
func TestStartDetached_LogOpenFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: "/usr/bin/true",
		Detached:   true,
		LogFile:    "/nonexistent/dir/test.out",
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when log file cannot be opened")
	}
}

// TestStartDetached_SuccessNoSDNotify exercises the successful detach path (no SDNotify).
func TestStartDetached_SuccessNoSDNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  "/bin/true",
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: false,
		Detached:    false,
	}

	err := startDetached(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDetached returned unexpected error: %v", err)
	}
}

// TestStartDetached_Success verifies startDetached spawns a background process.
func TestStartDetached_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:        "testdetached",
		BinaryPath:  "/usr/bin/sleep",
		BinaryArgs:  []string{"0"},
		Detached:    true,
		UseSDNotify: false,
		LogFile:     logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err != nil {
		t.Errorf("startDetached returned unexpected error: %v", err)
	}
}

// TestStartDetached_DefaultLogPath verifies startDetached uses basePath/log/<name>.out
// when LogFile is empty.
func TestStartDetached_DefaultLogPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := basePath
	basePath = "/nonexistent-base-path-xyz"
	defer func() { basePath = old }()

	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: "/usr/bin/true",
		Detached:   true,
		LogFile:    "",
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when default log path directory doesn't exist")
	}
}

// TestStartDetached_UseSDNotify exercises the UseSDNotify=true branch.
func TestStartDetached_UseSDNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		t.Skip("skipping: /bin/true not available")
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  truePath,
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: true,
		Detached:    false,
	}

	startDetached(context.Background(), "testservice", def)
}

// TestStartDetached_SDNotify_BinaryMissing verifies startDetached returns error
// when UseSDNotify=true and binary is missing.
func TestStartDetached_SDNotify_BinaryMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:        "testdetached",
		BinaryPath:  filepath.Join(tmp, "nonexistent-binary"),
		Detached:    true,
		UseSDNotify: true,
		LogFile:     logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when UseSDNotify=true and binary is missing")
	}
}
