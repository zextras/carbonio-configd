// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"strconv"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/provision"
)

func newProvDirectory(t *testing.T, s *lwServer) ldapDirectory {
	t.Helper()

	c, err := ldap.NewClient(&ldap.ClientConfig{
		URLs: []string{s.URL}, BindDN: "uid=zimbra,cn=admins,cn=zimbra", Password: "secret", MaxRetries: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	return ldapDirectory{c}
}

func TestLDAPDirectorySearch_MapsScopesAndLowercasesAttributeNames(t *testing.T) {
	scopes := map[provision.Scope]int64{
		provision.ScopeBase: 0,
		provision.ScopeOne:  1,
		provision.ScopeSub:  2,
	}

	for scope, want := range scopes {
		s := lwStart(t, lwOpts{})
		s.Search = func(string, int64, string) ([]lwEntry, uint8) {
			return []lwEntry{{
				DN:    "uid=u,ou=people,dc=x",
				Attrs: map[string][]string{"zimbraMailHost": {"mbox1"}, "objectClass": {"top", "person"}},
			}}, goldap.LDAPResultSuccess
		}

		entries, err := newProvDirectory(t, s).Search("ou=people,dc=x", scope, "(uid=u)", "zimbraMailHost")

		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "uid=u,ou=people,dc=x", entries[0].DN)
		assert.Equal(t, map[string][]string{
			"zimbramailhost": {"mbox1"},
			"objectclass":    {"top", "person"},
		}, entries[0].Attrs)
		assert.Contains(t, s.Seen(), "search ou=people,dc=x|"+strconv.FormatInt(want, 10)+"|(uid=u)|zimbraMailHost")
	}
}

func TestLDAPDirectorySearch_AMissingBaseHasNoEntries(t *testing.T) {
	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) { return nil, goldap.LDAPResultNoSuchObject }

	entries, err := newProvDirectory(t, s).Search("ou=none,dc=x", provision.ScopeSub, "(objectClass=*)")

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestLDAPDirectorySearch_OtherRefusalsAreReturned(t *testing.T) {
	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) {
		return nil, goldap.LDAPResultInsufficientAccessRights
	}

	_, err := newProvDirectory(t, s).Search("dc=x", provision.ScopeBase, "(objectClass=*)")

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights))
}

func TestLDAPDirectoryAdd_CreatesTheEntry(t *testing.T) {
	s := lwStart(t, lwOpts{})

	err := newProvDirectory(t, s).Add("uid=u,dc=x", map[string][]string{"uid": {"u"}})

	require.NoError(t, err)
	assert.Contains(t, s.Seen(), "add uid=u,dc=x|uid=u")
}

func TestLDAPDirectoryAdd_ATakenDNIsErrEntryExists(t *testing.T) {
	s := lwStart(t, lwOpts{})
	s.AddCode = func(string) uint8 { return goldap.LDAPResultEntryAlreadyExists }

	err := newProvDirectory(t, s).Add("uid=u,dc=x", map[string][]string{"uid": {"u"}})

	require.ErrorIs(t, err, provision.ErrEntryExists)
}

func TestLDAPDirectoryAdd_OtherRefusalsAreReturned(t *testing.T) {
	s := lwStart(t, lwOpts{})
	s.AddCode = func(string) uint8 { return goldap.LDAPResultObjectClassViolation }

	err := newProvDirectory(t, s).Add("uid=u,dc=x", map[string][]string{"uid": {"u"}})

	require.Error(t, err)
	assert.NotErrorIs(t, err, provision.ErrEntryExists)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultObjectClassViolation))
}

func TestLDAPDirectoryDelete_RemovesTheEntryAndReportsRefusals(t *testing.T) {
	s := lwStart(t, lwOpts{})
	d := newProvDirectory(t, s)

	require.NoError(t, d.Delete("uid=u,dc=x"))
	assert.Contains(t, s.Seen(), "del uid=u,dc=x")

	s.DelCode = func(string) uint8 { return goldap.LDAPResultNoSuchObject }

	err := d.Delete("uid=gone,dc=x")

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject))
}

func TestProvOpenLDAP_ConnectsToTheMasterWithTheWriteCredentials(t *testing.T) {
	s := lwStart(t, lwOpts{})
	e, _ := certTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{
		"zimbra_server_hostname":  "mail.example.com",
		"ldap_url":                s.URL,
		"ldap_master_url":         s.URL,
		"ldap_is_master":          "true",
		"ldap_starttls_supported": "0",
		"zimbra_ldap_userdn":      "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password":    "secret",
	}))

	dir, err := provOpenLDAP(e)
	require.NoError(t, err)

	defer func() { _ = dir.Close() }()

	require.NoError(t, dir.Add("uid=u,dc=x", map[string][]string{"uid": {"u"}}))
	assert.Contains(t, s.Seen(), "bind uid=zimbra,cn=admins,cn=zimbra")
	assert.Contains(t, s.Seen(), "add uid=u,dc=x|uid=u")
}

func TestProvOpenLDAP_AnIncompleteLocalConfigIsRefused(t *testing.T) {
	e, _ := certTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{"zimbra_server_hostname": "mail.example.com"}))

	_, err := provOpenLDAP(e)

	require.Error(t, err)
}

func TestRunProv_AnUnreadableSchemaIsReported(t *testing.T) {
	e, _, _ := testEnv(t)
	provSchemas.Delete(e)

	err := e.Prov("mcf", "zimbraDefaultDomainName", "example.com")

	require.ErrorIs(t, err, ErrProv)
	assert.Empty(t, provTestLDAP().values(provGlobalDN, "zimbraDefaultDomainName"))
}
