// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyProxyGenArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"other command untouched", []string{"service", "-v"}, []string{"service", "-v"}},
		{"root version untouched", []string{"-v"}, []string{"-v"}},
		{"proxy conf untouched", []string{"proxy", "conf", "-v"}, []string{"proxy", "conf", "-v"}},
		{"verbose", []string{"proxy", "gen", "-v"}, []string{"proxy", "gen", "--verbose"}},
		{"bundle", []string{"proxy", "gen", "-vn"}, []string{"proxy", "gen", "--verbose", "-n"}},
		{"bundle ends at a value flag", []string{"proxy", "gen", "-nvw/x"}, []string{"proxy", "gen", "--verbose", "-nw/x"}},
		{"value flag first keeps v as value", []string{"proxy", "gen", "-wv"}, []string{"proxy", "gen", "-wv"}},
		{"long untouched", []string{"proxy", "gen", "--dry-run"}, []string{"proxy", "gen", "--dry-run"}},
		{"values untouched", []string{"proxy", "gen", "-c", "a=b"}, []string{"proxy", "gen", "-c", "a=b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, legacyProxyGenArgs(tt.in))
		})
	}
}

func parseCLI(t *testing.T, args ...string) *CLI {
	t.Helper()

	cli, _, err := parseArgv(t, args...)
	require.NoError(t, err)

	return cli
}

func TestProxyGenCmd_ParsesLegacyFlags(t *testing.T) {
	cli := parseCLI(t, "proxy", "gen", "-vn", "-s", "mbox", "-w", "/w", "-t", "/t", "-p", "p.conf", "-P", "t.conf",
		"-i", "inc", "-f", "-D", "-c", "a=b", "c=d", "stray")

	g := cli.Proxy.Gen
	assert.True(t, g.Verbose)
	assert.True(t, g.DryRun)
	assert.True(t, g.Definitions)
	assert.True(t, g.ForceDNS)
	assert.False(t, g.Defaults)
	assert.Equal(t, "mbox", g.Server)
	assert.Equal(t, "/w", g.WorkDir)
	assert.Equal(t, "/t", g.TemplateDir)
	assert.Equal(t, "p.conf", g.Prefix)
	assert.Equal(t, "t.conf", g.TemplatePrefix)
	assert.Equal(t, "inc", g.IncludeDir)
	assert.Equal(t, []string{"a=b"}, g.Config)
	assert.Equal(t, []string{"c=d", "stray"}, g.Args)
}

func TestProxyGenCmd_ParsesLongFlags(t *testing.T) {
	cli := parseCLI(t, "proxy", "gen", "--verbose", "--dry-run", "--defaults", "--server", "m", "--workdir", "/w",
		"--templatedir", "/t", "--prefix", "p", "--template-prefix", "q", "--include-dir", "i",
		"--force-dns-resolution", "--definitions", "--config", "a=b", "--config", "c=d")

	g := cli.Proxy.Gen
	assert.True(t, g.Verbose && g.DryRun && g.Defaults && g.Definitions && g.ForceDNS)
	assert.Equal(t, []string{"a=b", "c=d"}, g.Config)
}

func TestProxyConfCmd_ParsesLegacyLongFlags(t *testing.T) {
	cli := parseCLI(t, "proxy", "conf", "--nocomments", "--noempty", "-m", "/f")

	c := cli.Proxy.Conf
	assert.True(t, c.NoComments && c.NoEmpty && c.Markers)
	assert.Equal(t, "/f", c.ConfigFile)

	cli = parseCLI(t, "proxy", "conf", "--no-comments", "--no-empty")
	assert.True(t, cli.Proxy.Conf.NoComments && cli.Proxy.Conf.NoEmpty)
}
