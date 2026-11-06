// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	testTemplates = "testdata/templates"
	// realTemplatesEnv names a directory of real templates (carbonio-proxy)
	// the safety test also covers when set.
	realTemplatesEnv = "CONFIGD_PROXY_TEMPLATES"
)

func fixtureFiles(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("testdata/fixtures/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}

	return files
}

func renderFixture(t *testing.T, fixture, templates string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	fx, err := ParseFixture(data)
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()

	written, err := RenderOffline(context.Background(), sys.Real(), fx, templates, out)
	if err != nil {
		t.Fatalf("render %s: %v", fixture, err)
	}

	files := make(map[string]string, len(written))

	for _, rel := range written {
		b, err := os.ReadFile(filepath.Join(out, rel))
		if err != nil {
			t.Fatal(err)
		}

		files[rel] = string(b)
	}

	return files
}

func TestRenderOffline_FixtureSwitches(t *testing.T) {
	t.Parallel()

	both := renderFixture(t, "testdata/fixtures/ipboth-strict-mail.yaml", testTemplates)
	v4 := renderFixture(t, "testdata/fixtures/ipv4only-nostrict-nomail.yaml", testTemplates)

	for _, name := range []string{
		"nginx.conf", "nginx/includes/nginx.conf.web.https", "nginx/includes/nginx.conf.web.upstreams",
		"nginx/includes/nginx.conf.stream.addressBook", "nginx/includes/nginx.conf.mail",
	} {
		if _, ok := both[name]; !ok {
			t.Errorf("%s not written; got %v", name, keys(both))
		}
	}

	https := both["nginx/includes/nginx.conf.web.https"]

	for _, want := range []string{"server_name  mail.example.com;", "server_name  sso.example.org;", "listen   [::]:"} {
		if !strings.Contains(https, want) {
			t.Errorf("ipboth https lacks %q:\n%s", want, https)
		}
	}

	for _, l := range strings.Split(v4["nginx/includes/nginx.conf.web.https"], "\n") {
		if strings.Contains(l, "[::]:") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			t.Errorf("ipv4only listens on IPv6: %q", l)
		}
	}

	if !strings.Contains(both["nginx/includes/nginx.conf.stream.addressBook"], "server mbox2.example.com:7070;") {
		t.Errorf("server explode lacks mbox2:\n%s", both["nginx/includes/nginx.conf.stream.addressBook"])
	}

	if !strings.Contains(both["nginx/includes/nginx.conf.web.upstreams"], "server 10.0.0.11:8080;") {
		t.Errorf("variable override not rendered:\n%s", both["nginx/includes/nginx.conf.web.upstreams"])
	}
}

func TestParseFixture_Errors(t *testing.T) {
	t.Parallel()

	if _, err := ParseFixture([]byte("bogus: 1\n")); err == nil {
		t.Error("unknown field accepted")
	}

	fx, err := ParseFixture([]byte("variables:\n  no.such.var: x\n"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := RenderOffline(context.Background(), sys.Real(), fx, testTemplates, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("err = %v, want unknown variable", err)
	}

	fx, _ = ParseFixture([]byte("variables:\n  main.workers: [1]\n"))
	if _, err := RenderOffline(context.Background(), sys.Real(), fx, testTemplates, t.TempDir()); err == nil {
		t.Error("list of non-strings accepted")
	}
}

func TestRegistry(t *testing.T) {
	t.Parallel()

	reg := TemplateRegistry{}

	for name, want := range map[string]tmpl.Kind{
		"core.ipboth.enabled": tmpl.KindEnabler,
		"web.https.port":      tmpl.KindScalar,
		"web.ssl.protocols":   tmpl.KindText,
	} {
		if k, ok := reg.Kind(name); !ok || k != want {
			t.Errorf("Kind(%s) = %v, %v; want %v", name, k, ok, want)
		}
	}

	if _, ok := reg.Kind("no.such.var"); ok {
		t.Error("unknown variable is known")
	}

	for name, want := range map[string]bool{
		"core.ipboth.enabled":      true,
		"web.strict.servername":    true, // a text variable that renders "#"
		"web.ews.upstream.disable": true,
		"web.https.port":           false,
		"web.ssl.protocols":        false,
		"listen.:addresses":        false,
		"nope":                     false,
	} {
		if IsLineSwitch(name) != want {
			t.Errorf("IsLineSwitch(%s) = %v, want %v", name, !want, want)
		}
	}

	// The variables whose value renders with a leading space.
	for name, want := range map[string]bool{
		"web.ssl.protocols":  true,
		"mail.ssl.protocols": true,
		"mail.imapcapa":      true,
		"mail.pop3capa":      true,
		"ssl.protocols":      false,
		"web.ssl.ciphers":    false,
		"nope":               false,
	} {
		if HasLeadingSpace(name) != want {
			t.Errorf("HasLeadingSpace(%s) = %v, want %v", name, !want, want)
		}
	}
}

// squash drops all whitespace, line by line: the rendered output may differ
// from before only in whitespace (a disabled line keeps "#" but not the gap
// the enabler prefix used to leave after it).
func squash(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), "")
	}

	return strings.Join(lines, "\n")
}

// formatDir copies the *.template files of src to a temp dir, formatted.
func formatDir(t *testing.T, src string) string {
	t.Helper()

	dst := t.TempDir()

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".template") {
			continue
		}

		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}

		once := tmpl.Format(string(b), FormatOptions())
		if twice := tmpl.Format(once, FormatOptions()); twice != once {
			t.Errorf("%s: fmt is not idempotent", e.Name())
		}

		if err := os.WriteFile(filepath.Join(dst, e.Name()), []byte(once), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dst
}

// TestFormatPreservesRender is the safety property of "template fmt": for
// every fixture, rendering the formatted templates gives the same files as
// rendering the originals, up to whitespace.
func TestFormatPreservesRender(t *testing.T) {
	t.Parallel()

	dirs := []string{testTemplates}
	if dir := os.Getenv(realTemplatesEnv); dir != "" {
		dirs = append(dirs, dir)
	}

	for _, dir := range dirs {
		formatted := formatDir(t, dir)

		for _, fixture := range fixtureFiles(t) {
			before := renderFixture(t, fixture, dir)
			after := renderFixture(t, fixture, formatted)

			if len(before) != len(after) {
				t.Fatalf("%s %s: %d files before, %d after", dir, fixture, len(before), len(after))
			}

			for name, b := range before {
				if squash(b) != squash(after[name]) {
					t.Errorf("%s %s: %s differs after fmt:\n--- before\n%s\n--- after\n%s",
						dir, fixture, name, b, after[name])
				}
			}
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
