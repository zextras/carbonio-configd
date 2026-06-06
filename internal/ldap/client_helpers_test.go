// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"testing"
)

func TestServerHasService(t *testing.T) {
	tests := []struct {
		name           string
		serviceEnabled string
		serviceName    string
		want           bool
	}{
		{
			name:           "exact match",
			serviceEnabled: "mailbox",
			serviceName:    "mailbox",
			want:           true,
		},
		{
			name:           "multi-valued exact match",
			serviceEnabled: "mailbox\nldap\nmta",
			serviceName:    "ldap",
			want:           true,
		},
		{
			name:           "no match",
			serviceEnabled: "mailbox\nldap\nmta",
			serviceName:    "zimbra",
			want:           false,
		},
		{
			name:           "substring not matched",
			serviceEnabled: "zimbraAdmin\nmailbox",
			serviceName:    "zimbra",
			want:           false,
		},
		{
			name:           "empty input",
			serviceEnabled: "",
			serviceName:    "mailbox",
			want:           false,
		},
		{
			name:           "whitespace trimmed",
			serviceEnabled: " mailbox \n ldap ",
			serviceName:    "ldap",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverHasService(tt.serviceEnabled, tt.serviceName)
			if got != tt.want {
				t.Errorf("serverHasService(%q, %q) = %v, want %v",
					tt.serviceEnabled, tt.serviceName, got, tt.want)
			}
		})
	}
}

// TestQueryDomains_LDAPClientError verifies that QueryDomains returns a non-nil error
// when the underlying LDAP client has no URLs configured.
func TestQueryDomains_LDAPClientError(t *testing.T) {
	c := &Client{} // no URLs → GetAllDomainsWithAttributes will fail
	domains, err := c.QueryDomains(context.Background())
	if err == nil {
		t.Fatal("QueryDomains() with no URLs: expected error, got nil")
	}
	if domains != nil {
		t.Fatalf("QueryDomains() with no URLs: expected nil slice, got %v", domains)
	}
}

// TestQueryServers_LDAPClientError verifies that QueryServers returns a non-nil error
// when the underlying LDAP client has no URLs configured.
func TestQueryServers_LDAPClientError(t *testing.T) {
	c := &Client{} // no URLs → GetAllServersWithAttributes will fail
	servers, err := c.QueryServers(context.Background(), "mailbox")
	if err == nil {
		t.Fatal("QueryServers() with no URLs: expected error, got nil")
	}
	if servers != nil {
		t.Fatalf("QueryServers() with no URLs: expected nil slice, got %v", servers)
	}
}
