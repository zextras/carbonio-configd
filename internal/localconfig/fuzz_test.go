// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// parseKeyValueLines turns "key=value" lines into a config map; lines without
// '=' are ignored. It lets the fuzzer drive Interpolate with a single string.
func parseKeyValueLines(input string) map[string]string {
	cfg := make(map[string]string)

	for line := range strings.SplitSeq(input, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			cfg[k] = v
		}
	}

	return cfg
}

// refNames returns the variable names referenced by value via ${name}.
func refNames(value string) []string {
	matches := varPattern.FindAllStringSubmatch(value, -1)
	names := make([]string, 0, len(matches))

	for _, m := range matches {
		names = append(names, m[1])
	}

	return names
}

// isWellFormed reports whether value uses '$', '{' and '}' only inside
// complete ${name} references, so substitution cannot assemble new references
// out of adjacent fragments.
func isWellFormed(value string) bool {
	return !strings.ContainsAny(varPattern.ReplaceAllString(value, ""), "${}")
}

// refDepth returns the longest reference chain starting at key, or -1 when a
// cycle (including a self-reference) is reachable.
func refDepth(cfg map[string]string, key string, visiting map[string]bool, memo map[string]int) int {
	if d, ok := memo[key]; ok {
		return d
	}

	if visiting[key] {
		return -1
	}

	visiting[key] = true

	depth := 0

	for _, name := range refNames(cfg[key]) {
		if _, exists := cfg[name]; !exists {
			continue
		}

		d := refDepth(cfg, name, visiting, memo)
		if d < 0 {
			delete(visiting, key)

			return -1
		}

		depth = max(depth, d+1)
	}

	delete(visiting, key)

	memo[key] = depth

	return depth
}

// referenceResolve is an independent, recursive implementation of the
// documented Interpolate semantics for acyclic, well-formed configs.
func referenceResolve(cfg map[string]string, key string, memo map[string]string) string {
	if v, ok := memo[key]; ok {
		return v
	}

	out := varPattern.ReplaceAllStringFunc(cfg[key], func(match string) string {
		name := match[2 : len(match)-1]
		if _, ok := cfg[name]; !ok {
			return match
		}

		return referenceResolve(cfg, name, memo)
	})

	memo[key] = out

	return out
}

// FuzzInterpolate drives Interpolate with arbitrary key=value text.
//
// Invariants for every input: terminates (bounded passes), the key set never
// changes, values without "${" are untouched, and a zero substitution count
// means the map is unchanged. For acyclic, well-formed configs with a
// reference chain shorter than the pass limit the result must additionally
// match an independent recursive resolver and be independent of Go's random
// map iteration order.
func FuzzInterpolate(f *testing.F) {
	seeds := []string{
		"",
		"zimbra_home=/opt/zextras\nzimbra_log_directory=${zimbra_home}/log",
		"zimbra_home=/opt/zextras\nzimbra_log_directory=${zimbra_home}/log\nmysql_errlogfile=${zimbra_log_directory}/mysql_error.log",
		"value_with_missing=prefix-${does_not_exist}-suffix",
		"a=${a}",
		"a=${b}\nb=${a}",
		"a=${b}\nb=${c}\nc=${a}",
		"a=${b}${b}\nb=${c}${c}\nc=x",
		"networks=127.0.0.0/8 [::1]/128\nref=${networks}",
		"k=${}\nj=${\nl=}${",
		"a=$${b}\nb={c}\nc=z",
		"a=${b}\r\nb=value\r\n",
		"ключ=${значение}\nзначение=☕",
		"empty=\nref=${empty}",
		"x=${y}\ny=${x",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		cfg := parseKeyValueLines(input)
		before := maps.Clone(cfg)

		subs := Interpolate(cfg)

		if subs < 0 {
			t.Fatalf("negative substitution count %d", subs)
		}

		if len(cfg) != len(before) {
			t.Fatalf("key set size changed: %d -> %d", len(before), len(cfg))
		}

		for k, old := range before {
			now, ok := cfg[k]
			if !ok {
				t.Fatalf("key %q disappeared", k)
			}

			if !strings.Contains(old, "${") && now != old {
				t.Fatalf("value without reference changed: %q: %q -> %q", k, old, now)
			}
		}

		// A substitution may legitimately be a no-op (self-reference such as
		// "a=${a}" resolves to itself), so only subs==0 implies unchanged.
		if subs == 0 && !maps.Equal(before, cfg) {
			t.Fatalf("subs=0 but config changed")
		}

		checkInterpolateAgainstReference(t, before, cfg)
	})
}

// checkInterpolateAgainstReference verifies got (the result of Interpolate on
// input) for the class of configs where the outcome is fully specified.
func checkInterpolateAgainstReference(t *testing.T, input, got map[string]string) {
	t.Helper()

	// The recursive oracle can expand exponentially for adversarial fan-out.
	const oracleBudget = 1024

	size := 0

	for k, v := range input {
		size += len(k) + len(v)

		if !isWellFormed(v) {
			return
		}
	}

	if size > oracleBudget {
		return
	}

	visiting, memo := map[string]bool{}, map[string]int{}

	for k := range input {
		if d := refDepth(input, k, visiting, memo); d < 0 || d >= maxInterpolationDepth {
			return
		}
	}

	refMemo := map[string]string{}

	for k := range input {
		want := referenceResolve(input, k, refMemo)
		if got[k] != want {
			t.Fatalf("key %q: Interpolate=%q, reference=%q (input %q)", k, got[k], want, input)
		}
	}

	// Order independence: a second run over a fresh copy must agree.
	again := maps.Clone(input)
	Interpolate(again)

	if !maps.Equal(got, again) {
		t.Fatalf("Interpolate depends on map iteration order:\n%q\n%q", got, again)
	}
}

