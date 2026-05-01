// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"
)

// TestStartWithoutSystemd_CustomStart verifies that CustomStart is called.
func TestStartWithoutSystemd_CustomStart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	called := false
	def := &ServiceDef{
		Name: "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startWithoutSystemd with CustomStart returned error: %v", err)
	}
	if !called {
		t.Error("CustomStart was not called")
	}
}

// TestStartWithoutSystemd_NoBinaryWithProcessName verifies the "managed via deps" path.
func TestStartWithoutSystemd_NoBinaryWithProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "testservice",
		ProcessName: "someprocess",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("expected nil when ProcessName set and no BinaryPath, got: %v", err)
	}
}

// TestStartWithoutSystemd_NoBinaryNoProcessName verifies the error path.
func TestStartWithoutSystemd_NoBinaryNoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name: "testservice",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when no launcher and no ProcessName")
	}
}

// TestStartWithoutSystemd_BinaryNotFound verifies startDirect returns error for missing binary.
func TestStartWithoutSystemd_BinaryNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/nonexistent/binary/xyz-configd-test",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}
