// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
)

// createAdmin creates the admin account as the bootstrap does.
func (r *rig) createAdmin(t *testing.T) string {
	t.Helper()

	return r.run(t, "ca", adminAddr, adminPass,
		"zimbraAdminConsoleUIComponents", "cartBlancheUI",
		"description", "Administrative Account",
		"displayName", "Carbonio Admin",
		"zimbraIsAdminAccount", "TRUE")
}

func TestCreateAccount_WritesTheAdminAccountEntry(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)

	id := r.createAdmin(t)

	assert.Regexp(t, uuidPattern, id)
	assert.Equal(t, map[string][]string{
		"objectclass":                    {"inetOrgPerson", "zimbraAccount", "amavisAccount"},
		"zimbraid":                       {id},
		"zimbracreatetimestamp":          {"20261112223616.047Z"},
		"zimbraaccountstatus":            {"active"},
		"zimbramailhost":                 {host},
		"zimbramailtransport":            {"lmtp:" + host + ":7025"},
		"zimbramailstatus":               {"enabled"},
		"zimbramaildeliveryaddress":      {adminAddr},
		"mail":                           {adminAddr},
		"cn":                             {"Carbonio Admin"},
		"sn":                             {"admin"},
		"uid":                            {"admin"},
		"zimbrapasswordmodifiedtime":     {"20261112223616.047Z"},
		"zimbraadminconsoleuicomponents": {"cartBlancheUI"},
		"description":                    {"Administrative Account"},
		"displayname":                    {"Carbonio Admin"},
		"zimbraisadminaccount":           {"TRUE"},
	}, r.dir.Attrs(adminDN))

	assert.Equal(t, map[string]string{adminDN: adminPass}, r.dir.Passwords, "the server hashes the password it is given")
	assert.Empty(t, r.dir.Values(adminDN, "userPassword"))
}

func TestCreateAccount_NameDefaultsAndPasswordHandling(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)

	r.run(t, "ca", "Plain@Example.com", "")
	r.run(t, "ca", "hashed@example.com", "ignored", "userPassword", "{SSHA}abc", "zimbraAccountStatus", "locked")

	plain := "uid=plain,ou=people,dc=example,dc=com"
	assert.Equal(t, []string{"plain"}, r.dir.Values(plain, "cn"), "the cn is the local part without a display name")
	assert.Equal(t, []string{"plain"}, r.dir.Values(plain, "sn"))
	assert.Equal(t, []string{"plain@example.com"}, r.dir.Values(plain, "mail"), "the address is lower-cased")

	hashed := "uid=hashed,ou=people,dc=example,dc=com"
	assert.Equal(t, []string{"{SSHA}abc"}, r.dir.Values(hashed, "userPassword"))
	assert.Equal(t, []string{"locked"}, r.dir.Values(hashed, "zimbraAccountStatus"))
	assert.Empty(t, r.dir.Passwords, "no password is set for an account without one or that comes hashed")
}

func TestCreateAccount_Refusals(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.createAdmin(t)

	for name, tc := range map[string]struct {
		args []string
		code string
	}{
		"exists":        {[]string{"ca", adminAddr, "x"}, provision.CodeAccountExists},
		"no domain":     {[]string{"ca", "a@nowhere.org", "x"}, provision.CodeNoSuchDomain},
		"not an email":  {[]string{"ca", "nobody", "x"}, provision.CodeInvalidRequest},
		"two ats":       {[]string{"ca", "a@b@example.com", "x"}, provision.CodeInvalidRequest},
		"display name":  {[]string{"ca", "Name <n@example.com>", "x"}, provision.CodeInvalidAttrValue},
		"derived uid":   {[]string{"ca", "u@example.com", "x", "uid", "other"}, provision.CodeInvalidRequest},
		"derived alias": {[]string{"ca", "u@example.com", "x", "zimbraMailAlias", "u2@example.com"}, provision.CodeInvalidRequest},
		"status":        {[]string{"ca", "u@example.com", "x", "zimbraAccountStatus", "bogus"}, provision.CodeInvalidAttrValue},
		"add form":      {[]string{"ca", "u@example.com", "x", "+description", "d"}, provision.CodeInvalidRequest},
		"cos missing":   {[]string{"ca", "u@example.com", "x", "zimbraCOSId", "cccccccc-0000-4000-8000-0000000000aa"}, provision.CodeNoSuchCOS},
		"cos not an id": {[]string{"ca", "u@example.com", "x", "zimbraCOSId", "nosuch"}, provision.CodeInvalidAttrValue},
		"odd attribute": {[]string{"ca", "u@example.com", "x", "description"}, provision.CodeInvalidRequest},
	} {
		_, err := r.Exec(tc.args)
		assert.True(t, hasCode(err, tc.code), "%s: %v", name, err)
	}
}

