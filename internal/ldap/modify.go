// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"maps"
	"slices"

	"github.com/go-ldap/ldap/v3"
)

// modifyKind is the LDAP modify operation of a modifyOp.
type modifyKind int

const (
	modifyDelete modifyKind = iota
	modifyAdd
	modifyReplace
)

// modifyOp is one attribute modification of a modify request.
type modifyOp struct {
	kind   modifyKind
	attr   string
	values []string
}

// ModifyValues applies value-level modifications to the entry dn and is
// idempotent: it converges on the requested state however often it runs.
//
//   - add adds each listed value to its attribute; a value the attribute
//     already holds (LDAP result 20, attributeOrValueExists) is a success.
//   - del removes each listed value from its attribute; a value or an attribute
//     that is not there (LDAP result 16, noSuchAttribute) is a success. An
//     empty value list removes the whole attribute.
//   - replace sets the attribute to exactly the listed values.
//
// The request is sent whole, deletes first, then adds, then replaces, so a
// remove-then-add of one attribute stays atomic. When the server refuses it with
// result 16 or 20 the operations are re-applied one value at a time and those
// two results are ignored, which is what makes the call idempotent.
func (c *Client) ModifyValues(dn string, add, del, replace map[string][]string) error {
	ops := modifyOps(add, del, replace)
	if len(ops) == 0 {
		return nil
	}

	err := c.modify(dn, ops)
	if !isAlreadyConverged(err) {
		return err
	}

	for _, op := range singleValueOps(ops) {
		if err := c.modify(dn, []modifyOp{op}); err != nil && !isAlreadyConverged(err) {
			return err
		}
	}

	return nil
}

// singleValueOps splits every add and delete into one operation per value, so a
// value the entry already has (or lacks) cannot veto the others of its attribute.
func singleValueOps(ops []modifyOp) []modifyOp {
	split := make([]modifyOp, 0, len(ops))

	for _, op := range ops {
		if op.kind == modifyReplace || len(op.values) <= 1 {
			split = append(split, op)

			continue
		}

		for _, v := range op.values {
			split = append(split, modifyOp{kind: op.kind, attr: op.attr, values: []string{v}})
		}
	}

	return split
}

// modifyOps orders the modifications: deletes, adds, replaces; each group by
// attribute name so the request is deterministic.
func modifyOps(add, del, replace map[string][]string) []modifyOp {
	ops := make([]modifyOp, 0, len(add)+len(del)+len(replace))

	for _, group := range []struct {
		kind   modifyKind
		values map[string][]string
	}{{modifyDelete, del}, {modifyAdd, add}, {modifyReplace, replace}} {
		for _, attr := range slices.Sorted(maps.Keys(group.values)) {
			ops = append(ops, modifyOp{kind: group.kind, attr: attr, values: group.values[attr]})
		}
	}

	return ops
}

// modify sends ops to dn as one modify request.
func (c *Client) modify(dn string, ops []modifyOp) error {
	return c.executeWithRetry(c.lifecycle(), func(conn *ldap.Conn) error {
		req := ldap.NewModifyRequest(dn, nil)

		for _, op := range ops {
			switch op.kind {
			case modifyAdd:
				req.Add(op.attr, op.values)
			case modifyDelete:
				req.Delete(op.attr, op.values)
			case modifyReplace:
				req.Replace(op.attr, op.values)
			}
		}

		return conn.Modify(req)
	})
}

// isAlreadyConverged reports whether err is LDAP result 20 (adding a value
// that exists) or 16 (deleting one that does not): the entry already is in the
// state the modification asked for.
func isAlreadyConverged(err error) bool {
	return ldap.IsErrorAnyOf(err, ldap.LDAPResultAttributeOrValueExists, ldap.LDAPResultNoSuchAttribute)
}
