// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"maps"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// bufioMaxLine is bufio.Scanner's default token limit; longer lines make the
// scanner stop, which is outside the contract the round-trip invariant covers.
const bufioMaxLine = 64 * 1024

// isRoundTripKey reports whether key can be emitted by FormatAsZmprovOutput and
// recovered unchanged by parseLDAPCommandOutput.
func isRoundTripKey(key string) bool {
	return key != "" &&
		!strings.ContainsAny(key, ":\n") &&
		key == strings.TrimSpace(key) &&
		!strings.HasPrefix(key, "#") &&
		len(key) < bufioMaxLine/4
}

// isRoundTripValue reports whether every newline-separated line of value
// survives the "key: line" encoding: lines must already be trimmed (the parser
// trims), must not start with ':' (that marks base64) and must stay below the
// scanner limit.
func isRoundTripValue(value string) bool {
	for line := range strings.SplitSeq(value, "\n") {
		if line != strings.TrimSpace(line) ||
			strings.HasPrefix(line, ":") ||
			len(line) >= bufioMaxLine/4 {
			return false
		}
	}

	return true
}

// FuzzParseLDAPCommandOutput feeds arbitrary zmprov-style text to
// parseLDAPCommandOutput and checks structural invariants that must hold for
// every input.
func FuzzParseLDAPCommandOutput(f *testing.F) {
	seeds := []string{
		"",
		"   \n   \n   ",
		"zimbraServiceEnabled: mailbox mta\nzimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3",
		"zimbraPublicKey:: AQIDBAUG==\nzimbraNormalKey: normal value",
		"key:: AAA=",
		"zimbraServiceEnabled: mailbox\nzimbraServiceEnabled: mta\nzimbraServiceEnabled: ldap",
		"zimbraKey1: value1\n\n# This is a comment\nzimbraKey2: value2\n",
		"zimbraURL: https://example.com:8443/path\nzimbraTime: 12:34:56",
		"zimbraMtaMyNetworks: 127.0.0.0/8 [::1]/128 172.24.0.0/21\nzimbraLocalBindAddress: ::1",
		"zimbraMtaMyNetworks2: fe80::/10\nzimbraMtaMyNetworks2: 2001:db8::1",
		"zimbraValidKey: valid\nmalformed line without colon\nzimbraAnotherKey: another",
		"crlf: one\r\ncrlf: two\r\n",
		"unicode: caffè ☕ 日本語\n",
		":\n::\n: :\n:: x\n",
		"a:b:c:d\n",
		"   spaced   :   value   \n",
		"#comment: not-a-key\n",
		"k:\nk:\nk:\n",
		"mix: 1\nmix:: b64\nmix: 2\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got := parseLDAPCommandOutput(input)

		for key, value := range got {
			if strings.ContainsAny(key, ":\n") {
				t.Fatalf("key %q contains ':' or newline", key)
			}

			if key != strings.TrimSpace(key) {
				t.Fatalf("key %q is not trimmed", key)
			}

			if strings.HasPrefix(key, "#") {
				t.Fatalf("comment line leaked as key %q", key)
			}

			for line := range strings.SplitSeq(value, "\n") {
				if line != strings.TrimSpace(line) {
					t.Fatalf("key %q value line %q is not trimmed", key, line)
				}
			}
		}

		// Deterministic: same input, same output.
		if again := parseLDAPCommandOutput(input); !maps.Equal(got, again) {
			t.Fatalf("non-deterministic parse: %v vs %v", got, again)
		}

		// Parsing is line-oriented: appending a trailing newline changes nothing
		// unless the last line was cut by the scanner's token-size limit.
		if len(input) < bufioMaxLine {
			if withNL := parseLDAPCommandOutput(input + "\n"); !maps.Equal(got, withNL) {
				t.Fatalf("trailing newline changed result: %v vs %v", got, withNL)
			}
		}
	})
}

// FuzzParseLDAPCommandOutputRoundTrip checks that ldap.FormatAsZmprovOutput
// followed by parseLDAPCommandOutput is the identity for every map whose keys
// and values are expressible in the zmprov line format.
func FuzzParseLDAPCommandOutputRoundTrip(f *testing.F) {
	f.Add("zimbraServiceEnabled", "mailbox\nmta\nldap", "zimbraMtaMyNetworks", "127.0.0.0/8 [::1]/128 172.24.0.0/21")
	f.Add("key", "AAA=", "other", "fe80::/10\n2001:db8::1")
	f.Add("zimbraURL", "https://example.com:8443/path", "zimbraTime", "12:34:56")
	f.Add("empty", "", "other", "x")
	f.Add("k", "a\n\nb", "k2", "v")
	f.Add("unicode", "caffè ☕ 日本語", "ключ", "значение")
	f.Add("same", "1", "same", "2")

	f.Fuzz(func(t *testing.T, key1, value1, key2, value2 string) {
		in := map[string]string{}

		for _, kv := range [][2]string{{key1, value1}, {key2, value2}} {
			if isRoundTripKey(kv[0]) && isRoundTripValue(kv[1]) {
				in[kv[0]] = kv[1]
			}
		}

		out := parseLDAPCommandOutput(ldap.FormatAsZmprovOutput(in))

		if !maps.Equal(in, out) {
			t.Fatalf("round trip mismatch:\n in: %q\nout: %q", in, out)
		}
	})
}
