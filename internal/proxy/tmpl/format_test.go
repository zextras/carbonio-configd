// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"math/rand/v2"
	"strings"
	"testing"
)

type formatCase struct{ name, in, want string }

// runFormatCases checks every case, and that formatting the result again, and
// formatting the already formatted input, change nothing.
func runFormatCases(t *testing.T, cases []formatCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := format(tc.in)
			if got != tc.want {
				t.Fatalf("\n got:\n%s\nwant:\n%s", got, tc.want)
			}

			if again := format(got); again != got {
				t.Errorf("not idempotent:\n%s\nthen:\n%s", got, again)
			}

			if len(SplitLines(got)) != len(SplitLines(tc.in)) {
				t.Errorf("line count changed")
			}
		})
	}
}

// Rule 1: a line-leading switch stays at column 0, the indent goes after it;
// braces in comments and escaped braces do not count.
func TestFormatSwitchAtColumnZero(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"block guarded by a text switch",
			"${strict}# The $\\{listen.:addresses\\} is not demarcated {\n" +
				"${strict}server {\n" +
				"${strict}  ${on}listen 1;\n" +
				"${strict}        server_name _;\n" +
				"${strict}\n" +
				"${strict}  # Extra listen { addresses \\{\n" +
				"${port}\n" +
				"${strict}   return 400;\n" +
				"${strict}}\n" +
				"after;\n",
			"${strict}# The $\\{listen.:addresses\\} is not demarcated {\n" +
				"${strict}server {\n" +
				"${strict}    ${on}listen 1;\n" +
				"${strict}    server_name _;\n" +
				"${strict}\n" +
				"${strict}    # Extra listen { addresses \\{\n" +
				"${port}\n" +
				"${strict}    return 400;\n" +
				"${strict}}\n" +
				"after;\n",
		},
		{
			"the same with an enabler",
			"${on}server {\n${on}listen 1;\n${on}}\n",
			"${on}server {\n${on}    listen 1;\n${on}}\n",
		},
		{
			"an indented switch is preceded by the indent",
			"a {\n${on}b {\n        ${on}c;\n    ${on}}\n}\n",
			"a {\n${on}    b {\n        ${on}c;\n    ${on}}\n}\n",
		},
		{
			"chain of switches",
			"a {\n${strict}${on}listen 1;\n${strict}  ${off}  listen 2;\n  ${strict}${on}listen 3;\n}\n",
			"a {\n${strict}    ${on}listen 1;\n${strict}    ${off}listen 2;\n    ${strict}${on}listen 3;\n}\n",
		},
		{
			"a switch alone on its line",
			"a {\n${strict}\n    ${on}\n${strict}${on}\n}\n",
			"a {\n${strict}\n    ${on}\n${strict}    ${on}\n}\n",
		},
		{
			"a text variable at column 0 expands to its own indentation",
			"a {\n${port}\n    ${port}\n  ${port}\n}\n",
			"a {\n${port}\n    ${port}\n    ${port}\n}\n",
		},
		{
			"a line that only starts with a variable is text",
			"a {\n${port}  x;\n}\n",
			"a {\n    ${port}  x;\n}\n",
		},
		{
			"comment and escapes with braces after a switch",
			"${strict}# {{{\n${strict}x \\{ \\} \"{\" ; # {\ny;\n",
			"${strict}# {{{\n${strict}x \\{ \\} \"{\" ; # {\ny;\n",
		},
	})
}

// Rule 2: the ssl_* and proxy_ssl_* directives are one group.
func TestFormatSSLGroup(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"proxy_ssl_* join the ssl_* lines",
			"s {\n" +
				"    ssl_protocols           ${sp};\n" +
				"    ssl_prefer_server_ciphers ${port};\n" +
				"    ssl_certificate_key     k;\n" +
				"    proxy_ssl_protocols           ${sp};\n" +
				"    proxy_ssl_ciphers             c;\n" +
				"    ${on}ssl_dhparam             d;\n" +
				"}\n",
			"s {\n" +
				"    ssl_protocols            ${sp};\n" +
				"    ssl_prefer_server_ciphers ${port};\n" +
				"    ssl_certificate_key       k;\n" +
				"    proxy_ssl_protocols      ${sp};\n" +
				"    proxy_ssl_ciphers         c;\n" +
				"    ${on}ssl_dhparam               d;\n" +
				"}\n",
		},
	})
}

