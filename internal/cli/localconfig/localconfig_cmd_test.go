// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
)

// newTestLocalconfig writes a minimal localconfig.xml and returns its path.
func newTestLocalconfig(t *testing.T, keys map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "localconfig.xml")
	cfg := &lcfg.LocalConfig{}

	for name, value := range keys {
		cfg.Keys = append(cfg.Keys, lcfg.Key{Name: name, Value: value})
	}

	require.NoError(t, lcfg.SaveLocalConfig(path, cfg))

	return path
}

func TestLocalconfigCmd_ShowPath(t *testing.T) {
	cmd := &Options{ConfigPath: "/tmp/some/localconfig.xml", ShowPath: true}
	require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
}

func TestLocalconfigCmd_Edit(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"existing": "old"})

	cmd := &Options{
		ConfigPath: path,
		Edit:       true,
		Keys:       []string{"existing=new", "added=fresh"},
	}
	require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))

	cfg, err := lcfg.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", cfg["existing"])
	assert.Equal(t, "fresh", cfg["added"])
}

func TestLocalconfigCmd_EditDangerousKeyWithForce(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	cmd := &Options{
		ConfigPath: path,
		Edit:       true,
		Force:      true,
		Keys:       []string{"ldap_url=ldap://localhost:389"},
	}
	require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))

	cfg, err := lcfg.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "ldap://localhost:389", cfg["ldap_url"])
}

func TestLocalconfigCmd_EditRandom(t *testing.T) {
	path := newTestLocalconfig(t, nil)

	cmd := &Options{
		ConfigPath: path,
		Edit:       true,
		Random:     true,
		Keys:       []string{"my_generated_password"},
	}
	require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))

	cfg, err := lcfg.LoadResolvedConfigFromFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, cfg["my_generated_password"], "random edit must generate a value")
}

func TestLocalconfigCmd_Unset(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"doomed": "value", "keeper": "stays"})

	cmd := &Options{
		ConfigPath: path,
		Unset:      true,
		Keys:       []string{"doomed"},
	}
	require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))

	cfg, err := lcfg.LoadResolvedConfigFromFile(path)
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
			cmd := &Options{ConfigPath: path, Mode: mode}
			require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
		})
	}
}

func TestLocalconfigCmd_ReadFilters(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"alpha": "1", "beta": "2"})

	t.Run("specific key", func(t *testing.T) {
		cmd := &Options{ConfigPath: path, Mode: "plain", Keys: []string{"alpha"}}
		require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
	})

	t.Run("show changed", func(t *testing.T) {
		cmd := &Options{ConfigPath: path, Mode: "plain", ShowChanged: true}
		require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
	})

	t.Run("show defaults", func(t *testing.T) {
		cmd := &Options{ConfigPath: path, Mode: "plain", ShowDefaults: true}
		require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
	})

	t.Run("show passwords", func(t *testing.T) {
		cmd := &Options{ConfigPath: path, Mode: "plain", ShowPasswords: true}
		require.Zero(t, Run(context.Background(), io.Discard, io.Discard, cmd))
	})
}
