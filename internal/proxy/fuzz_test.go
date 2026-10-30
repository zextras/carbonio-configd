// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// newFuzzProcessor builds a TemplateProcessor over an in-memory generator.
// Explode providers are injected so no LDAP client is ever consulted.
func newFuzzProcessor() *TemplateProcessor {
	gen := &Generator{Variables: map[string]*Variable{
		"mail.imap.enabled":  {Keyword: "mail.imap.enabled", ValueType: ValueTypeEnabler, Value: true},
		"mail.pop3.enabled":  {Keyword: "mail.pop3.enabled", ValueType: ValueTypeEnabler, Value: false},
		"mail.imaps.enabled": {Keyword: "mail.imaps.enabled", ValueType: ValueTypeEnabler, Value: "server"},
		"core.ipboth.enabled": {
			Keyword: "core.ipboth.enabled", ValueType: ValueTypeEnabler, Value: 0,
		},
		"main.workers":    {Keyword: "main.workers", ValueType: ValueTypeInteger, Value: 4},
		"web.http.port":   {Keyword: "web.http.port", ValueType: ValueTypeInteger, Value: int64(8080)},
		"main.ctimeout":   {Keyword: "main.ctimeout", ValueType: ValueTypeTime, Value: 120000},
		"main.rtimeout":   {Keyword: "main.rtimeout", ValueType: ValueTypeTimeInSec, Value: 30000},
		"ssl.crt":         {Keyword: "ssl.crt", ValueType: ValueTypeString, Value: "/opt/zextras/conf/nginx.crt"},
		"ssl.key":         {Keyword: "ssl.key", ValueType: ValueTypeString, Value: "/opt/zextras/conf/nginx.key"},
		"listen.ipv6":     {Keyword: "listen.ipv6", ValueType: ValueTypeString, Value: "[::1]/128"},
		"upstream.multi":  {Keyword: "upstream.multi", ValueType: ValueTypeString, Value: "server a:80;\nserver b:80;"},
		"web.sslprotocol": {Keyword: "web.sslprotocol", ValueType: ValueTypeString, Value: []string{"TLSv1.2", "TLSv1.3"}},
		"flag.on":         {Keyword: "flag.on", ValueType: ValueTypeBoolean, Value: true},
		"nil.value":       {Keyword: "nil.value", ValueType: ValueTypeString, Value: nil},
	}}

	tp := NewTemplateProcessor(gen, "", "")
	tp.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{Name: "example.com", VirtualHostname: "mail.example.com", VirtualIPAddress: "192.168.1.10", ClientCertMode: "on"},
			{Name: "novhn.test", VirtualIPAddress: "[::1]"},
			{Name: "ssl.test", VirtualHostname: "ssl.test", SSLCertificate: "CERT", SSLPrivateKey: "KEY", ClientCertMode: "off"},
		}
	}
	tp.serverProvider = func(service string) []ServerInfo {
		if service != "mailbox" {
			return nil
		}

		return []ServerInfo{
			{ID: "id1", Hostname: "mbox1.example.com", Services: []string{"mailbox"}},
			{ID: "id2", Hostname: "mbox2.example.com", Services: []string{"mailbox", "proxy"}},
		}
	}

	return tp
}

