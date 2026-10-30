// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"slices"
	"strings"
	"testing"
)

// FuzzResolveEditKeyValue fuzzes the "key=value" splitting used by
// "configd localconfig --edit".
//
// Invariants: an argument without a key (no '=' at index > 0) is rejected with
// the legacy message; otherwise the key is the text before the first '=',
// contains no '=', the value is everything after it, and key+"="+value
// reproduces the argument.
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
		key, value, err := resolveEditKeyValue(arg, false)

		if idx := strings.IndexByte(arg, '='); idx <= 0 {
			if err == nil || err.Error() != "argument '"+arg+"' not in key=value form" {
				t.Fatalf("argument %q must be rejected with the legacy message, got %v", arg, err)
			}

			return
		}

		if err != nil {
			t.Fatalf("argument %q rejected: %v", arg, err)
		}

		if key == "" || strings.Contains(key, "=") {
			t.Fatalf("invalid key %q from %q", key, arg)
		}

		if key+"="+value != arg {
			t.Fatalf("split not lossless: %q + %q != %q", key, value, arg)
		}
	})
}

// FuzzOptionSequence fuzzes the command-line scan that orders legacy option
// ids for checkCompatible. Invariants: only known ids come back, the scan is
// deterministic, and nothing before the "localconfig" word is interpreted.
func FuzzOptionSequence(f *testing.F) {
	for _, s := range []string{
		"-q -s -m export",
		"-fe a=1",
		"-c /tmp/x.xml -p",
		"--mode=xml --quiet",
		"-e -- -p=1",
		"-mxml",
		"- -- ---",
		"",
	} {
		f.Add(s)
	}

	known := append(slices.Clone(optionOrder), "k")

	f.Fuzz(func(t *testing.T, line string) {
		args := append([]string{"configd", "localconfig"}, strings.Fields(line)...)
		seq := optionSequence(args)

		for _, id := range seq {
			if !slices.Contains(known, id) && len(id) != 1 {
				t.Fatalf("unknown option id %q from %q", id, line)
			}
		}

		if !slices.Equal(seq, optionSequence(args)) {
			t.Fatalf("non-deterministic scan of %q", line)
		}

		if got := optionSequence(append([]string{"-q", "-e"}, args[:1]...)); got != nil {
			t.Fatalf("options before the subcommand must be ignored, got %v", got)
		}
	})
}

// FuzzWrapDoc fuzzes the legacy LocalConfig.fmt word wrap. Invariants: output
// ends with a newline unless empty, and no output line is longer than the
// limit unless it is a single over-long token.
func FuzzWrapDoc(f *testing.F) {
	for _, s := range []string{
		"Path to a JDK/J2SDK.",
		"Zimbra installation root and home directory of ‘zimbra’ UNIX user.",
		"one  two\tthree\r\nfour",
		"",
		strings.Repeat("x", 120),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		var out strings.Builder

		wrapDoc(&out, text, docWrapWidth)

		got := out.String()
		if got != "" && !strings.HasSuffix(got, "\n") {
			t.Fatalf("output of %q does not end with a newline: %q", text, got)
		}

		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if utf16Len(line) > docWrapWidth && strings.Contains(line, " ") {
				t.Fatalf("line %q of %q exceeds %d columns", line, text, docWrapWidth)
			}
		}
	})
}
