// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
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
	args   []string
	code   int
	stdin  string
}

func newToolHarness(t *testing.T, root bool) *toolHarness {
	t.Helper()

	h := &toolHarness{root: root}

	oldEUID, oldGAL, oldKey, oldAuth, oldSyslog := geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys, setupSyslog
	oldUID, oldAuthAll, oldDH, oldPC := zextrasUID, updateAuthKeysAll, installDHParam, runProxyConfig
	oldZmDH, oldPasswd, oldCertMgr := runDHParam, runLDAPPasswd, runCertMgr

	t.Cleanup(func() {
		geteuid, createGalSyncAccount, generateSSHKey, updateAuthKeys, setupSyslog = oldEUID, oldGAL, oldKey, oldAuth, oldSyslog
		zextrasUID, updateAuthKeysAll, installDHParam, runProxyConfig = oldUID, oldAuthAll, oldDH, oldPC
		runDHParam, runLDAPPasswd, runCertMgr = oldZmDH, oldPasswd, oldCertMgr
	})

	script := func(name string) func(*setup.Env, []string, io.Reader, io.Writer, io.Writer) int {
		return func(e *setup.Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			h.calls, h.env, h.args = append(h.calls, name), e, args

			in, _ := io.ReadAll(stdin)
			h.stdin = string(in)

			_, _ = io.WriteString(stdout, name+" out\n")
			_, _ = io.WriteString(stderr, name+" err\n")

			return h.code
		}
	}

	runDHParam, runLDAPPasswd = script("zmdhparam"), script("zmldappasswd")

	zextrasUID = func() int { return 999 }
	runProxyConfig = func(e *setup.Env, args []string, stdout, stderr io.Writer) int {
		h.calls, h.env, h.args = append(h.calls, "proxyconfig"), e, args

		_, _ = io.WriteString(stdout, "out\n")
		_, _ = io.WriteString(stderr, "err\n")

		return h.code
	}
	runCertMgr = func(e *setup.Env, args []string, stdout, stderr io.Writer) int {
		h.calls, h.env, h.args = append(h.calls, "zmcertmgr"), e, args

		_, _ = io.WriteString(stdout, "zmcertmgr out\n")
		_, _ = io.WriteString(stderr, "zmcertmgr err\n")

		return h.code
	}
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

// The commands that replace zmsshkeygen and zmupdateauthkeys take the scripts' arguments, and zextras may run them as it ran
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

// proxyconfig has the script's own exit status and output, so it is not one of
// toolRuns (whose failures are "Error: ..." and 1).
func TestRunProxyConfig_PassesTheOptionsAndTheScriptsStatus(t *testing.T) {
	for _, root := range []bool{true, false} {
		h := newToolHarness(t, root)
		h.code = 2
		zextrasUID = func() int { return 1000 } // the caller is zextras when not root

		var out, errb bytes.Buffer

		status := RunProxyConfig(context.Background(), systest.New().System(), &out, &errb, []string{"-m", "-e", "-H", "h"})

		assert.Equal(t, 2, status, "the status of the port, root %v", root)
		assert.Equal(t, "out\n", out.String())
		assert.Equal(t, "err\n", errb.String())
		assert.Equal(t, []string{"-m", "-e", "-H", "h"}, h.args)
		assert.Nil(t, h.env.Cfg)
	}
}

func TestRunProxyConfig_RefusesOtherUsers(t *testing.T) {
	h := newToolHarness(t, false)

	var out, errb bytes.Buffer

	status := RunProxyConfig(context.Background(), systest.New().System(), &out, &errb, []string{"-h"})

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "carbonio-bootstrap proxyconfig must run as root or zextras")
	assert.Empty(t, h.calls)
}

// "setup dhparam install" is the step of setup.pl; every other command line,
// none included, is zmdhparam, which keeps its output and exit status, and
// stdin reaches it.
func TestRunDHParam_StepWithInstallScriptWithTheRest(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		h := newToolHarness(t, true)

		var out, errb bytes.Buffer

		status := RunDHParam(context.Background(), systest.New().System(), strings.NewReader("in"), &out, &errb,
			[]string{"install"})

		assert.Equal(t, 0, status)
		assert.Equal(t, []string{"dhparam"}, h.calls)
		assert.Empty(t, out.String())
		assert.Empty(t, errb.String())
	})

	// bin/zmdhparam passes its arguments through, and the script asked for a
	// command when it had none: writing the parameters silently was a change.
	t.Run("no arguments is zmdhparam, which asks for a command", func(t *testing.T) {
		h := newToolHarness(t, true)

		var out, errb bytes.Buffer

		status := RunDHParam(context.Background(), systest.New().System(), strings.NewReader("in"), &out, &errb, nil)

		assert.Equal(t, 0, status, "the status of the port")
		assert.Equal(t, []string{"zmdhparam"}, h.calls)
		assert.Empty(t, h.args)
	})

	for _, root := range []bool{true, false} {
		t.Run("zmdhparam", func(t *testing.T) {
			h := newToolHarness(t, root)
			h.code = 1
			zextrasUID = func() int { return 1000 } // the caller is zextras when not root

			var out, errb bytes.Buffer

			status := RunDHParam(context.Background(), systest.New().System(), strings.NewReader("PEM"), &out, &errb,
				[]string{"set", "-in", "-"})

			assert.Equal(t, 1, status, "the status of the port")
			assert.Equal(t, []string{"zmdhparam"}, h.calls)
			assert.Equal(t, []string{"set", "-in", "-"}, h.args)
			assert.Equal(t, "PEM", h.stdin)
			assert.Equal(t, "zmdhparam out\n", out.String())
			assert.Equal(t, "zmdhparam err\n", errb.String())
			assert.Nil(t, h.env.Cfg)
		})
	}

	t.Run("other users", func(t *testing.T) {
		h := newToolHarness(t, false)

		var out, errb bytes.Buffer

		status := RunDHParam(context.Background(), systest.New().System(), strings.NewReader(""), &out, &errb,
			[]string{"get"})

		assert.Equal(t, 1, status)
		assert.Equal(t, "Error: carbonio-bootstrap dhparam must run as root or zextras\n", errb.String())
		assert.Empty(t, h.calls)
	})
}

