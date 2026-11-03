// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeLC(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte("<localconfig>"+body+"</localconfig>"), 0o600))

	return path
}

func TestLoadEffectiveFrom_Layers(t *testing.T) {
	path := writeLC(t, `
<key name="ldap_url"><value>ldap://file:389</value></key>
<key name="zimbra_ldap_userdn"><value>uid=file</value></key>
<key name="ldap_root_password"><value>from-file</value></key>`)

	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")
	t.Setenv("CONFIGD_LC_zimbra_ldap_userdn", "uid=env")

	lc, err := LoadEffectiveFrom(path, map[string]string{"zimbra_ldap_userdn": "uid=flag"})
	require.NoError(t, err)

	assert.Equal(t, "ldap://env:389", lc["ldap_url"], "env beats the file")
	assert.Equal(t, "uid=flag", lc["zimbra_ldap_userdn"], "flag overrides beat env")
	assert.Equal(t, "from-file", lc["ldap_root_password"], "the file beats the defaults")
	assert.Equal(t, Defaults["ldap_starttls_supported"], lc["ldap_starttls_supported"], "defaults fill the rest")
}

func TestLoadEffectiveFrom_MissingFileIsEmptyLayer(t *testing.T) {
	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")

	lc, err := LoadEffectiveFrom(filepath.Join(t.TempDir(), "absent.xml"), nil)
	require.NoError(t, err)

	assert.Equal(t, "ldap://env:389", lc["ldap_url"])
	assert.Equal(t, Defaults["zimbra_ldap_userdn"], lc["zimbra_ldap_userdn"])
}

func TestLoadEffectiveFrom_MalformedFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte("<localconfig><key"), 0o600))

	_, err := LoadEffectiveFrom(path, nil)
	assert.Error(t, err)
}

func TestLoadEffectiveFrom_InterpolatesAcrossLayers(t *testing.T) {
	path := writeLC(t, `<key name="ldap_url"><value>ldap://${zimbra_server_hostname}:389</value></key>`)

	t.Setenv("CONFIGD_LC_zimbra_server_hostname", "ldap.pod")

	lc, err := LoadEffectiveFrom(path, map[string]string{"ldap_master_url": "ldaps://${zimbra_server_hostname}:636"})
	require.NoError(t, err)

	assert.Equal(t, "ldap://ldap.pod:389", lc["ldap_url"], "a file value sees an env key")
	assert.Equal(t, "ldaps://ldap.pod:636", lc["ldap_master_url"], "a flag value sees an env key")
}

func TestLoadEffectiveFrom_EnvValueIsKeptVerbatim(t *testing.T) {
	t.Setenv("CONFIGD_LC_zimbra_ldap_password", "  spaced \n")

	lc, err := LoadEffectiveFrom(filepath.Join(t.TempDir(), "absent.xml"), nil)
	require.NoError(t, err)

	assert.Equal(t, "  spaced \n", lc["zimbra_ldap_password"])
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CONFIGD_LC_a_key", "1")
	t.Setenv("CONFIGD_LC_empty", "")
	t.Setenv("CONFIGD_LC_", "ignored")
	t.Setenv("UNRELATED_a_key", "x")

	overrides := EnvOverrides()

	assert.Equal(t, "1", overrides["a_key"])
	assert.Contains(t, overrides, "empty")
	assert.NotContains(t, overrides, "")
	assert.NotContains(t, overrides, "UNRELATED_a_key")

	v, ok := EnvOverride("empty")
	assert.True(t, ok, "an empty value still counts as set")
	assert.Empty(t, v)

	_, ok = EnvOverride("never_set_key")
	assert.False(t, ok)

	assert.Equal(t, "CONFIGD_LC_a_key", EnvVar("a_key"))
}
