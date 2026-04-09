// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"testing"
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

	domains, err := mgr.QueryDomains(context.Background())
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

	servers, err := mgr.QueryServers(context.Background(), "ldap")
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

	servers, err := mgr.QueryServers(context.Background(), "nonexistent-service-12345")
	if err != nil {
		t.Fatalf("QueryServers(nonexistent): %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("QueryServers(nonexistent) returned %d servers, want 0", len(servers))
	}
}