func TestRunLDAPPasswd_PassesTheOptionsAndStdin(t *testing.T) {
	for _, root := range []bool{true, false} {
		h := newToolHarness(t, root)
		h.code = 255
		zextrasUID = func() int { return 1000 }

		var out, errb bytes.Buffer

		status := RunLDAPPasswd(context.Background(), systest.New().System(), strings.NewReader("secret\n"), &out, &errb,
			[]string{"-c", "-l"})

		assert.Equal(t, 255, status, "the status of the port, root %v", root)
		assert.Equal(t, []string{"zmldappasswd"}, h.calls)
		assert.Equal(t, []string{"-c", "-l"}, h.args, "the password is not an argument")
		assert.Equal(t, "secret\n", h.stdin)
		assert.Equal(t, "zmldappasswd out\n", out.String())
		assert.Nil(t, h.env.Cfg)
	}
}

func TestRunLDAPPasswd_RefusesOtherUsers(t *testing.T) {
	h := newToolHarness(t, false)

	var out, errb bytes.Buffer

	status := RunLDAPPasswd(context.Background(), systest.New().System(), strings.NewReader("secret\n"), &out, &errb,
		[]string{"-p"})

	assert.Equal(t, 1, status)
	assert.Equal(t, "Error: carbonio-bootstrap ldap-passwd must run as root or zextras\n", errb.String())
	assert.Empty(t, h.calls)
	assert.Empty(t, h.stdin, "the password of a refused caller is not read")
}

func TestRunCertMgr_PassesTheCommandLineAndTheScriptsStatus(t *testing.T) {
	for _, root := range []bool{true, false} {
		h := newToolHarness(t, root)
		h.code = 255
		zextrasUID = func() int { return 1000 }

		var out, errb bytes.Buffer

		status := RunCertMgr(context.Background(), systest.New().System(), &out, &errb,
			[]string{"deploycrt", "comm", "-deploy", "mta,proxy", "/tmp/c.crt", "/tmp/ca.crt"})

		assert.Equal(t, 255, status, "the status of the port, root %v", root)
		assert.Equal(t, "zmcertmgr out\n", out.String())
		assert.Equal(t, "zmcertmgr err\n", errb.String())
		assert.Equal(t, []string{"deploycrt", "comm", "-deploy", "mta,proxy", "/tmp/c.crt", "/tmp/ca.crt"}, h.args)
		assert.Equal(t, !root, h.env.IsZextras, "a zextras caller runs the tools without runuser")

		// The script prints no command lines: the Env logs to no file.
		h.env.Log.Detail("*** Running as zextras: probe")
		assert.Equal(t, "zmcertmgr err\n", errb.String())
	}
}

func TestRunCertMgr_RefusesOtherUsers(t *testing.T) {
	h := newToolHarness(t, false)

	var out, errb bytes.Buffer

	status := RunCertMgr(context.Background(), systest.New().System(), &out, &errb, []string{"viewdeployedcrt"})

	assert.Equal(t, 1, status)
	assert.Equal(t, "Error: carbonio-bootstrap certmgr must run as root or zextras\n", errb.String())
	assert.Empty(t, h.calls)
}

// A tool prints its progress on stdout and nothing of its internals on stderr:
// the scripts it replaces had no such lines.
func TestRunTool_DetailsStayOffStderr(t *testing.T) {
	newToolHarness(t, true)

	var out, errb bytes.Buffer

	status := runTool(context.Background(), systest.New().System(), &out, &errb, "x", func(e *setup.Env) error {
		e.Log.Info("progress")
		e.Log.Detail("internal command line")

		return nil
	})

	assert.Equal(t, 0, status)
	assert.Contains(t, out.String(), "progress")
	assert.NotContains(t, out.String(), "internal command line")
	assert.Empty(t, errb.String())
}

// ToolUsage is the text of the script, for the tools that print one when asked
// and for no other.
func TestToolUsage(t *testing.T) {
	paths := setup.DefaultPaths()

	assert.Equal(t, setup.LDAPPasswdUsage(paths.Path("bin", "zmldappasswd")), ToolUsage("ldap-passwd"))
	assert.Equal(t, setup.ProxyConfigUsage(paths.Path("libexec", "zmproxyconfig")), ToolUsage("proxyconfig"))
	assert.Equal(t, setup.RCUsage, ToolUsage("rc"))
	assert.Equal(t, setup.RCDUsage, ToolUsage("rcd"))

	for _, name := range []string{"prov", "ssh-keygen", "ldap-schema", "no-such-tool", ""} {
		assert.Empty(t, ToolUsage(name), name)
	}

	assert.Contains(t, ToolUsage("ldap-passwd"), "zmldappasswd")
}
