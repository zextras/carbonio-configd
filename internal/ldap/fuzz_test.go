// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"os"
	"path/filepath"
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

// FuzzParseServerInheritedAttrs feeds arbitrary attrs.xml content to the
// schema parser.
//
// Invariants: never panics; on success every name is non-empty and unique.
func FuzzParseServerInheritedAttrs(f *testing.F) {
	seed, err := os.ReadFile(filepath.Join("testdata", "attrs", "attrs.xml"))
	if err != nil {
		f.Fatal(err)
	}

	for _, s := range []string{
		string(seed),
		"",
		`<attrs><attr name="a" flags="serverInherited"/></attrs>`,
		`<attrs><attr name="a" flags="x, serverInherited"><attr name="a" flags="serverInherited"/></attr>`,
		`<attr name="" flags="serverInherited">`,
		`<!-- <attr name="{name}" flags="serverInherited"> --><attrs>`,
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, doc string) {
		names, err := ParseServerInheritedAttrs(strings.NewReader(doc))
		if err != nil {
			return
		}

		seen := make(map[string]struct{}, len(names))
		for _, n := range names {
			if n == "" {
				t.Fatal("empty attribute name returned")
			}

			if _, dup := seen[n]; dup {
				t.Fatalf("duplicate attribute name %q", n)
			}

			seen[n] = struct{}{}
		}
	})
}
