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
		{"daemon", func(env sys.System) error { return (&DaemonCmd{}).Run(cli, env) }},
		{"shadow", func(env sys.System) error { return (&ShadowCmd{Once: true}).Run(cli, env) }},
		{"rewrite", func(env sys.System) error { return (&RewriteCmd{ConfigNames: []string{"mta"}}).Run(cli, env) }},
		{"plan", func(env sys.System) error { return (&PlanCmd{JSON: true}).Run(cli, env) }},
		{"service start", func(env sys.System) error { return (&ServiceStartCmd{Name: "mta"}).Run(env) }},
		{"service start norewrite", func(env sys.System) error {
			return (&ServiceStartCmd{Name: "mta", Extra: []string{norewriteArg}}).Run(env)
		}},
		{"service stop", func(env sys.System) error { return (&ServiceStopCmd{Name: "mta"}).Run(env) }},
		{"service restart", func(env sys.System) error { return (&ServiceRestartCmd{Name: "mta", NoRewrite: true}).Run(env) }},
		{"service reload", func(env sys.System) error { return (&ServiceReloadCmd{Name: "mta"}).Run(env) }},
		{"service start-systemd", func(env sys.System) error { return (&ServiceStartSystemdCmd{Name: "mta"}).Run(env) }},
		{"service stop-systemd", func(env sys.System) error { return (&ServiceStopSystemdCmd{Name: "mta"}).Run(env) }},
		{"proxy gen", func(env sys.System) error { return (&ProxyGenCmd{ExtraConfigs: []string{"mta"}}).Run(env) }},
		{"proxy rewrite", func(env sys.System) error { return (&ProxyRewriteCmd{}).Run(env) }},
		{"proxy enable", func(env sys.System) error { return (&ProxyEnableCmd{Protocol: "imap"}).Run(env) }},
		{"proxy disable", func(env sys.System) error { return (&ProxyDisableCmd{Protocol: "imap"}).Run(env) }},
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

	require.NoError(t, (&ProxyConfCmd{}).Run(systest.New().System()))
}

func TestServiceListCmd_ListsWithoutAnError(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	require.NoError(t, (&ServiceListCmd{}).Run(systest.New().System()))
}

func TestServiceStatusCmd_UnknownServiceFails(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/nonexistent-base")

	err := (&ServiceStatusCmd{Name: "nosuch"}).Run(systest.New().System())

	assert.Equal(t, exitStatus(1), err)
}
