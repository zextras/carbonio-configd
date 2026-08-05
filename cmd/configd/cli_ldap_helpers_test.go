// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
)

func TestOpenWriteLDAPClient_NoURLs(t *testing.T) {
	_, err := openWriteLDAPClient(map[string]string{})
	if err == nil {
		t.Fatal("expected error when both ldap_master_url and ldap_url are empty")
	}

	if !strings.Contains(err.Error(), "LDAP not configured") {
		t.Errorf("error = %q, want substring %q", err, "LDAP not configured")
	}
}

func TestOpenWriteLDAPClient_WhitespaceOnly(t *testing.T) {
	_, err := openWriteLDAPClient(map[string]string{
		"ldap_master_url": "   ",
		"ldap_url":        "\t\n",
	})
	if err == nil {
		t.Fatal("expected error when ldap_master_url + ldap_url are only whitespace")
	}
}

func TestOpenWriteLDAPClient_FallsBackToLdapURL(t *testing.T) {
	client, err := openWriteLDAPClient(map[string]string{
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
	client, err := openWriteLDAPClient(map[string]string{
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
