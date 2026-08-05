// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedSearch captures the arguments of the last search call.
type recordedSearch struct {
	baseDN     string
	filter     string
	attributes []string
	scope      int
}

// newGetterTestClient builds a Client whose search round-trip is stubbed.
// The returned *recordedSearch is updated on every call.
func newGetterTestClient(result *ldap.SearchResult, err error) (*Client, *recordedSearch) {
	rec := &recordedSearch{}
	c := &Client{baseDN: defaultBaseDN}
	c.searchFn = func(baseDN, filter string, attributes []string, scope int) (*ldap.SearchResult, error) {
		rec.baseDN = baseDN
		rec.filter = filter
		rec.attributes = attributes
		rec.scope = scope

		return result, err
	}

	return c, rec
}

func entryWithAttrs(dn string, attrs map[string][]string) *ldap.Entry {
	entry := &ldap.Entry{DN: dn}
	for name, values := range attrs {
		entry.Attributes = append(entry.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}

	return entry
}

func TestGetGlobalConfig(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=config,cn=zimbra", map[string][]string{
			"zimbraSmtpHostname": {"mail.example.com"},
			"cn":                 {"config"},
		}),
	}}
	c, rec := newGetterTestClient(result, nil)

	cfg, err := c.GetGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "mail.example.com", cfg["zimbraSmtpHostname"])
	assert.Equal(t, "cn=config,cn=zimbra", rec.baseDN)
	assert.Equal(t, ldap.ScopeBaseObject, rec.scope)
}

func TestGetGlobalConfig_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetGlobalConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get global config")
}

func TestGetGlobalConfig_NotFound(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	_, err := c.GetGlobalConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global config not found")
}

func TestGetServerConfig(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=mail.example.com,cn=servers,cn=zimbra", map[string][]string{
			"zimbraServiceEnabled": {"proxy", "mta"},
		}),
	}}
	c, rec := newGetterTestClient(result, nil)

	cfg, err := c.GetServerConfig("mail.example.com")
	require.NoError(t, err)
	// Multi-valued attributes are newline-joined by entryToMap.
	assert.Equal(t, "proxy\nmta", cfg["zimbraServiceEnabled"])
	assert.Equal(t, "cn=mail.example.com,cn=servers,cn=zimbra", rec.baseDN)
}

func TestGetServerConfig_NotFound(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	_, err := c.GetServerConfig("ghost.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server not found: ghost.example.com")
}

func TestGetAllServers(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=a,cn=servers,cn=zimbra", map[string][]string{"cn": {"a.example.com"}}),
		entryWithAttrs("cn=b,cn=servers,cn=zimbra", map[string][]string{"cn": {"b.example.com"}}),
		entryWithAttrs("cn=empty,cn=servers,cn=zimbra", map[string][]string{"cn": {""}}),
	}}
	c, rec := newGetterTestClient(result, nil)

	servers, err := c.GetAllServers()
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, servers)
	assert.Equal(t, "cn=servers,cn=zimbra", rec.baseDN)
	assert.Equal(t, "(objectClass=zimbraServer)", rec.filter)
	assert.Equal(t, ldap.ScopeSingleLevel, rec.scope)
}

func TestGetAllServers_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("conn refused"))

	_, err := c.GetAllServers()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all servers")
}

func TestGetAllServersWithAttributes(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=a,cn=servers,cn=zimbra", map[string][]string{
			"cn":                   {"a.example.com"},
			"zimbraServiceEnabled": {"mta"},
		}),
		entryWithAttrs("cn=nokey,cn=servers,cn=zimbra", map[string][]string{"description": {"no cn"}}),
	}}
	c, _ := newGetterTestClient(result, nil)

	servers, err := c.GetAllServersWithAttributes()
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "mta", servers["a.example.com"]["zimbraServiceEnabled"])
}

func TestGetAllServersWithAttributes_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetAllServersWithAttributes()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all servers with attributes")
}

func TestGetAllDomains(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=example,dc=com", map[string][]string{attrZimbraDomainName: {"example.com"}}),
		entryWithAttrs("dc=other,dc=net", map[string][]string{attrZimbraDomainName: {"other.net"}}),
		entryWithAttrs("dc=anon,dc=net", map[string][]string{"description": {"missing name"}}),
	}}
	c, rec := newGetterTestClient(result, nil)

	domains, err := c.GetAllDomains()
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com", "other.net"}, domains)
	// Domains live under DC-based subtrees: search must start at root DSE.
	assert.Empty(t, rec.baseDN)
	assert.Equal(t, ldap.ScopeWholeSubtree, rec.scope)
}

