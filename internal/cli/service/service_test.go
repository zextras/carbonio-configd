// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"bytes"
	"context"
	"os/user"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestRun_MutatingActionsNotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	for _, action := range []Action{
		ActionStart, ActionStop, ActionRestart, ActionReload, ActionStartSystemd, ActionStopSystemd,
	} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{Action: action, Name: "mta"})

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

func TestRun_StatusNeedsNoPermission(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{Action: ActionStatus, Name: "no-such-service"})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if want := "Error: failed to get status for service no-such-service: unknown service: no-such-service\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRun_ListNeedsNoPermission(t *testing.T) {
	t.Parallel()

	env := systest.New().System()

	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), env, &stdout, &stderr, &Options{Action: ActionList}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	want := len(services.NewController(env).AllServiceNames())
	if n := bytes.Count(stdout.Bytes(), []byte("\n")); n != want {
		t.Errorf("listed %d rows, want %d: %q", n, want, stdout.String())
	}
}

func TestRun_UnknownServiceWrapsError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	opts := &Options{Action: ActionStop, Name: "no-such-service"}
	code := newRunner(systest.New().System(), opts).run(context.Background(), &stdout, &stderr, opts)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if got, prefix := stderr.String(), "Error: failed to stop service no-such-service: "; !strings.HasPrefix(got, prefix) {
		t.Errorf("stderr = %q, want prefix %q", got, prefix)
	}
}

func TestRun_UnknownAction(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	opts := &Options{Action: "bogus"}
	code := newRunner(systest.New().System(), opts).run(context.Background(), &stdout, &stderr, opts)

	if code != 1 || stderr.String() != "Error: unknown service action: \"bogus\"\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}

// TestNoRewrite_OnlyForStartAndRestart: --no-rewrite reaches the Controller
// built for the invocation only for start and restart.
func TestNoRewrite_OnlyForStartAndRestart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		action Action
		flag   bool
		want   bool
	}{
		{ActionStart, true, true},
		{ActionRestart, true, true},
		{ActionRestart, false, false},
		{ActionStop, true, false},
		{ActionReload, true, false},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()

			if got := noRewrite(&Options{Action: tc.action, NoRewrite: tc.flag}); got != tc.want {
				t.Errorf("NoRewrite = %v, want %v", got, tc.want)
			}
		})
	}
}
