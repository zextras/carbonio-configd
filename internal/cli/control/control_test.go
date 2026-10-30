// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"context"
	"errors"
	"os/user"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const testBase = "/zx"

// testOrchestrator returns an orchestrator over fake host operations in
// legacy (non-systemd) mode. registry replaces the default service registry;
// lc is the localconfig every loader of the Controller returns.
func testOrchestrator(
	registry map[string]*services.ServiceDef, lc map[string]string,
) (*orchestrator, *systest.Fakes) {
	f := systest.New()
	f.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, "")) // legacy mode

	load := func() (map[string]string, error) { return lc, nil }
	ctl := services.NewController(f.System(),
		services.WithBaseDir(testBase),
		services.WithConfigLoader(load),
		services.WithRawConfigLoader(load),
		services.WithRegistry(registry),
	)

	return &orchestrator{ctl: ctl, reporter: status.NewReporter(f.System(), ctl), svcStatus: ctl.ServiceStatus}, f
}

func newConsole() (*cliutil.Console, *bytes.Buffer) {
	var out bytes.Buffer

	return cliutil.NewConsole(&out, &bytes.Buffer{}), &out
}

func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	for _, action := range []Action{ActionStart, ActionStop, ActionRestart, ActionStatus} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{Action: action})

			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}

			if want := "Please run this program as the 'zextras' user.\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

// TestRun_StatusPropagatesNonZeroRC: legacy doStatus exits non-zero when a
// service is stopped, with no "Error:" line.
func TestRun_StatusPropagatesNonZeroRC(t *testing.T) {
	t.Parallel()

	const proc = "/usr/bin/fake-configd"

	for _, tc := range []struct {
		name    string
		running bool
		want    int
	}{
		{"all running", true, 0},
		{"one stopped", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o, f := testOrchestrator(
				map[string]*services.ServiceDef{
					"configd": {Name: "configd", DisplayName: "configd", ProcessName: proc},
				},
				map[string]string{"zimbra_server_hostname": "host.example.com"},
			)

			// Discovery falls back to the cache: LDAP is not configured.
			f.FS.Put(testBase+"/log/.zmcontrol.cache", []byte("configd\n"), 0o600)

			if tc.running {
				f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{proc}, Exe: proc, Comm: "fake-configd"})
			}

			var stdout, stderr bytes.Buffer

			got := o.run(context.Background(), &stdout, &stderr, &Options{Action: ActionStatus})
			if got != tc.want {
				t.Errorf("run = %d, want %d (stdout %q)", got, tc.want, stdout.String())
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestRun_UnknownAction(t *testing.T) {
	t.Parallel()

	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})

	var stdout, stderr bytes.Buffer

	code := o.run(context.Background(), &stdout, &stderr, &Options{Action: "bogus"})

	if code != 1 || stderr.String() != "Error: unknown control action: \"bogus\"\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestStatusForStart_ErrorAborts(t *testing.T) {
	t.Parallel()

	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	o.svcStatus = func(context.Context, string) (bool, error) { return false, errors.New("exec failed") }

	c, out := newConsole()

	if _, err := o.statusForStart(context.Background(), c, "ldap"); err == nil {
		t.Fatal("expected error on status failure")
	}

	if want := "\tWARNING: Cannot determine status of ldap: exec failed\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestRunningForStop_ErrorStillStops(t *testing.T) {
	t.Parallel()

	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	o.svcStatus = func(context.Context, string) (bool, error) { return false, errors.New("exec failed") }

	c, _ := newConsole()

	if !o.runningForStop(context.Background(), c, "mta") {
		t.Fatal("status error must not skip stop")
	}

	o.svcStatus = func(context.Context, string) (bool, error) { return false, nil }
	if o.runningForStop(context.Background(), c, "mta") {
		t.Fatal("plain stopped must skip")
	}
}

func TestStopLDAPIfLocal(t *testing.T) {
	t.Parallel()

	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{
		"zimbra_server_hostname": "mbox.example.com",
		"ldap_url":               "ldap://ldap.example.com:389",
	})
	c, _ := newConsole()

	// enabledSet excluding ldap: returns 0 without touching any service.
	if rc := o.stopLDAPIfLocal(t.Context(), c, map[string]bool{"mta": true, "proxy": true}); rc != 0 {
		t.Errorf("rc = %d, want 0 when ldap is not enabled", rc)
	}

	// The LDAP URL does not name this host: LDAP is not local.
	if o.ctl.IsLDAPLocal() {
		t.Fatal("LDAP must not be local when ldap_url names another host")
	}

	if rc := o.stopLDAPIfLocal(t.Context(), c, nil); rc != 0 {
		t.Errorf("rc = %d, want 0 when LDAP is not local", rc)
	}
}

func TestStartEnabledServices_SkipsUnknownLDAPAndConfigd(t *testing.T) {
	t.Parallel()

	o, f := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	c, out := newConsole()

	enabled := map[string]bool{"nonexistent-service-xyz": true, "ldap": true, "configd": true}

	if rc := o.startEnabledServices(context.Background(), c, enabled, 100); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}

	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out)
	}

	if calls := f.Exec.Lines(); len(calls) != 0 {
		t.Errorf("no service may be touched, got %q", calls)
	}
}

// TestHeaderPrintedOnceAcrossRestart: restart's stop and start share one
// Console, so the Host header appears once.
func TestHeaderPrintedOnceAcrossRestart(t *testing.T) {
	t.Parallel()

	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	o.svcStatus = func(context.Context, string) (bool, error) { return false, nil }

	c, out := newConsole()

	_ = o.controlStop(context.Background(), c)
	_ = o.controlStop(context.Background(), c)

	if n := bytes.Count(out.Bytes(), []byte("Host ")); n != 1 {
		t.Errorf("Host header printed %d times, want 1: %q", n, out)
	}
}
