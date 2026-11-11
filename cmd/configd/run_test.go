// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// runConfigd runs "configd args..." in-process on a fake host with no Carbonio
// installation and returns the exit status and what it wrote.
func runConfigd(t *testing.T, args ...string) (status int, stdout, stderr string) {
	t.Helper()

	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	var out, errb bytes.Buffer

	status = run(append([]string{"configd"}, args...), systest.New().System(), nil, &out, &errb)

	return status, out.String(), errb.String()
}

const notZextras = "Please run this program as the 'zextras' user.\n"

// TestRun_ExitStatusAndStreams pins the process-level contract of every
// command's failure and success paths: the exit status and the exact text on
// stdout and stderr, since callers (the legacy zm* wrappers, systemd units,
// the Jenkins shell suite) read both.
func TestRun_ExitStatusAndStreams(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStatus int
		wantStdout string
		wantStderr string
	}{
		{"unknown flag is a parse error with the legacy status 1", []string{"--bogus"}, 1, "",
			"configd: error: unknown flag --bogus\n"},
		{"unknown command", []string{"nosuchcmd"}, 1, "", "configd: error: unexpected argument nosuchcmd\n"},
		{"rewrite needs a config name", []string{"rewrite"}, 1, "",
			"configd: error: expected \"<config-names> ...\"\n"},
		{"control needs a verb", []string{"control", "bogus"}, 1, "", "configd: error: unexpected argument bogus\n"},
		{"ldap gcf needs attributes", []string{"ldap", "gcf"}, 1, "", "configd: error: expected \"<attrs> ...\"\n"},
		{"service without a verb prints the usage", []string{"service"}, 1, "",
			"Usage: configd service start|stop|kill|restart|reload|status|refresh|rotate <service> [norewrite]\n"},
		{"service with an unknown verb prints the usage", []string{"service", "bogus", "x"}, 1, "",
			"Usage: configd service start|stop|kill|restart|reload|status|refresh|rotate <service> [norewrite]\n"},
		{"service status of an unknown service", []string{"service", "status", "nosuch"}, 1, "",
			"Error: failed to get status for service nosuch: unknown service: nosuch\n"},
		{"status of an unknown service", []string{"status", "nosuch"}, 1, "",
			"Error: failed to get status for service nosuch: unknown service: nosuch\n"},
		{"localconfig with an unknown output mode", []string{"localconfig", "-m", "bogus"}, 1, "",
			"Warning: local config file `/nonexistent-base/conf/localconfig.xml' is not readable\n" +
				"Error: failed to create writer bogus\n"},
		{"localconfig -p", []string{"localconfig", "-p"}, 0, "/nonexistent-base/conf/localconfig.xml\n",
			"Warning: local config file `/nonexistent-base/conf/localconfig.xml' is not readable\n"},
		{"proxy conf without nginx reports on stdout", []string{"proxy", "conf"}, 1,
			"Nginx not installed, exiting\n", ""},
		{"release", []string{"release"}, 0, "Carbonio Release \n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, stdout, stderr := runConfigd(t, tt.args...)

			assert.Equal(t, tt.wantStatus, status, "exit status")
			assert.Equal(t, tt.wantStdout, stdout, "stdout")
			assert.Equal(t, tt.wantStderr, stderr, "stderr")
		})
	}
}

// TestRun_HelpAndVersionExitZero pins that --help and --version end the run
// with status 0 and print on stdout.
func TestRun_HelpAndVersionExitZero(t *testing.T) {
	status, stdout, stderr := runConfigd(t, "--version")
	assert.Equal(t, 0, status)
	assert.Equal(t, formatVersion()+"\n", stdout)
	assert.Empty(t, stderr)

	status, stdout, stderr = runConfigd(t, "--help")
	assert.Equal(t, 0, status)
	assert.Contains(t, stdout, "Usage: configd <command> [flags]")
	assert.Empty(t, stderr)
}

// TestRun_RefusesANonZextrasCaller pins the status of every command that
// changes the host or talks to the daemon when run by another user: 1 (2 for
// plan, whose 1 means "would change") with the one permission line on stderr
// and nothing on stdout.
func TestRun_RefusesANonZextrasCaller(t *testing.T) {
	skipIfZextras(t)

	tests := []struct {
		name       string
		args       []string
		wantStatus int
	}{
		{"daemon", nil, 1},
		{"shadow", []string{"shadow", "--single-cycle"}, 1},
		{"rewrite", []string{"rewrite", "mta"}, 1},
		{"plan", []string{"plan"}, 2},
		{"control", []string{"control", "status"}, 1},
		{"tls", []string{"tls", "bogus"}, 1},
		{"init", []string{"init", "mta"}, 1},
		{"proxy enable", []string{"proxy", "enable", "bogus"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, stdout, stderr := runConfigd(t, tt.args...)

			assert.Equal(t, tt.wantStatus, status, "exit status")
			assert.Empty(t, stdout)
			assert.Equal(t, notZextras, stderr)
		})
	}
}

// TestRun_ErrorFromACommandIsPrintedOnceWithStatus1 pins the single place that
// turns a command's error into "Error: <err>" and status 1 (exitCode).
func TestRun_ErrorFromACommandIsPrintedOnceWithStatus1(t *testing.T) {
	status, stdout, stderr := runConfigd(t, "status", "nosuch")

	assert.Equal(t, 1, status)
	assert.Empty(t, stdout)
	assert.Equal(t, 1, bytes.Count([]byte(stderr), []byte("Error: ")))
}

// TestRun_WarnsWhenThePerlEnvironmentCannotBeSet pins that a failing Setenv is
// reported (once, on stderr) and does not stop the command.
func TestRun_WarnsWhenThePerlEnvironmentCannotBeSet(t *testing.T) {
	unsetEnv(t, "PERL5LIB")
	unsetEnv(t, "PERLLIB")
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	fakes := systest.New()
	// A NUL byte makes the value unsettable.
	fakes.Exec.OnPath("/usr/bin/perl", systest.Reply("archname='x86\x00linux';\n"))

	var out, errb bytes.Buffer

	status := run([]string{"configd", "release"}, fakes.System(), nil, &out, &errb)

	assert.Equal(t, 0, status, "the command still runs")
	assert.Equal(t, "Carbonio Release \n", out.String())
	assert.Contains(t, errb.String(), "Warning: perl environment not set: ")
	assert.Contains(t, errb.String(), "set PERLLIB")
	assert.Contains(t, errb.String(), "set PERL5LIB")
}

// deadlineExec records the deadline of the context every command runs under.
type deadlineExec struct {
	sys.Exec

	deadline time.Time
	hasDline bool
}

func (d *deadlineExec) Run(ctx context.Context, c *sys.Cmd) (sys.Result, error) {
	d.deadline, d.hasDline = ctx.Deadline()

	return d.Exec.Run(ctx, c)
}

// TestPerlArchname_IsBounded pins that a wedged perl cannot keep configd from
// starting: the probe runs under a deadline.
func TestPerlArchname_IsBounded(t *testing.T) {
	t.Parallel()

	fakes := systest.New()
	env := fakes.System()
	probe := &deadlineExec{Exec: env.Exec}
	env.Exec = probe

	start := time.Now()
	perlArchname(t.Context(), env)

	require.True(t, probe.hasDline, "perl -V:archname must run under a deadline")
	assert.LessOrEqual(t, probe.deadline.Sub(start), perlArchnameTimeout+time.Second)
}
