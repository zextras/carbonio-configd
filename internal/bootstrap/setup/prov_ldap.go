// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/provision"
)

// ldapDirectory is the provision.Directory of a node: the LDAP client with the
// write credentials.
type ldapDirectory struct{ *ldap.Client }

// searchScopes maps the provisioning scopes to the LDAP ones.
var searchScopes = map[provision.Scope]int{
	provision.ScopeBase: goldap.ScopeBaseObject,
	provision.ScopeOne:  goldap.ScopeSingleLevel,
	provision.ScopeSub:  goldap.ScopeWholeSubtree,
}

// Search implements provision.Directory: a base that does not exist has no
// entries.
func (d ldapDirectory) Search(
	base string, scope provision.Scope, filter string, attrs ...string,
) ([]provision.Entry, error) {
	res, err := d.Client.Search(base, filter, attrs, searchScopes[scope])
	if err != nil {
		if goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject) {
			return nil, nil
		}

		return nil, err
	}

	entries := make([]provision.Entry, 0, len(res.Entries))

	for _, e := range res.Entries {
		attrs := make(map[string][]string, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs[strings.ToLower(a.Name)] = a.Values
		}

		entries = append(entries, provision.Entry{DN: e.DN, Attrs: attrs})
	}

	return entries, nil
}

// Add implements provision.Directory.
func (d ldapDirectory) Add(dn string, attrs map[string][]string) error {
	err := d.AddEntry(dn, attrs)
	if ldap.IsEntryExists(err) {
		return provision.ErrEntryExists
	}

	return err
}

// Delete implements provision.Directory.
func (d ldapDirectory) Delete(dn string) error { return d.DeleteEntry(dn) }