// templateSeeds are realistic nginx template fragments (see docs/proxy-templates.md).
var templateSeeds = []string{
	"",
	"worker_processes ${main.workers};",
	"    ${mail.imap.enabled} include ${core.includes}/nginx.mail.imap.conf;",
	"    ${mail.pop3.enabled}listen ${listen.ipv6}:110;",
	"    ${mail.imaps.enabled}  ${upstream.multi}",
	"    ${core.ipboth.enabled}listen [::]:443 ssl;",
	"ssl_certificate ${ssl.crt};\nssl_certificate_key ${ssl.key};",
	"proxy_connect_timeout ${main.ctimeout};\nproxy_read_timeout ${main.rtimeout};",
	"    imap_id         ;",
	"    proxy_issue_pop3_xoip   ;",
	"    ${missing.variable} listen 80;\n    keepalive ${missing.too};",
	"!{explode domain(vhn)}\nserver {\n    server_name ${vhn};\n    listen ${vip}:443;\n    ssl_certificate ${ssl.crt};\n}",
	"!{explode domain(vhn, sso)}\nserver ${vhn} ${ssl.key};",
	"!{explode server(mailbox)}\n# comment skipped\nserver ${server_id} ${server_hostname}:7071;",
	"!{explode server()}\nx",
	"!{explode unknown(x)}\nx",
	"!{explode domain()}\nx",
	"!{explode domain(vhn,,sso)}\nx",
	"line one\r\nline two\r\n",
	"${} ${a b} ${${main.workers}} $${main.workers} ${main.workers",
	"unicode ${ключ} caffè ☕ 日本語 ${web.http.port}",
	"${flag.on}${nil.value}${web.sslprotocol}",
}

// FuzzInterpolateLine drives TemplateProcessor.interpolateLine with arbitrary
// single template lines.
//
// Invariants: never panics; never returns an error (unknown variables expand
// to the empty string); deterministic; a line without "${" is returned
// verbatim unless it is an argument-less directive ("name ;"), which must be
// commented out as "    # <trimmed line>".
func FuzzInterpolateLine(f *testing.F) {
	for _, s := range templateSeeds {
		for line := range strings.SplitSeq(s, "\n") {
			f.Add(line)
		}
	}

	ctx := logger.ContextWithLogger(context.Background(), slog.New(slog.DiscardHandler))
	tp := newFuzzProcessor()

	f.Fuzz(func(t *testing.T, line string) {
		if strings.Contains(line, "\n") {
			t.Skip("lines never contain newlines")
		}

		got, err := tp.interpolateLine(ctx, line)
		if err != nil {
			t.Fatalf("interpolateLine(%q) returned error: %v", line, err)
		}

		again, _ := tp.interpolateLine(ctx, line)
		if again != got {
			t.Fatalf("non-deterministic interpolateLine(%q): %q vs %q", line, got, again)
		}

		if !strings.Contains(line, "${") {
			want := line
			if tp.emptyDirectivePattern.MatchString(line) {
				want = "    # " + strings.TrimSpace(line)
			}

			if got != want {
				t.Fatalf("variable-free line altered: %q -> %q (want %q)", line, got, want)
			}
		}
	})
}

// FuzzProcessTemplate drives TemplateProcessor.ProcessTemplate with arbitrary
// multi-line templates, including !{explode ...} headers.
//
// Invariants: never panics; deterministic output/error; the generator's
// variable table is left exactly as found (explode must not leak vhn/vip/
// server_* or SSL overrides); without an explode header every input line
// yields exactly one newline-terminated output line when no variable
// expands to a newline.
func FuzzProcessTemplate(f *testing.F) {
	for _, s := range templateSeeds {
		f.Add(s)
	}

	ctx := logger.ContextWithLogger(context.Background(), slog.New(slog.DiscardHandler))
	tp := newFuzzProcessor()
	baseline := maps.Clone(tp.generator.Variables)

	f.Fuzz(func(t *testing.T, content string) {
		tmpl := &Template{Name: "fuzz", Content: content, Lines: strings.Split(content, "\n")}

		out, err := tp.ProcessTemplate(ctx, tmpl)

		if !maps.Equal(baseline, tp.generator.Variables) {
			t.Fatalf("ProcessTemplate(%q) leaked generator variables: %v", content, tp.generator.Variables)
		}

		out2, err2 := tp.ProcessTemplate(ctx, tmpl)
		if out != out2 || (err == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic ProcessTemplate(%q): %q/%v vs %q/%v", content, out, err, out2, err2)
		}

		if err != nil {
			return
		}

		if out != "" && !strings.HasSuffix(out, "\n") {
			t.Fatalf("output not newline-terminated: %q", out)
		}

		if !strings.HasPrefix(content, "!{explode") && !strings.Contains(content, "${upstream.multi}") {
			if got, want := strings.Count(out, "\n"), len(tmpl.Lines); got != want {
				t.Fatalf("expected %d output lines, got %d for %q -> %q", want, got, content, out)
			}
		}
	})
}

