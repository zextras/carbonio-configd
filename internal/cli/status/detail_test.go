// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const (
	testProc = "/usr/bin/fake-svc"
	testPID  = 4242
	testBase = "/zx"
)

// fixture is a Reporter over fake host operations.
type fixture struct {
	f   *systest.Fakes
	ctl *services.Controller
	r   *Reporter
}

// newFixture returns a fixture whose Controller serves registry. In systemd
// mode (strict) status goes through `systemctl is-active`; in legacy mode it
// goes through the process table.
func newFixture(systemd bool, registry map[string]*services.ServiceDef) *fixture {
	f := systest.New()
	if systemd {
		f.FS.PutDir("/run/systemd/system", 0o755) // sd_booted(3)
	} else {
		f.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, ""))
	}

	load := func() (map[string]string, error) { return map[string]string{}, nil }
	ctl := services.NewController(f.System(),
		services.WithBaseDir(testBase),
		services.WithConfigLoader(load),
		services.WithRawConfigLoader(load),
		services.WithRegistry(registry),
	)

	r := NewReporter(f.System(), ctl)
	r.loadLocalConfig = load

	return &fixture{f: f, ctl: ctl, r: r}
}

// svcRegistry returns a one-service registry for name, identified by testProc.
func svcRegistry(name string) map[string]*services.ServiceDef {
	return map[string]*services.ServiceDef{
		name: {Name: name, DisplayName: name, ProcessName: testProc, SystemdUnits: []string{name + ".service"}},
	}
}

// start puts the test process in the process table (legacy "running").
func (fx *fixture) start() {
	fx.f.Proc.Add(sys.ProcEntry{PID: testPID, Argv: []string{testProc}, Exe: testProc, Comm: "fake-svc"})
	fx.f.FS.PutDir("/proc/"+strconv.Itoa(testPID), 0o555)
}

// inactive makes every `systemctl is-active` exit 3 (systemd mode "stopped").
func (fx *fixture) inactive() {
	fx.f.Exec.OnCmdline("/usr/bin/systemctl is-active", systest.Fail(3, "inactive"))
}

// newConsole returns a console writing to buffers, and the stdout buffer.
func newConsole() (*cliutil.Console, *bytes.Buffer) {
	var out bytes.Buffer

	return cliutil.NewConsole(&out, &bytes.Buffer{}), &out
}

// TestShowServiceDetail_LegacyReportsPidAndSince is the regression guard for
// the "Since: n/a" bug: with no Carbonio target enabled the detail must come
// from /proc, never from systemctl show of a unit that may not even exist.
func TestShowServiceDetail_LegacyReportsPidAndSince(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))
	fx.start()

	c, out := newConsole()
	fx.r.showServiceDetail(context.Background(), c, fx.ctl.LookupService("test"))

	if !strings.Contains(out.String(), "  PID: "+strconv.Itoa(testPID)+"\n") {
		t.Errorf("output missing PID: %q", out)
	}

	if !strings.Contains(out.String(), "  Since: ") {
		t.Errorf("output missing start time: %q", out)
	}

	if strings.Contains(out.String(), "n/a") {
		t.Errorf("start time must never render as n/a: %q", out)
	}

	for _, line := range fx.f.Exec.Lines() {
		if strings.HasPrefix(line, "systemctl show") {
			t.Errorf("legacy detail must not query systemctl show: %q", line)
		}
	}
}

// TestShowServiceDetail_NoPidPrintsNothing covers the not-resolvable case:
// no pidfile, no matching process, so there is no detail to print.
func TestShowServiceDetail_NoPidPrintsNothing(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))

	c, out := newConsole()
	fx.r.showServiceDetail(context.Background(), c, fx.ctl.LookupService("test"))

	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out)
	}
}

// TestShowUnitDetail_UnknownUnitPrintsNoSince asserts systemd's "n/a"
// placeholder for a never-activated unit is suppressed rather than displayed.
func TestShowUnitDetail_UnknownUnitPrintsNoSince(t *testing.T) {
	t.Parallel()

	fx := newFixture(true, svcRegistry("test"))
	fx.f.Exec.OnCmdline("systemctl show", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("MainPID=0\nActiveEnterTimestamp=n/a\nMemoryCurrent=[not set]\n")}, nil
	})

	c, out := newConsole()
	fx.r.showUnitDetail(context.Background(), c, "carbonio-nonexistent-for-test.service")

	if out.Len() != 0 {
		t.Errorf("unknown unit must yield no detail lines, got %q", out)
	}
}

