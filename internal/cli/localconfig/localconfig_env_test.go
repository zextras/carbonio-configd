// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
)

func TestLocalconfigCmd_EditRefusesEnvKey(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"ldap_url": "ldap://file:389", "other": "old"})

	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")

	var stderr bytes.Buffer

	code := Run(context.Background(), io.Discard, &stderr, &Options{
		ConfigPath: path,
		Edit:       true,
		Keys:       []string{"other=new", "ldap_url=ldap://x:389"},
	})

	require.NotZero(t, code)
	assert.Contains(t, stderr.String(), "key ldap_url is set by environment CONFIGD_LC_ldap_url")

	cfg, err := lcfg.LoadEffectiveFrom(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "old", cfg["other"], "a refused edit saves nothing")
}

func TestLocalconfigCmd_UnsetRefusesEnvKey(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"ldap_url": "ldap://file:389"})

	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")

	var stderr bytes.Buffer

	code := Run(context.Background(), io.Discard, &stderr, &Options{
		ConfigPath: path,
		Unset:      true,
		Keys:       []string{"ldap_url"},
	})

	require.NotZero(t, code)
	assert.Contains(t, stderr.String(), "key ldap_url is set by environment CONFIGD_LC_ldap_url")
}

func TestLocalconfigCmd_ReadShowsEnvValue(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"ldap_url": "ldap://file:389"})

	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")

	var stdout bytes.Buffer

	code := Run(context.Background(), &stdout, io.Discard, &Options{
		ConfigPath: path,
		Mode:       "plain",
		Keys:       []string{"ldap_url"},
	})

	require.Zero(t, code)
	assert.Contains(t, stdout.String(), "ldap://env:389")
	assert.NotContains(t, stdout.String(), "ldap://file:389")
}
