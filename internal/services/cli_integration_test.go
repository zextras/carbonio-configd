// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"
)

// TestStartService_NonSystemdCustomStart verifies startService uses CustomStart
// when systemd is not booted.
func TestStartService_NonSystemdCustomStart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("startService non-systemd path not reachable on systemd-booted host")
	}

	called := false
	def := &ServiceDef{
		Name:         "testservice",
		SystemdUnits: []string{"test.service"},
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := startService(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startService returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected CustomStart to be called")
	}
}

// TestStopService_NonSystemdCustomStop verifies stopService uses CustomStop
// when systemd is not booted.
func TestStopService_NonSystemdCustomStop(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("stopService non-systemd path not reachable on systemd-booted host")
	}

	called := false
	def := &ServiceDef{
		Name:         "testservice",
		SystemdUnits: []string{"test.service"},
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := stopService(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopService returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected CustomStop to be called")
	}
}

// TestStopService_LegacyMode_BypassesSystemctl asserts that in legacy mode
// stopService goes straight to stopWithoutSystemd (CustomStop / pkill) without
// invoking systemctl. This guards the container fix: statsCustomStop must
// actually run to terminate workers that live outside any systemd cgroup.
func TestStopService_LegacyMode_BypassesSystemctl(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	withMode(t, false)

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	called := false
	def := *orig
	def.SystemdUnits = []string{"carbonio-fake-stop-test-xyzzy.service"}
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		called = true
		return nil
	}
	Registry["memcached"] = &def

	if err := stopService(context.Background(), "memcached", &def); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected CustomStop to run in legacy mode; systemctl must not be invoked, fall-through must not exist")
	}
}
