// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
)

// searchOnce runs a base search so the client dials, upgrades and binds.
func searchOnce(t *testing.T, lc map[string]string) error {
	t.Helper()

	client, err := OpenWriteLDAPClient(lc)
	if err != nil {
		t.Fatalf("OpenWriteLDAPClient: %v", err)
	}

	defer func() { _ = client.Close() }()

	_, err = client.Search("cn=zimbra", "(objectClass=*)", []string{"cn"}, ldap.ScopeBaseObject)

	return err //nolint:wrapcheck // test helper
}

// TestOpenWriteLDAPClient_StartTLSFollowsLocalconfig is the GCR-01 regression:
// the client used to hard-code StartTLS with no TLS config, which crypto/tls
// rejects ("either ServerName or InsecureSkipVerify must be specified"), and
// ignored the localconfig switches the legacy Java client honours.
func TestOpenWriteLDAPClient_StartTLSFollowsLocalconfig(t *testing.T) {
	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	base := map[string]string{
		"ldap_master_url":      srv.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password": "secret",
	}

	// Legacy defaults: no StartTLS, clear text bind.
	if err := searchOnce(t, base); err != nil {
		t.Fatalf("plain search: %v", err)
	}

	if srv.StartTLSOps() != 0 || srv.Binds() != 1 || srv.BindsInTLS() != 0 {
		t.Fatalf("defaults: startTLS=%d binds=%d inTLS=%d, want 0/1/0", srv.StartTLSOps(), srv.Binds(), srv.BindsInTLS())
	}

	// All three legacy switches on: StartTLS, bind inside TLS. Certificates
	// are unverified by default (ssl_allow_untrusted_certs=true).
	secure := map[string]string{
		"ldap_starttls_supported":              "1",
		"zimbra_require_interprocess_security": "1",
	}
	for k, v := range base {
		secure[k] = v
	}

	if err := searchOnce(t, secure); err != nil {
		t.Fatalf("StartTLS search: %v", err)
	}

	if srv.StartTLSOps() != 1 || srv.BindsInTLS() != 1 {
		t.Fatalf("secure: startTLS=%d bindsInTLS=%d, want 1/1", srv.StartTLSOps(), srv.BindsInTLS())
	}
}

func TestOpenWriteLDAPClient_VerifiesWithCarbonioCA(t *testing.T) {
	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755); err != nil {
		t.Fatal(err)
	}

	lc := map[string]string{
		"ldap_url":                             srv.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":                   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password":                 "secret",
		"ldap_starttls_supported":              "1",
		"zimbra_require_interprocess_security": "1",
		"ssl_allow_untrusted_certs":            "false",
		"zimbra_home":                          home,
	}

	if err := searchOnce(t, lc); err == nil {
		t.Fatal("self-signed server accepted although its CA is not trusted")
	}

	if err := os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), pk.CAPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := searchOnce(t, lc); err != nil {
		t.Fatalf("search with conf/ca/ca.pem trusted: %v", err)
	}
}

func TestOpenWriteLDAPClient_NoURLs(t *testing.T) {
	_, err := OpenWriteLDAPClient(map[string]string{})
	if err == nil {
		t.Fatal("expected error when both ldap_master_url and ldap_url are empty")
	}

	if !strings.Contains(err.Error(), "LDAP not configured") {
		t.Errorf("error = %q, want substring %q", err, "LDAP not configured")
	}
}

func TestOpenWriteLDAPClient_WhitespaceOnly(t *testing.T) {
	_, err := OpenWriteLDAPClient(map[string]string{
		"ldap_master_url": "   ",
		"ldap_url":        "\t\n",
	})
	if err == nil {
		t.Fatal("expected error when ldap_master_url + ldap_url are only whitespace")
	}
}

func TestOpenWriteLDAPClient_FallsBackToLdapURL(t *testing.T) {
	client, err := OpenWriteLDAPClient(map[string]string{
		"ldap_url":             "ldap://srv1:389 ldap://srv2:389",
		"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password": "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client == nil {
		t.Fatal("client should not be nil when ldap_url provides URLs")
	}

	_ = client.Close()
}

func TestOpenWriteLDAPClient_MasterURLPreferredOverLdapURL(t *testing.T) {
	client, err := OpenWriteLDAPClient(map[string]string{
		"ldap_master_url":      "ldap://master:389",
		"ldap_url":             "ldap://replica:389",
		"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password": "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	defer func() { _ = client.Close() }()
}
