// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
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

	oldEUID, oldGAL, oldKey, oldAuth := geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys

	t.Cleanup(func() {
		geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys = oldEUID, oldGAL, oldKey, oldAuth
	})

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

			s := RunSSHKeygen(context.Background(), sysenv().System(), &out, &errb)

			return s, out.String(), errb.String()
		},
		"update-authkeys": func(t *testing.T) (int, string, string) {
			t.Helper()

			var out, errb bytes.Buffer

			s := RunUpdateAuthKeys(context.Background(), sysenv().System(), &out, &errb)

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
