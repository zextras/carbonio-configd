// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"net/mail"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
)

// maxDomainNameLen is NameUtil.MAX_DOMAIN_NAME_LEN (RFC 1035).
const maxDomainNameLen = 255

// The DIT of LdapDIT with the default layout.
const (
	ouPeople      = "ou=people"
	cnGroups      = "cn=groups"
	cnServers     = "cn=servers"
	cnCOS         = "cn=cos"
	cnConfig      = "cn=config"
	defaultCOS    = "default"
	attrObjClass  = "objectClass"
	attrZimbraID  = "zimbraId"
	attrCN        = "cn"
	attrUID       = "uid"
	attrMail      = "mail"
	attrOrg       = "o"
	attrDC        = "dc"
	attrMailAlias = "zimbraMailAlias"
)

// validEmail is NameUtil.validEmailAddress: a single address, with a display
// name only when personal is set.
func validEmail(addr string, personal bool) error {
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return invalidValue("must be valid email address: %s", addr)
	}

	if !personal && (parsed.Name != "" || parsed.Address != addr) {
		return invalidValue("must be valid email address: %s", addr)
	}

	return nil
}

// asciiDomain is IDNUtil.toAsciiDomainName for the names the bootstrap handles:
// a name with non-ASCII characters is refused, as the internationalized
// conversion is not provided.
func asciiDomain(name string) (string, error) {
	for _, r := range name {
		if r > 127 {
			return "", invalidRequest("internationalized domain name %s is not supported, give its ASCII (punycode) form", name)
		}
	}

	return name, nil
}

// splitEmail lowercases and trims addr and splits it into local part and
// ASCII domain; it must be exactly local@domain.
func splitEmail(addr string) (local, domain string, err error) {
	addr = strings.ToLower(strings.TrimSpace(addr))

	parts := strings.Split(addr, "@")
	if len(parts) != 2 {
		return "", "", invalidRequest("must be valid email address: %s", addr)
	}

	domain, err = asciiDomain(parts[1])

	return parts[0], domain, err
}

// domainDNS is LdapDIT.domainToDNs: the DN of each suffix of the domain name,
// from the domain itself (index 0) up to its top-level label, "dc=foo,dc=com",
// "dc=com".
func domainDNS(parts []string) []string {
	dns := make([]string, len(parts))

	for i := range parts {
		rdns := make([]string, 0, len(parts)-i)
		for _, p := range parts[i:] {
			rdns = append(rdns, "dc="+goldap.EscapeDN(p))
		}

		dns[i] = strings.Join(rdns, ",")
	}

	return dns
}

// emailDN is LdapDIT.emailToDN: uid=<local>,ou=people,<domain DN>.
func emailDN(local, domain string) string {
	return attrUID + "=" + goldap.EscapeDN(local) + "," + ouPeople + "," + domainDNS(strings.Split(domain, "."))[0]
}

// newID is LdapUtil.generateUUID.
func newID() string { return uuid.NewString() }

// gentime is LdapDateUtil.toGeneralizedTime: UTC, with milliseconds when the
// local server enables zimbraLdapGentimeFractionalSecondsEnabled.
func gentime(t time.Time, millis bool) string {
	t = t.UTC()
	if millis {
		return t.Format("20060102150405.000") + "Z"
	}

	return t.Format("20060102150405") + "Z"
}
