// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"bytes"
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// runWithFakes runs "configd status" over fake host operations in systemd mode
// (the fake systemctl reports every unit active unless inactive is set).
func runWithFakes(name string, inactive bool) (rc int, stdout, stderr string) {
	f := systest.New()
	f.FS.PutDir("/run/systemd/system", 0o755) // sd_booted(3)

	if inactive {
		f.Exec.OnCmdline("/usr/bin/systemctl is-active", systest.Fail(3, "inactive"))
	}

	var out, errOut bytes.Buffer

	rc = Run(context.Background(), f.System(), &out, &errOut, &Options{Name: name})

	return rc, out.String(), errOut.String()
}

func TestRun_UnknownService(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := runWithFakes("no-such-service", false)

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	if want := "Error: failed to get status for service no-such-service: unknown service: no-such-service\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestRun_ServiceNotRunning(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := runWithFakes("cbpolicyd", true)

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if want := "Error: service cbpolicyd is not running\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	if stdout == "" {
		t.Error("expected the 'is not running' line on stdout")
	}
}

func TestRun_ServiceRunning(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := runWithFakes("cbpolicyd", false)

	if rc != 0 || stderr != "" {
		t.Errorf("rc = %d, stderr = %q; want 0 and empty", rc, stderr)
	}

	if want := services.NewController(systest.New().System()).LookupService("cbpolicyd").DisplayName + " is running.\n"; len(stdout) < len(want) || stdout[:len(want)] != want {
		t.Errorf("stdout = %q, want prefix %q", stdout, want)
	}
}
