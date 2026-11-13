// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"errors"
	"strings"
)

// ErrEntryExists is what Directory.Add returns for a DN that is taken
// (LDAP result 68, entryAlreadyExists).
var ErrEntryExists = errors.New("entry already exists")

// Scope is the scope of a search.
type Scope int

// The search scopes.
const (
	ScopeBase Scope = iota
	ScopeOne
	ScopeSub
)

// Entry is a directory entry as the provisioning reads it: attribute names are
// lower case.
type Entry struct {
	DN    string
	Attrs map[string][]string
}

// Values returns the values of the attribute (name in any case).
func (e *Entry) Values(name string) []string {
	if e == nil {
		return nil
	}

	return e.Attrs[strings.ToLower(name)]
}

// First returns the first value of the attribute, "" when it has none.
func (e *Entry) First(name string) string {
	if v := e.Values(name); len(v) > 0 {
		return v[0]
	}

	return ""
}

// Has reports whether the attribute has a value.
func (e *Entry) Has(name string) bool { return len(e.Values(name)) > 0 }

// Directory is the LDAP access of the provisioning: the master of the
// directory, bound with the write credentials of the node.
type Directory interface {
	// BaseDN is the base of the configuration branch, "cn=zimbra".
	BaseDN() string
	// Search returns the entries under base; a base that does not exist yields
	// no entries and no error.
	Search(base string, scope Scope, filter string, attrs ...string) ([]Entry, error)
	// Add creates the entry; a DN that is taken is ErrEntryExists.
	Add(dn string, attrs map[string][]string) error
	// ModifyValues applies value-level modifications, idempotently (see
	// ldap.Client.ModifyValues).
	ModifyValues(dn string, add, del, replace map[string][]string) error
	// SetPassword sets the password of the entry with the LDAP Password Modify
	// extended operation (RFC 3062): the server hashes it.
	SetPassword(dn, password string) error
	// Delete removes the entry.
	Delete(dn string) error
}
