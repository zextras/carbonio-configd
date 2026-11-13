// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/provision"
)

func TestCreateServer_WritesTheServerEntry(t *testing.T) {
	r := newRig(t)

	id := r.run(t, "cs", "Mail.Example.com")

	assert.Regexp(t, uuidPattern, id)
	assert.Equal(t, map[string][]string{
		"objectclass":           {"zimbraServer"},
		"zimbraid":              {id},
		"zimbracreatetimestamp": {"20261112223616Z"},
		"cn":                    {host},
		"zimbraservicehostname": {host},
	}, r.dir.Attrs(serverDN), "the first server has no local server to enable fractional seconds: legacy timestamp")
}

func TestCreateServer_TimestampHasMillisecondsOnceTheLocalServerEnablesThem(t *testing.T) {
	r := newRig(t)
	r.withServer(t)

	r.run(t, "cs", "other.example.com")

	assert.Equal(t, "20261112223616.047Z", r.dir.First("cn=other.example.com,cn=servers,cn=zimbra", "zimbraCreateTimestamp"))
}

func TestCreateServer_AttributesAndRefusals(t *testing.T) {
	r := newRig(t)
	r.dir.Set(globalDN, "zimbraServerExtraObjectClass", "extraServer")

	r.run(t, "cs", host, "zimbraServiceEnabled", "service", "zimbraServiceHostname", "other.example.com")

	assert.Equal(t, []string{"zimbraServer", "extraServer"}, r.dir.Values(serverDN, "objectClass"))
	assert.Equal(t, []string{"other.example.com"}, r.dir.Values(serverDN, "zimbraServiceHostname"), "a given hostname is kept")
	assert.Equal(t, []string{"service"}, r.dir.Values(serverDN, "zimbraServiceEnabled"))

	_, err := r.Exec([]string{"cs", host})
	assert.True(t, hasCode(err, provision.CodeServerExists), "%v", err)

	_, err = r.Exec([]string{"cs", "x.example.com", "zimbraMailLocalBind", "TRUE"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "an immutable attribute: %v", err)
}

func TestCreateDomain_WritesTheDomainItsParentsAndBases(t *testing.T) {
	r := newRig(t)

	id := r.run(t, "cd", "Sub.Example.com")

	assert.Regexp(t, uuidPattern, id)
	assert.Equal(t, map[string][]string{
		"objectclass":           {"dcObject", "organization", "zimbraDomain", "amavisAccount"},
		"zimbraid":              {id},
		"zimbracreatetimestamp": {"20261112223616Z"},
		"zimbradomainname":      {"sub.example.com"},
		"zimbramailstatus":      {"enabled"},
		"o":                     {"sub.example.com domain"},
		"dc":                    {"sub"},
		"zimbradomaintype":      {"local"},
		"zimbradomainstatus":    {"active"},
	}, r.dir.Attrs("dc=sub,dc=example,dc=com"))

	for dn, label := range map[string]string{"dc=example,dc=com": "example", "dc=com": "com"} {
		assert.Equal(t, map[string][]string{
			"objectclass": {"dcObject", "organization"}, "o": {label + " domain"}, "dc": {label},
		}, r.dir.Attrs(dn), dn)
	}

	assert.Equal(t, map[string][]string{
		"objectclass": {"organizationalRole"}, "ou": {"people"}, "cn": {"people"},
	}, r.dir.Attrs("ou=people,dc=sub,dc=example,dc=com"))
	assert.Equal(t, map[string][]string{
		"objectclass": {"organizationalRole"}, "cn": {"groups"}, "description": {"dynamic groups base"},
	}, r.dir.Attrs("cn=groups,dc=sub,dc=example,dc=com"))
}

func TestCreateDomain_GivenAttributesAndRefusals(t *testing.T) {
	r := newRig(t)

	r.run(t, "cd", "example.com", "zimbraDomainType", "alias", "zimbraDomainStatus", "locked", "zimbraMailStatus", "disabled")

	assert.Equal(t, []string{"alias"}, r.dir.Values(domainDN, "zimbraDomainType"))
	assert.Equal(t, []string{"locked"}, r.dir.Values(domainDN, "zimbraDomainStatus"))
	assert.Equal(t, []string{"disabled"}, r.dir.Values(domainDN, "zimbraMailStatus"))

	_, err := r.Exec([]string{"cd", "example.com"})
	assert.True(t, hasCode(err, provision.CodeDomainExists), "%v", err)

	for name, domain := range map[string]string{
		"too long":      string(make([]byte, 300)),
		"not a name":    "bad domain",
		"international": "caf\u00e9.com",
	} {
		_, err := r.Exec([]string{"cd", domain})
		assert.True(t, hasCode(err, provision.CodeInvalidRequest), "%s: %v", name, err)
	}

	r.dir.Set(globalDN, "zimbraAllowNonLDHCharsInDomain", "FALSE")
	_, err = r.Exec([]string{"cd", "a_b.example.org"})
	assert.True(t, hasCode(err, provision.CodeInvalidRequest), "non-LDH refused when not allowed: %v", err)

	_, err = r.Exec([]string{"cd", "x.org", "zimbraDomainMaxAccounts", "-1"})
	assert.True(t, hasCode(err, provision.CodeInvalidAttrValue), "the value checks apply: %v", err)
}

func TestCreateDomain_KeepsAParentThatIsADomainAndReplacesAStaleOne(t *testing.T) {
	r := newRig(t)
	r.run(t, "cd", "com")

	r.run(t, "cd", "example.com")

	assert.Equal(t, []string{"com"}, r.dir.Values("dc=com", "zimbraDomainName"), "the domain com stays")
	assert.Equal(t, []string{"example.com"}, r.dir.Values(domainDN, "zimbraDomainName"))
}

func TestCreateDomain_ReplacesAParentEntryOfTheSameDN(t *testing.T) {
	r := newRig(t)
	r.run(t, "cd", "sub.example.com") // creates dc=example,dc=com as a plain parent

	r.run(t, "cd", "example.com")

	assert.Contains(t, r.dir.Values(domainDN, "objectClass"), "zimbraDomain")
	assert.Equal(t, []string{"example.com domain"}, r.dir.Values(domainDN, "o"))
	assert.True(t, r.dir.Exists("dc=sub,dc="+"example,dc=com"), "the sub domain is untouched")
}