func TestGetAllDomains_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetAllDomains()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all domains")
}

func TestGetDomain(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=example,dc=com", map[string][]string{
			attrZimbraDomainName: {"example.com"},
			"zimbraDomainType":   {"local"},
		}),
	}}
	c, rec := newGetterTestClient(result, nil)

	cfg, err := c.GetDomain("example.com")
	require.NoError(t, err)
	assert.Equal(t, "local", cfg["zimbraDomainType"])
	assert.Equal(t, "(&(objectClass=zimbraDomain)(zimbraDomainName=example.com))", rec.filter)
}

func TestGetDomain_EscapesFilter(t *testing.T) {
	c, rec := newGetterTestClient(&ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=x", map[string][]string{attrZimbraDomainName: {"x"}}),
	}}, nil)

	_, err := c.GetDomain("evil)(objectClass=*")
	require.NoError(t, err)
	assert.NotContains(t, rec.filter, "evil)(objectClass=*", "filter injection must be escaped")
}

func TestGetDomain_NotFound(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	_, err := c.GetDomain("ghost.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain not found: ghost.example.com")
}

func TestGetDomain_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetDomain("example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get domain config for example.com")
}

func TestGetAllDomainsWithAttributes(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("dc=example,dc=com", map[string][]string{
			attrZimbraDomainName: {"example.com"},
			"zimbraDomainType":   {"local"},
		}),
		entryWithAttrs("dc=anon", map[string][]string{"description": {"skipped"}}),
	}}
	c, _ := newGetterTestClient(result, nil)

	domains, err := c.GetAllDomainsWithAttributes()
	require.NoError(t, err)
	require.Len(t, domains, 1)
	assert.Equal(t, "local", domains["example.com"]["zimbraDomainType"])
}

func TestGetAllDomainsWithAttributes_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetAllDomainsWithAttributes()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get all domains with attributes")
}

func TestGetEnabledServices(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=mail.example.com,cn=servers,cn=zimbra", map[string][]string{
			AttrZimbraServiceEnabled: {"proxy", "mta", "ldap"},
		}),
	}}
	c, rec := newGetterTestClient(result, nil)

	services, err := c.GetEnabledServices("mail.example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy", "mta", "ldap"}, services)
	assert.Equal(t, []string{AttrZimbraServiceEnabled}, rec.attributes)
}

func TestGetEnabledServices_NotFound(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	_, err := c.GetEnabledServices("ghost.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server not found: ghost.example.com")
}

func TestGetEnabledServices_SearchError(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, err := c.GetEnabledServices("mail.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get enabled services")
}

func TestGetEntry_AndReadAttribute(t *testing.T) {
	result := &ldap.SearchResult{Entries: []*ldap.Entry{
		entryWithAttrs("cn=config,cn=zimbra", map[string][]string{
			"withValue": {"v1", "v2"},
			"empty":     {},
		}),
	}}
	c, _ := newGetterTestClient(result, nil)

	entry, err := c.GetEntry("cn=config,cn=zimbra", []string{"*"})
	require.NoError(t, err)
	assert.Equal(t, "cn=config,cn=zimbra", entry.DN)

	value, present, err := c.ReadAttribute("cn=config,cn=zimbra", "withValue")
	require.NoError(t, err)
	assert.True(t, present)
	assert.Equal(t, "v1", value)

	value, present, err = c.ReadAttribute("cn=config,cn=zimbra", "empty")
	require.NoError(t, err)
	assert.True(t, present)
	assert.Empty(t, value)

	_, present, err = c.ReadAttribute("cn=config,cn=zimbra", "absent")
	require.NoError(t, err)
	assert.False(t, present)
}

func TestGetEntry_NotFound(t *testing.T) {
	c, _ := newGetterTestClient(&ldap.SearchResult{}, nil)

	_, err := c.GetEntry("cn=ghost", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entry not found")
}

func TestReadAttribute_Error(t *testing.T) {
	c, _ := newGetterTestClient(nil, errors.New("boom"))

	_, _, err := c.ReadAttribute("cn=x", "attr")
	require.Error(t, err)
}

func TestProbeDN(t *testing.T) {
	okClient, _ := newGetterTestClient(&ldap.SearchResult{}, nil)
	assert.True(t, okClient.ProbeDN("cn=accesslog"))

	failClient, _ := newGetterTestClient(nil, errors.New("no such object"))
	assert.False(t, failClient.ProbeDN("cn=accesslog"))
}