func TestCreateAccount_RefusesADomainThatIsNotLocalOrLimitsAccounts(t *testing.T) {
	r := newRig(t)
	r.run(t, "cd", "alias.example.org", "zimbraDomainType", "alias")
	r.run(t, "cd", "limited.example.org", "zimbraDomainMaxAccounts", "5")

	for _, d := range []string{"alias.example.org", "limited.example.org"} {
		_, err := r.Exec([]string{"ca", "a@" + d, "", "zimbraMailHost", host})
		assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%s: %v", d, err)
	}
}

func TestCreateAccount_PasswordPolicyOfTheClassOfService(t *testing.T) {
	for name, tc := range map[string]struct {
		policy   map[string]string
		password string
		ok       bool
	}{
		"no policy":        {nil, "x", true},
		"too short":        {map[string]string{"zimbraPasswordMinLength": "8"}, "short", false},
		"long enough":      {map[string]string{"zimbraPasswordMinLength": "8"}, "long-enough", true},
		"too long":         {map[string]string{"zimbraPasswordMaxLength": "4"}, "toolong", false},
		"upper needed":     {map[string]string{"zimbraPasswordMinUpperCaseChars": "1"}, "lower1", false},
		"upper given":      {map[string]string{"zimbraPasswordMinUpperCaseChars": "1"}, "Lower1", true},
		"lower needed":     {map[string]string{"zimbraPasswordMinLowerCaseChars": "1"}, "UPPER1", false},
		"digit needed":     {map[string]string{"zimbraPasswordMinNumericChars": "2"}, "pass1", false},
		"digits given":     {map[string]string{"zimbraPasswordMinNumericChars": "2"}, "pass12", true},
		"punct needed":     {map[string]string{"zimbraPasswordMinPunctuationChars": "1"}, "pass12", false},
		"punct given":      {map[string]string{"zimbraPasswordMinPunctuationChars": "1"}, "pass-12", true},
		"alpha needed":     {map[string]string{"zimbraPasswordMinAlphaChars": "4"}, "ab-123", false},
		"digit or punct":   {map[string]string{"zimbraPasswordMinDigitsOrPuncs": "2"}, "abc1", false},
		"digit or punct 2": {map[string]string{"zimbraPasswordMinDigitsOrPuncs": "2"}, "abc1-", true},
		"allowed chars":    {map[string]string{"zimbraPasswordAllowedChars": "[a-z]"}, "abC", false},
		"allowed punct":    {map[string]string{"zimbraPasswordAllowedPunctuationChars": "[-]", "zimbraPasswordMinPunctuationChars": "1"}, "ab_c", false},
		"allowed punct ok": {map[string]string{"zimbraPasswordAllowedPunctuationChars": "[-]", "zimbraPasswordMinPunctuationChars": "1"}, "ab-c", true},
		"bad regex":        {map[string]string{"zimbraPasswordAllowedChars": "[a-"}, "abc", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.withServer(t)
			r.withDomain(t)

			args := []string{"mc", "default"}
			for k, v := range tc.policy {
				args = append(args, k, v)
			}

			if len(tc.policy) > 0 {
				r.run(t, args...)
			}

			_, err := r.Exec([]string{"ca", "p@example.com", tc.password})
			if tc.ok {
				require.NoError(t, err)

				return
			}

			assert.True(t, hasCode(err, provision.CodeBadPassword), "%v", err)
			assert.False(t, r.dir.Exists("uid=p,ou=people,dc=example,dc=com"), "no account is created with a password the policy refuses")
		})
	}
}

func TestCreateAccount_PasswordPolicyGivenAtCreationWins(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.run(t, "mc", "default", "zimbraPasswordMinLength", "20")

	r.run(t, "ca", "p@example.com", "short", "zimbraPasswordMinLength", "3")

	assert.Equal(t, "short", r.dir.Passwords["uid=p,ou=people,dc=example,dc=com"])
}

func TestCreateAccount_SetPasswordFailureIsReported(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.dir.FailPassword["uid=p,ou=people,dc=example,dc=com"] = errors.New("constraint violation")

	_, err := r.Exec([]string{"ca", "p@example.com", "secret-pass"})

	require.Error(t, err)
	assert.True(t, hasCode(err, provision.CodeFailure))
	assert.NotContains(t, err.Error(), "secret-pass")
	assert.True(t, r.dir.Exists("uid=p,ou=people,dc=example,dc=com"))
}

