// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryDomains(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=example,dc=com", map[string][]string{
			attrZimbraDomainName:               {"example.com"},
			"zimbraVirtualHostname":            {"mail.example.com"},
			"zimbraVirtualIPAddress":           {"10.0.0.1"},
			"zimbraReverseProxyClientCertMode": {"off"},
			"zimbraReverseProxyClientCertCA":   {"CA-PEM"},
		}),
		entryWithAttrs("dc=novhost,dc=com", map[string][]string{
			attrZimbraDomainName: {"novhost.com"},
			// No virtual hostname: must be filtered out.
		}),
	}}
	c, _ := newGetterTestClient(result, nil)

	domains, err := c.QueryDomains(context.Background())
	require.NoError(t, err)
	require.Len(t, domains, 1, "domains without virtual hostname must be filtered")
	assert.Equal(t, "example.com", domains[0].DomainName)
	assert.Equal(t, "mail.example.com", domains[0].VirtualHostname)
	assert.Equal(t, "10.0.0.1", domains[0].VirtualIPAddress)
	assert.Equal(t, "off", domains[0].ClientCertMode)
	assert.Equal(t, "CA-PEM", domains[0].ClientCertCA)
}

// TestQueryDomains_MultipleVirtualHostnames mirrors legacy one-item-per-vhn
// expansion (ProxyConfGen.java:319-352): the i-th vip pairs with the i-th vhn
// when counts match, otherwise the first vip is used.
func TestQueryDomains_MultipleVirtualHostnames(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=b,dc=com", map[string][]string{
			attrZimbraDomainName:     {"b.com"},
			"zimbraVirtualHostname":  {"m1.b.com", "m2.b.com"},
			"zimbraVirtualIPAddress": {"10.0.0.1", "10.0.0.2"},
			"zimbraSSLCertificate":   {"PEM"},
		}),
		entryWithAttrs("dc=a,dc=com", map[string][]string{
			attrZimbraDomainName:     {"a.com"},
			"zimbraVirtualHostname":  {"m1.a.com", "m2.a.com"},
			"zimbraVirtualIPAddress": {"10.1.0.1"},
			"zimbraSSLPrivateKey":    {"KEY"},
		}),
		entryWithAttrs("dc=c,dc=com", map[string][]string{
			attrZimbraDomainName:                 {"c.com"},
			"zimbraVirtualHostname":              {"m.c.com"},
			AttrZimbraReverseProxyClientCertMode: {"on"},
		}),
		entryWithAttrs("dc=d,dc=com", map[string][]string{
			// Virtual hostname but no cert/key/mode/CA: uses the global config (filtered).
			attrZimbraDomainName:    {"d.com"},
			"zimbraVirtualHostname": {"m.d.com"},
		}),
		entryWithAttrs("dc=e,dc=com", map[string][]string{
			// Cert but no virtual hostname: filtered.
			attrZimbraDomainName:   {"e.com"},
			"zimbraSSLCertificate": {"PEM"},
		}),
	}}
	c, _ := newGetterTestClient(result, nil)

	domains, err := c.QueryDomains(context.Background())
	require.NoError(t, err)
	require.Len(t, domains, 5)

	got := make([]string, 0, len(domains))
	for _, d := range domains {
		got = append(got, d.DomainName+"|"+d.VirtualHostname+"|"+d.VirtualIPAddress)
	}

	assert.ElementsMatch(t, []string{
		"a.com|m1.a.com|10.1.0.1", "a.com|m2.a.com|10.1.0.1",
		"b.com|m1.b.com|10.0.0.1", "b.com|m2.b.com|10.0.0.2",
		"c.com|m.c.com|",
	}, got)
	assert.Equal(t, "a.com", domains[0].DomainName, "result must be ordered by domain name")
}

func TestSetDomainSSLCertKey_UnknownDomain(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	err := c.SetDomainSSLCertKey("nope.com", "c", "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such domain")
}

func TestSetDomainSSLCertKey_LookupError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("ldap down"))

	err := c.SetDomainSSLCertKey("x.com", "c", "k")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to look up domain")
}

func TestQueryDomains_Error(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("ldap down"))

	_, err := c.QueryDomains(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get domains with attributes")
}

func TestQueryServers(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=proxy1,cn=servers,cn=zimbra", map[string][]string{
			"cn":                     {"proxy1"},
			"zimbraId":               {"id-1"},
			"zimbraServiceHostname":  {"proxy1.example.com"},
			AttrZimbraServiceEnabled: {"proxy", "stats"},
		}),
		entryWithAttrs("cn=mta1,cn=servers,cn=zimbra", map[string][]string{
			"cn":                     {"mta1"},
			"zimbraId":               {"id-2"},
			"zimbraServiceHostname":  {"mta1.example.com"},
			AttrZimbraServiceEnabled: {"mta"},
		}),
		entryWithAttrs("cn=incomplete,cn=servers,cn=zimbra", map[string][]string{
			"cn":                     {"incomplete"},
			AttrZimbraServiceEnabled: {"proxy"},
			// Missing zimbraId/zimbraServiceHostname: must be skipped.
		}),
	}}
	c, _ := newGetterTestClient(result, nil)

	servers, err := c.QueryServers(context.Background(), "proxy")
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "id-1", servers[0].ServerID)
	assert.Equal(t, "proxy1.example.com", servers[0].ServiceHostname)
}

func TestQueryServers_Error(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("ldap down"))

	_, err := c.QueryServers(context.Background(), "proxy")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query servers")
}

func TestServerHasService_ExactMatch(t *testing.T) {
	assert.True(t, serverHasService("proxy\nmta", "proxy"))
	assert.True(t, serverHasService(" proxy ", "proxy"))
	assert.False(t, serverHasService("zimbraAdmin", "zimbra"), "substring must not match")
	assert.False(t, serverHasService("", "proxy"))
}
