// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - SSL/TLS resolver tests
package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestResolveWebSSLProtocols tests all branches of resolveWebSSLProtocols
func TestResolveWebSSLProtocols(t *testing.T) {
	tests := []struct {
		name       string
		serverData map[string]string
		expected   []string
	}{
		{
			name:       "returns defaults when attribute not set",
			serverData: map[string]string{},
			expected:   []string{"TLSv1.2", "TLSv1.3"},
		},
		{
			name:       "returns configured space-separated protocols",
			serverData: map[string]string{"zimbraReverseProxySSLProtocols": "TLSv1.2 TLSv1.3"},
			expected:   []string{"TLSv1.2", "TLSv1.3"},
		},
		{
			name:       "returns configured comma-separated protocols",
			serverData: map[string]string{"zimbraReverseProxySSLProtocols": "TLSv1.2,TLSv1.3"},
			expected:   []string{"TLSv1.2", "TLSv1.3"},
		},
		{
			name:       "returns single protocol",
			serverData: map[string]string{"zimbraReverseProxySSLProtocols": "TLSv1.3"},
			expected:   []string{"TLSv1.3"},
		},
		{
			name:       "returns default when value is empty after filtering",
			serverData: map[string]string{"zimbraReverseProxySSLProtocols": "   "},
			expected:   []string{"TLSv1.2", "TLSv1.3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				ServerConfig: &config.ServerConfig{Data: tt.serverData},
			}
			result, err := g.resolveWebSSLProtocols(context.Background())
			if err != nil {
				t.Fatalf("resolveWebSSLProtocols failed: %v", err)
			}
			protocols, ok := result.([]string)
			if !ok {
				t.Fatalf("expected []string, got %T", result)
			}
			if len(protocols) != len(tt.expected) {
				t.Fatalf("expected %v protocols, got %v: %v", len(tt.expected), len(protocols), protocols)
			}
			for i, p := range protocols {
				if p != tt.expected[i] {
					t.Errorf("protocols[%d]: expected %q, got %q", i, tt.expected[i], p)
				}
			}
		})
	}
}

// TestResolveClientCertCADefault tests all branches of resolveClientCertCADefault
func TestResolveClientCertCADefault(t *testing.T) {
	t.Run("returns :empty: when file does not exist", func(t *testing.T) {
		g := &Generator{ConfDir: "/tmp/nonexistent-dir-xyz"}
		result, err := g.resolveClientCertCADefault(context.Background())
		if err != nil {
			t.Fatalf("resolveClientCertCADefault failed: %v", err)
		}
		if result != ":empty:" {
			t.Errorf("expected :empty:, got %v", result)
		}
	})

	t.Run("returns path when file exists", func(t *testing.T) {
		dir := t.TempDir()
		caPath := filepath.Join(dir, "nginx.client.ca.crt")
		if err := os.WriteFile(caPath, []byte("fake-cert"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		g := &Generator{ConfDir: dir}
		result, err := g.resolveClientCertCADefault(context.Background())
		if err != nil {
			t.Fatalf("resolveClientCertCADefault failed: %v", err)
		}
		if result != caPath {
			t.Errorf("expected %q, got %v", caPath, result)
		}
	})
}

// TestResolveDHParamEnabled tests all branches of resolveDHParamEnabled
func TestResolveDHParamEnabled(t *testing.T) {
	t.Run("returns empty string when file does not exist", func(t *testing.T) {
		g := &Generator{ConfDir: "/tmp/nonexistent-dir-xyz"}
		result, err := g.resolveDHParamEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveDHParamEnabled failed: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %v", result)
		}
	})

	t.Run("returns ssl_dhparam keyword when file exists", func(t *testing.T) {
		dir := t.TempDir()
		dhPath := filepath.Join(dir, "dhparam.pem")
		if err := os.WriteFile(dhPath, []byte("fake-dhparam"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		g := &Generator{ConfDir: dir}
		result, err := g.resolveDHParamEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveDHParamEnabled failed: %v", err)
		}
		if result != "ssl_dhparam" {
			t.Errorf("expected ssl_dhparam, got %v", result)
		}
	})
}

// TestResolveSSLSessionCacheSize tests resolveSSLSessionCacheSize branches
func TestResolveSSLSessionCacheSize(t *testing.T) {
	t.Run("returns default 10m when not configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveSSLSessionCacheSize(context.Background())
		if err != nil {
			t.Fatalf("resolveSSLSessionCacheSize failed: %v", err)
		}
		if result != "shared:SSL:10m" {
			t.Errorf("expected shared:SSL:10m, got %v", result)
		}
	})

	t.Run("returns configured size", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxySSLSessionCacheSize": "20m",
			}},
		}
		result, err := g.resolveSSLSessionCacheSize(context.Background())
		if err != nil {
			t.Fatalf("resolveSSLSessionCacheSize failed: %v", err)
		}
		if result != "shared:SSL:20m" {
			t.Errorf("expected shared:SSL:20m, got %v", result)
		}
	})
}

