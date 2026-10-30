// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// newConsole returns a console writing to buffers, and the stdout buffer.
func newConsole() (*cliutil.Console, *bytes.Buffer) {
	var out bytes.Buffer

	return cliutil.NewConsole(&out, &bytes.Buffer{}), &out
}

// livePidFileDef returns a def whose PidFile points at the test process and
// whose ProcessName is the test binary, so the legacy detail path resolves a
// real PID (the pidfile PID is believed only when its binary matches the
// identity) and a real /proc start time.
func livePidFileDef(t *testing.T) *services.ServiceDef {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	pidFile := filepath.Join(t.TempDir(), "live.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return &services.ServiceDef{
		Name:        "test",
		DisplayName: "test",
		PidFile:     pidFile,
		ProcessName: self,
	}
}

// stubProbes replaces the live status/mode probes for the test's duration.
func stubProbes(t *testing.T, running bool, systemd bool) {
	t.Helper()

	origStatus, origMode := serviceStatusFn, isSystemdModeFn

	t.Cleanup(func() {
		serviceStatusFn = origStatus
		isSystemdModeFn = origMode
	})

	serviceStatusFn = func(_ context.Context, _ string) (bool, error) { return running, nil }
	isSystemdModeFn = func() bool { return systemd }
}

// TestShowServiceDetail_LegacyReportsPidAndSince is the regression guard for
// the "Since: n/a" bug: with no Carbonio target enabled the detail must come
// from /proc, never from systemctl show of a unit that may not even exist.
func TestShowServiceDetail_LegacyReportsPidAndSince(t *testing.T) {
	stubProbes(t, true, false)

	def := livePidFileDef(t)
	def.SystemdUnits = []string{"carbonio-nonexistent-for-test.service"}

	c, out := newConsole()
	showServiceDetail(context.Background(), c, def)

	if !strings.Contains(out.String(), "  PID: "+strconv.Itoa(os.Getpid())+"\n") {
		t.Errorf("output missing own PID: %q", out)
	}

	if !strings.Contains(out.String(), "  Since: ") {
		t.Errorf("output missing start time: %q", out)
	}

	if strings.Contains(out.String(), "n/a") {
		t.Errorf("start time must never render as n/a: %q", out)
	}
}

// TestShowServiceDetail_NoPidPrintsNothing covers the not-resolvable case:
// no pidfile, no matching process, so there is no detail to print.
func TestShowServiceDetail_NoPidPrintsNothing(t *testing.T) {
	stubProbes(t, true, false)

	def := &services.ServiceDef{
		Name:        "test",
		DisplayName: "test",
		ProcessName: "nonexistent-for-test-xyz",
	}

	c, out := newConsole()
	showServiceDetail(context.Background(), c, def)

	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out)
	}
}

// TestShowUnitDetail_UnknownUnitPrintsNoSince asserts systemd's "n/a"
// placeholder for a never-activated unit is suppressed rather than displayed.
func TestShowUnitDetail_UnknownUnitPrintsNoSince(t *testing.T) {
	c, out := newConsole()
	showUnitDetail(context.Background(), c, "carbonio-nonexistent-for-test.service")

	if strings.Contains(out.String(), "Since:") || strings.Contains(out.String(), "PID:") {
		t.Errorf("unknown unit must yield no detail lines, got %q", out)
	}
}

func TestPrintServiceStatus_UnknownService(t *testing.T) {
	stubProbes(t, false, false)

	c, out := newConsole()

	err := PrintServiceStatus(context.Background(), c, "no-such-service")
	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}

	if out.Len() != 0 {
		t.Errorf("expected no output for an unknown service, got %q", out)
	}
}

