// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// toolHarness replaces the functions the commands run and records the Env
// they were given.
type toolHarness struct {
	root   bool
	err    error
	calls  []string
	env    *setup.Env
	domain string
	server string
}

func newToolHarness(t *testing.T, root bool) *toolHarness {
	t.Helper()

	h := &toolHarness{root: root}

	oldEUID, oldGAL, oldKey, oldAuth, oldSyslog := geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys, setupSyslog
	oldUID, oldAuthAll, oldDH := zextrasUID, updateAuthKeysAll, installDHParam

	t.Cleanup(func() {
		geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys, setupSyslog = oldEUID, oldGAL, oldKey, oldAuth, oldSyslog
		zextrasUID, updateAuthKeysAll, installDHParam = oldUID, oldAuthAll, oldDH
	})

	zextrasUID = func() int { return 999 }
	updateAuthKeysAll = func(e *setup.Env) error {
		h.calls, h.env = append(h.calls, "authkeys -a"), e

		return h.err
	}
	installDHParam = func(e *setup.Env) error {
		h.calls, h.env = append(h.calls, "dhparam"), e

		return h.err
	}

	geteuid = func() int {
		if h.root {
			return 0
		}

		return 1000
	}
	createGalSyncAccount = func(e *setup.Env, domain, server string) (zxadmin.GalSyncAccount, error) {
		h.calls, h.env, h.domain, h.server = append(h.calls, "gal"), e, domain, server

		return zxadmin.GalSyncAccount{Name: "galsync.abc@" + domain, ID: "id-42"}, h.err
	}
	generateSSHKey = func(e *setup.Env) error {
		h.calls, h.env = append(h.calls, "keygen"), e

		return h.err
	}
	updateAuthKeys = func(e *setup.Env) error {
		h.calls, h.env = append(h.calls, "authkeys"), e

		return h.err
	}

	setupSyslog = func(e *setup.Env) error {
		h.calls, h.env = append(h.calls, "syslog"), e

		if h.err == nil {
			e.Log.Info("updateSyslog: Updating /etc/rsyslog.conf...done.")
		}

		return h.err
	}

	return h
}

type toolRun func(*testing.T) (status int, stdout, stderr string)

func toolRuns() map[string]toolRun {
	sysenv := func() *systest.Fakes { return systest.New() }

	return map[string]toolRun{
		"gal-sync-account": func(t *testing.T) (int, string, string) {
			t.Helper()

			var out, errb bytes.Buffer

			s := RunGalSyncAccount(context.Background(), sysenv().System(), &out, &errb, "example.com", "store.example.com")

			return s, out.String(), errb.String()
		},
		"ssh-keygen": func(t *testing.T) (int, string, string) {
			t.Helper()

			var out, errb bytes.Buffer

			s := RunSSHKeygen(context.Background(), sysenv().System(), &out, &errb, nil)

			return s, out.String(), errb.String()
		},
		"syslog": func(t *testing.T) (int, string, string) {
			t.Helper()

			var out, errb bytes.Buffer

			s := RunSyslog(context.Background(), sysenv().System(), &out, &errb, nil)

			return s, out.String(), errb.String()
		},
		"update-authkeys": func(t *testing.T) (int, string, string) {
			t.Helper()

			var out, errb bytes.Buffer

			s := RunUpdateAuthKeys(context.Background(), sysenv().System(), &out, &errb, nil)

			return s, out.String(), errb.String()
		},
	}
}

func TestTools_RefuseNonRootBeforeTouchingTheNode(t *testing.T) {
	for name, run := range toolRuns() {
		t.Run(name, func(t *testing.T) {
			h := newToolHarness(t, false)

			status, out, errb := run(t)

			assert.Equal(t, 1, status)
			assert.Empty(t, out)
			assert.Contains(t, errb, "carbonio-bootstrap "+name+" must run as root")
			assert.Empty(t, h.calls, "nothing runs")
		})
	}
}

func TestTools_ReportFailuresWithExitStatus1(t *testing.T) {
	for name, run := range toolRuns() {
		t.Run(name, func(t *testing.T) {
			h := newToolHarness(t, true)
			h.err = errors.New("fault account.NO_SUCH_DOMAIN: no such domain")

			status, out, errb := run(t)

			assert.Equal(t, 1, status)
			assert.Empty(t, out, "nothing is printed as a result")
			assert.Equal(t, "Error: fault account.NO_SUCH_DOMAIN: no such domain\n", errb)
		})
	}
}

