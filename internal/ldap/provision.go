// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"maps"
	"slices"

	"github.com/go-ldap/ldap/v3"
)

// AddEntry creates the entry dn with the attributes. A DN that is taken is
// reported as an error for which IsEntryExists is true, and is not retried.
func (c *Client) AddEntry(dn string, attrs map[string][]string) error {
	return c.executeWithRetry(c.lifecycle(), func(conn *ldap.Conn) error {
		req := ldap.NewAddRequest(dn, nil)

		for _, name := range slices.Sorted(maps.Keys(attrs)) {
			req.Attribute(name, attrs[name])
		}

		return conn.Add(req)
	})
}

// DeleteEntry removes the entry dn.
func (c *Client) DeleteEntry(dn string) error {
	return c.executeWithRetry(c.lifecycle(), func(conn *ldap.Conn) error {
		return conn.Del(ldap.NewDelRequest(dn, nil))
	})
}

// SetPassword sets the password of the entry dn with the Password Modify
// extended operation (RFC 3062) without the old password: the server hashes it
// with its own scheme and checks its password policy. The password is sent on
// the connection only; it is never part of an error.
func (c *Client) SetPassword(dn, password string) error {
	return c.executeWithRetry(c.lifecycle(), func(conn *ldap.Conn) error {
		_, err := conn.PasswordModify(ldap.NewPasswordModifyRequest(dn, "", password))

		return err
	})
}

// IsEntryExists reports whether err is the refusal of an add because the entry
// exists (LDAP result 68, entryAlreadyExists).
func IsEntryExists(err error) bool {
	return ldap.IsErrorWithCode(err, ldap.LDAPResultEntryAlreadyExists)
}