// Rule 3: a value that renders with a leading space gets one space less.
func TestFormatLeadingSpace(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"one less before a leading-space value",
			"a {\n    ssl_protocols ${sp};\n    ssl_ciphers   ${port};\n    ssl_ecdh_curve  x;\n}\n",
			"a {\n    ssl_protocols ${sp};\n    ssl_ciphers    ${port};\n    ssl_ecdh_curve x;\n}\n",
		},
		{
			"the leading-space line sets the column when its name is the longest",
			"a {\n    ssl_protocols ${sp};\n    ssl_ciphers  ${port};\n}\n",
			"a {\n    ssl_protocols ${sp};\n    ssl_ciphers    ${port};\n}\n",
		},
		{
			"a leading-space value that is not first renders no space",
			"a {\n    ssl_a   \"${sp}\";\n    ssl_bcd  x;\n}\n",
			"a {\n    ssl_a   \"${sp}\";\n    ssl_bcd x;\n}\n",
		},
		{
			"two leading-space values align with each other",
			"a {\n    ssl_protocols    ${sp};\n    ssl_p  ${sp};\n}\n",
			"a {\n    ssl_protocols ${sp};\n    ssl_p         ${sp};\n}\n",
		},
		{
			"the rendered values line up",
			"a {\n    ab  ${sp};\n    abcd   x;\n}\n",
			"a {\n    ab  ${sp};\n    abcd x;\n}\n",
		},
	})
}

// Rule 4: only a group that the source shows aligned is aligned.
func TestFormatAlignsOnlyIntendedGroups(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"a lone directive keeps its single space",
			"a {\n    http2 on;\n}\n",
			"a {\n    http2 on;\n}\n",
		},
		{
			"a lone padded directive gets a single space",
			"a {\n    client_max_body_size      0;\n}\n",
			"a {\n    client_max_body_size 0;\n}\n",
		},
		{
			"single-spaced directives are not padded",
			"map {\n    default ${port};\n    include y;\n    a_long_name z;\n}\n",
			"map {\n    default ${port};\n    include y;\n    a_long_name z;\n}\n",
		},
		{
			"a short single-spaced line below an aligned group stays as it is",
			"a {\n    keepalive_timeout     65;\n    keepalive_requests    100;\n    http2 on;\n}\n",
			"a {\n    keepalive_timeout  65;\n    keepalive_requests 100;\n    http2 on;\n}\n",
		},
		{
			"a short single-spaced line between aligned groups splits them",
			"a {\n    keepalive_timeout     65;\n    keepalive_requests    100;\n    return 200 \"x\";\n" +
				"    sendfile_max_chunk    1;\n    send_lowat            2;\n}\n",
			"a {\n    keepalive_timeout  65;\n    keepalive_requests 100;\n    return 200 \"x\";\n" +
				"    sendfile_max_chunk 1;\n    send_lowat         2;\n}\n",
		},
		{
			"the longest line of an aligned group needs only a single space",
			"a {\n    ssl_certificate     x;\n    ssl_certificate_key y;\n}\n",
			"a {\n    ssl_certificate     x;\n    ssl_certificate_key y;\n}\n",
		},
		{
			"a comment, a blank line or a block ends a group",
			"a {\n    aa       1;\n    # c\n    bbbb      2;\n\n    c        3;\n    dd       4;\n    location / {\n        e        5;\n    }\n}\n",
			"a {\n    aa 1;\n    # c\n    bbbb 2;\n\n    c  3;\n    dd 4;\n    location / {\n        e 5;\n    }\n}\n",
		},
		{
			"directives of other depths are other groups",
			"a {\n    aa      1;\n    b {\n        cccc   2;\n        dd     3;\n    }\n    ee      4;\n}\n",
			"a {\n    aa 1;\n    b {\n        cccc 2;\n        dd   3;\n    }\n    ee 4;\n}\n",
		},
		{
			"a continuation line is not a directive",
			"a {\n    x     1\n  y     2;\n}\n",
			"a {\n    x     1\n        y     2;\n}\n",
		},
		{
			"a map keeps single spaces when the source has them",
			"map $a $b {\n    default 0;\n    \"~x\" 1;\n}\n",
			"map $a $b {\n    default 0;\n    \"~x\" 1;\n}\n",
		},
	})
}

// Rule 5: the group column follows the longest name, not the source column.
func TestFormatGroupColumn(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"narrower than the source",
			"a {\n    a          1;\n    bb         2;\n}\n",
			"a {\n    a  1;\n    bb 2;\n}\n",
		},
		{
			"wider than the source",
			"a {\n    a   1;\n    bbbbbbb  2;\n    cc 3;\n}\n",
			"a {\n    a       1;\n    bbbbbbb 2;\n    cc 3;\n}\n",
		},
		{
			"the longest name sets the column",
			"a {\n    ssl_session_cache         x;\n    ssl_prefer_server_ciphers y;\n    ssl_ciphers          z;\n    ssl_dhparam          w;\n}\n",
			"a {\n    ssl_session_cache         x;\n    ssl_prefer_server_ciphers y;\n    ssl_ciphers               z;\n    ssl_dhparam               w;\n}\n",
		},
	})
}

