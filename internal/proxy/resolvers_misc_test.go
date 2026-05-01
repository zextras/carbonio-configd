// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - miscellaneous resolver tests
package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestResolveSaslHostFromIP tests resolveSaslHostFromIP branches
func TestResolveSaslHostFromIP(t *testing.T) {
	t.Run("returns off when not configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveSaslHostFromIP(context.Background())
		if err != nil {
			t.Fatalf("resolveSaslHostFromIP failed: %v", err)
		}
		if result != "off" {
			t.Errorf("expected off, got %v", result)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxySaslHostFromIP": "on",
			}},
		}
		result, err := g.resolveSaslHostFromIP(context.Background())
		if err != nil {
			t.Fatalf("resolveSaslHostFromIP failed: %v", err)
		}
		if result != "on" {
			t.Errorf("expected on, got %v", result)
		}
	})
}

// TestResolveLookupTargetAvailable tests resolveLookupTargetAvailable branches
func TestResolveLookupTargetAvailable(t *testing.T) {
	t.Run("returns false when attribute not set", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveLookupTargetAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveLookupTargetAvailable failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false, got %v", result)
		}
	})

	t.Run("returns true when attribute is set", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyLookupTarget": "TRUE",
			}},
		}
		result, err := g.resolveLookupTargetAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveLookupTargetAvailable failed: %v", err)
		}
		if result != true {
			t.Errorf("expected true, got %v", result)
		}
	})
}

// TestResolveLookupAvailable tests resolveLookupAvailable branches
func TestResolveLookupAvailable(t *testing.T) {
	t.Run("returns true when lookup targets configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "host1 host2",
			}},
		}
		result, err := g.resolveLookupAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveLookupAvailable failed: %v", err)
		}
		if result != true {
			t.Errorf("expected true, got %v", result)
		}
	})

	t.Run("returns false when attribute not set", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveLookupAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveLookupAvailable failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false, got %v", result)
		}
	})
}

// TestMakeTimeoutResolver tests makeTimeoutResolver with config and defaults
func TestMakeTimeoutResolver(t *testing.T) {
	tests := []struct {
		name        string
		localData   map[string]string
		configKey   string
		defaultBase int
		offset      int
		expected    int
	}{
		{
			name:        "uses default base when key not in config",
			localData:   map[string]string{},
			configKey:   "zimbra_proxy_timeout",
			defaultBase: 60,
			offset:      10,
			expected:    70,
		},
		{
			name:        "uses configured value plus offset",
			localData:   map[string]string{"zimbra_proxy_timeout": "120"},
			configKey:   "zimbra_proxy_timeout",
			defaultBase: 60,
			offset:      10,
			expected:    130,
		},
		{
			name:        "uses default when config value is invalid",
			localData:   map[string]string{"zimbra_proxy_timeout": "notanumber"},
			configKey:   "zimbra_proxy_timeout",
			defaultBase: 60,
			offset:      10,
			expected:    70,
		},
		{
			name:        "zero offset",
			localData:   map[string]string{"zimbra_proxy_timeout": "30"},
			configKey:   "zimbra_proxy_timeout",
			defaultBase: 60,
			offset:      0,
			expected:    30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				LocalConfig: &config.LocalConfig{Data: tt.localData},
			}
			resolver := g.makeTimeoutResolver(tt.configKey, tt.defaultBase, tt.offset)
			result, err := resolver(context.Background())
			if err != nil {
				t.Fatalf("makeTimeoutResolver failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestResolveHostToIP_DNSFailure tests resolveHostToIP with DNS failure
func TestResolveHostToIP_DNSFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := resolveHostToIP(ctx, "nonexistent.invalid.example.xyz")
	if result != "nonexistent.invalid.example.xyz" {
		t.Errorf("expected hostname returned as-is on DNS failure, got %q", result)
	}
}

// TestResolveHostToIP_Localhost tests resolveHostToIP with localhost
func TestResolveHostToIP_Localhost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := resolveHostToIP(ctx, "127.0.0.1")
	if result == "" {
		t.Errorf("expected non-empty result for 127.0.0.1, got empty string")
	}
}