func TestCreateAccount_ClassOfServiceSelection(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)

	other := "cn=other,cn=cos,cn=zimbra"
	r.dir.Set(other, "objectClass", "zimbraCOS")
	r.dir.Set(other, "zimbraId", "aaaaaaaa-0000-4000-8000-000000000002")
	r.dir.Set(other, "cn", "other")
	r.dir.Set(other, "zimbraPasswordMinLength", "12")

	r.run(t, "ca", "named@example.com", "", "zimbraCOSId", "aaaaaaaa-0000-4000-8000-000000000002")
	assert.Equal(t, []string{"aaaaaaaa-0000-4000-8000-000000000002"}, r.dir.Values("uid=named,ou=people,dc=example,dc=com", "zimbraCOSId"))

	_, err := r.Exec([]string{"ca", "strict@example.com", "short", "zimbraCOSId", "aaaaaaaa-0000-4000-8000-000000000002"})
	assert.True(t, hasCode(err, provision.CodeBadPassword), "the class of service given applies: %v", err)

	r.dir.Set(domainDN, "zimbraDomainDefaultCOSId", "aaaaaaaa-0000-4000-8000-000000000002")
	_, err = r.Exec([]string{"ca", "viadomain@example.com", "short"})
	assert.True(t, hasCode(err, provision.CodeBadPassword), "the default class of service of the domain applies: %v", err)

	r.dir.Set(domainDN, "zimbraDomainDefaultCOSId", "bbbbbbbb-0000-4000-8000-0000000000ff")
	r.run(t, "ca", "fallback@example.com", "short")
	assert.Empty(t, r.dir.Values("uid=fallback,ou=people,dc=example,dc=com", "zimbraCOSId"), "no class of service id is stored for the default")
}

