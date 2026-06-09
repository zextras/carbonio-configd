// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// newTestLocalconfig writes a minimal localconfig.xml and returns its path.
func newTestLocalconfig(t *testing.T, keys map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "localconfig.xml")
	cfg := &localconfig.LocalConfig{}

	for name, value := range keys {
		cfg.Keys = append(cfg.Keys, localconfig.Key{Name: name, Value: value})
	}

	require.NoError(t, localconfig.SaveLocalConfig(path, cfg))

	return path
}

func TestLocalconfigCmd_ShowPath(t *testing.T) {
	cmd := &LocalconfigCmd{ConfigPath: "/tmp/some/localconfig.xml", ShowPath: true}
	require.NoError(t, cmd.Run())
}

func TestLocalconfigCmd_Edit(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"existing": "old"})

	cmd := &LocalconfigCmd{
		ConfigPath: path,
		Edit:       true,
		KeyArgs:    []string{"existing=new", "added=fresh"},
	}
	require.NoError(t, cmd.Run())

	cfg, err := localconfig.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", cfg["existing"])
	assert.Equal(t, "fresh", cfg["added"])
}

func TestLocalconfigCmd_EditDangerousKeyWithForce(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	cmd := &LocalconfigCmd{
		ConfigPath: path,
		Edit:       true,
		Force:      true,
		KeyArgs:    []string{"ldap_url=ldap://localhost:389"},
	}
	require.NoError(t, cmd.Run())

	cfg, err := localconfig.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ldap://localhost:389", cfg["ldap_url"])
}

func TestLocalconfigCmd_EditRandom(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	cmd := &LocalconfigCmd{
		ConfigPath: path,
		Edit:       true,
		Random:     true,
		KeyArgs:    []string{"my_generated_password"},
	}
	require.NoError(t, cmd.Run())

	cfg, err := localconfig.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, cfg["my_generated_password"], "random edit must generate a value")
}

func TestLocalconfigCmd_Unset(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"doomed": "value", "keeper": "stays"})

	cmd := &LocalconfigCmd{
		ConfigPath: path,
		Unset:      true,
		KeyArgs:    []string{"doomed"},
	}
	require.NoError(t, cmd.Run())

	cfg, err := localconfig.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.NotContains(t, cfg, "doomed")
	assert.Equal(t, "stays", cfg["keeper"])
}

func TestLocalconfigCmd_ReadModes(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{
		"plain_key":          "plain_value",
		"ldap_root_password": "secret",
	})

	for _, mode := range []string{"plain", "shell", "export", "nokey", "xml"} {
		t.Run(mode, func(t *testing.T) {
			cmd := &LocalconfigCmd{ConfigPath: path, Mode: mode}
			require.NoError(t, cmd.Run())
		})
	}
}

func TestLocalconfigCmd_ReadFilters(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"alpha": "1", "beta": "2"})

	t.Run("specific key", func(t *testing.T) {
		cmd := &LocalconfigCmd{ConfigPath: path, Mode: "plain", Key: []string{"alpha"}}
		require.NoError(t, cmd.Run())
	})

	t.Run("show changed", func(t *testing.T) {
		cmd := &LocalconfigCmd{ConfigPath: path, Mode: "plain", ShowChanged: true}
		require.NoError(t, cmd.Run())
	})

	t.Run("show defaults", func(t *testing.T) {
		cmd := &LocalconfigCmd{ConfigPath: path, Mode: "plain", ShowDefaults: true}
		require.NoError(t, cmd.Run())
	})

	t.Run("show passwords", func(t *testing.T) {
		cmd := &LocalconfigCmd{ConfigPath: path, Mode: "plain", ShowPasswords: true}
		require.NoError(t, cmd.Run())
	})
}
