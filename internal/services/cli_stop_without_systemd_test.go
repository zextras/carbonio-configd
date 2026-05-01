// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"
)

// TestStopWithoutSystemd_CustomStop verifies CustomStop is called.
func TestStopWithoutSystemd_CustomStop(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	called := false
	def := &ServiceDef{
		Name: "testservice",
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopWithoutSystemd with CustomStop returned error: %v", err)
	}
	if !called {
		t.Error("CustomStop was not called")
	}
}

// TestStopWithoutSystemd_NoProcessName verifies error when neither CustomStop nor
// ProcessName is configured.
func TestStopWithoutSystemd_NoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name: "testservice",
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when no CustomStop and no ProcessName")
	}
}

// TestStopWithoutSystemd_ProcessName verifies killProcess is called.
func TestStopWithoutSystemd_ProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "testservice",
		ProcessName: "carbonio-configd-test-needle-xyzzy-99999",
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopWithoutSystemd with ProcessName returned unexpected error: %v", err)
	}
}
