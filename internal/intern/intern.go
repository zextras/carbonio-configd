// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package intern canonicalises the short, high-repeat strings that flow through
// the configd hot paths (local-config keys, LDAP attribute names, Carbonio
// service names) so that equality comparisons and map lookups touch a single
// backing address per unique value.
//
// The implementation wraps the standard library's unique package introduced
// in Go 1.23. Each category uses a distinct named type so that the underlying
// unique-handle pools do not share entries — that keeps cleanup behaviour
// predictable and lets the runtime release categories independently under
// memory pressure.
//
// Callers always see plain strings. The unique.Handle values never escape
// this package.
package intern

import "unique"

// Distinct named types give each interning category its own handle pool in
// the runtime. Values round-trip through string(...) conversions at the
// public API boundary.
type (
	keyStr     string
	attrStr    string
	serviceStr string
)

// Key returns the canonical form of a local-configuration or MTA-config key.
// Repeated calls with the same input yield a string value whose backing
// storage is shared across the process.
func Key(s string) string {
	return string(unique.Make(keyStr(s)).Value())
}

// Attr returns the canonical form of an LDAP attribute name.
func Attr(s string) string {
	return string(unique.Make(attrStr(s)).Value())
}

// Service returns the canonical form of a Carbonio service name.
func Service(s string) string {
	return string(unique.Make(serviceStr(s)).Value())
}