// FuzzLoadLocalConfigFromFile feeds arbitrary bytes as localconfig.xml.
// Invariants: never panics; on success every value is trimmed, loading is
// deterministic, and the resolved loader (defaults + interpolation) succeeds
// with a superset of the plain keys.
func FuzzLoadLocalConfigFromFile(f *testing.F) {
	seeds := []string{
		"",
		`<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<localconfig>
  <key name="zimbra_home">
    <value>/opt/zextras</value>
  </key>
  <key name="ldap_host">
    <value>localhost</value>
  </key>
  <key name="zimbra_log_directory">
    <value>${zimbra_home}/log</value>
  </key>
</localconfig>`,
		`<localconfig><key name="ldap_url"><value>ldap://[::1]:389 ldap://ldap2.example.com:389</value></key></localconfig>`,
		`<localconfig><key name="k"><value>  padded  </value></key><key name="k"><value>dup</value></key></localconfig>`,
		"<localconfig>\r\n<key name=\"crlf\">\r\n<value>v\r\n</value></key>\r\n</localconfig>\r\n",
		`<localconfig><key name="b64"><value>AAA=</value></key><key name="uni"><value>caffè ☕ 日本語</value></key></localconfig>`,
		`<localconfig><key name="empty"><value></value></key><key><value>noname</value></key></localconfig>`,
		`<localconfig><key name="x"><value><![CDATA[<raw & text>]]></value></key></localconfig>`,
		`<localconfig><key name="unclosed"><value>v</key>`,
		`<!DOCTYPE x [<!ENTITY a "b">]><localconfig><key name="e"><value>&a;</value></key></localconfig>`,
		`not xml at all`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	path := filepath.Join(f.TempDir(), "localconfig.xml")

	f.Fuzz(func(t *testing.T, data []byte) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		cfg, err := LoadLocalConfigFromFile(path)
		if err != nil {
			if cfg != nil {
				t.Fatalf("non-nil config %v alongside error %v", cfg, err)
			}

			return
		}

		for k, v := range cfg {
			if v != strings.TrimSpace(v) {
				t.Fatalf("value of %q not trimmed: %q", k, v)
			}
		}

		again, err := LoadLocalConfigFromFile(path)
		if err != nil || !maps.Equal(cfg, again) {
			t.Fatalf("non-deterministic load: %v / %v (err %v)", cfg, again, err)
		}

		resolved, err := LoadResolvedConfigFromFile(path)
		if err != nil {
			t.Fatalf("LoadResolvedConfigFromFile failed after successful load: %v", err)
		}

		for k := range cfg {
			if _, ok := resolved[k]; !ok {
				t.Fatalf("resolved config lost key %q", k)
			}
		}
	})
}

// isXMLChar reports whether r may appear in an XML 1.0 document.
func isXMLChar(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// isXMLText reports whether s is valid UTF-8 made only of legal XML characters.
func isXMLText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}

	for _, r := range s {
		if !isXMLChar(r) {
			return false
		}
	}

	return true
}

// FuzzFormatXMLRoundTrip checks FormatXML followed by LoadLocalConfigFromFile:
// every key and (whitespace-trimmed) value must survive, whatever markup
// characters, entities or CRLF they contain.
func FuzzFormatXMLRoundTrip(f *testing.F) {
	f.Add("zimbra_home", "/opt/zextras", "ldap_url", "ldap://[::1]:389 ldap://h:389")
	f.Add("k", "a<b>&\"'c", "k2", "  padded  ")
	f.Add("crlf", "line1\r\nline2", "uni", "caffè ☕ 日本語")
	f.Add("b64", "AAA=", "empty", "")
	f.Add("${ref}", "${other}", "other", "x")
	f.Add("", "v", "name with spaces", "]]>")

	path := filepath.Join(f.TempDir(), "localconfig.xml")

	f.Fuzz(func(t *testing.T, key1, value1, key2, value2 string) {
		in := map[string]string{}

		for _, kv := range [][2]string{{key1, value1}, {key2, value2}} {
			if isXMLText(kv[0]) && isXMLText(kv[1]) {
				in[kv[0]] = kv[1]
			}
		}

		var buf bytes.Buffer
		if err := FormatXML(&buf, in); err != nil {
			t.Fatalf("FormatXML: %v", err)
		}

		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got, err := LoadLocalConfigFromFile(path)
		if err != nil {
			t.Fatalf("reload of FormatXML output failed: %v\n%s", err, buf.String())
		}

		want := make(map[string]string, len(in))
		for k, v := range in {
			want[k] = strings.TrimSpace(v)
		}

		if !maps.Equal(want, got) {
			t.Fatalf("round trip mismatch:\nwant %q\n got %q\nxml:\n%s", want, got, buf.String())
		}
	})
}

// FuzzShellEscape checks that ShellEscape only rewrites single quotes, so that
// undoing the rewrite recovers the original string and no bare quote remains.
func FuzzShellEscape(f *testing.F) {
	for _, s := range []string{"", "plain", "it's", "'''", "a'b'c", "$(rm -rf /)", "caffè ☕", "line\nbreak", "[::1]/128"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		escaped := ShellEscape(s)

		if got := strings.ReplaceAll(escaped, `'\''`, "'"); got != s {
			t.Fatalf("escape not reversible: %q -> %q -> %q", s, escaped, got)
		}

		// Outside the '\'' sequences there must be no quote left.
		if rest := strings.ReplaceAll(escaped, `'\''`, ""); strings.Contains(rest, "'") {
			t.Fatalf("unescaped single quote in %q (from %q)", escaped, s)
		}

		if !strings.Contains(s, "'") && escaped != s {
			t.Fatalf("string without quotes modified: %q -> %q", s, escaped)
		}
	})
}
