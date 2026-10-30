// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
)

// FuzzResolveEditKeyValue fuzzes the "key=value" splitting used by
// "configd localconfig --edit". resolveEditKeyValue calls os.Exit on arguments
// without a key, so only arguments it accepts (an '=' at index > 0) are fed
// to it.
//
// Invariants: the key is the text before the first '=', contains no '=', the
// value is everything after it, and key+"="+value reproduces the argument.
func FuzzResolveEditKeyValue(f *testing.F) {
	for _, s := range []string{
		"smtp_port=587",
		"java_opts=-Xmx=512m",
		"mykey=",
		"k=v=extra",
		"ldap_url=ldap://[::1]:389 ldap://h:389",
		"mailboxd_java_options=-Dfoo=bar -Dbaz=qux",
		"zimbra_ldap_password=p@ss=w0rd==",
		"unicode=caffè ☕ 日本語",
		"crlf=value\r\n",
		"key with spaces=v",
		"a==b",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, arg string) {
		if idx := strings.IndexByte(arg, '='); idx <= 0 {
			t.Skip("rejected by resolveEditKeyValue via os.Exit")
		}

		key, value := resolveEditKeyValue(arg, false)

		if key == "" || strings.Contains(key, "=") {
			t.Fatalf("invalid key %q from %q", key, arg)
		}

		if key+"="+value != arg {
			t.Fatalf("split not lossless: %q + %q != %q", key, value, arg)
		}
	})
}