// TestPrintServiceStatus_NotRunning covers the stopped path: the service
// prints "is not running" and the error drives the exit code.
func TestPrintServiceStatus_NotRunning(t *testing.T) {
	stubProbes(t, false, false)

	c, out := newConsole()

	err := PrintServiceStatus(context.Background(), c, "cbpolicyd")
	if err == nil || err.Error() != "service cbpolicyd is not running" {
		t.Fatalf("err = %v, want 'service cbpolicyd is not running'", err)
	}

	if got, want := out.String(), services.LookupService("cbpolicyd").DisplayName+" is not running.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPrintServiceStatus_ProbeError(t *testing.T) {
	orig := serviceStatusFn
	t.Cleanup(func() { serviceStatusFn = orig })

	serviceStatusFn = func(context.Context, string) (bool, error) { return false, errors.New("boom") }

	c, out := newConsole()

	err := PrintServiceStatus(context.Background(), c, "cbpolicyd")
	if err == nil || err.Error() != "failed to get status for service cbpolicyd: boom" {
		t.Fatalf("err = %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("unexpected output %q", out)
	}
}

// TestPrintServiceStatus_RunningShowsDetail covers the running branch, which
// depends on a live probe, hence the serviceStatusFn seam.
func TestPrintServiceStatus_RunningShowsDetail(t *testing.T) {
	stubProbes(t, true, false)

	c, out := newConsole()

	if err := PrintServiceStatus(context.Background(), c, "cbpolicyd"); err != nil {
		t.Fatalf("PrintServiceStatus = %v, want nil", err)
	}

	want := services.LookupService("cbpolicyd").DisplayName + " is running.\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Errorf("output = %q, want prefix %q", out, want)
	}
}

// TestPrintServiceStatus_ProbeOKButUnknownDef covers the nil-def guard: the
// probe answering for a name the registry does not know must not panic.
func TestPrintServiceStatus_ProbeOKButUnknownDef(t *testing.T) {
	stubProbes(t, true, false)

	c, _ := newConsole()

	err := PrintServiceStatus(context.Background(), c, "no-such-service")
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

	stubProbes(t, true, true)

	def := &services.ServiceDef{
		Name:         "test",
		DisplayName:  "test",
		SystemdUnits: []string{"dbus.service"},
	}

	c, out := newConsole()
	showServiceDetail(context.Background(), c, def)

	if !strings.Contains(out.String(), "PID: ") || !strings.Contains(out.String(), "Since: ") {
		t.Errorf("expected unit detail from systemctl show, got %q", out)
	}
}

