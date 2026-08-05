// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = w

	fn()

	_ = w.Close()

	os.Stdout = old

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	return buf.String()
}

// livePidFileDef returns a def whose PidFile points at the test process, so
// the legacy detail path resolves a real PID and a real /proc start time.
func livePidFileDef(t *testing.T) *services.ServiceDef {
	t.Helper()

	pidFile := filepath.Join(t.TempDir(), "live.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return &services.ServiceDef{
		Name:        "test",
		DisplayName: "test",
		PidFile:     pidFile,
		ProcessName: "nonexistent-for-test-xyz",
	}
}

// TestShowServiceDetail_LegacyReportsPidAndSince is the regression guard for
// the "Since: n/a" bug: with no Carbonio target enabled the detail must come
// from /proc, never from systemctl show of a unit that may not even exist.
func TestShowServiceDetail_LegacyReportsPidAndSince(t *testing.T) {
	if services.IsSystemdMode() {
		t.Skip("host has Carbonio systemd targets enabled; legacy path not exercised")
	}

	def := livePidFileDef(t)
	def.SystemdUnits = []string{"carbonio-nonexistent-for-test.service"}

	out := captureStdout(t, func() { showServiceDetail(context.Background(), def) })

	if !strings.Contains(out, "PID: "+strconv.Itoa(os.Getpid())) {
		t.Errorf("output missing own PID: %q", out)
	}

	if !strings.Contains(out, "Since: ") {
		t.Errorf("output missing start time: %q", out)
	}

	if strings.Contains(out, "n/a") {
		t.Errorf("start time must never render as n/a: %q", out)
	}
}

// TestShowServiceDetail_NoPidPrintsNothing covers the not-resolvable case:
// no pidfile, no matching process, so there is no detail to print.
func TestShowServiceDetail_NoPidPrintsNothing(t *testing.T) {
	if services.IsSystemdMode() {
		t.Skip("host has Carbonio systemd targets enabled; legacy path not exercised")
	}

	def := &services.ServiceDef{
		Name:        "test",
		DisplayName: "test",
		ProcessName: "nonexistent-for-test-xyz",
	}

	if out := captureStdout(t, func() { showServiceDetail(context.Background(), def) }); out != "" {
		t.Errorf("expected no output, got %q", out)
	}
}

// TestShowUnitDetail_UnknownUnitPrintsNoSince asserts systemd's "n/a"
// placeholder for a never-activated unit is suppressed rather than displayed.
func TestShowUnitDetail_UnknownUnitPrintsNoSince(t *testing.T) {
	out := captureStdout(t, func() {
		showUnitDetail(context.Background(), "carbonio-nonexistent-for-test.service")
	})

	if strings.Contains(out, "Since:") || strings.Contains(out, "PID:") {
		t.Errorf("unknown unit must yield no detail lines, got %q", out)
	}
}

func TestPrintServiceStatus_UnknownService(t *testing.T) {
	var err error

	out := captureStdout(t, func() { err = printServiceStatus(context.Background(), "no-such-service") })

	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}

	if out != "" {
		t.Errorf("expected no output for an unknown service, got %q", out)
	}
}

// TestPrintServiceStatus_NotRunning covers the stopped path: a registered
// service that cannot be running on a developer host (no /opt/zextras tree)
// must report "is not running" and return a non-nil error for the exit code.
func TestPrintServiceStatus_NotRunning(t *testing.T) {
	if services.IsSystemdMode() {
		t.Skip("host has Carbonio systemd targets enabled; probe would query systemctl")
	}

	var err error

	out := captureStdout(t, func() { err = printServiceStatus(context.Background(), "cbpolicyd") })

	if err == nil {
		t.Skip("cbpolicyd appears to be running on this host")
	}

	if !strings.Contains(out, "is not running") {
		t.Errorf("expected a not-running line, got %q", out)
	}
}

func TestStatusCmd_SingleServiceDelegates(t *testing.T) {
	var err error

	out := captureStdout(t, func() { err = (&StatusCmd{Name: "no-such-service"}).Run() })

	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}

	if out != "" {
		t.Errorf("expected no output for an unknown service, got %q", out)
	}
}

func TestAdvancedErrorLine(t *testing.T) {
	if got := advancedErrorLine(zxadmin.ErrAdvancedNotRunning); got != "Carbonio Advanced is not running" {
		t.Errorf("404 sentinel rendered as %q", got)
	}

	got := advancedErrorLine(errors.New("dial tcp 127.0.0.1:7071: connection refused"))
	if !strings.HasPrefix(got, "module status unavailable: ") {
		t.Errorf("other errors must keep their detail, got %q", got)
	}
}

