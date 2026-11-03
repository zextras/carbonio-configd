// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// newLegacyGen builds a generator under a temp work dir with the given settings.
func newLegacyGen(t *testing.T, s Settings) (*Generator, string) {
	t.Helper()

	dir := t.TempDir()

	gen, err := LoadConfiguration(context.Background(), sys.Real(),
		&config.Config{BaseDir: dir, Hostname: "h"}, Sources{Settings: &s})
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	return gen, dir
}

func TestSettings_DefaultsAndOverrides(t *testing.T) {
	gen, dir := newLegacyGen(t, Settings{})

	if gen.TemplateDir != filepath.Join(dir, "conf", "nginx", "templates") ||
		gen.IncludesDir != filepath.Join(dir, "conf", "nginx", "includes") ||
		gen.confPrefix() != "nginx.conf" || gen.templatePrefix() != "nginx.conf" {
		t.Errorf("defaults wrong: %+v", gen)
	}

	gen, dir = newLegacyGen(t, Settings{TemplateDir: "/t", IncludeDir: "inc", ConfPrefix: "x.conf"})

	if gen.TemplateDir != "/t" || gen.IncludesDir != filepath.Join(dir, "conf", "inc") ||
		gen.confPrefix() != "x.conf" || gen.templatePrefix() != "x.conf" {
		t.Errorf("overrides wrong: %+v", gen)
	}

	if v := gen.Variables["core.cprefix"].Value; v != "x.conf" {
		t.Errorf("core.cprefix = %v, want x.conf", v)
	}

	gen, _ = newLegacyGen(t, Settings{ConfPrefix: "x.conf", TemplatePrefix: "t.conf"})
	if gen.templatePrefix() != "t.conf" {
		t.Errorf("template prefix = %q, want t.conf", gen.templatePrefix())
	}
}

func TestOutputName(t *testing.T) {
	gen, _ := newLegacyGen(t, Settings{})
	if n, ok := gen.outputName("nginx.conf.web.template"); !ok || n != "nginx.conf.web" {
		t.Errorf("default: %q %v", n, ok)
	}

	gen, _ = newLegacyGen(t, Settings{ConfPrefix: "c.conf", TemplatePrefix: "t.conf"})

	for tpl, want := range map[string]string{"t.conf.template": "c.conf", "t.conf.web.template": "c.conf.web"} {
		if n, ok := gen.outputName(tpl); !ok || n != want {
			t.Errorf("%s -> %q %v, want %q", tpl, n, ok, want)
		}
	}

	if _, ok := gen.outputName("nginx.conf.web.template"); ok {
		t.Error("a template of another prefix must be skipped")
	}
}

// TestGenerateAll_Prefixes writes a core and an include file under custom prefixes.
func TestGenerateAll_Prefixes(t *testing.T) {
	gen, dir := newLegacyGen(t, Settings{ConfPrefix: "c.conf", TemplatePrefix: "t.conf", IncludeDir: "inc"})

	tplDir := gen.TemplateDir
	for name, body := range map[string]string{
		"t.conf.template":     "core ${core.cprefix}\n",
		"t.conf.web.template": "web\n",
		"nginx.conf.template": "ignored\n",
	} {
		if err := os.MkdirAll(tplDir, 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(tplDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}

	if b, err := os.ReadFile(filepath.Join(dir, "conf", "c.conf")); err != nil || !strings.HasPrefix(string(b), "core c.conf\n") {
		t.Errorf("core conf = %q, %v", b, err)
	}

	if _, err := os.Stat(filepath.Join(dir, "conf", "inc", "c.conf.web")); err != nil {
		t.Errorf("include file missing: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "conf", "nginx.conf")); err == nil {
		t.Error("a template of another prefix must not be generated")
	}
}

func TestApplyOverrides(t *testing.T) {
	gen, _ := newLegacyGen(t, Settings{})
	gen.ApplyOverrides(context.Background(), []string{"core.cprefix=zzz", "nope=1", "=x", "bad", "web.http.port=8080=1"})

	got, err := gen.ExpandVariable(context.Background(), "core.cprefix")
	if err != nil || got != "zzz" {
		t.Errorf("core.cprefix = %q, %v, want zzz", got, err)
	}

	if _, exists := gen.Variables["nope"]; exists {
		t.Error("unknown variable must not be created")
	}

	if got, _ := gen.ExpandVariable(context.Background(), "web.http.port"); got != "8080=1" {
		t.Errorf("value keeps text after the first '=': %q", got)
	}
}

func TestWriteVariables(t *testing.T) {
	gen, _ := newLegacyGen(t, Settings{})

	var out bytes.Buffer
	if err := gen.WriteVariables(context.Background(), &out); err != nil {
		t.Fatal(err)
	}

	s := out.String()
	for _, want := range []string{
		"  NGINX Keyword:         core.cprefix\n",
		"  Value Type:            STRING\n",
		"  Controlling Attribute: (none)\n",
		"  Config Text:           nginx.conf\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q", want)
		}
	}

	if strings.Index(s, "core.cprefix") > strings.Index(s, "core.workdir") {
		t.Error("variables must be sorted by keyword")
	}
}

func TestForceDNSResolution(t *testing.T) {
	gen, _ := newLegacyGen(t, Settings{ForceDNSResolution: true})
	tp := NewTemplateProcessor(gen, gen.env.FS, gen.TemplateDir, gen.IncludesDir)

	tp.domainProvider = func() []DomainInfo { return nil }
	if err := gen.checkVirtualHostnames(context.Background(), tp); err != nil {
		t.Errorf("no domains: %v", err)
	}

	tp.domainProvider = func() []DomainInfo {
		return []DomainInfo{{Name: "d", VirtualHostname: "no-such-host.invalid"}}
	}

	err := gen.checkVirtualHostnames(context.Background(), tp)
	if err == nil || !strings.Contains(err.Error(), `virtual host name "no-such-host.invalid" is not resolvable`) {
		t.Errorf("err = %v, want the not-resolvable error", err)
	}

	tp.domainProvider = func() []DomainInfo {
		return []DomainInfo{{Name: "d", VirtualHostname: "no-such-host.invalid", VirtualIPAddress: "127.0.0.1"}}
	}

	if err := gen.checkVirtualHostnames(context.Background(), tp); err != nil {
		t.Errorf("a resolvable virtual IP wins over the hostname: %v", err)
	}
}