// FuzzParseExplodeDirective checks that the explode header parser never panics,
// only fires on a leading "!{explode", returns the tail of the lines untouched
// and yields a word-only type.
func FuzzParseExplodeDirective(f *testing.F) {
	f.Add("!{explode domain(vhn)}", "server {")
	f.Add("!{explode domain(vhn, sso)}", "")
	f.Add("!{explode server(mailbox)}", "# c")
	f.Add("!{explode  server( mailbox )} trailing", "x")
	f.Add("!{explode domain()}", "x")
	f.Add(" !{explode domain(vhn)}", "x")
	f.Add("!{explode domain(vhn)", "x")
	f.Add("!{explode дом(vhn)}", "x")
	f.Add("", "")

	tp := newFuzzProcessor()

	f.Fuzz(func(t *testing.T, first, rest string) {
		lines := []string{first, rest}

		typ, args, remaining, ok := tp.parseExplodeDirective(lines)
		if !ok {
			if typ != "" || args != "" || remaining != nil {
				t.Fatalf("ok=false but non-zero results: %q %q %v", typ, args, remaining)
			}

			return
		}

		if !strings.HasPrefix(first, "!{explode") {
			t.Fatalf("directive recognised without prefix: %q", first)
		}

		isWord := func(r rune) bool {
			return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		}

		if typ == "" || strings.IndexFunc(typ, func(r rune) bool { return !isWord(r) }) >= 0 {
			t.Fatalf("invalid explode type %q from %q", typ, first)
		}

		if strings.Contains(args, ")") {
			t.Fatalf("args %q contain ')'", args)
		}

		if len(remaining) != 1 || remaining[0] != rest {
			t.Fatalf("remaining lines altered: %q", remaining)
		}
	})
}

// FuzzParseMultiValueAttribute checks the "Header: Value" multi-value splitter:
// never empty, single value when no newline survives trimming, otherwise
// every element is trimmed and non-empty.
func FuzzParseMultiValueAttribute(f *testing.F) {
	for _, s := range []string{
		"", "single value", "X-Frame-Options: SAMEORIGIN\nX-XSS-Protection: 1",
		"a\n\n  b  \n", "\n\n", "  \n  ", "crlf: one\r\ncrlf: two\r\n", "unicode: caffè ☕\n日本語: 値",
		"ipv6: [::1]/128\nfe80::/10",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, attr string) {
		got := parseMultiValueAttribute(attr)
		if len(got) == 0 {
			t.Fatalf("empty result for %q", attr)
		}

		if !strings.Contains(attr, "\n") {
			if len(got) != 1 || got[0] != attr {
				t.Fatalf("no-newline input %q split into %q", attr, got)
			}

			return
		}

		if len(got) == 1 && got[0] == attr {
			// Every line was blank: the input itself is returned as the fallback.
			if strings.TrimSpace(strings.ReplaceAll(attr, "\n", "")) != "" {
				t.Fatalf("fallback used for non-blank input %q", attr)
			}

			return
		}

		for _, v := range got {
			if v == "" || v != strings.TrimSpace(v) || strings.Contains(v, "\n") {
				t.Fatalf("bad element %q from %q", v, attr)
			}
		}
	})
}

// FuzzNormalizeMultiValue checks that normalizeMultiValue removes every
// newline and comma and is idempotent.
func FuzzNormalizeMultiValue(f *testing.F) {
	for _, s := range []string{"", "a,b,c", "a\nb\nc", "a, b\n c,", ",,\n\n", "crlf\r\nx", "caffè,☕", "[::1]/128,fe80::/10"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		got := normalizeMultiValue(s)
		if strings.ContainsAny(got, ",\n") {
			t.Fatalf("separator survived: %q -> %q", s, got)
		}

		if again := normalizeMultiValue(got); again != got {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, got, again)
		}
	})
}
