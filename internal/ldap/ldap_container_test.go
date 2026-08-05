//go:build integration
// +build integration

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/config"
)

func newTestLdapMgr(t *testing.T) (*Ldap, *Client) {
	t.Helper()

	client := newTestClient(t)

	mgr := NewLdap(context.Background(), nil)
	mgr.SetNativeClient(context.Background(), client)

	return mgr, client
}

func TestContainer_SetNativeClient(t *testing.T) {
	client := newTestClient(t)

	mgr := NewLdap(context.Background(), nil)
	if mgr.NativeClient != nil {
		t.Fatal("NativeClient should be nil before SetNativeClient")
	}

	mgr.SetNativeClient(context.Background(), client)

	if mgr.NativeClient == nil {
		t.Fatal("NativeClient is nil after SetNativeClient")
	}
}

func TestContainer_QueryDomains(t *testing.T) {
	mgr, _ := newTestLdapMgr(t)

	domains, err := mgr.NativeClient.QueryDomains(context.Background())
	if err != nil {
		t.Fatalf("QueryDomains: %v", err)
	}

	t.Logf("QueryDomains returned %d domain(s)", len(domains))
	for _, d := range domains {
		t.Logf("  domain=%s vhost=%s vip=%s", d.DomainName, d.VirtualHostname, d.VirtualIPAddress)
	}
}

func TestContainer_QueryServers_Known(t *testing.T) {
	mgr, _ := newTestLdapMgr(t)

	servers, err := mgr.NativeClient.QueryServers(context.Background(), "ldap")
	if err != nil {
		t.Fatalf("QueryServers(ldap): %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("QueryServers(ldap) returned no servers")
	}

	for _, s := range servers {
		if s.ServiceHostname == "" {
			t.Fatal("server has empty ServiceHostname")
		}
	}
}

func TestContainer_QueryServers_Unknown(t *testing.T) {
	mgr, _ := newTestLdapMgr(t)

	servers, err := mgr.NativeClient.QueryServers(context.Background(), "nonexistent-service-12345")
	if err != nil {
		t.Fatalf("QueryServers(nonexistent): %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("QueryServers(nonexistent) returned %d servers, want 0", len(servers))
	}
}

// TestContainer_Ldap_ModifyAttribute exercises the Ldap wrapper write path
// end-to-end against a real LDAP container: keymap resolution -> transform ->
// NativeClient.ModifyAttribute -> read-back -> restore. This covers the wiring
// that unit tests can only check with a nil NativeClient.
func TestContainer_Ldap_ModifyAttribute(t *testing.T) {
	client := newTestClient(t)

	// A non-nil config is required: ModifyAttribute reads config.LdapIsMaster.
	mgr := NewLdap(context.Background(), &config.Config{LdapIsMaster: true})
	mgr.IsMaster = true
	mgr.SetNativeClient(context.Background(), client)

	// keyLDAPCommonLoglevel maps to olcLogLevel on cn=config with a "%s"
	// transform, so the wrapper resolves DN/attr from the keymap and writes
	// through the native client.
	const key = keyLDAPCommonLoglevel
	const dn = ldapCnConfig
	const attr = "olcLogLevel"

	before, err := client.getEntityConfig(dn, ldapFilterAllObjects, ldap.ScopeBaseObject, "config", dn)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	original := before[attr]

	if err := mgr.ModifyAttribute(context.Background(), key, "256"); err != nil {
		t.Fatalf("Ldap.ModifyAttribute: %v", err)
	}

	after, err := client.getEntityConfig(dn, ldapFilterAllObjects, ldap.ScopeBaseObject, "config", dn)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if got := after[attr]; got != "256" {
		t.Fatalf("%s = %q, want %q", attr, got, "256")
	}

	// Restore the original value when one existed.
	if original != "" && original != "256" {
		if err := mgr.ModifyAttribute(context.Background(), key, original); err != nil {
			t.Fatalf("Ldap.ModifyAttribute restore: %v", err)
		}
	}
}
