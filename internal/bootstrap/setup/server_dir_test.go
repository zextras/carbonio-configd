// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"os"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

const (
	lwSrvBind = "uid=zimbra,cn=admins,cn=zimbra"
	lwSrvPw   = "Admin-Secret-7"
)

// lwSrvDir is the directory the server steps read, over the fake server.
func lwSrvDir(t *testing.T, s *lwServer) *srvLDAP {
	t.Helper()

	c, err := ldap.NewClient(&ldap.ClientConfig{
		URLs: []string{s.URL}, BindDN: lwSrvBind, Password: lwSrvPw, MaxRetries: 1,
	})
	require.NoError(t, err)

	d := &srvLDAP{c: c}
	t.Cleanup(func() { _ = d.Close() })

	return d
}

// lwSrvRefuse answers every search with the code.
func lwSrvRefuse(code uint8) func(string, int64, string) ([]lwEntry, uint8) {
	return func(string, int64, string) ([]lwEntry, uint8) { return nil, code }
}

func TestSrvLDAP_GlobalReadsTheConfigEntryWithEveryValueLowerCased(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) {
		return []lwEntry{{DN: "cn=config,cn=zimbra", Attrs: map[string][]string{
			"zimbraMtaRestriction": {"reject_non_fqdn_sender", "reject_unknown_sender_domain"},
			"zimbraMailMode":       {"https"},
		}}}, goldap.LDAPResultSuccess
	}
	d := lwSrvDir(t, s)

	got, err := d.Global()
	require.NoError(t, err)

	assert.Equal(t, srvEntry{
		"zimbramtarestriction": {"reject_non_fqdn_sender", "reject_unknown_sender_domain"},
		"zimbramailmode":       {"https"},
	}, got)
	assert.Equal(t, "https", got.first("zimbraMailMode"), "lookups are case-insensitive")
	assert.Equal(t, []string{"reject_non_fqdn_sender", "reject_unknown_sender_domain"},
		got.values("zimbraMtaRestriction"), "a multi-valued attribute keeps every value")

	assert.Equal(t, []string{"bind " + lwSrvBind, "search cn=config,cn=zimbra|0|(objectClass=*)|*"}, s.Seen(),
		"the global config is read as the base object")
}

func TestSrvLDAP_GlobalErrors(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	d := lwSrvDir(t, s)

	_, err := d.Global()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global config not found")

	s.Search = lwSrvRefuse(goldap.LDAPResultInsufficientAccessRights)

	_, err = d.Global()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global config:")
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights), "%v", err)
}

func TestSrvLDAP_ServerAndCOSSearchOneLevelByCommonName(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(base string, _ int64, _ string) ([]lwEntry, uint8) {
		if base == "cn=servers,cn=zimbra" {
			return []lwEntry{{DN: "cn=mail1.example.com,cn=servers,cn=zimbra", Attrs: map[string][]string{
				"zimbraServiceEnabled": {"mailbox", "mta"},
			}}}, goldap.LDAPResultSuccess
		}

		return []lwEntry{{DN: "cn=default,cn=cos,cn=zimbra", Attrs: map[string][]string{
			"zimbraMailQuota": {"0"},
		}}}, goldap.LDAPResultSuccess
	}
	d := lwSrvDir(t, s)

	srv, found, err := d.Server("mail1.example.com")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []string{"mailbox", "mta"}, srv.values("zimbraServiceEnabled"))

	cos, found, err := d.COS("default")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "0", cos.first("zimbraMailQuota"))

	assert.Equal(t, []string{
		"bind " + lwSrvBind,
		"search cn=servers,cn=zimbra|1|(&(objectClass=zimbraServer)(cn=mail1.example.com))|*",
		"search cn=cos,cn=zimbra|1|(&(objectClass=zimbraCOS)(cn=default))|*",
	}, s.Seen(), "one level under cn=servers / cn=cos, not a subtree walk")
}

func TestSrvLDAP_NameIsEscapedInTheFilter(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	d := lwSrvDir(t, s)

	_, found, err := d.Server("a*)(uid=*")
	require.NoError(t, err)
	assert.False(t, found)

	seen := s.Seen()
	require.Len(t, seen, 2)
	assert.Equal(t, `search cn=servers,cn=zimbra|1|(&(objectClass=zimbraServer)(cn=a\2a\29\28uid=\2a))|*`, seen[1],
		"a name must not be able to widen the search")
}

func TestSrvLDAP_NoSuchServerOrCOSIsNotAnError(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	d := lwSrvDir(t, s)

	srv, found, err := d.Server("nobody.example.com")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, srv)

	cos, found, err := d.COS("nothing")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, cos)
}