func TestRunGalSyncAccount_PrintsNameAndIDLikeZmgsautil(t *testing.T) {
	h := newToolHarness(t, true)

	status, out, errb := toolRuns()["gal-sync-account"](t)

	require.Equal(t, 0, status, errb)
	assert.Equal(t, "galsync.abc@example.com\tid-42\n", out)
	assert.Equal(t, "example.com", h.domain)
	assert.Equal(t, "store.example.com", h.server)
}

func TestRunSSHKeygenAndUpdateAuthKeys_RunTheStepFunctionsOnTheNode(t *testing.T) {
	for name, want := range map[string]string{"ssh-keygen": "keygen", "update-authkeys": "authkeys"} {
		t.Run(name, func(t *testing.T) {
			h := newToolHarness(t, true)

			status, out, errb := toolRuns()[name](t)

			require.Equal(t, 0, status, errb)
			assert.Empty(t, out)
			assert.Equal(t, []string{want}, h.calls)
		})
	}
}

// The commands have no bootstrap configuration: the functions get the
// node's own paths and a log, and no Cfg to read.
func TestTools_EnvIsTheNodeWithoutAConfiguration(t *testing.T) {
	h := newToolHarness(t, true)

	status, _, errb := toolRuns()["ssh-keygen"](t)
	require.Equal(t, 0, status, errb)

	require.NotNil(t, h.env)
	assert.Equal(t, setup.DefaultPaths(), h.env.Paths)
	assert.Nil(t, h.env.Cfg)
	assert.NotNil(t, h.env.Log)
	assert.NotNil(t, h.env.Ctx)
}

func TestRunSyslog_PrintsWhatItDidLikeZmsyslogsetup(t *testing.T) {
	h := newToolHarness(t, true)

	status, out, errb := toolRuns()["syslog"](t)

	require.Equal(t, 0, status, errb)
	assert.Equal(t, "updateSyslog: Updating /etc/rsyslog.conf...done.\n", out)
	assert.Equal(t, []string{"syslog"}, h.calls)
}

func TestRunSyslog_TakesNoArguments(t *testing.T) {
	h := newToolHarness(t, true)

	var out, errb bytes.Buffer

	status := RunSyslog(context.Background(), systest.New().System(), &out, &errb, []string{"-x"})

	assert.Equal(t, 2, status)
	assert.Equal(t, "Usage: syslog\n", errb.String())
	assert.Empty(t, h.calls)
}

func TestRunSyslog_IsForRootOnly(t *testing.T) {
	h := newToolHarness(t, false)

	oldUID := zextrasUID
	zextrasUID = func() int { return 1000 }

	t.Cleanup(func() { zextrasUID = oldUID })

	status, _, errb := toolRuns()["syslog"](t)

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "must run as root")
	assert.Empty(t, h.calls, "zextras is refused")
}

// The commands that replace zmsshkeygen, zmupdateauthkeys and setup.pl's
// dhparam take the scripts' arguments, and zextras may run them as it ran
// the scripts (zmrc runs zmupdateauthkeys remotely as zextras).
func TestScriptCommands_ArgumentsAndUsers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		run    func(context.Context, sys.System, io.Writer, io.Writer, []string) int
		args   []string
		euid   int
		status int
		calls  []string
		errb   string
	}{
		{"keygen rsa", RunSSHKeygen, []string{"rsa"}, 999, 0, []string{"keygen"}, ""},
		{"keygen bad type", RunSSHKeygen, []string{"dsa"}, 0, 1, nil, "Bad keytype: dsa\n"},
		{"authkeys", RunUpdateAuthKeys, nil, 999, 0, []string{"authkeys"}, ""},
		{"authkeys everywhere", RunUpdateAuthKeys, []string{"-a"}, 0, 0, []string{"authkeys -a"}, ""},
		{"authkeys bad option", RunUpdateAuthKeys, []string{"-x"}, 0, 2, nil, "Usage: update-authkeys [-a]\n"},
		{"dhparam", RunDHParam, nil, 0, 0, []string{"dhparam"}, ""},
		{"dhparam argument", RunDHParam, []string{"2048"}, 0, 2, nil, "Usage: dhparam\n"},
		{"another user", RunUpdateAuthKeys, nil, 1000, 1, nil,
			"Error: carbonio-bootstrap update-authkeys must run as root or zextras\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newToolHarness(t, true)
			geteuid = func() int { return tc.euid }

			var out, errb bytes.Buffer

			status := tc.run(context.Background(), systest.New().System(), &out, &errb, tc.args)

			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.calls, h.calls)
			assert.Equal(t, tc.errb, errb.String())
		})
	}
}
