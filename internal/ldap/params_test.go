// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseLC() map[string]string {
	return map[string]string{
		"ldap_url":                "ldap://replica:389 ldap://replica2:389",
		"ldap_master_url":         "ldaps://master:636",
		"zimbra_ldap_userdn":      "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password":    "secret",
		"ldap_starttls_supported": "1",
	}
}

func TestParamsFrom_Read(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(baseLC(), PurposeRead)
	require.NoError(t, err)

	assert.Equal(t, []string{"ldap://replica:389", "ldap://replica2:389"}, cfg.URLs)
	assert.Equal(t, "uid=zimbra,cn=admins,cn=zimbra", cfg.BindDN)
	assert.Equal(t, "secret", cfg.Password)
	assert.Equal(t, "cn=zimbra", cfg.BaseDN)
	assert.Equal(t, 5, cfg.PoolSize)
	assert.False(t, cfg.StartTLS, "the cycle decides StartTLS for the read client")
}

func TestParamsFrom_ReadErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		drop string
		want error
	}{
		{"ldap_url", ErrNoURL},
		{"zimbra_ldap_userdn", ErrNoBindDN},
		{"zimbra_ldap_password", ErrNoPassword},
	} {
		lc := baseLC()
		lc[tc.drop] = ""

		_, err := ParamsFrom(lc, PurposeRead)
		assert.True(t, errors.Is(err, tc.want), "%s: got %v", tc.drop, err)
	}
}

func TestParamsFrom_Write(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(baseLC(), PurposeWrite)
	require.NoError(t, err)

	assert.Equal(t, []string{"ldaps://master:636"}, cfg.URLs)
	assert.True(t, cfg.StartTLS)
}

func TestParamsFrom_WriteFallsBackToReplica(t *testing.T) {
	t.Parallel()

	lc := baseLC()
	lc["ldap_master_url"] = ""

	cfg, err := ParamsFrom(lc, PurposeWrite)
	require.NoError(t, err)
	assert.Equal(t, []string{"ldap://replica:389", "ldap://replica2:389"}, cfg.URLs)

	lc["ldap_url"] = ""
	_, err = ParamsFrom(lc, PurposeWrite)
	assert.ErrorIs(t, err, ErrNoURL)
}

func TestParamsFrom_Discovery(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(baseLC(), PurposeDiscovery)
	require.NoError(t, err)
	assert.Equal(t, []string{"ldaps://master:636"}, cfg.URLs)
	assert.True(t, cfg.StartTLS)

	lc := baseLC()
	lc["ldap_starttls_supported"] = "0"

	cfg, err = ParamsFrom(lc, PurposeDiscovery)
	require.NoError(t, err)
	assert.False(t, cfg.StartTLS)

	lc["ldap_master_url"] = ""
	_, err = ParamsFrom(lc, PurposeDiscovery)
	assert.ErrorIs(t, err, ErrNoURL, "discovery never falls back to the replica")
}

func TestStartTLSSupported(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]bool{"": false, "0": false, "1": true, "true": true} {
		assert.Equal(t, want, StartTLSSupported(map[string]string{"ldap_starttls_supported": value}), value)
	}
}
