// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - mail, IMAP, and POP3 resolver tests
package proxy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
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
			globalData:  config.NewConfigMapFrom(map[string]string{}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Carbonio %s IMAP4 ready",
			expectEmpty: true,
		},
		{
			name: "returns empty when attribute is FALSE",
			globalData: config.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "FALSE",
			}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Carbonio %s IMAP4 ready",
			expectEmpty: true,
		},
		{
			name: "returns greeting when attribute is TRUE",
			globalData: config.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "TRUE",
			}),
			attribute:   "zimbraReverseProxyImapExposeVersionOnBanner",
			format:      "* OK Carbonio %s IMAP4 ready",
			expectEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
				LocalConfig:  &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			if !tt.expectEmpty && !strings.Contains(str, "Carbonio") {
				t.Errorf("expected Carbonio in greeting, got %q", str)
			}
		})
	}
}

// TestResolveIMAPId tests resolveIMAPId branches
func TestResolveIMAPId(t *testing.T) {
	t.Run("returns UNKNOWN version when no local config", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
		}
		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "UNKNOWN") {
			t.Errorf("expected UNKNOWN in result, got %q", str)
		}
		if !strings.Contains(str, "Zimbra") {
			t.Errorf("expected Zimbra in result, got %q", str)
		}
	})

	t.Run("appends build number when configured and version has no underscore", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{
				"zimbra_buildnum": "12345",
			})},
		}
		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "ZEXTRAS_12345") {
			t.Errorf("expected build number in result, got %q", str)
		}
	})
}

// TestResolveIMAPIdWithVersion tests resolveIMAPId when zimbra_home points to a real file
func TestResolveIMAPIdWithVersion(t *testing.T) {
	dir := t.TempDir()
	versionContent := "25.3.0"
	if err := os.WriteFile(dir+"/.version", []byte(versionContent+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Run("version file read and used", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{
				Data: config.NewConfigMapFrom(map[string]string{"zimbra_home": dir}),
			},
		}
		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, versionContent) {
			t.Errorf("expected version %q in result, got %q", versionContent, str)
		}
	})

	t.Run("version already has underscore, buildnum not appended", func(t *testing.T) {
		if err := os.WriteFile(dir+"/.version", []byte("25.3.0_ZEXTRAS_99999\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		g := &Generator{
			LocalConfig: &config.LocalConfig{
				Data: config.NewConfigMapFrom(map[string]string{
					"zimbra_home":     dir,
					"zimbra_buildnum": "11111",
				}),
			},
		}
		result, err := g.resolveIMAPId(context.Background())
		if err != nil {
			t.Fatalf("resolveIMAPId: %v", err)
		}
		str := result.(string)
		// build number should NOT be appended again
		if strings.Contains(str, "11111") {
			t.Errorf("build number was appended to version that already had underscore: %q", str)
		}
	})
}

// TestResolveIMAPGreeting tests resolveIMAPGreeting
func TestResolveIMAPGreeting(t *testing.T) {
	t.Run("returns empty when disabled", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
			LocalConfig:  &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyImapExposeVersionOnBanner": "TRUE",
			})},
			LocalConfig: &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			LocalConfig:  &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyPop3ExposeVersionOnBanner": "TRUE",
			})},
			LocalConfig: &config.LocalConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			ServerConfig: &config.ServerConfig{Data: config.NewConfigMapFrom(map[string]string{})},
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
			ServerConfig: &config.ServerConfig{Data: config.NewConfigMapFrom(map[string]string{
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
			ServerConfig: &config.ServerConfig{Data: config.NewConfigMapFrom(map[string]string{
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
			ServerConfig: &config.ServerConfig{Data: config.NewConfigMapFrom(map[string]string{})},
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMapFrom(map[string]string{
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
