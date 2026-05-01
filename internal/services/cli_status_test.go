// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zextras/carbonio-configd/internal/systemd"
)

// TestServiceStatus_NoPidFileNoProcessName verifies ServiceStatus returns (false, nil)
// when no detection method is configured (non-systemd hosts only).
func TestServiceStatus_NoPidFileNoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: on systemd host ServiceStatus uses systemctl, not PID/process detection")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = ""
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false when no detection method configured")
	}
}

// TestServiceStatus_WithPidFile_Running verifies ServiceStatus detects running via PID file.
func TestServiceStatus_WithPidFile_Running(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping PID-file detection test on systemd-booted host")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "test.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !running {
		t.Error("expected running=true when pidfile contains our own PID")
	}
}

// TestServiceStatus_WithProcessName verifies ServiceStatus uses ProcessName fallback.
func TestServiceStatus_WithProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: on systemd host ServiceStatus uses systemctl, not process name scan")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = ""
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process name")
	}
}

// TestServiceStatus_PidFileUnreadable_FallsBackToProcessName exercises the
// "PID file unreadable → fall through to ProcessName" path.
func TestServiceStatus_PidFileUnreadable_FallsBackToProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: PID fallback test on systemd-booted host")
	}
	if os.Getuid() == 0 {
		t.Skip("cannot test unreadable pidfile as root")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "unreadable.pid")
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(pidFile, 0o644) //nolint:errcheck

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = pidFile
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process")
	}
}

// TestServiceStatus_PidFileMissing_FallsToProcessName exercises the PID file absent
// path falling through to ProcessName on non-systemd hosts.
func TestServiceStatus_PidFileMissing_FallsToProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: legacy PID fallback test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = "/nonexistent-pid-file-xyz-test.pid"
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process")
	}
}

// TestServiceStatus_LegacyMode_UsesPidProbeRegardlessOfSystemdUnit asserts that
// in legacy mode (no Carbonio target enabled) ServiceStatus ignores the
// service's SystemdUnits entirely and reports running from the PID file.
// This is the container regression test: stats in a podman install has an
// inactive carbonio-stats.service but live zmstat-* workers; before the
// IsSystemdMode()-gated refactor, a systemd-booted host would query
// systemctl and report stopped even though workers were alive.
func TestServiceStatus_LegacyMode_UsesPidProbeRegardlessOfSystemdUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	withMode(t, false)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "live.pid")

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = []string{"carbonio-nonexistent-test-xyz.service"}
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !running {
		t.Error("expected running=true in legacy mode: PID file points to a live process, SystemdUnits must be ignored")
	}
}

// TestServiceStatus_StrictMode_TrustsSystemctlOverPidProbe asserts that in
// strict systemd mode a non-existent unit causes ServiceStatus to return
// (false, nil) even when a live PID file exists. This guards against
// reintroducing the hybrid fall-through (which would have returned true by
// checking the PID file after systemctl failed).
func TestServiceStatus_StrictMode_TrustsSystemctlOverPidProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	if !systemd.IsBooted() {
		t.Skip("skipping: strict-mode path requires a host that can actually run systemctl")
	}

	withMode(t, true)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "live.pid")

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = []string{"carbonio-nonexistent-test-xyz.service"}
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if running {
		t.Error("expected running=false in strict mode: systemd unit is not-active and must be authoritative")
	}
}