// TestResolveWebSSLDhparamEnabled tests resolveWebSSLDhparamEnabled branches
func TestResolveWebSSLDhparamEnabled(t *testing.T) {
	t.Run("returns true when default dhparam file exists", func(t *testing.T) {
		dir := t.TempDir()
		dhPath := filepath.Join(dir, "dhparam.pem")
		if err := os.WriteFile(dhPath, []byte("fake-dhparam"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: map[string]string{
				"web.ssl.dhparam.file": dhPath,
			}},
		}
		result, err := g.resolveWebSSLDhparamEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveWebSSLDhparamEnabled failed: %v", err)
		}
		if result != true {
			t.Errorf("expected true when file exists, got %v", result)
		}
	})

	t.Run("returns false when file does not exist", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: map[string]string{
				"web.ssl.dhparam.file": "/tmp/nonexistent-dhparam-xyz.pem",
			}},
		}
		result, err := g.resolveWebSSLDhparamEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveWebSSLDhparamEnabled failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false when file missing, got %v", result)
		}
	})

	t.Run("uses default path when LocalConfig not set", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveWebSSLDhparamEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveWebSSLDhparamEnabled failed: %v", err)
		}
		_ = result // Just verifying no panic; /opt/zextras/conf/dhparam.pem likely doesn't exist
	})
}

// TestResolveSSLClientCertCAEnabled tests resolveSSLClientCertCAEnabled branches
func TestResolveSSLClientCertCAEnabled(t *testing.T) {
	t.Run("returns true when file exists and has content", func(t *testing.T) {
		dir := t.TempDir()
		caPath := filepath.Join(dir, "nginx.client.ca.crt")
		if err := os.WriteFile(caPath, []byte("certificate-content"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		g := &Generator{ConfDir: dir}
		result, err := g.resolveSSLClientCertCAEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveSSLClientCertCAEnabled failed: %v", err)
		}
		if result != true {
			t.Errorf("expected true when file has content, got %v", result)
		}
	})

	t.Run("returns false when file exists but is empty", func(t *testing.T) {
		dir := t.TempDir()
		caPath := filepath.Join(dir, "nginx.client.ca.crt")
		if err := os.WriteFile(caPath, []byte(""), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		g := &Generator{ConfDir: dir}
		result, err := g.resolveSSLClientCertCAEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveSSLClientCertCAEnabled failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false when file is empty, got %v", result)
		}
	})

	t.Run("returns false when file does not exist", func(t *testing.T) {
		g := &Generator{ConfDir: "/tmp/nonexistent-dir-xyz"}
		result, err := g.resolveSSLClientCertCAEnabled(context.Background())
		if err != nil {
			t.Fatalf("resolveSSLClientCertCAEnabled failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false when file missing, got %v", result)
		}
	})
}
