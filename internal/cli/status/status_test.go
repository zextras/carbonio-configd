// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"bytes"
	"context"
	"testing"
)

func TestRun_UnknownService(t *testing.T) {
	stubProbes(t, false, false)

	var stdout, stderr bytes.Buffer

	rc := Run(context.Background(), &stdout, &stderr, &Options{Name: "no-such-service"})

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", &stdout)
	}

	if want := "Error: unknown service: no-such-service\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", &stderr, want)
	}
}

func TestRun_ServiceNotRunning(t *testing.T) {
	stubProbes(t, false, false)

	var stdout, stderr bytes.Buffer

	rc := Run(context.Background(), &stdout, &stderr, &Options{Name: "cbpolicyd"})

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if want := "Error: service cbpolicyd is not running\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", &stderr, want)
	}

	if stdout.Len() == 0 {
		t.Error("expected the 'is not running' line on stdout")
	}
}

func TestRun_ServiceRunning(t *testing.T) {
	stubProbes(t, true, false)

	var stdout, stderr bytes.Buffer

	rc := Run(context.Background(), &stdout, &stderr, &Options{Name: "cbpolicyd"})

	if rc != 0 || stderr.Len() != 0 {
		t.Errorf("rc = %d, stderr = %q; want 0 and empty", rc, &stderr)
	}

	if stdout.Len() == 0 {
		t.Error("expected the 'is running' line on stdout")
	}
}
