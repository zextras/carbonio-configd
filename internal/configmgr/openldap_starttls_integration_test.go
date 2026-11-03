// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
	"github.com/zextras/carbonio-configd/test"
)

// startTLSLDAP starts slapd with a TLS certificate and olcSecurity tls=1, so
// only connections that negotiated TLS (StartTLS or ldaps://) are served, and
// returns the CA that signed the certificate.
func startTLSLDAP(t *testing.T) (*test.OpenLDAP, pki.Server) {
	t.Helper()

	pk := pki.NewServer(t, "localhost", "127.0.0.1", "::1")

	ol := test.StartOpenLDAP(context.Background(), t, test.OpenLDAPFixtures{
		Slapd:  "testdata/openldap/slapd.ldif",
		Schema: "testdata/openldap/zimbra-schema.ldif",
		Seed:   "testdata/openldap/seed.ldif",
		TLS:    &test.OpenLDAPTLS{CAPEM: pk.CAPEM, CertPEM: pk.CertPEM, KeyPEM: pk.KeyPEM},
	})

	return ol, pk
}

// carbonioHome returns a zimbra_home whose conf/ca/ca.pem is the given CA,
// the file the legacy client trusts through the Java truststore.
func carbonioHome(t *testing.T, caPEM []byte) string {
	t.Helper()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), caPEM, 0o644))

	return home
}

func searchConfig(c *ldap.Client) error {
	_, err := c.Search("cn=config,cn=zimbra", "(objectClass=*)", []string{"cn"}, goldap.ScopeBaseObject)

	return err //nolint:wrapcheck // test helper
}

// TestOpenLDAP_StartTLS runs the three LDAP client constructions of configd
// (CLI writers, daemon, discovery path through ResolveTLS) against a slapd that
// refuses clear text, with certificate verification enabled.
func TestOpenLDAP_StartTLS(t *testing.T) {
	ol, pk := startTLSLDAP(t)
	home := carbonioHome(t, pk.CAPEM)

	lc := map[string]string{
		"ldap_url":                             ol.URL,
		"ldap_master_url":                      ol.URL,
		"zimbra_ldap_userdn":                   test.OpenLDAPDataBindDN,
		"zimbra_ldap_password":                 test.OpenLDAPDataPassword,
		"ldap_starttls_supported":              "1",
		"zimbra_require_interprocess_security": "1",
		"ssl_allow_untrusted_certs":            "false",
		"zimbra_home":                          home,
	}

	t.Run("clear text is refused by the server", func(t *testing.T) {
		c, err := ldap.NewClient(&ldap.ClientConfig{
			URL: ol.URL, BindDN: test.OpenLDAPDataBindDN, Password: test.OpenLDAPDataPassword,
			MaxRetries: 1, StartTLS: false,
		})
		require.NoError(t, err)

		defer func() { _ = c.Close() }()

		require.Error(t, searchConfig(c), "slapd must demand TLS: the fixture would not prove StartTLS otherwise")
	})

	t.Run("CLI writer client (cliutil.OpenWriteLDAPClient)", func(t *testing.T) {
		c, err := cliutil.OpenWriteLDAPClient(lc)
		require.NoError(t, err)

		defer func() { _ = c.Close() }()

		require.NoError(t, searchConfig(c))
	})

	t.Run("daemon client (initNativeLdapClient)", func(t *testing.T) {
		cm := &ConfigManager{
			env:             hostEnv(),
			loadLocalConfig: func() (map[string]string, error) { return lc, nil },
			mainConfig:      &config.Config{},
			CommandRegistry: commands.NewRegistry(),
		}
		cm.initNativeLdapClient(context.Background())
		require.NotNil(t, cm.NativeLdapClient)

		defer func() { _ = cm.NativeLdapClient.Close() }()

		require.NoError(t, searchConfig(cm.NativeLdapClient))
	})

	t.Run("ldaps with the Carbonio CA", func(t *testing.T) {
		setup := ldap.ResolveTLS(context.Background(), lc, nil)

		c, err := ldap.NewClient(&ldap.ClientConfig{
			URL: ol.LDAPSURL, BindDN: test.OpenLDAPDataBindDN, Password: test.OpenLDAPDataPassword,
			TLSConfig: setup.Config, MaxRetries: 1,
		})
		require.NoError(t, err)

		defer func() { _ = c.Close() }()

		require.NoError(t, searchConfig(c))
	})

	t.Run("untrusted CA is rejected, no downgrade to clear text", func(t *testing.T) {
		strict := map[string]string{}
		for k, v := range lc {
			strict[k] = v
		}

		strict["zimbra_home"] = t.TempDir() // no conf/ca/ca.pem: CA unknown

		c, err := cliutil.OpenWriteLDAPClient(strict)
		require.NoError(t, err)

		defer func() { _ = c.Close() }()

		require.Error(t, searchConfig(c))
	})

	t.Run("untrusted certificates allowed (legacy default)", func(t *testing.T) {
		loose := map[string]string{}
		for k, v := range lc {
			loose[k] = v
		}

		delete(loose, "ssl_allow_untrusted_certs")

		loose["zimbra_home"] = t.TempDir()

		c, err := cliutil.OpenWriteLDAPClient(loose)
		require.NoError(t, err)

		defer func() { _ = c.Close() }()

		require.NoError(t, searchConfig(c))
	})
}
