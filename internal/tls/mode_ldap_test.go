// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLDAP implements LDAPClient with programmable responses.
type stubLDAP struct {
	entries map[string]*ldap.Entry // keyed by DN, for GetEntry
	// searchResults is consumed in FIFO order: GetProxiesForHost issues two
	// searches when no designated proxies exist (designated, then enumerate).
	searchResults []*ldap.SearchResult
	searchFilters []string // records filters, in call order
	getEntryErr   error
	searchErr     error
	modifyErr     error
	modified      []string // "dn|attr|value" per ModifyAttribute call
}

func (s *stubLDAP) GetEntry(dn string, _ []string) (*ldap.Entry, error) {
	if s.getEntryErr != nil {
		return nil, s.getEntryErr
	}

	entry, ok := s.entries[dn]
	if !ok {
		return nil, errors.New("entry not found: " + dn)
	}

	return entry, nil
}

func (s *stubLDAP) Search(_, filter string, _ []string, _ int) (*ldap.SearchResult, error) {
	s.searchFilters = append(s.searchFilters, filter)
	if s.searchErr != nil {
		return nil, s.searchErr
	}

	if len(s.searchResults) == 0 {
		return &ldap.SearchResult{}, nil
	}

	res := s.searchResults[0]
	s.searchResults = s.searchResults[1:]

	return res, nil
}

func (s *stubLDAP) ModifyAttribute(dn, attribute, value string) error {
	s.modified = append(s.modified, dn+"|"+attribute+"|"+value)

	return s.modifyErr
}

func serverEntry(cn string, attrs map[string][]string) *ldap.Entry {
	entry := &ldap.Entry{DN: "cn=" + cn + ",cn=servers,cn=zimbra"}
	entry.Attributes = append(entry.Attributes, &ldap.EntryAttribute{Name: "cn", Values: []string{cn}})
	for name, values := range attrs {
		entry.Attributes = append(entry.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}

	return entry
}

func searchResult(entries ...*ldap.Entry) *ldap.SearchResult {
	return &ldap.SearchResult{Entries: entries}
}

func TestIsReverseProxyBackend(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"true uppercase", "TRUE", true},
		{"true lowercase", "true", true},
		{"false", "FALSE", false},
		{"absent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := map[string][]string{}
			if tt.value != "" {
				attrs["zimbraReverseProxyLookupTarget"] = []string{tt.value}
			}

			stub := &stubLDAP{entries: map[string]*ldap.Entry{
				"cn=mail.example.com,cn=servers,cn=zimbra": serverEntry("mail.example.com", attrs),
			}}

			got, err := IsReverseProxyBackend(stub, "mail.example.com")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsReverseProxyBackend_Error(t *testing.T) {
	stub := &stubLDAP{getEntryErr: errors.New("ldap down")}

	_, err := IsReverseProxyBackend(stub, "mail.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup backend flag")
}

func TestEnumerateProxies(t *testing.T) {
	stub := &stubLDAP{searchResults: []*ldap.SearchResult{
		searchResult(
			serverEntry("proxy1.example.com", nil),
			serverEntry("proxy2.example.com", nil),
		),
	}}

	proxies, err := EnumerateProxies(stub)
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy1.example.com", "proxy2.example.com"}, proxies)
	require.Len(t, stub.searchFilters, 1)
	assert.Contains(t, stub.searchFilters[0], "zimbraServiceEnabled=proxy")
}

func TestEnumerateProxies_Error(t *testing.T) {
	stub := &stubLDAP{searchErr: errors.New("ldap down")}

	_, err := EnumerateProxies(stub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enumerate proxies")
}

func TestGetProxiesForHost_Designated(t *testing.T) {
	stub := &stubLDAP{searchResults: []*ldap.SearchResult{
		searchResult(serverEntry("proxy1.example.com", nil)),
	}}

	proxies, err := GetProxiesForHost(stub, "mail.example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy1.example.com"}, proxies)
	require.Len(t, stub.searchFilters, 1, "designated hit must not fall back")
	assert.Contains(t, stub.searchFilters[0], "zimbraReverseProxyAvailableLookupTargets")
}

func TestGetProxiesForHost_FallbackToAllProxies(t *testing.T) {
	stub := &stubLDAP{searchResults: []*ldap.SearchResult{
		searchResult(), // no designated proxies
		searchResult(serverEntry("proxy1.example.com", nil), serverEntry("proxy2.example.com", nil)),
	}}

	proxies, err := GetProxiesForHost(stub, "mail.example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"proxy1.example.com", "proxy2.example.com"}, proxies)
	require.Len(t, stub.searchFilters, 2, "must fall back to EnumerateProxies")
}

func TestGetProxiesForHost_Error(t *testing.T) {
	stub := &stubLDAP{searchErr: errors.New("ldap down")}

	_, err := GetProxiesForHost(stub, "mail.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup designated proxies")
}

func TestReadProxySettings(t *testing.T) {
	tests := []struct {
		name      string
		attrs     map[string][]string
		wantMode  string
		wantValid bool
		wantTrue  bool
	}{
		{
			name: "ssl upstream true",
			attrs: map[string][]string{
				attrReverseProxyMailMode: {"HTTPS"},
				attrReverseProxySSLToUp:  {"TRUE"},
			},
			wantMode: "https", wantValid: true, wantTrue: true,
		},
		{
			name: "ssl upstream false",
			attrs: map[string][]string{
				attrReverseProxyMailMode: {"both"},
				attrReverseProxySSLToUp:  {"FALSE"},
			},
			wantMode: "both", wantValid: true, wantTrue: false,
		},
		{
			name:     "ssl upstream absent",
			attrs:    map[string][]string{attrReverseProxyMailMode: {"http"}},
			wantMode: "http", wantValid: false, wantTrue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubLDAP{entries: map[string]*ldap.Entry{
				"cn=proxy1.example.com,cn=servers,cn=zimbra": serverEntry("proxy1.example.com", tt.attrs),
			}}

			s, err := ReadProxySettings(stub, "proxy1.example.com")
			require.NoError(t, err)
			assert.Equal(t, "proxy1.example.com", s.Proxy)
			assert.Equal(t, tt.wantMode, s.MailMode)
			assert.Equal(t, tt.wantValid, s.SSLToUpstreamValid)
			assert.Equal(t, tt.wantTrue, s.SSLToUpstreamTrue)
		})
	}
}

func TestReadProxySettings_Error(t *testing.T) {
	stub := &stubLDAP{getEntryErr: errors.New("ldap down")}

	_, err := ReadProxySettings(stub, "proxy1.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read proxy settings for proxy1.example.com")
}

func TestSetMailMode(t *testing.T) {
	stub := &stubLDAP{}

	require.NoError(t, SetMailMode(stub, "mail.example.com", ModeHTTPS))
	require.Len(t, stub.modified, 1)
	assert.Equal(t, "cn=mail.example.com,cn=servers,cn=zimbra|zimbraMailMode|https", stub.modified[0])
}

func TestSetMailMode_Error(t *testing.T) {
	stub := &stubLDAP{modifyErr: errors.New("modify denied")}

	err := SetMailMode(stub, "mail.example.com", ModeBoth)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set zimbraMailMode=both on mail.example.com")
}
