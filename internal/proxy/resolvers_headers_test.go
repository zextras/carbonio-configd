// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - headers and HTTP resolver tests
package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestResolveAddHeadersDefault tests all branches of resolveAddHeadersDefault
func TestResolveAddHeadersDefault(t *testing.T) {
	tests := []struct {
		name       string
		globalData map[string]string
		contains   []string
		empty      bool
	}{
		{
			name:       "no headers configured returns empty string",
			globalData: map[string]string{},
			empty:      true,
		},
		{
			name: "single response header generates add_header directive",
			globalData: map[string]string{
				"zimbraReverseProxyResponseHeaders": "X-Frame-Options: SAMEORIGIN",
			},
			contains: []string{"add_header X-Frame-Options SAMEORIGIN;"},
		},
		{
			name: "CSP header generates add_header directive",
			globalData: map[string]string{
				"carbonioReverseProxyResponseCSPHeader": "Content-Security-Policy: default-src 'self'",
			},
			contains: []string{"add_header Content-Security-Policy default-src 'self';"},
		},
		{
			name: "multiple headers with newlines",
			globalData: map[string]string{
				"zimbraReverseProxyResponseHeaders": "X-Frame-Options: SAMEORIGIN\nX-XSS-Protection: 1; mode=block",
			},
			contains: []string{"add_header X-Frame-Options SAMEORIGIN;", "add_header X-XSS-Protection 1; mode=block;"},
		},
		{
			name: "malformed header without colon is skipped",
			globalData: map[string]string{
				"zimbraReverseProxyResponseHeaders": "malformed-no-colon",
			},
			empty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			result, err := g.resolveAddHeadersDefault(context.Background())
			if err != nil {
				t.Fatalf("resolveAddHeadersDefault failed: %v", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T", result)
			}
			if tt.empty {
				if str != "" {
					t.Errorf("expected empty string, got %q", str)
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(str, want) {
					t.Errorf("expected result to contain %q, got %q", want, str)
				}
			}
		})
	}
}

// TestResolveProxyHTTPCompression tests all branches of resolveProxyHTTPCompression
func TestResolveProxyHTTPCompression(t *testing.T) {
	tests := []struct {
		name        string
		serverData  map[string]string
		expectEmpty bool
		expectGzip  bool
	}{
		{
			name:        "no config defaults to enabled",
			serverData:  map[string]string{},
			expectEmpty: false,
			expectGzip:  true,
		},
		{
			name:        "explicitly enabled returns directives",
			serverData:  map[string]string{"zimbraHttpCompressionEnabled": "TRUE"},
			expectEmpty: false,
			expectGzip:  true,
		},
		{
			name:        "disabled returns empty string",
			serverData:  map[string]string{"zimbraHttpCompressionEnabled": "FALSE"},
			expectEmpty: true,
		},
		{
			name:        "value 1 enables compression",
			serverData:  map[string]string{"zimbraHttpCompressionEnabled": "1"},
			expectEmpty: false,
			expectGzip:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				ServerConfig: &config.ServerConfig{Data: tt.serverData},
			}
			result, err := g.resolveProxyHTTPCompression(context.Background())
			if err != nil {
				t.Fatalf("resolveProxyHTTPCompression failed: %v", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T", result)
			}
			if tt.expectEmpty && str != "" {
				t.Errorf("expected empty string, got non-empty")
			}
			if tt.expectGzip && !strings.Contains(str, "gzip on;") {
				t.Errorf("expected gzip directive in result, got %q", str)
			}
			if tt.expectGzip && !strings.Contains(str, "brotli on;") {
				t.Errorf("expected brotli directive in result, got %q", str)
			}
		})
	}
}

// TestResolveHTTPEnabled tests resolveHTTPEnabled branches
func TestResolveHTTPEnabled(t *testing.T) {
	tests := []struct {
		name       string
		globalData map[string]string
		expected   bool
	}{
		{
			name:       "returns true when not configured",
			globalData: map[string]string{},
			expected:   true,
		},
		{
			name:       "returns false when mail mode is https",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "https"},
			expected:   false,
		},
		{
			name:       "returns false when mail mode is HTTPS (case insensitive)",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "HTTPS"},
			expected:   false,
		},
		{
			name:       "returns true when mail mode is http",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "http"},
			expected:   true,
		},
		{
			name:       "returns true when mail mode is mixed",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "mixed"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			result, err := g.resolveHTTPEnabled(context.Background())
			if err != nil {
				t.Fatalf("resolveHTTPEnabled failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestResolveHTTPSEnabled tests resolveHTTPSEnabled branches
func TestResolveHTTPSEnabled(t *testing.T) {
	tests := []struct {
		name       string
		globalData map[string]string
		expected   bool
	}{
		{
			name:       "returns true when not configured",
			globalData: map[string]string{},
			expected:   true,
		},
		{
			name:       "returns false when mail mode is http",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "http"},
			expected:   false,
		},
		{
			name:       "returns false when mail mode is HTTP (case insensitive)",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "HTTP"},
			expected:   false,
		},
		{
			name:       "returns true when mail mode is https",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "https"},
			expected:   true,
		},
		{
			name:       "returns true when mail mode is mixed",
			globalData: map[string]string{"zimbraReverseProxyMailMode": "mixed"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			result, err := g.resolveHTTPSEnabled(context.Background())
			if err != nil {
				t.Fatalf("resolveHTTPSEnabled failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
