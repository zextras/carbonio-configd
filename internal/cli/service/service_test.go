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
)

func TestRun_MutatingActionsNotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	for _, action := range []Action{
		ActionStart, ActionStop, ActionRestart, ActionReload, ActionStartSystemd, ActionStopSystemd,
	} {
		t.Run(string(action), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(context.Background(), &stdout, &stderr, &Options{Action: action, Name: "mta"})

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
	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{Action: ActionStatus, Name: "no-such-service"})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if want := "Error: failed to get status for service no-such-service: unknown service: no-such-service\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRun_ListNeedsNoPermission(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), &stdout, &stderr, &Options{Action: ActionList}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	if n := bytes.Count(stdout.Bytes(), []byte("\n")); n != len(services.AllServiceNames()) {
		t.Errorf("listed %d rows, want %d: %q", n, len(services.AllServiceNames()), stdout.String())
	}
}

func TestRun_UnknownServiceWrapsError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), &stdout, &stderr, &Options{Action: ActionStop, Name: "no-such-service"})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if got, prefix := stderr.String(), "Error: failed to stop service no-such-service: "; !strings.HasPrefix(got, prefix) {
		t.Errorf("stderr = %q, want prefix %q", got, prefix)
	}
}

func TestRun_UnknownAction(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), &stdout, &stderr, &Options{Action: "bogus"})

	if code != 1 || stderr.String() != "Error: unknown service action: \"bogus\"\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestMutate_NoRewriteOnlyForStartAndRestart(t *testing.T) {
	orig := services.NoRewrite
	t.Cleanup(func() { services.NoRewrite = orig })

	services.NoRewrite = false

	_ = mutate(context.Background(), &Options{Action: ActionStart, Name: "no-such-service", NoRewrite: true})

	if !services.NoRewrite {
		t.Error("start must propagate NoRewrite")
	}

	_ = mutate(context.Background(), &Options{Action: ActionRestart, Name: "no-such-service", NoRewrite: false})

	if services.NoRewrite {
		t.Error("restart must propagate NoRewrite=false")
	}

	_ = mutate(context.Background(), &Options{Action: ActionStop, Name: "no-such-service", NoRewrite: true})

	if services.NoRewrite {
		t.Error("stop must leave NoRewrite alone")
	}
}