// Rule 6: a switch prefix before a directive renders "" or "#"; the width of
// the name is measured without it.
func TestFormatInlineSwitchWidth(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"the variants of a directive get the same padding",
			"s {\n    ${on}listen                  1;\n    ${off}listen                2;\n    listen        3;\n    ${on}keepalive_timeout 5;\n}\n",
			"s {\n    ${on}listen            1;\n    ${off}listen            2;\n    listen            3;\n    ${on}keepalive_timeout 5;\n}\n",
		},
		{
			"with nothing longer they are single-spaced",
			"s {\n    ${on}listen                  1;\n    ${off}listen                2;\n    ${on}${off}listen        3;\n}\n",
			"s {\n    ${on}listen 1;\n    ${off}listen 2;\n    ${on}${off}listen 3;\n}\n",
		},
		{
			"a switch at column 0 does not count either",
			"s {\n${strict}    ${on}listen                  1;\n${strict}    ${off}ssl_dhparam  2;\n}\n",
			"s {\n${strict}    ${on}listen      1;\n${strict}    ${off}ssl_dhparam 2;\n}\n",
		},
	})
}

func TestFormatTextSwitchIsPrefix(t *testing.T) {
	t.Parallel()

	got := format("a {\n    ${strict}    proxy_pass   x;\n    ${strict}    proxy_cache  y;\n}\n")
	if want := "a {\n    ${strict}proxy_pass  x;\n    ${strict}proxy_cache y;\n}\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A formatted template renders as before, up to whitespace.
func TestFormatPreservesRenderedText(t *testing.T) {
	t.Parallel()

	src := "a {\n${strict}    ${on}listen   1;\n${strict}  ${off}x      2;\n    ssl_protocols    ${sp};\n    ssl_ciphers   y;\n}\n"
	got := format(src)

	render := func(s string) string {
		var out []string

		for _, l := range SplitLines(s) {
			out = append(out, strings.Join(strings.Fields(RenderLine(l, res)), ""))
		}

		return strings.Join(out, "\n")
	}

	// "strict" is a text variable here: not defined, it renders as nothing.
	if render(src) != render(got) {
		t.Errorf("rendering changed:\n%s\n---\n%s", render(src), render(got))
	}
}

// Aligning a group can bring a neighbour into reach: the result is formatted
// again until it settles, so Format stays idempotent.
func TestFormatSettles(t *testing.T) {
	t.Parallel()

	runFormatCases(t, []formatCase{
		{
			"a longer single-spaced line is the overflow of the aligned group",
			"s {\n    ${on}server_name ${port};\n    ab                    ${port};\n    cdefg                 ${port};\n}\n",
			"s {\n    ${on}server_name ${port};\n    ab          ${port};\n    cdefg       ${port};\n}\n",
		},
		{
			"but not a shorter one, nor one before the padded column",
			"s {\n    ab                    ${port};\n    cdefg                 ${port};\n    xyz ${port};\n    ${on}server_name ${port};\n}\n",
			"s {\n    ab    ${port};\n    cdefg ${port};\n    xyz ${port};\n    ${on}server_name ${port};\n}\n",
		},
	})
}

// TestFormatRandomTemplates checks idempotency, the line count and the
// rendered text (up to whitespace) on generated templates.
func TestFormatRandomTemplates(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(1, 2))
	names := []string{"a", "ab", "listen", "http2", "ssl_protocols", "ssl_prefer_server_ciphers", "server_name", "return", "include"}
	values := []string{"1;", "${port};", "${sp};", "x ${sp};", "\"${sp}\";", "[::]:${port} ssl;"}
	prefixes := []string{"", "", "${on}", "${off}", "${strict}", "${on}${off}", "${port}"}
	others := []string{"", "# comment {", "b {", "}", "loc / {", "# ${on}", "${strict}", "${port}"}

	space := func(n int) string { return strings.Repeat(" ", rng.IntN(n)) }

	render := func(s string) string {
		var out []string

		for _, l := range SplitLines(s) {
			out = append(out, strings.Join(strings.Fields(RenderLine(l, res)), ""))
		}

		return strings.Join(out, "\n")
	}

	for range 3000 {
		var src strings.Builder

		for range 1 + rng.IntN(12) {
			if rng.IntN(5) == 0 {
				src.WriteString(space(6) + others[rng.IntN(len(others))] + "\n")

				continue
			}

			src.WriteString(space(6) + prefixes[rng.IntN(len(prefixes))] + space(3) +
				names[rng.IntN(len(names))] + " " + space(14) + values[rng.IntN(len(values))] + "\n")
		}

		once := format(src.String())
		if twice := format(once); twice != once {
			t.Fatalf("not idempotent:\n%s\nthen:\n%s\nfrom:\n%s", once, twice, src.String())
		}

		if len(SplitLines(once)) != len(SplitLines(src.String())) {
			t.Fatalf("line count changed:\n%s\nto:\n%s", src.String(), once)
		}

		// "${strict}" and "${port}" are not enablers here, and "sp" renders
		// no leading space: compare what the renderer gives.
		if render(src.String()) != render(once) {
			t.Fatalf("rendering changed:\n%s\nto:\n%s", src.String(), once)
		}
	}
}
