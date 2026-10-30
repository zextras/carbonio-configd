// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - mail, IMAP, and POP3 resolver tests
package proxy

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/config/configtest"
)

// TestResolveGreeting tests resolveGreeting branches
func TestResolveGreeting(t *testing.T) {
	tests := []struct {
		name        string
		globalData  *config.ConfigMap
		attribute   string
		format      string
		expectEmpty bool
	}{
		{
			name:        "returns empty when attribute not set",
			globalData:  configtest.NewConfigMapFrom(map[string]string{}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Zimbra %s IMAP4 ready",
			expectEmpty: true,
		},
		{
			name: "returns empty when attribute is FALSE",
			globalData: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "FALSE",
			}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Zimbra %s IMAP4 ready",
			expectEmpty: true,
		},
		{
			name: "returns greeting when attribute is TRUE",
			globalData: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "TRUE",
			}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Zimbra %s IMAP4 ready",
			expectEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
				LocalConfig:  &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
			}
			result, err := g.resolveGreeting(tt.attribute, tt.format)
			if err != nil {
				t.Fatalf("resolveGreeting failed: %v", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T", result)
			}
			if tt.expectEmpty && str != "" {
				t.Errorf("expected empty string, got %q", str)
			}
			if !tt.expectEmpty && !strings.Contains(str, "Zimbra") {
				t.Errorf("expected Zimbra in greeting, got %q", str)
			}
		})
	}
}

// writeBuildInfoJar creates <base>/mailbox/jars/mailbox.jar holding the given
// buildInfo.properties content, mirroring the layout the legacy BuildInfo reads.
func writeBuildInfoJar(t *testing.T, base, props string) {
	t.Helper()

	dir := filepath.Join(base, "mailbox", "jars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	f, err := os.Create(filepath.Join(dir, "mailbox.jar"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	zw := zip.NewWriter(f)

	w, err := zw.Create("com/zimbra/cs/util/buildInfo.properties")
	if err != nil {
		t.Fatalf("zip Create: %v", err)
	}

	if _, err := w.Write([]byte(props)); err != nil {
		t.Fatalf("zip Write: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestResolveIMAPId verifies the IMAP ID value matches the legacy mail.imapid default:
// "Zimbra" plus BuildInfo.VERSION/RELEASE read from mailbox.jar's buildInfo.properties.
func TestResolveIMAPId(t *testing.T) {
	t.Run("reads VERSION and RELEASE from mailbox.jar", func(t *testing.T) {
		dir := t.TempDir()
		writeBuildInfoJar(t, dir, "MAJORVERSION=4\nVERSION=4.38.10_ZEXTRAS_202610\nRELEASE=carbonio\nDATE=202610\n")

		g := &Generator{WorkingDir: dir}

		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId failed: %v", err)
		}

		want := `"NAME" "Zimbra" "VERSION" "4.38.10_ZEXTRAS_202610" "RELEASE" "carbonio"`
		if result != want {
			t.Errorf("got %q, want %q", result, want)
		}
	})

	t.Run("unknown when mailbox.jar is missing", func(t *testing.T) {
		g := &Generator{WorkingDir: t.TempDir()}

		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId failed: %v", err)
		}

		want := `"NAME" "Zimbra" "VERSION" "unknown" "RELEASE" "unknown"`
		if result != want {
			t.Errorf("got %q, want %q", result, want)
		}
	})
}

// TestResolveIMAPGreeting tests resolveIMAPGreeting
func TestResolveIMAPGreeting(t *testing.T) {
	t.Run("returns empty when disabled", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
			LocalConfig:  &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
		}
		result, err := g.resolveIMAPGreeting(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPGreeting failed: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %v", result)
		}
	})

	t.Run("returns IMAP greeting when enabled", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "TRUE",
			})},
			LocalConfig: &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
		}
		result, err := g.resolveIMAPGreeting(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPGreeting failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "IMAP4") {
			t.Errorf("expected IMAP4 in greeting, got %q", str)
		}
	})
}

// TestResolvePOP3Greeting tests resolvePOP3Greeting
func TestResolvePOP3Greeting(t *testing.T) {
	t.Run("returns empty when disabled", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
			LocalConfig:  &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
		}
		result, err := g.resolvePOP3Greeting(context.Background())
		if err != nil {
			t.Fatalf("resolvePOP3Greeting failed: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %v", result)
		}
	})

	t.Run("returns POP3 greeting when enabled", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyPop3ExposeVersionOnBanner": "TRUE",
			})},
			LocalConfig: &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
		}
		result, err := g.resolvePOP3Greeting(context.Background())
		if err != nil {
			t.Fatalf("resolvePOP3Greeting failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "POP3") {
			t.Errorf("expected POP3 in greeting, got %q", str)
		}
	})
}

// TestResolveMailWhitelistIPs tests resolveMailWhitelistIPs
func TestResolveMailWhitelistIPs(t *testing.T) {
	t.Run("returns empty when not configured", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		}
		result, err := g.resolveMailWhitelistIPs(context.Background())
		if err != nil {
			t.Fatalf("resolveMailWhitelistIPs failed: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("returns single IP", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyIPThrottleWhitelist": "1.2.3.4",
			})},
		}
		result, err := g.resolveMailWhitelistIPs(context.Background())
		if err != nil {
			t.Fatalf("resolveMailWhitelistIPs failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		expected := "mail_whitelist_ip    1.2.3.4;\n"
		if str != expected {
			t.Errorf("expected %q, got %q", expected, str)
		}
	})

	t.Run("returns multiple IPs with proper formatting", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyIPThrottleWhitelist": "1.2.3.4\n5.6.7.8",
			})},
		}
		result, err := g.resolveMailWhitelistIPs(context.Background())
		if err != nil {
			t.Fatalf("resolveMailWhitelistIPs failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "mail_whitelist_ip    1.2.3.4;") {
			t.Errorf("expected first IP directive in %q", str)
		}
		if !strings.Contains(str, "    mail_whitelist_ip    5.6.7.8;") {
			t.Errorf("expected second IP indented directive in %q", str)
		}
	})

	t.Run("falls back to global config", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: configtest.NewConfigMapFrom(map[string]string{})},
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyIPThrottleWhitelist": "10.0.0.1",
			})},
		}
		result, err := g.resolveMailWhitelistIPs(context.Background())
		if err != nil {
			t.Fatalf("resolveMailWhitelistIPs failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "10.0.0.1") {
			t.Errorf("expected IP 10.0.0.1 in %q", str)
		}
	})
}
