// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalconfigCmd_Fields(t *testing.T) {
	cmd := &LocalconfigCmd{
		Mode:          "export",
		ConfigPath:    "/tmp/lc.xml",
		Quiet:         true,
		ShowPath:      true,
		ShowPasswords: false,
		ShowDefaults:  true,
		ShowChanged:   false,
		Edit:          false,
		Unset:         false,
		Random:        false,
		Force:         true,
		Key:           []string{"key1"},
		KeyArgs:       []string{"key2"},
	}

	if cmd.Mode != "export" {
		t.Errorf("Mode = %q, want %q", cmd.Mode, "export")
	}
	if cmd.ConfigPath != "/tmp/lc.xml" {
		t.Errorf("ConfigPath = %q, want %q", cmd.ConfigPath, "/tmp/lc.xml")
	}
	if !cmd.Quiet {
		t.Error("expected Quiet=true")
	}
	if !cmd.ShowPath {
		t.Error("expected ShowPath=true")
	}
	if !cmd.ShowDefaults {
		t.Error("expected ShowDefaults=true")
	}
	if !cmd.Force {
		t.Error("expected Force=true")
	}
	if len(cmd.Key) != 1 || cmd.Key[0] != "key1" {
		t.Errorf("Key = %v, want [key1]", cmd.Key)
	}
}

func TestLegacyLongOptionNames(t *testing.T) {
	parse := func(t *testing.T, argv ...string) *LocalconfigCmd {
		t.Helper()

		cli := &CLI{}
		parser, err := kong.New(cli, kong.Vars{"version": "test"})
		require.NoError(t, err)

		_, err = parser.Parse(append([]string{"localconfig"}, argv...))
		require.NoError(t, err, argv)

		return &cli.Localconfig
	}

	legacy := parse(t, "--config", "/x.xml", "--format", "xml", "--show", "--changed", "--quiet", "--expand", "k")
	assert.Equal(t, "/x.xml", legacy.ConfigPath)
	assert.Equal(t, "xml", legacy.Mode)
	assert.True(t, legacy.ShowPasswords)
	assert.True(t, legacy.ShowChanged)
	assert.True(t, legacy.Quiet)
	assert.True(t, legacy.Expand)
	assert.Equal(t, []string{"k"}, legacy.KeyArgs)

	assert.True(t, parse(t, "--path").ShowPath)
	assert.True(t, parse(t, "--default").ShowDefaults)
	assert.True(t, parse(t, "--edit", "a=1").Edit)
	assert.True(t, parse(t, "--unset", "a").Unset)
	assert.True(t, parse(t, "--random", "--edit", "a").Random)
	assert.True(t, parse(t, "--force", "--edit", "a=1").Force)
	assert.True(t, parse(t, "--info").Info)
	assert.True(t, parse(t, "--all").All)
	assert.True(t, parse(t, "--reload").Reload)
	assert.Equal(t, "xml", parse(t, "--format=xml").Mode)
	assert.Equal(t, "/y.xml", parse(t, "--config=/y.xml").ConfigPath)

	// The names Kong derived before the aliases keep working.
	current := parse(t, "--config-path", "/z.xml", "--mode", "shell", "--show-passwords", "--show-changed",
		"--show-path", "--show-defaults")
	assert.Equal(t, "/z.xml", current.ConfigPath)
	assert.Equal(t, "shell", current.Mode)
	assert.True(t, current.ShowPasswords)
	assert.True(t, current.ShowChanged)
	assert.True(t, current.ShowPath)
	assert.True(t, current.ShowDefaults)
}

func TestLocalconfigCmd_Options(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/base")

	cmd := &LocalconfigCmd{ConfigPath: "/x.xml", Key: []string{"a"}, KeyArgs: []string{"b=1"}, Edit: true}
	opts := cmd.options([]string{"configd", "localconfig", "-e"})

	assert.Equal(t, "/x.xml", opts.ConfigPath)
	assert.Equal(t, "/base/conf/localconfig.xml", opts.DefaultPath)
	assert.Equal(t, []string{"a", "b=1"}, opts.Keys)
	assert.Equal(t, []string{"configd", "localconfig", "-e"}, opts.Args)
	assert.True(t, opts.Edit)
}
