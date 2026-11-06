// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"slices"
	"strings"
	"testing"
)

type fakeResolver struct {
	enablers map[string]bool
	values   map[string]string
}

func (f fakeResolver) Enabler(name string) (on, ok bool) {
	on, ok = f.enablers[name]

	return on, ok
}

func (f fakeResolver) Value(name string) (string, bool) {
	v, ok := f.values[name]

	return v, ok
}

func (f fakeResolver) kind(name string) (Kind, bool) {
	if _, ok := f.enablers[name]; ok {
		return KindEnabler, true
	}

	if _, ok := f.values[name]; ok {
		if name == "num" {
			return KindScalar, true
		}

		return KindText, true
	}

	return 0, false
}

// isSwitch reports the enablers and "strict", a text variable that renders as
// "" or "#" (like web.strict.servername).
func (f fakeResolver) isSwitch(name string) bool {
	_, ok := f.enablers[name]

	return ok || name == "strict"
}

// hasLeadingSpace reports "sp", whose value renders with a leading space.
func (fakeResolver) hasLeadingSpace(name string) bool { return name == "sp" }

var res = fakeResolver{
	enablers: map[string]bool{"on": true, "off": false},
	values:   map[string]string{"port": "8080", "num": "1", "empty": ""},
}

func TestRenderLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"    listen ${port};", "    listen 8080;"},
		{"    ${on}listen 1;", "    listen 1;"},
		{"    ${off}listen 1;", "    #listen 1;"},
		{"    ${on}${off}listen 1;", "    #listen 1;"},
		{"    ${unknown}x;", "    x;"},
		{"    ${port}x;", "    8080x;"},
		{"    value ${empty};", "    # value ;"},
		{"value ${empty};", "value ;"},
		{"# ${port}", "# 8080"},
	} {
		if got := RenderLine(tc.in, res); got != tc.want {
			t.Errorf("RenderLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTokenizeRoundTrip(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"", "plain", "    ${on}${off}listen ${port};", "a ${b} c ${d.e:f}", "${a}", "$ {x} ${",
	} {
		var sb strings.Builder
		for _, tok := range Tokenize(line) {
			if tok.Kind == Text {
				sb.WriteString(tok.Text)
			} else {
				sb.WriteString("${" + tok.Text + "}")
			}
		}

		if sb.String() != line {
			t.Errorf("Tokenize(%q) round trip = %q", line, sb.String())
		}
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()

	if got := SplitLines("a\nb\n"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %q", got)
	}

	if got := SplitLines("a\n\n"); !slices.Equal(got, []string{"a", ""}) {
		t.Errorf("got %q", got)
	}
}

func TestExplodeHelpers(t *testing.T) {
	t.Parallel()

	typ, args, ok := ParseExplode("!{explode domain(vhn, sso)}")
	if !ok || typ != ExplodeDomain || args != "vhn, sso" {
		t.Errorf("ParseExplode = %q %q %v", typ, args, ok)
	}

	if _, _, ok := ParseExplode("server {"); ok {
		t.Error("plain line parsed as explode")
	}

	if got, err := DomainArgs("vhn, sso"); err != nil || !slices.Equal(got, []string{"vhn", "sso"}) {
		t.Errorf("DomainArgs = %q, %v", got, err)
	}

	if svc, err := ServerService("mailbox"); err != nil || svc != "mailbox" {
		t.Errorf("ServerService = %q, %v", svc, err)
	}

	if name, ok := OutputName("nginx.conf.web.template", ConfPrefix, ConfPrefix); !ok || name != "nginx.conf.web" {
		t.Errorf("OutputName = %q, %v", name, ok)
	}
}

func format(src string) string {
	return Format(src, FormatOptions{IsSwitch: res.isSwitch, LeadingSpace: res.hasLeadingSpace})
}

func TestFormat(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ in, want string }{
		"braces re-indent": {
			"server\n{\n  a  1;\n      location /x\n  {\n  b 2;\n  }\n}\n",
			"server\n{\n    a 1;\n    location /x\n    {\n        b 2;\n    }\n}\n",
		},
		"quoted and commented braces": {
			"a {\n x \"}\" ; # }\n}\n",
			"a {\n    x \"}\" ; # }\n}\n",
		},
		"enabler prefix": {
			// A prefix at column 0 stays there, the indent follows it; an
			// indented prefix is preceded by the indent.
			"a {\n${on}    listen 1;\n  ${on}${off}  listen 2;\n}\n",
			"a {\n${on}    listen 1;\n    ${on}${off}listen 2;\n}\n",
		},
		"unknown prefix is text": {
			"a {\n${other}  x;\n}\n",
			"a {\n    ${other}  x;\n}\n",
		},
		"explode line untouched": {
			"!{explode domain(vhn)}\n  x 1;\n",
			"!{explode domain(vhn)}\nx 1;\n",
		},
		"final newline added": {"a {\nb 1;\n}", "a {\n    b 1;\n}\n"},
		"empty":               {"", ""},
	} {
		if got := format(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

func TestFormatKeepsExplodeFirstLine(t *testing.T) {
	t.Parallel()

	got := format("!{explode   domain( vhn )}\nx;\n")
	if first, _, _ := strings.Cut(got, "\n"); first != "!{explode   domain( vhn )}" {
		t.Errorf("first line = %q", first)
	}
}

func TestFormatIdempotentAndLineCount(t *testing.T) {
	t.Parallel()

	for _, src := range []string{
		"server\n{\n  ${on}listen   1;\n${off}  ssl_x   ${port};\n\tvalue ${empty};\n  location / {\n a  b;\n}\n}\n",
		"a { b \"multi\n   line\" ; }\nc;\n",
		"x ${port} \\\n   continued;\n",
		"# comment {\n  y;\n",
	} {
		once := format(src)
		if twice := format(once); twice != once {
			t.Errorf("not idempotent:\n%q\n%q", once, twice)
		}

		if len(SplitLines(once)) != len(SplitLines(src)) {
			t.Errorf("line count changed: %q -> %q", src, once)
		}
	}
}

func TestFormatKeepsVanishingDirectiveIndent(t *testing.T) {
	t.Parallel()

	// "value ${empty};" renders as a comment only when indented, so fmt must
	// not add or remove the indent.
	for _, src := range []string{"a {\nvalue ${empty};\n}\n", "a {\n      value ${empty};\n}\n"} {
		got := format(src)
		render := func(s string) string {
			var out []string
			for _, l := range SplitLines(s) {
				out = append(out, RenderLine(l, res))
			}

			return strings.Join(out, "\n")
		}

		if strings.Contains(render(src), "# value") != strings.Contains(render(got), "# value") {
			t.Errorf("fmt changed how %q renders: %q", src, got)
		}
	}
}

func lint(src string) []string {
	var out []string
	for _, f := range Lint("f", src, registry{res}) {
		out = append(out, f.String())
	}

	return out
}

type registry struct{ fakeResolver }

func (r registry) Kind(name string) (Kind, bool) { return r.kind(name) }

func TestLint(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ src, want string }{
		"clean":             {"!{explode domain(vhn)}\na {\n    ${on}b ${port};\n}\n", ""},
		"unknown":           {"a ${nope};\n", "f:1:3: unknown variable ${nope} [unknown-variable]"},
		"enabler as value":  {"a ${on};\n", "${on} is an enabler variable but is used as a value"},
		"scalar as enabler": {"${num}a;\n", "is a number or on/off value"},
		"explode not first": {"a;\n!{explode domain(vhn)}\n", "explode"},
		"tab":               {"a {\n\tb;\n}\n", "f:2:1: tab character, indent with spaces [whitespace]"},
		"trailing":          {"a; \n", "f:1:3: trailing whitespace [whitespace]"},
		"unbalanced file":   {"a {\nb;\n", "braces"},
		"extra close":       {"a;\n}\n", "braces"},
		"enabler group":     {"${on}a {\nb;\n", "braces"},
		"braces in quotes":  {"a \"{\";\n# {\n", ""},
	} {
		got := strings.Join(lint(tc.src), "\n")
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	if Diff("f", "a\n", "a\n") != "" {
		t.Error("equal inputs gave a diff")
	}

	got := Diff("f", "a\n b\nc\n", "a\nb\nc\n")
	want := "--- f\n+++ f\n@@ -1,3 +1,3 @@\n a\n- b\n+b\n c\n"

	if got != want {
		t.Errorf("got %q want %q", got, want)
	}

	if got := Diff("f", "a", "a\n"); !strings.Contains(got, "\\ No newline at end of file") {
		t.Errorf("missing no-newline marker: %q", got)
	}
}

func TestRenderLineIndentsMultilineValues(t *testing.T) {
	t.Parallel()

	r := fakeResolver{
		enablers: map[string]bool{"on": true, "off": false},
		values: map[string]string{
			"three": "add_header A 1;\nadd_header B 2;\nadd_header C 3;",
			"gaps":  "a;\n\nb;\n",
			"one":   "single;",
		},
	}

	for _, tc := range []struct{ name, in, want string }{
		{
			"nested placeholder at 8 spaces, three lines",
			"        ${three}",
			"        add_header A 1;\n        add_header B 2;\n        add_header C 3;",
		},
		{
			"placeholder after text uses the line indentation",
			"    set $x ${three}",
			"    set $x add_header A 1;\n    add_header B 2;\n    add_header C 3;",
		},
		{
			"empty lines stay empty, trailing newline kept",
			"    ${gaps}",
			"    a;\n\n    b;\n",
		},
		{"single-line value unaffected", "        ${one}", "        single;"},
		{"column 0 adds nothing", "${three}", "add_header A 1;\nadd_header B 2;\nadd_header C 3;"},
		{
			"enabler on keeps the whitespace after it",
			"    ${on}    ${three}",
			"        add_header A 1;\n        add_header B 2;\n        add_header C 3;",
		},
		{
			"enabler off comments every line, not only the first",
			"    ${off}    ${three}",
			"    #    add_header A 1;\n    #    add_header B 2;\n    #    add_header C 3;",
		},
		{
			"enabler off keeps blank lines blank",
			"    ${off}${gaps}",
			"    #a;\n\n    #b;\n",
		},
	} {
		if got := RenderLine(tc.in, r); got != tc.want {
			t.Errorf("%s: RenderLine(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
