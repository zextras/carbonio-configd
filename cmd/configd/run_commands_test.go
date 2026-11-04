// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// skipIfZextras skips tests that rely on the commands refusing a caller that
// is not the zextras user.
func skipIfZextras(t *testing.T) {
	t.Helper()

	if cliutil.RequireZextras(io.Discard) {
		t.Skip("running as the zextras user, the permission gate does not trigger")
	}
}

// TestCommands_RefuseANonZextrasCaller pins that every command that changes
// the host or talks to the daemon stops with status 1 for any other user,
// before it touches the system.
func TestCommands_RefuseANonZextrasCaller(t *testing.T) {
	skipIfZextras(t)

	cli := &CLI{}

	tests := []struct {
		name string
		run  func(env sys.System) error
	}{
		{"daemon", func(env sys.System) error { return (&DaemonCmd{}).Run(cli, env, discardStdio()) }},
		{"shadow", func(env sys.System) error { return (&ShadowCmd{Once: true}).Run(cli, env, discardStdio()) }},
		{"rewrite", func(env sys.System) error {
			return (&RewriteCmd{ConfigNames: []string{"mta"}}).Run(cli, env, discardStdio())
		}},
		{"plan", func(env sys.System) error { return (&PlanCmd{JSON: true}).Run(cli, env, discardStdio()) }},
		{"service start", func(env sys.System) error { return (&ServiceStartCmd{Name: "mta"}).Run(env, discardStdio()) }},
		{"service start norewrite", func(env sys.System) error {
			return (&ServiceStartCmd{Name: "mta", Extra: []string{norewriteArg}}).Run(env, discardStdio())
		}},
		{"service stop", func(env sys.System) error { return (&ServiceStopCmd{Name: "mta"}).Run(env, discardStdio()) }},
		{"service restart", func(env sys.System) error {
			return (&ServiceRestartCmd{Name: "mta", NoRewrite: true}).Run(env, discardStdio())
		}},
		{"service kill", func(env sys.System) error { return (&ServiceKillCmd{Name: "mta"}).Run(env, discardStdio()) }},
		{"service refresh", func(env sys.System) error {
			return (&ServiceRefreshCmd{Name: "milter"}).Run(env, discardStdio())
		}},
		{"service rotate", func(env sys.System) error {
			return (&ServiceRotateCmd{Name: "stats"}).Run(env, discardStdio())
		}},
		{"service reload", func(env sys.System) error { return (&ServiceReloadCmd{Name: "mta"}).Run(env, discardStdio()) }},
		{"service start-systemd", func(env sys.System) error {
			return (&ServiceStartSystemdCmd{Name: "mta"}).Run(env, discardStdio())
		}},
		{"service stop-systemd", func(env sys.System) error {
			return (&ServiceStopSystemdCmd{Name: "mta"}).Run(env, discardStdio())
		}},
		{"proxy gen", func(env sys.System) error { return (&ProxyGenCmd{}).Run(env, discardStdio()) }},
		{"proxy rewrite", func(env sys.System) error { return (&ProxyRewriteCmd{}).Run(env, discardStdio()) }},
		{"proxy enable", func(env sys.System) error { return (&ProxyEnableCmd{Protocol: "imap"}).Run(env, discardStdio()) }},
		{"proxy disable", func(env sys.System) error { return (&ProxyDisableCmd{Protocol: "imap"}).Run(env, discardStdio()) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakes := systest.New()

			err := tt.run(fakes.System())

			status, ok := errors.AsType[exitStatus](err)
			require.True(t, ok, "got %v", err)
			assert.Positive(t, int(status))
			assert.Empty(t, fakes.Rec.Ops(), "nothing may reach the system before the permission check")
		})
	}
}

func TestProxyConfCmd_NginxNotInstalled(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	// legacy zmproxyconf prints the message on stdout and exits 1
	assert.Equal(t, exitStatus(1), (&ProxyConfCmd{}).Run(systest.New().System(), discardStdio()))
}

func TestServiceListCmd_ListsWithoutAnError(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	require.NoError(t, (&ServiceListCmd{}).Run(systest.New().System(), discardStdio()))
}

func TestServiceStatusCmd_UnknownServiceFails(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	err := (&ServiceStatusCmd{Name: "nosuch"}).Run(systest.New().System(), discardStdio())

	assert.Equal(t, exitStatus(1), err)
}
