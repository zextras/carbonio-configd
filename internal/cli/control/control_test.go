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
	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/services"
)

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
			var stdout, stderr bytes.Buffer

			code := Run(context.Background(), &stdout, &stderr, &Options{Action: action})

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
	orig := controlStatusFn
	t.Cleanup(func() { controlStatusFn = orig })

	for _, rc := range []int{0, 1} {
		controlStatusFn = func(context.Context, *cliutil.Console) int { return rc }

		var stdout, stderr bytes.Buffer

		if got := run(context.Background(), &stdout, &stderr, &Options{Action: ActionStatus}); got != rc {
			t.Errorf("run = %d, want %d", got, rc)
		}

		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty", stderr.String())
		}
	}
}

func TestRun_UnknownAction(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), &stdout, &stderr, &Options{Action: "bogus"})

	if code != 1 || stderr.String() != "Error: unknown control action: \"bogus\"\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestStatusForStart_ErrorAborts(t *testing.T) {
	old := controlSvcStatusFn
	controlSvcStatusFn = func(context.Context, string) (bool, error) { return false, errors.New("exec failed") }

	defer func() { controlSvcStatusFn = old }()

	c, out := newConsole()

	if _, err := statusForStart(context.Background(), c, "ldap"); err == nil {
		t.Fatal("expected error on status failure")
	}

	if want := "\tWARNING: Cannot determine status of ldap: exec failed\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestRunningForStop_ErrorStillStops(t *testing.T) {
	old := controlSvcStatusFn
	controlSvcStatusFn = func(context.Context, string) (bool, error) { return false, errors.New("exec failed") }

	defer func() { controlSvcStatusFn = old }()

	c, _ := newConsole()

	if !runningForStop(context.Background(), c, "mta") {
		t.Fatal("status error must not skip stop")
	}

	controlSvcStatusFn = func(context.Context, string) (bool, error) { return false, nil }
	if runningForStop(context.Background(), c, "mta") {
		t.Fatal("plain stopped must skip")
	}
}

func TestStopLDAPIfLocal(t *testing.T) {
	c, _ := newConsole()

	// enabledSet excluding ldap: returns 0 without touching any service.
	if rc := stopLDAPIfLocal(t.Context(), c, map[string]bool{"mta": true, "proxy": true}); rc != 0 {
		t.Errorf("rc = %d, want 0 when ldap is not enabled", rc)
	}

	if services.IsLDAPLocal() {
		t.Skip("LDAP is local on this host")
	}

	if rc := stopLDAPIfLocal(t.Context(), c, nil); rc != 0 {
		t.Errorf("rc = %d, want 0 when LDAP is not local", rc)
	}
}

func TestStartEnabledServices_SkipsUnknownLDAPAndConfigd(t *testing.T) {
	c, out := newConsole()

	enabled := map[string]bool{"nonexistent-service-xyz": true, "ldap": true, "configd": true}

	if rc := startEnabledServices(context.Background(), c, enabled, 100); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}

	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestHeaderPrintedOnceAcrossRestart: restart's stop and start share one
// Console, so the Host header appears once.
func TestHeaderPrintedOnceAcrossRestart(t *testing.T) {
	c, out := newConsole()

	orig := controlSvcStatusFn
	t.Cleanup(func() { controlSvcStatusFn = orig })

	controlSvcStatusFn = func(context.Context, string) (bool, error) { return false, nil }

	_ = controlStop(context.Background(), c)
	_ = controlStop(context.Background(), c)

	if n := bytes.Count(out.Bytes(), []byte("Host ")); n != 1 {
		t.Errorf("Host header printed %d times, want 1: %q", n, out)
	}
}
