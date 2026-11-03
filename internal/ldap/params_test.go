// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
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

	cfg, err := ParamsFrom(context.Background(), baseLC(), PurposeRead, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"ldap://replica:389", "ldap://replica2:389"}, cfg.URLs)
	assert.Equal(t, "uid=zimbra,cn=admins,cn=zimbra", cfg.BindDN)
	assert.Equal(t, "secret", cfg.Password)
	assert.Equal(t, "cn=zimbra", cfg.BaseDN)
	assert.Equal(t, 5, cfg.PoolSize)
	assert.False(t, cfg.StartTLS, "StartTLS needs ldap_starttls_supported AND zimbra_require_interprocess_security")
	require.NotNil(t, cfg.TLSConfig)
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

		_, err := ParamsFrom(context.Background(), lc, PurposeRead, nil)
		assert.True(t, errors.Is(err, tc.want), "%s: got %v", tc.drop, err)
	}
}

func TestParamsFrom_Write(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(context.Background(), baseLC(), PurposeWrite, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"ldaps://master:636"}, cfg.URLs)
}

func TestParamsFrom_WriteFallsBackToReplica(t *testing.T) {
	t.Parallel()

	lc := baseLC()
	lc["ldap_master_url"] = ""

	cfg, err := ParamsFrom(context.Background(), lc, PurposeWrite, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"ldap://replica:389", "ldap://replica2:389"}, cfg.URLs)

	lc["ldap_url"] = ""
	_, err = ParamsFrom(context.Background(), lc, PurposeWrite, nil)
	assert.ErrorIs(t, err, ErrNoURL)
}

func TestParamsFrom_Discovery(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(context.Background(), baseLC(), PurposeDiscovery, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"ldaps://master:636"}, cfg.URLs)

	lc := baseLC()
	lc["ldap_master_url"] = ""
	_, err = ParamsFrom(context.Background(), lc, PurposeDiscovery, nil)
	assert.ErrorIs(t, err, ErrNoURL, "discovery never falls back to the replica")
}

// TestParamsFrom_StartTLSFollowsLegacyRule pins that every purpose takes its
// transport security from ResolveTLS: StartTLS needs ldap_starttls_supported=1,
// ldap_starttls_required (default true) and zimbra_require_interprocess_security=1.
func TestParamsFrom_StartTLSFollowsLegacyRule(t *testing.T) {
	t.Parallel()

	for _, purpose := range []Purpose{PurposeRead, PurposeWrite, PurposeDiscovery} {
		lc := baseLC()

		cfg, err := ParamsFrom(context.Background(), lc, purpose, nil)
		require.NoError(t, err)
		assert.False(t, cfg.StartTLS, "interprocess security is off")
		require.NotNil(t, cfg.TLSConfig)

		lc["zimbra_require_interprocess_security"] = "1"

		cfg, err = ParamsFrom(context.Background(), lc, purpose, nil)
		require.NoError(t, err)
		assert.True(t, cfg.StartTLS)

		lc["ldap_starttls_supported"] = "0"

		cfg, err = ParamsFrom(context.Background(), lc, purpose, nil)
		require.NoError(t, err)
		assert.False(t, cfg.StartTLS, "the directory does not advertise StartTLS")
	}
}

func TestParamsFrom_AttrsDirFollowsTemplateDir(t *testing.T) {
	t.Parallel()

	cfg, err := ParamsFrom(context.Background(), baseLC(), PurposeRead, nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.AttrsDir)

	lc := baseLC()
	lc["zmconfigd_template_dir"] = "/share/configd"

	for _, purpose := range []Purpose{PurposeRead, PurposeWrite, PurposeDiscovery} {
		cfg, err = ParamsFrom(context.Background(), lc, purpose, nil)
		require.NoError(t, err)
		assert.Equal(t, "/share/configd/conf/attrs", cfg.AttrsDir)
	}
}