func TestSrvLDAP_ServerAndCOSErrorsNameTheEntry(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = lwSrvRefuse(goldap.LDAPResultInsufficientAccessRights)
	d := lwSrvDir(t, s)

	_, found, err := d.Server("mail1.example.com")
	require.Error(t, err)
	assert.False(t, found)
	assert.Contains(t, err.Error(), "zimbraServer mail1.example.com:")

	_, found, err = d.COS("default")
	require.Error(t, err)
	assert.False(t, found)
	assert.Contains(t, err.Error(), "zimbraCOS default:")
}

func TestSrvLDAP_ServerNamesAppliesTheFilterFragment(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(_ string, _ int64, filter string) ([]lwEntry, uint8) {
		if filter != "(&(objectClass=zimbraServer)(zimbraServiceEnabled=mta))" {
			return nil, goldap.LDAPResultSuccess
		}

		return []lwEntry{
			{DN: "cn=mta1.example.com,cn=servers,cn=zimbra", Attrs: map[string][]string{"cn": {"mta1.example.com"}}},
			{DN: "cn=mta2.example.com,cn=servers,cn=zimbra", Attrs: map[string][]string{"cn": {"mta2.example.com"}}},
		}, goldap.LDAPResultSuccess
	}
	d := lwSrvDir(t, s)

	names, err := d.ServerNames("(zimbraServiceEnabled=mta)")
	require.NoError(t, err)
	assert.Equal(t, []string{"mta1.example.com", "mta2.example.com"}, names)

	none, err := d.ServerNames("(zimbraServiceEnabled=proxy)")
	require.NoError(t, err)
	assert.Empty(t, none)

	assert.Equal(t, "search cn=servers,cn=zimbra|1|(&(objectClass=zimbraServer)(zimbraServiceEnabled=proxy))|*",
		s.Seen()[len(s.Seen())-1])
}

func TestSrvLDAP_ServerNamesEmptyFilterListsAllAndErrorsAreWrapped(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	d := lwSrvDir(t, s)

	_, err := d.ServerNames("")
	require.NoError(t, err)
	assert.Equal(t, "search cn=servers,cn=zimbra|1|(&(objectClass=zimbraServer))|*", s.Seen()[1])

	s.Search = lwSrvRefuse(goldap.LDAPResultInsufficientAccessRights)

	_, err = d.ServerNames("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "servers:")
}

func TestSrvLDAP_VIPDomainsSearchesTheWholeTreeForDomainsWithAVIP(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) {
		return []lwEntry{
			{DN: "zimbraDomainName=a.example.com,cn=zimbra", Attrs: map[string][]string{
				"zimbraDomainName": {"a.example.com"}, "zimbraVirtualIPAddress": {"10.0.0.5", "10.0.0.6"},
			}},
		}, goldap.LDAPResultSuccess
	}
	d := lwSrvDir(t, s)

	doms, err := d.VIPDomains()
	require.NoError(t, err)
	require.Len(t, doms, 1)
	assert.Equal(t, "a.example.com", doms[0].first("zimbraDomainName"))
	assert.Equal(t, []string{"10.0.0.5", "10.0.0.6"}, doms[0].values("zimbraVirtualIPAddress"))
	assert.Equal(t, "search |2|(&(objectClass=zimbraDomain)(zimbraVirtualIPAddress=*))|*", s.Seen()[1])

	s.Search = lwSrvRefuse(goldap.LDAPResultInsufficientAccessRights)

	_, err = d.VIPDomains()
	require.Error(t, err)
}

func TestSrvLDAP_CloseReleasesTheClient(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	d := lwSrvDir(t, s)

	_, err := d.ServerNames("")
	require.NoError(t, err)
	require.NoError(t, d.Close())

	_, err = d.ServerNames("")
	assert.Error(t, err, "a closed directory cannot be read")
}

func TestSrvOpenDir_UsesTheMasterAndCredentialsOfTheLocalConfig(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) {
		return []lwEntry{{DN: "cn=config,cn=zimbra", Attrs: map[string][]string{"zimbraMailMode": {"https"}}}},
			goldap.LDAPResultSuccess
	}

	e, _, _ := ldapTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{
		"ldap_master_url":      s.URL,
		"zimbra_ldap_userdn":   lwSrvBind,
		"zimbra_ldap_password": lwSrvPw,
	}))

	var mode string

	require.NoError(t, srvRead(e, func(d srvDirectory) error {
		g, err := d.Global()
		mode = g.first("zimbraMailMode")

		return err
	}))
	assert.Equal(t, "https", mode)
	assert.Equal(t, "bind "+lwSrvBind, s.Seen()[0], "the directory is read with the localconfig account")
}

func TestSrvRead_ReportsAnUnusableLocalConfig(t *testing.T) {
	t.Parallel()

	e, _, _ := ldapTestEnv(t)
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte("<localconfig><key"), 0o600))

	called := false
	err := srvRead(e, func(srvDirectory) error { called = true; return nil })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "opening LDAP:")
	assert.False(t, called)
}
