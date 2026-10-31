// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"context"
	"strings"
	"testing"
)

func TestRun_Summary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stopped     bool
		wantErr     string
		wantStopped bool
		wantRunning bool
	}{
		{name: "all running", wantRunning: true},
		{name: "some stopped", stopped: true, wantErr: "some services are not running", wantStopped: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fx := newFixture(true, svcRegistry("mta"))
			if tt.stopped {
				fx.inactive()
			}

			c, out := newConsole()

			err := run(context.Background(), fx.ctl, fx.r, c, &Options{})

			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("err = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}

			if got := out.String(); !strings.Contains(got, "Host ") ||
				strings.Contains(got, "Running") != tt.wantRunning || strings.Contains(got, "Stopped") != tt.wantStopped {
				t.Errorf("stdout = %q, running=%v stopped=%v expected", got, tt.wantRunning, tt.wantStopped)
			}
		})
	}
}

func TestRun_SummaryViaRun(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := runWithFakes("", true)

	if rc != 1 {
		t.Errorf("rc = %d, want 1 when services are inactive", rc)
	}

	if want := "Error: some services are not running\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	if !strings.Contains(stdout, "Stopped") {
		t.Errorf("stdout = %q, want Stopped rows", stdout)
	}
}