// TestAdvancedJARsPresent drives both branches through CARBONIO_BASE_DIR.
func TestAdvancedJARsPresent(t *testing.T) {
	base := t.TempDir()

	extDir := filepath.Join(base, "lib", "ext", "carbonio")
	if err := os.MkdirAll(extDir, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CARBONIO_BASE_DIR", base)

	if advancedJARsPresent() {
		t.Error("empty ext dir must not count as Advanced installed")
	}

	if err := os.WriteFile(filepath.Join(extDir, "carbonio-advanced-core-26.6.5.jar"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if !advancedJARsPresent() {
		t.Error("carbonio-advanced-*.jar must count as Advanced installed")
	}
}

// TestPrintServiceStatus_RunningShowsDetail covers the running branch, which
// depends on a live probe, hence the serviceStatusFn seam.
func TestPrintServiceStatus_RunningShowsDetail(t *testing.T) {
	origStatus := serviceStatusFn
	origMode := isSystemdModeFn

	defer func() {
		serviceStatusFn = origStatus
		isSystemdModeFn = origMode
	}()

	serviceStatusFn = func(_ context.Context, _ string) (bool, error) { return true, nil }
	isSystemdModeFn = func() bool { return false }

	var err error

	out := captureStdout(t, func() { err = printServiceStatus(context.Background(), "cbpolicyd") })
	if err != nil {
		t.Fatalf("printServiceStatus = %v, want nil", err)
	}

	if !strings.Contains(out, "is running.") {
		t.Errorf("expected a running line, got %q", out)
	}
}

// TestPrintServiceStatus_ProbeOKButUnknownDef covers the nil-def guard: the
// probe answering for a name the registry does not know must not panic.
func TestPrintServiceStatus_ProbeOKButUnknownDef(t *testing.T) {
	orig := serviceStatusFn
	defer func() { serviceStatusFn = orig }()

	serviceStatusFn = func(_ context.Context, _ string) (bool, error) { return true, nil }

	err := printServiceStatus(context.Background(), "no-such-service")
	if err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("err = %v, want an unknown-service error", err)
	}
}

// TestShowServiceDetail_SystemdModeQueriesUnit covers the strict-systemd
// branch against a unit that really is active on any systemd host.
func TestShowServiceDetail_SystemdModeQueriesUnit(t *testing.T) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl unavailable")
	}

	if err := exec.Command("systemctl", "is-active", "--quiet", "dbus.service").Run(); err != nil {
		t.Skip("dbus.service is not active on this host")
	}

	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return true }

	def := &services.ServiceDef{
		Name:         "test",
		DisplayName:  "test",
		SystemdUnits: []string{"dbus.service"},
	}

	out := captureStdout(t, func() { showServiceDetail(context.Background(), def) })

	if !strings.Contains(out, "PID: ") || !strings.Contains(out, "Since: ") {
		t.Errorf("expected unit detail from systemctl show, got %q", out)
	}
}

// TestCheckAdvancedStatus_LocalconfigFailure and the probe-failure case below
// cover the two rendered error paths; both need Advanced to look installed.
func TestCheckAdvancedStatus_LocalconfigFailure(t *testing.T) {
	fakeAdvancedInstall(t)

	orig := loadLocalConfigFn
	defer func() { loadLocalConfigFn = orig }()

	loadLocalConfigFn = func() (map[string]string, error) { return nil, errors.New("localconfig.xml missing") }

	out := captureStdout(t, func() { checkAdvancedStatus(context.Background()) })

	if !strings.Contains(out, "module status unavailable: localconfig.xml missing") {
		t.Errorf("expected the loader error to surface, got %q", out)
	}
}

func TestCheckAdvancedStatus_ProbeFailure(t *testing.T) {
	fakeAdvancedInstall(t)

	orig := loadLocalConfigFn
	defer func() { loadLocalConfigFn = orig }()

	// Port 1 has no listener, so the probe fails fast instead of waiting out
	// the client's timeout.
	loadLocalConfigFn = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_ldap_user":     "zimbra",
			"zimbra_ldap_password": "x",
			"mailboxd_admin_port":  "1",
		}, nil
	}

	out := captureStdout(t, func() { checkAdvancedStatus(context.Background()) })

	if !strings.Contains(out, "Carbonio Advanced") {
		t.Errorf("expected the Advanced section header, got %q", out)
	}

	if !strings.Contains(out, "module status unavailable") && !strings.Contains(out, "is not running") {
		t.Errorf("expected a failure line, got %q", out)
	}
}

// fakeAdvancedInstall points CARBONIO_BASE_DIR at a tree that looks like an
// Advanced install, so advancedJARsPresent passes.
func fakeAdvancedInstall(t *testing.T) {
	t.Helper()

	base := t.TempDir()

	extDir := filepath.Join(base, "lib", "ext", "carbonio")
	if err := os.MkdirAll(extDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(extDir, "carbonio-advanced-core-26.6.5.jar"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CARBONIO_BASE_DIR", base)
}