func TestCreateAccount_MailHostSelection(t *testing.T) {
	r := newRig(t)
	r.run(t, "cd", "example.com")

	_, err := r.Exec([]string{"ca", "nohost@example.com", ""})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "no server can host the mailbox: %v", err)

	// The first usable server is taken when there is no local server.
	r.run(t, "cs", "a.example.com", "zimbraServiceEnabled", "service")
	r.run(t, "ca", "first@example.com", "")
	assert.Equal(t, []string{"a.example.com"}, r.dir.Values("uid=first,ou=people,dc=example,dc=com", "zimbraMailHost"))

	// The pool of the class of service wins, and a server that cannot host is skipped.
	poolID := r.run(t, "cs", "pool.example.com", "zimbraServiceEnabled", "service", "zimbraLmtpBindPort", "7026")
	idle := r.run(t, "cs", "idle.example.com", "zimbraServiceEnabled", "service")
	r.run(t, "mc", "default", "zimbraMailHostPool", idle, "zimbraMailHostPool", poolID)
	r.run(t, "ms", "idle.example.com", "-zimbraServiceEnabled", "service")
	r.warns = nil

	r.run(t, "ca", "pooled@example.com", "")
	pooled := "uid=pooled,ou=people,dc=example,dc=com"
	assert.Equal(t, []string{"pool.example.com"}, r.dir.Values(pooled, "zimbraMailHost"))
	assert.Equal(t, []string{"lmtp:pool.example.com:7026"}, r.dir.Values(pooled, "zimbraMailTransport"))
	assert.Contains(t, r.warns, "cos(%s) mailHostPool server(%s) is not a mailclient server with service webapp enabled")

	// A given transport is kept, and a host with it is refused as in Java.
	r.run(t, "ca", "given@example.com", "", "zimbraMailTransport", "lmtp:elsewhere:7025")
	assert.Equal(t, []string{"lmtp:elsewhere:7025"}, r.dir.Values("uid=given,ou=people,dc=example,dc=com", "zimbraMailTransport"))

	_, err = r.Exec([]string{"ca", "both@example.com", "", "zimbraMailHost", "x", "zimbraMailTransport", "lmtp:x:7025"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%v", err)
}

func TestCreateAccount_LocalServerHostsWhenThePoolIsEmpty(t *testing.T) {
	r := newRig(t)
	r.run(t, "cs", "a.example.com", "zimbraServiceEnabled", "service")
	r.withServer(t)
	r.withDomain(t)

	r.run(t, "ca", "local@example.com", "")

	assert.Equal(t, []string{host}, r.dir.Values("uid=local,ou=people,dc=example,dc=com", "zimbraMailHost"))
}

func TestAddAlias(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	adminID := r.createAdmin(t)

	r.run(t, "aaa", adminAddr, "Root@example.com")

	root := "uid=root,ou=people,dc=example,dc=com"
	assert.Equal(t, map[string][]string{
		"objectclass":           {"zimbraAlias"},
		"uid":                   {"root"},
		"zimbraid":              {r.dir.First(root, "zimbraId")},
		"zimbracreatetimestamp": {"20261112223616.047Z"},
		"zimbraaliastargetid":   {adminID},
	}, r.dir.Attrs(root))
	assert.Regexp(t, uuidPattern, r.dir.First(root, "zimbraId"))
	assert.Equal(t, []string{"root@example.com"}, r.dir.Values(adminDN, "zimbraMailAlias"))
	assert.Equal(t, []string{adminAddr, "root@example.com"}, r.dir.Values(adminDN, "mail"))

	// The account is found by its alias too.
	r.run(t, "aaa", "root@example.com", "postmaster@example.com")
	assert.Equal(t, []string{"root@example.com", "postmaster@example.com"}, r.dir.Values(adminDN, "zimbraMailAlias"))
}

func TestAddAlias_Refusals(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	r.createAdmin(t)
	r.run(t, "aaa", adminAddr, "root@example.com")
	r.run(t, "ca", "other@example.com", "")

	for name, tc := range map[string]struct {
		args []string
		code string
	}{
		"listed already":  {[]string{"aaa", adminAddr, "root@example.com"}, provision.CodeAccountExists},
		"taken by other":  {[]string{"aaa", "other@example.com", "root@example.com"}, provision.CodeAccountExists},
		"an account":      {[]string{"aaa", adminAddr, "other@example.com"}, provision.CodeAccountExists},
		"no account":      {[]string{"aaa", "ghost@example.com", "g@example.com"}, provision.CodeNoSuchAccount},
		"no domain":       {[]string{"aaa", adminAddr, "a@nowhere.org"}, provision.CodeNoSuchDomain},
		"not an email":    {[]string{"aaa", adminAddr, "alias"}, provision.CodeInvalidRequest},
		"too few args":    {[]string{"aaa", adminAddr}, provision.CodeInvalidRequest},
		"alias bad email": {[]string{"aaa", adminAddr, "<a@example.com>"}, provision.CodeInvalidAttrValue},
	} {
		_, err := r.Exec(tc.args)
		assert.True(t, hasCode(err, tc.code), "%s: %v", name, err)
	}
}

func TestAddAlias_ReplacesADanglingAliasAndCompletesAHalfDoneOne(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	adminID := r.createAdmin(t)

	dangling := "uid=gone,ou=people,dc=example,dc=com"
	r.dir.Set(dangling, "objectClass", "zimbraAlias")
	r.dir.Set(dangling, "zimbraAliasTargetId", "deadbeef-0000-4000-8000-000000000000")

	r.run(t, "aaa", adminAddr, "gone@example.com")

	assert.Equal(t, []string{dangling}, r.dir.Deleted)
	assert.Equal(t, []string{adminID}, r.dir.Values(dangling, "zimbraAliasTargetId"))

	half := "uid=half,ou=people,dc=example,dc=com"
	r.dir.Set(half, "objectClass", "zimbraAlias")
	r.dir.Set(half, "zimbraAliasTargetId", adminID)
	r.warns = nil

	r.run(t, "aaa", adminAddr, "half@example.com")

	assert.Contains(t, r.dir.Values(adminDN, "zimbraMailAlias"), "half@example.com")
	assert.NotEmpty(t, r.warns)

	// An entry that is not an alias is in the way.
	r.dir.Set("uid=taken,ou=people,dc=example,dc=com", "objectClass", "inetOrgPerson")
	_, err := r.Exec([]string{"aaa", adminAddr, "taken@example.com"})
	assert.True(t, hasCode(err, provision.CodeAccountExists), "%v", err)
}

func TestCreateAccount_DirectoryFailuresAreReported(t *testing.T) {
	r := newRig(t)
	r.withServer(t)
	r.withDomain(t)
	boom := errors.New("insufficient access")

	r.dir.FailAdd["uid=a,ou=people,dc=example,dc=com"] = boom
	_, err := r.Exec([]string{"ca", "a@example.com", ""})
	assert.True(t, hasCode(err, provision.CodeFailure))
	assert.ErrorIs(t, err, boom)

	r.dir.FailAdd[adminDN] = boom
	r.run(t, "ca", "b@example.com", "")
	r.dir.FailAdd["uid=r,ou=people,dc=example,dc=com"] = boom
	_, err = r.Exec([]string{"aaa", "b@example.com", "r@example.com"})
	assert.ErrorIs(t, err, boom)
}
