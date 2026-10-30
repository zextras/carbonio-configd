// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"strings"
	"testing"
	"unicode"
)

// FuzzParseURLs feeds arbitrary localconfig ldap_url strings to ParseURLs.
//
// Invariants: never returns an empty non-nil slice (callers test for nil);
// every entry is non-empty and free of whitespace; entries joined by a single
// space equal the whitespace-normalised input (order and content preserved).
func FuzzParseURLs(f *testing.F) {
	for _, s := range []string{
		"",
		"   \t\n ",
		"ldap://ldap.example.com:389",
		"ldap://srv3.example.com:389 ldap://srv1.example.com:389",
		"ldap://srv1:389\tldaps://srv2:636\nldap://[::1]:389",
		"  ldap://leading.example.com:389  ",
		"ldap://a:389\r\nldap://b:389\r\n",
		"ldap://ключ.example.com:389 ldap://caffè.example.com:389",
		"ldap://\u00a0nbsp:389",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got := ParseURLs(raw)

		want := strings.Join(strings.Fields(raw), " ")
		if want == "" {
			if got != nil {
				t.Fatalf("blank input %q must return nil, got %q", raw, got)
			}

			return
		}

		for _, u := range got {
			if u == "" || strings.IndexFunc(u, unicode.IsSpace) >= 0 {
				t.Fatalf("bad entry %q from %q", u, raw)
			}
		}

		if joined := strings.Join(got, " "); joined != want {
			t.Fatalf("entries %q do not reproduce input %q", got, raw)
		}
	})
}
