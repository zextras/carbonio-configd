//go:build integration

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/test"
)

const serverHost = "mail.test.example"

// startDirectory runs the sanitized OpenLDAP container and points the
// environment at it the way a pod is configured: CONFIGD_LC_* only, no
// localconfig.xml, and a base dir holding nothing but the attrs schema.
func startDirectory(t *testing.T) {
	t.Helper()

	ol := test.StartOpenLDAP(context.Background(), t, test.OpenLDAPFixtures{
		Slapd:  "../../configmgr/testdata/openldap/slapd.ldif",
		Schema: "../../configmgr/testdata/openldap/zimbra-schema.ldif",
		Seed:   "../../configmgr/testdata/openldap/seed.ldif",
	})

	attrs, err := os.ReadFile("../../ldap/testdata/attrs/attrs.xml")
	require.NoError(t, err)

	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "conf", "attrs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "conf", "attrs", "attrs.xml"), attrs, 0o600))

	t.Setenv("CARBONIO_BASE_DIR", base)
	t.Setenv("CONFIGD_LC_ldap_url", ol.URL)
	t.Setenv("CONFIGD_LC_zimbra_ldap_userdn", test.OpenLDAPDataBindDN)
	t.Setenv("CONFIGD_LC_zimbra_ldap_password", test.OpenLDAPDataPassword)
	t.Setenv("CONFIGD_LC_ldap_starttls_supported", "0")
	t.Setenv("CONFIGD_LC_zimbra_server_hostname", serverHost)
}

func runLive(t *testing.T, opts *Options) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, opts)

	return code, stdout.String(), stderr.String()
}

func TestContainer_VerbsFromEnvOnly(t *testing.T) {
	startDirectory(t)

	code, stdout, stderr := runLive(t, &Options{Verb: VerbGacf})
	require.Zero(t, code, stderr)
	assert.Contains(t, stdout, "zimbraMailMode: https\n")
	assert.Contains(t, stdout, "zimbraMtaRestriction: reject_non_fqdn_sender\nzimbraMtaRestriction: reject_unknown_client_hostname\n")

	code, stdout, stderr = runLive(t, &Options{Verb: VerbGcf, Args: []string{"zimbraMailSSLPort"}})
	require.Zero(t, code, stderr)
	assert.Equal(t, "zimbraMailSSLPort: 443\n", stdout)

	// gs defaults to zimbra_server_hostname and inherits the global values the
	// server does not override.
	code, stdout, stderr = runLive(t, &Options{Verb: VerbGs})
	require.Zero(t, code, stderr)
	assert.Contains(t, stdout, "zimbraServiceEnabled: mailbox\n")
	assert.Contains(t, stdout, "zimbraMilterServerEnabled: FALSE\n", "the server's own value wins")
	assert.Contains(t, stdout, "zimbraMilterBindPort: 7026\n", "inherited from the global config")

	code, stdout, stderr = runLive(t, &Options{
		Verb: VerbSearch, Base: "cn=servers,cn=zimbra", Scope: ScopeOne, Filter: "(cn=other.test.example)", Attrs: []string{"zimbraServiceHostname"},
	})
	require.Zero(t, code, stderr)
	assert.Equal(t, "dn: cn=other.test.example,cn=servers,cn=zimbra\nzimbraServiceHostname: other.test.example\n", stdout)
}

func TestContainer_BadPasswordFails(t *testing.T) {
	startDirectory(t)

	pw := filepath.Join(t.TempDir(), "pw")
	require.NoError(t, os.WriteFile(pw, []byte("wrong\n"), 0o600))

	code, _, stderr := runLive(t, &Options{Verb: VerbGacf, PasswordFile: pw})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "Error:")
}

func TestContainer_McfAddRemoveIsIdempotent(t *testing.T) {
	startDirectory(t)

	mcf := func(args ...string) {
		t.Helper()

		code, stdout, stderr := runLive(t, &Options{Verb: VerbMcf, Args: args})
		require.Zero(t, code, stderr)
		assert.Empty(t, stdout)
	}

	restrictions := func() string {
		t.Helper()

		code, stdout, stderr := runLive(t, &Options{Verb: VerbGcf, Args: []string{"zimbraMtaRestriction"}})
		require.Zero(t, code, stderr)

		return stdout
	}

	const (
		seed = "zimbraMtaRestriction: reject_non_fqdn_sender\nzimbraMtaRestriction: reject_unknown_client_hostname\n"
		both = seed + "zimbraMtaRestriction: reject_rbl_a\nzimbraMtaRestriction: reject_rbl_b\n"
	)

	// Adding twice, with one value already present, leaves one copy of each.
	mcf("+zimbraMtaRestriction", "reject_rbl_a", "+zimbraMtaRestriction", "reject_rbl_b")
	assert.Equal(t, both, restrictions())

	mcf("+zimbraMtaRestriction", "reject_rbl_a")
	mcf("+zimbraMtaRestriction", "reject_rbl_a", "+zimbraMtaRestriction", "reject_rbl_b")
	assert.Equal(t, both, restrictions())

	// Removing twice, with one value already gone, leaves the others alone.
	mcf("-zimbraMtaRestriction", "reject_rbl_a", "-zimbraMtaRestriction", "reject_rbl_b")
	assert.Equal(t, seed, restrictions())

	mcf("-zimbraMtaRestriction", "reject_rbl_a")
	mcf("-zimbraMtaRestriction", "reject_rbl_a", "-zimbraMtaRestriction", "reject_unknown_client_hostname")
	assert.Equal(t, "zimbraMtaRestriction: reject_non_fqdn_sender\n", restrictions())

	// Removing an attribute that is gone, and replacing, also converge.
	mcf("-zimbraSSLExcludeCipherSuites", "")
	mcf("-zimbraSSLExcludeCipherSuites", "")
	mcf("zimbraMtaRestriction", "reject_only")
	mcf("zimbraMtaRestriction", "reject_only")
	assert.Equal(t, "zimbraMtaRestriction: reject_only\n", restrictions())

	// A genuine failure is still an error.
	code, _, stderr := runLive(t, &Options{Verb: VerbMcf, Args: []string{"zimbraNoSuchAttribute", "x"}})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "Error:")
}
