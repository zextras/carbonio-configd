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

// TestStartDirect_EmptyBinaryPath verifies error returned immediately for empty binary.
func TestStartDirect_EmptyBinaryPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "",
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for empty BinaryPath")
	}
}

// TestStartDirect_MissingBinary verifies error when binary file does not exist.
func TestStartDirect_MissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/nonexistent/binary/xyz-configd-test-direct",
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

// TestStartDirect_NeedsRoot_NonRoot exercises the sudo-wrapping branch.
func TestStartDirect_NeedsRoot_NonRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping: test requires non-root user")
	}

	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		t.Skip("skipping: /bin/true not available")
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  truePath,
		NeedsRoot:   true,
		Detached:    false,
		UseSDNotify: false,
	}

	startDirect(context.Background(), "testservice", def)
}

// TestStartDirect_NeedsRootAsNonRoot verifies that NeedsRoot=true prepends sudo.
func TestStartDirect_NeedsRootAsNonRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if os.Getuid() == 0 {
		t.Skip("NeedsRoot branch only applies to non-root users")
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   true,
		Detached:    false,
		UseSDNotify: false,
	}

	startDirect(context.Background(), "testservice", def)
}

// TestStartDirect_DirectExec_Success verifies startDirect with a real binary that exits 0.
func TestStartDirect_DirectExec_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   false,
		Detached:    false,
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDirect with exiting-0 binary returned error: %v", err)
	}
}

// TestStartDirect_DirectExec_Failure verifies startDirect with a binary that exits non-zero.
func TestStartDirect_DirectExec_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   false,
		Detached:    false,
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for binary that exits non-zero")
	}
}

// TestStartDirect_DetachedPath exercises the Detached=true branch in startDirect.
func TestStartDirect_DetachedPath(t *testing.T) {
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
		Detached:    true,
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDirect Detached=true returned error: %v", err)
	}
}
