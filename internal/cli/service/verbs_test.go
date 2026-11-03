// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// legacyRunner is a runner over a fake host in legacy (non-systemd) mode with
// the given localconfig.
func legacyRunner(lc map[string]string, opts *Options) (*runner, *systest.Fakes) {
	f := systest.New()
	f.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, ""))

	load := func() (map[string]string, error) { return lc, nil }
	ctl := services.NewController(f.System(),
		services.WithBaseDir("/zx"),
		services.WithConfigLoader(load),
		services.WithLDAPProbe(func(context.Context, []string) error { return nil }),
		services.WithNoRewrite(noRewrite(opts)),
	)

	r := newRunner(f.System(), opts)
	r.ctl = ctl
	r.reporter = status.NewReporter(f.System(), ctl)

	return r, f
}

func runVerb(lc map[string]string, opts *Options) (int, string, string) {
	r, _ := legacyRunner(lc, opts)

	var stdout, stderr bytes.Buffer

	code := r.run(context.Background(), &stdout, &stderr, opts)

	return code, stdout.String(), stderr.String()
}

// An empty or unknown verb prints the usage line and fails (legacy
// `$0 start|stop|kill|restart|reload|status`).
func TestRun_UsageVerb(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runVerb(nil, &Options{Action: ActionUsage, Extra: []string{"update", "mta"}})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.HasPrefix(stderr, "Usage: configd service start|stop|kill|restart|reload|status|refresh|rotate") {
		t.Errorf("stderr = %q, want the usage line", stderr)
	}

	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// kill is stop: both run the same Controller method.
func TestRun_KillIsStop(t *testing.T) {
	t.Parallel()

	r, _ := legacyRunner(nil, &Options{Action: ActionKill, Name: "mta"})

	if r.mutations[ActionKill].verb != "kill" {
		t.Errorf("kill mutation verb = %q, want kill (error messages name the verb used)", r.mutations[ActionKill].verb)
	}

	code, _, stderr := runVerb(nil, &Options{Action: ActionKill, Name: "no-such-service"})

	if code != 1 || !strings.HasPrefix(stderr, "Error: failed to kill service no-such-service: ") {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

// C-08: norewrite is honored by start, restart and reload only.
func TestNoRewrite_StopAndKillIgnoreIt(t *testing.T) {
	t.Parallel()

	for _, a := range []Action{ActionStop, ActionKill, ActionStatus, ActionRefresh, ActionRotate} {
		if noRewrite(&Options{Action: a, NoRewrite: true}) {
			t.Errorf("%s honors norewrite", a)
		}
	}
}

// C-16: a stopped service prints "<name> is not running." and fails, with no
// "Error:" line on stderr.
func TestRun_StatusNotRunningHasNoErrorLine(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runVerb(nil, &Options{Action: ActionStatus, Name: "memcached"})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stdout, "is not running.") {
		t.Errorf("stdout = %q, want the not-running line", stdout)
	}

	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// C-16: antispam not enabled on this host: status says nothing and succeeds.
func TestRun_StatusAntispamNotEnabledIsSilentSuccess(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runVerb(nil, &Options{Action: ActionStatus, Name: "antispam"})

	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("code = %d, stdout = %q, stderr = %q; want 0 and no output", code, stdout, stderr)
	}

	// Same for a verb that would act: nothing to do.
	for _, a := range []Action{ActionStop, ActionKill} {
		if code, _, _ := runVerb(nil, &Options{Action: a, Name: "antispam"}); code != 0 {
			t.Errorf("%s antispam (disabled): exit code = %d, want 0", a, code)
		}
	}
}

// Refresh on a stopped milter is a no-op that says so (legacy zmmilterctl
// refresh): rc 0.
func TestRun_RefreshStoppedServiceSaysSoAndSucceeds(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runVerb(nil, &Options{Action: ActionRefresh, Name: "milter"})

	if code != 0 || stderr != "" {
		t.Errorf("code = %d, stderr = %q; want 0 and empty", code, stderr)
	}

	if !strings.Contains(stdout, "is not currently running") {
		t.Errorf("stdout = %q, want the not-currently-running line", stdout)
	}
}

func TestRun_RefreshAndRotateRejectUnsupportedServices(t *testing.T) {
	t.Parallel()

	for _, a := range []Action{ActionRefresh, ActionRotate} {
		code, _, stderr := runVerb(nil, &Options{Action: a, Name: "mta"})

		if code != 1 || !strings.HasPrefix(stderr, "Error: failed to "+string(a)+" service mta: ") {
			t.Errorf("%s mta: code = %d, stderr = %q", a, code, stderr)
		}
	}
}

// Rotating stats with no collector pidfile fails (legacy zmstatctl rotate).
func TestRun_RotateStatsWithoutPidfilesFails(t *testing.T) {
	t.Parallel()

	code, _, stderr := runVerb(nil, &Options{Action: ActionRotate, Name: "stats"})

	if code != 1 || !strings.Contains(stderr, services.ErrNoStatsPidFiles.Error()) {
		t.Errorf("code = %d, stderr = %q; want rc 1 and ErrNoStatsPidFiles", code, stderr)
	}
}

// C-21: on a replica whose ldap_url is its ldap_master_url every ldap verb but
// stop is refused with the legacy message and exit 1.
func TestRun_LDAPReplicaGuard(t *testing.T) {
	t.Parallel()

	lc := map[string]string{
		"ldap_is_master":  "false",
		"ldap_url":        "ldap://m.example.com:389",
		"ldap_master_url": "ldap://m.example.com:389",
	}
	const msg = "ldap_url and ldap_master_url cannot be the same on an ldap replica\n"

	for _, a := range []Action{ActionStart, ActionRestart, ActionReload, ActionStatus, ActionRefresh, ActionStartSystemd} {
		code, stdout, _ := runVerb(lc, &Options{Action: a, Name: "ldap"})

		if code != 1 || stdout != msg {
			t.Errorf("%s: code = %d, stdout = %q; want 1 and %q", a, code, stdout, msg)
		}
	}

	for _, a := range []Action{ActionStop, ActionKill, ActionStopSystemd} {
		r, _ := legacyRunner(lc, &Options{Action: a, Name: "ldap"})

		var stdout, stderr bytes.Buffer

		r.run(context.Background(), &stdout, &stderr, &Options{Action: a, Name: "ldap"})

		if strings.Contains(stdout.String(), "cannot be the same") {
			t.Errorf("%s must not be refused by the replica guard: %q", a, stdout.String())
		}
	}
}

func TestRun_LDAPReplicaGuardOnlyOnMisconfiguredReplica(t *testing.T) {
	t.Parallel()

	for name, lc := range map[string]map[string]string{
		"master":          {"ldap_is_master": "true", "ldap_url": "ldap://a", "ldap_master_url": "ldap://a"},
		"replica, differ": {"ldap_is_master": "false", "ldap_url": "ldap://b", "ldap_master_url": "ldap://a"},
	} {
		_, stdout, _ := runVerb(lc, &Options{Action: ActionStatus, Name: "ldap"})

		if strings.Contains(stdout, "cannot be the same") {
			t.Errorf("%s: refused: %q", name, stdout)
		}
	}

	// The guard is about ldap only.
	lc := map[string]string{"ldap_is_master": "false", "ldap_url": "ldap://a", "ldap_master_url": "ldap://a"}

	if _, stdout, _ := runVerb(lc, &Options{Action: ActionStatus, Name: "memcached"}); strings.Contains(stdout, "cannot be the same") {
		t.Errorf("guard applied to memcached: %q", stdout)
	}
}