func TestShowUnitDetail_PrintsProperties(t *testing.T) {
	t.Parallel()

	fx := newFixture(true, svcRegistry("test"))
	fx.f.Exec.OnCmdline("systemctl show", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("MainPID=456\nActiveEnterTimestamp=Mon 2026-01-05 10:00:00 UTC\nMemoryCurrent=1024\n")}, nil
	})

	c, out := newConsole()
	fx.r.showUnitDetail(context.Background(), c, "x.service")

	want := "  PID: 456\n  Since: Mon 2026-01-05 10:00:00 UTC\n  Memory: 1024\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestPrintServiceStatus_UnknownService(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))

	c, out := newConsole()

	err := fx.r.PrintServiceStatus(context.Background(), c, "no-such-service")
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
	t.Parallel()

	fx := newFixture(false, svcRegistry("cbpolicyd"))

	c, out := newConsole()

	err := fx.r.PrintServiceStatus(context.Background(), c, "cbpolicyd")
	if err == nil || err.Error() != "service cbpolicyd is not running" {
		t.Fatalf("err = %v, want 'service cbpolicyd is not running'", err)
	}

	if got, want := out.String(), "cbpolicyd is not running.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestPrintServiceStatus_ProbeError(t *testing.T) {
	t.Parallel()

	fx := newFixture(true, svcRegistry("cbpolicyd"))
	fx.f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/usr/bin/systemctl" && c.Args[0] == "is-active" },
		errors.New("boom"))

	c, out := newConsole()

	err := fx.r.PrintServiceStatus(context.Background(), c, "cbpolicyd")
	if err == nil || !strings.HasPrefix(err.Error(), "failed to get status for service cbpolicyd: ") ||
		!strings.HasSuffix(err.Error(), ": boom") {
		t.Fatalf("err = %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("unexpected output %q", out)
	}
}

// TestPrintServiceStatus_RunningShowsDetail covers the running branch.
func TestPrintServiceStatus_RunningShowsDetail(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("cbpolicyd"))
	fx.start()

	c, out := newConsole()

	if err := fx.r.PrintServiceStatus(context.Background(), c, "cbpolicyd"); err != nil {
		t.Fatalf("PrintServiceStatus = %v, want nil", err)
	}

	want := "cbpolicyd is running.\n  PID: " + strconv.Itoa(testPID) + "\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Errorf("output = %q, want prefix %q", out, want)
	}
}

// TestShowServiceDetail_SystemdModeQueriesUnit covers the strict-systemd
// branch: the detail comes from `systemctl show` of each unit.
func TestShowServiceDetail_SystemdModeQueriesUnit(t *testing.T) {
	t.Parallel()

	fx := newFixture(true, svcRegistry("test"))
	fx.f.Exec.OnCmdline("systemctl show", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("MainPID=77\nActiveEnterTimestamp=Mon 2026-01-05 10:00:00 UTC\n")}, nil
	})

	c, out := newConsole()
	fx.r.showServiceDetail(context.Background(), c, fx.ctl.LookupService("test"))

	if !strings.Contains(out.String(), "PID: 77") || !strings.Contains(out.String(), "Since: ") {
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
	t.Parallel()

	ctx := context.Background()
	fx := newFixture(false, svcRegistry("mta"))

	if got := fx.r.ServiceDetail(ctx, "mta", false); got != "" {
		t.Errorf("not running: got %q, want empty", got)
	}

	if got := fx.r.ServiceDetail(ctx, "nonexistent-xyz", true); got != "" {
		t.Errorf("unknown service: got %q, want empty", got)
	}

	fx.start()

	want := "(pid " + strconv.Itoa(testPID) + ", since "
	if got := fx.r.ServiceDetail(ctx, "mta", true); !strings.HasPrefix(got, want) || strings.Contains(got, "n/a") {
		t.Errorf("running legacy service: got %q, want prefix %q and a real start time", got, want)
	}
}

