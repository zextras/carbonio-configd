// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - URL and redirect resolver tests
package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestMakeLoginURLResolver tests all branches of makeLoginURLResolver
func TestMakeLoginURLResolver(t *testing.T) {
	tests := []struct {
		name       string
		configKey  string
		globalData map[string]string
		expected   string
	}{
		{
			name:       "returns staticLoginPath when key not in config",
			configKey:  "zimbraWebClientLoginURL",
			globalData: map[string]string{},
			expected:   staticLoginPath,
		},
		{
			name:      "returns custom URL from config",
			configKey: "zimbraWebClientLoginURL",
			globalData: map[string]string{
				"zimbraWebClientLoginURL": "https://custom.example.com/login",
			},
			expected: "https://custom.example.com/login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			resolver := g.makeLoginURLResolver(tt.configKey)
			result, err := resolver(context.Background())
			if err != nil {
				t.Fatalf("makeLoginURLResolver failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %v", tt.expected, result)
			}
		})
	}
}

// TestMakeLogoutRedirectResolver tests all branches of makeLogoutRedirectResolver
func TestMakeLogoutRedirectResolver(t *testing.T) {
	tests := []struct {
		name       string
		configKey  string
		globalData map[string]string
		expected   string
	}{
		{
			name:       "returns return 307 path when key not in config",
			configKey:  "zimbraWebClientLogoutURL",
			globalData: map[string]string{},
			expected:   nginxReturn307Path,
		},
		{
			name:      "returns return 200 when key is set in config",
			configKey: "zimbraWebClientLogoutURL",
			globalData: map[string]string{
				"zimbraWebClientLogoutURL": "https://custom.example.com/logout",
			},
			expected: nginxReturn200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			resolver := g.makeLogoutRedirectResolver(tt.configKey)
			result, err := resolver(context.Background())
			if err != nil {
				t.Fatalf("makeLogoutRedirectResolver failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %v", tt.expected, result)
			}
		})
	}
}

// TestResolveErrorPages tests resolveErrorPages branches
func TestResolveErrorPages(t *testing.T) {
	t.Run("uses default static error pages when no URL configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveErrorPages(context.Background())
		if err != nil {
			t.Fatalf("resolveErrorPages failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "zmerror_upstream_502.html") {
			t.Errorf("expected static 502 error page, got %q", str)
		}
		if !strings.Contains(str, "zmerror_upstream_504.html") {
			t.Errorf("expected static 504 error page, got %q", str)
		}
	})

	t.Run("uses custom error handler URL when configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyErrorHandlerURL": "/error-handler",
			}},
		}
		result, err := g.resolveErrorPages(context.Background())
		if err != nil {
			t.Fatalf("resolveErrorPages failed: %v", err)
		}
		str := result.(string)
		if !strings.Contains(str, "/error-handler") {
			t.Errorf("expected custom URL in result, got %q", str)
		}
		if !strings.Contains(str, "err=502") {
			t.Errorf("expected err param in result, got %q", str)
		}
	})
}