func TestParseSystemctlShow(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{"standard properties", "MainPID=456\nActiveState=active\n", map[string]string{"MainPID": "456", "ActiveState": "active"}},
		{"empty input", "", map[string]string{}},
		{"line without equals is ignored", "NoEqualsHere\nKey=Val\n", map[string]string{"Key": "Val"}},
		{"value with equals sign", "ExecStart=/usr/bin/java -Xms=512m\n", map[string]string{"ExecStart": "/usr/bin/java -Xms=512m"}},
		{"empty value", "EmptyVal=\n", map[string]string{"EmptyVal": ""}},
		{"whitespace only lines", "\n\n", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSystemctlShow(tt.input)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("parseSystemctlShow(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestServiceDetail(t *testing.T) {
	ctx := context.Background()

	if got := ServiceDetail(ctx, "mta", false); got != "" {
		t.Errorf("not running: got %q, want empty", got)
	}

	if got := ServiceDetail(ctx, "nonexistent-xyz", true); got != "" {
		t.Errorf("unknown service: got %q, want empty", got)
	}
}

func TestServiceDetailFromSystemd_NoUnits(t *testing.T) {
	for _, units := range [][]string{nil, {}} {
		def := &services.ServiceDef{Name: "test", DisplayName: "Test", SystemdUnits: units}
		if got := serviceDetailFromSystemd(context.Background(), def); got != "" {
			t.Errorf("units %v: got %q, want empty", units, got)
		}
	}
}

func TestServiceDetailFromProc_ZeroPID(t *testing.T) {
	def := &services.ServiceDef{Name: "test", DisplayName: "Test", ProcessName: "nonexistent-process-xyz"}
	if got := serviceDetailFromProc(context.Background(), def); got != "" {
		t.Errorf("expected empty string for service with PID 0, got %q", got)
	}
}

// TestServiceDetailFromProc_IncludesSince guards the legacy-mode start time:
// a live PID must yield a "since <timestamp>", never a missing or "n/a" value.
func TestServiceDetailFromProc_IncludesSince(t *testing.T) {
	result := serviceDetailFromProc(context.Background(), livePidFileDef(t))

	if !strings.HasPrefix(result, "(pid "+strconv.Itoa(os.Getpid())+", since ") || strings.Contains(result, "n/a") {
		t.Errorf("expected detail to carry pid and a real start time, got %q", result)
	}
}

func TestProcStartTime(t *testing.T) {
	if since, ok := procStartTime(os.Getpid()); !ok || since == "" {
		t.Errorf("procStartTime(self) = %q, %v; want a timestamp", since, ok)
	}

	if since, ok := procStartTime(-1); ok {
		t.Errorf("procStartTime(-1) = %q, true; want not-ok", since)
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

func TestShortErr(t *testing.T) {
	if got := shortErr(errors.New("short error")); got != "short error" {
		t.Errorf("got %q", got)
	}

	if got := shortErr(errors.New("line1\nline2\nline3")); got != "line1 line2 line3" {
		t.Errorf("got %q", got)
	}

	long := strings.Repeat("x", 70) + "\n" + strings.Repeat("y", 70)
	if got := shortErr(errors.New(long)); len(got) != 140 || !strings.HasSuffix(got, "...") || strings.Contains(got, "\n") {
		t.Errorf("long multiline: got %q", got)
	}

	if got := shortErr(errors.New(strings.Repeat("x", 150))); len(got) != 140 || !strings.HasSuffix(got, "...") {
		t.Errorf("long single line: got %q", got)
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

// TestAdvancedJARsPresent drives both branches through CARBONIO_BASE_DIR.
func TestAdvancedJARsPresent(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	if advancedJARsPresent() {
		t.Error("missing ext dir must not count as Advanced installed")
	}

	fakeAdvancedInstall(t)

	if !advancedJARsPresent() {
		t.Error("carbonio-advanced-*.jar must count as Advanced installed")
	}
}

func TestAdvanced_NoJarsPrintsNothing(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	c, out := newConsole()
	Advanced(context.Background(), c)

	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out)
	}
}

func stubLocalConfig(t *testing.T, fn func() (map[string]string, error)) {
	t.Helper()

	orig := loadLocalConfigFn
	t.Cleanup(func() { loadLocalConfigFn = orig })

	loadLocalConfigFn = fn
}

func TestAdvanced_LocalconfigFailure(t *testing.T) {
	fakeAdvancedInstall(t)
	stubLocalConfig(t, func() (map[string]string, error) { return nil, errors.New("localconfig.xml missing") })

	c, out := newConsole()
	Advanced(context.Background(), c)

	want := "\n\tCarbonio Advanced\n\tmodule status unavailable: localconfig.xml missing\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestAdvanced_ProbeFailure(t *testing.T) {
	fakeAdvancedInstall(t)

	// Port 1 has no listener, so the probe fails fast instead of waiting out
	// the client's timeout.
	stubLocalConfig(t, func() (map[string]string, error) {
		return map[string]string{
			"zimbra_ldap_user":     "zimbra",
			"zimbra_ldap_password": "x",
			"mailboxd_admin_port":  "1",
		}, nil
	})

	c, out := newConsole()
	Advanced(context.Background(), c)

	if !strings.HasPrefix(out.String(), "\n\tCarbonio Advanced\n") {
		t.Errorf("expected the Advanced section header, got %q", out)
	}

	if !strings.Contains(out.String(), "module status unavailable") && !strings.Contains(out.String(), "is not running") {
		t.Errorf("expected a failure line, got %q", out)
	}
}