func TestServiceDetailFromSystemd(t *testing.T) {
	t.Parallel()

	for _, units := range [][]string{nil, {}} {
		fx := newFixture(true, svcRegistry("test"))
		def := &services.ServiceDef{Name: "test", DisplayName: "Test", SystemdUnits: units}

		if got := fx.r.serviceDetailFromSystemd(context.Background(), def); got != "" {
			t.Errorf("units %v: got %q, want empty", units, got)
		}
	}

	fx := newFixture(true, svcRegistry("test"))
	fx.f.Exec.OnCmdline("systemctl show", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("MainPID=77\nActiveEnterTimestamp=Mon 2026-01-05 10:00:00 UTC\n")}, nil
	})

	got := fx.r.serviceDetailFromSystemd(context.Background(), fx.ctl.LookupService("test"))
	if want := "(pid 77, since Mon 2026-01-05 10:00:00 UTC)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestServiceDetailFromProc_ZeroPID(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))

	if got := fx.r.serviceDetailFromProc(context.Background(), fx.ctl.LookupService("test")); got != "" {
		t.Errorf("expected empty string for service with PID 0, got %q", got)
	}
}

// TestServiceDetailFromProc_IncludesSince guards the legacy-mode start time:
// a live PID must yield a "since <timestamp>", never a missing or "n/a" value.
func TestServiceDetailFromProc_IncludesSince(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))
	fx.start()

	result := fx.r.serviceDetailFromProc(context.Background(), fx.ctl.LookupService("test"))

	if !strings.HasPrefix(result, "(pid "+strconv.Itoa(testPID)+", since ") || strings.Contains(result, "n/a") {
		t.Errorf("expected detail to carry pid and a real start time, got %q", result)
	}
}

func TestProcStartTime(t *testing.T) {
	t.Parallel()

	fx := newFixture(false, svcRegistry("test"))
	fx.start()

	if since, ok := fx.r.procStartTime(testPID); !ok || since == "" {
		t.Errorf("procStartTime(live) = %q, %v; want a timestamp", since, ok)
	}

	if since, ok := fx.r.procStartTime(-1); ok {
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

// fakeAdvancedInstall makes the fake filesystem look like an Advanced install
// under CARBONIO_BASE_DIR=testBase, so advancedJARsPresent passes. It sets the
// environment, so callers must not be parallel.
func fakeAdvancedInstall(t *testing.T, fx *fixture) {
	t.Helper()

	t.Setenv("CARBONIO_BASE_DIR", testBase)

	extDir := testBase + "/lib/ext/carbonio"
	fx.f.FS.PutDir(extDir, 0o750)
	fx.f.FS.Put(extDir+"/carbonio-advanced-core-26.6.5.jar", nil, 0o600)
}

// TestAdvancedJARsPresent drives both branches through CARBONIO_BASE_DIR.
func TestAdvancedJARsPresent(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", testBase)

	fx := newFixture(false, svcRegistry("test"))

	if fx.r.advancedJARsPresent() {
		t.Error("missing ext dir must not count as Advanced installed")
	}

	fakeAdvancedInstall(t, fx)

	if !fx.r.advancedJARsPresent() {
		t.Error("carbonio-advanced-*.jar must count as Advanced installed")
	}
}

func TestAdvanced_NoJarsPrintsNothing(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", testBase)

	fx := newFixture(false, svcRegistry("test"))

	c, out := newConsole()
	fx.r.Advanced(context.Background(), c)

	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out)
	}
}

func TestAdvanced_LocalconfigFailure(t *testing.T) {
	fx := newFixture(false, svcRegistry("test"))
	fakeAdvancedInstall(t, fx)

	fx.r.loadLocalConfig = func() (map[string]string, error) { return nil, errors.New("localconfig.xml missing") }

	c, out := newConsole()
	fx.r.Advanced(context.Background(), c)

	want := "\n\tCarbonio Advanced\n\tmodule status unavailable: localconfig.xml missing\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestAdvanced_ProbeFailure(t *testing.T) {
	fx := newFixture(false, svcRegistry("test"))
	fakeAdvancedInstall(t, fx)

	// Port 1 has no listener, so the probe fails fast instead of waiting out
	// the client's timeout (network I/O stays real: zxadmin dials TCP).
	fx.r.loadLocalConfig = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_ldap_user":     "zimbra",
			"zimbra_ldap_password": "x",
			"mailboxd_admin_port":  "1",
		}, nil
	}

	c, out := newConsole()
	fx.r.Advanced(context.Background(), c)

	if !strings.HasPrefix(out.String(), "\n\tCarbonio Advanced\n") {
		t.Errorf("expected the Advanced section header, got %q", out)
	}

	if !strings.Contains(out.String(), "module status unavailable") && !strings.Contains(out.String(), "is not running") {
		t.Errorf("expected a failure line, got %q", out)
	}
}
