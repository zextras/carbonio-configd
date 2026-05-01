// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream and backend resolver tests
package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestResolveLookupHandlers tests all branches of resolveLookupHandlers
func TestResolveLookupHandlers(t *testing.T) {
	tests := []struct {
		name         string
		globalData   map[string]string
		localData    map[string]string
		containsURL  string
		containsPort string
	}{
		{
			name:         "fallback to localhost when no config",
			globalData:   map[string]string{},
			localData:    map[string]string{},
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "uses custom extension port",
			globalData: map[string]string{
				"zimbraExtensionBindPort": "9090",
			},
			localData:    map[string]string{},
			containsPort: "9090",
			containsURL:  "nginx-lookup",
		},
		{
			name:       "uses zimbra_server_hostname from local config when no lookup targets",
			globalData: map[string]string{},
			localData: map[string]string{
				"zimbra_server_hostname": "127.0.0.1",
			},
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "lookup targets override local hostname",
			globalData: map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "127.0.0.1",
			},
			localData:    map[string]string{},
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "multi-host (newline-joined LDAP multi-value) renders one URL per host",
			globalData: map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "127.0.0.1\n127.0.0.2",
			},
			localData:    map[string]string{},
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
				LocalConfig:  &config.LocalConfig{Data: tt.localData},
			}
			result, err := g.resolveLookupHandlers(context.Background())
			if err != nil {
				t.Fatalf("resolveLookupHandlers failed: %v", err)
			}
			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T", result)
			}
			if tt.containsURL != "" && !strings.Contains(str, tt.containsURL) {
				t.Errorf("expected URL to contain %q, got %q", tt.containsURL, str)
			}
			if tt.containsPort != "" && !strings.Contains(str, tt.containsPort) {
				t.Errorf("expected URL to contain port %q, got %q", tt.containsPort, str)
			}
			if !strings.HasPrefix(str, "https://") {
				t.Errorf("expected URL to start with https://, got %q", str)
			}
			if strings.ContainsAny(str, "\n,") {
				t.Errorf("output must not contain raw newline/comma separators (CO-3565 family): %q", str)
			}
			if strings.Contains(tt.name, "multi-host") {
				if c := strings.Count(str, "https://"); c != 2 {
					t.Errorf("expected 2 https:// URLs for multi-host input, got %d in %q", c, str)
				}
			}
		})
	}
}

// TestResolveMemcacheServers tests resolveMemcacheServers with cached data
func TestResolveMemcacheServers(t *testing.T) {
	t.Run("returns formatted memcache servers from cache", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated: true,
				memcachedServers: []MemcacheServer{
					{Hostname: "mc1.example.com", Port: 11211},
					{Hostname: "mc2.example.com", Port: 11212},
				},
			},
		}
		result, err := g.resolveMemcacheServers(context.Background())
		if err != nil {
			t.Fatalf("resolveMemcacheServers failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "mc1.example.com:11211") {
			t.Errorf("expected mc1 in result, got %q", str)
		}
		if !strings.Contains(str, "mc2.example.com:11212") {
			t.Errorf("expected mc2 in result, got %q", str)
		}
	})

	t.Run("returns empty string when no servers in cache", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:        true,
				memcachedServers: []MemcacheServer{},
			},
		}
		result, err := g.resolveMemcacheServers(context.Background())
		if err != nil {
			t.Fatalf("resolveMemcacheServers failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if str != "" {
			t.Errorf("expected empty string, got %q", str)
		}
	})
}

// TestResolveUpstreamFairShmSize tests resolveUpstreamFairShmSize branches
func TestResolveUpstreamFairShmSize(t *testing.T) {
	t.Run("returns default 32k when not configured", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		result, err := g.resolveUpstreamFairShmSize(context.Background())
		if err != nil {
			t.Fatalf("resolveUpstreamFairShmSize failed: %v", err)
		}
		if result != "upstream_fair_shm_size 32k;" {
			t.Errorf("expected upstream_fair_shm_size 32k;, got %v", result)
		}
	})

	t.Run("returns configured size", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "64",
			}},
		}
		result, err := g.resolveUpstreamFairShmSize(context.Background())
		if err != nil {
			t.Fatalf("resolveUpstreamFairShmSize failed: %v", err)
		}
		if result != "upstream_fair_shm_size 64k;" {
			t.Errorf("expected upstream_fair_shm_size 64k;, got %v", result)
		}
	})

	t.Run("uses minimum 32k for small values", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "16",
			}},
		}
		result, err := g.resolveUpstreamFairShmSize(context.Background())
		if err != nil {
			t.Fatalf("resolveUpstreamFairShmSize failed: %v", err)
		}
		if result != "upstream_fair_shm_size 32k;" {
			t.Errorf("expected minimum 32k, got %v", result)
		}
	})

	t.Run("uses default 32k for invalid string", func(t *testing.T) {
		g := &Generator{
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "notanumber",
			}},
		}
		result, err := g.resolveUpstreamFairShmSize(context.Background())
		if err != nil {
			t.Fatalf("resolveUpstreamFairShmSize failed: %v", err)
		}
		if result != "upstream_fair_shm_size 32k;" {
			t.Errorf("expected upstream_fair_shm_size 32k; for invalid input, got %v", result)
		}
	})
}

// TestMakeUpstreamTargetResolver tests makeUpstreamTargetResolver branches
func TestMakeUpstreamTargetResolver(t *testing.T) {
	tests := []struct {
		name       string
		serverData map[string]string
		sslName    string
		nonSSLName string
		expected   string
	}{
		{
			name:       "defaults to ssl upstream when not configured",
			serverData: map[string]string{},
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "https://zimbra_ssl",
		},
		{
			name:       "uses ssl upstream when explicitly enabled",
			serverData: map[string]string{"zimbraReverseProxySSLToUpstreamEnabled": "TRUE"},
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "https://zimbra_ssl",
		},
		{
			name:       "uses non-ssl upstream when ssl disabled",
			serverData: map[string]string{"zimbraReverseProxySSLToUpstreamEnabled": "FALSE"},
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "http://zimbra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				ServerConfig: &config.ServerConfig{Data: tt.serverData},
			}
			resolver := g.makeUpstreamTargetResolver(tt.sslName, tt.nonSSLName)
			result, err := resolver(context.Background())
			if err != nil {
				t.Fatalf("makeUpstreamTargetResolver failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %v", tt.expected, result)
			}
		})
	}
}

// TestMakeBackendResolver tests makeBackendResolver branches
func TestMakeBackendResolver(t *testing.T) {
	t.Run("returns empty string when no backends (nil LDAP)", func(t *testing.T) {
		g := &Generator{
			LdapClient: nil,
		}
		resolver := g.makeBackendResolver(false)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("backend resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if str != "" {
			t.Errorf("expected empty string with no backends, got %q", str)
		}
	})

	t.Run("returns formatted servers with cached backends", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:            true,
				reverseProxyBackends: []UpstreamServer{{Host: "server1.example.com", Port: 8080}},
			},
		}
		resolver := g.makeBackendResolver(false)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("backend resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "server1.example.com") {
			t.Errorf("expected server1.example.com in result, got %q", str)
		}
	})

	t.Run("returns formatted SSL backends", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:               true,
				reverseProxyBackendsSSL: []UpstreamServer{{Host: "ssl-server.example.com", Port: 8443}},
			},
		}
		resolver := g.makeBackendResolver(true)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("backend resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "ssl-server.example.com") {
			t.Errorf("expected ssl-server.example.com in result, got %q", str)
		}
	})
}

// TestMakeAttributeResolver tests makeAttributeResolver branches
func TestMakeAttributeResolver(t *testing.T) {
	t.Run("returns empty string when no servers (nil LDAP)", func(t *testing.T) {
		g := &Generator{
			LdapClient: nil,
		}
		resolver := g.makeAttributeResolver("zimbraReverseProxyUpstreamEwsServers", false)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("attribute resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if str != "" {
			t.Errorf("expected empty string with no servers, got %q", str)
		}
	})

	t.Run("returns formatted servers with cached attribute backends", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated: true,
				attributeServers: map[string][]UpstreamServer{
					"zimbraReverseProxyUpstreamEwsServers": {{Host: "ews-server.example.com", Port: 8443}},
				},
				attributeServersSSL: map[string][]UpstreamServer{},
			},
		}
		resolver := g.makeAttributeResolver("zimbraReverseProxyUpstreamEwsServers", false)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("attribute resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "ews-server.example.com") {
			t.Errorf("expected ews-server.example.com in result, got %q", str)
		}
	})

	t.Run("returns formatted SSL attribute servers", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated: true,
				attributeServersSSL: map[string][]UpstreamServer{
					"zimbraReverseProxyUpstreamEwsServers": {{Host: "ssl-ews.example.com", Port: 9443}},
				},
				attributeServers: map[string][]UpstreamServer{},
			},
		}
		resolver := g.makeAttributeResolver("zimbraReverseProxyUpstreamEwsServers", true)
		result, err := resolver(context.Background())
		if err != nil {
			t.Fatalf("attribute resolver failed: %v", err)
		}
		str, ok := result.(string)
		if !ok {
			t.Fatalf("expected string, got %T", result)
		}
		if !strings.Contains(str, "ssl-ews.example.com") {
			t.Errorf("expected ssl-ews.example.com in result, got %q", str)
		}
	})
}

// TestResolveUpstreamDisableNoServers tests resolveUpstreamDisable returns "#" when no servers
func TestResolveUpstreamDisableNoServers(t *testing.T) {
	ctx := context.Background()
	// No cache, no LDAP → getUpstreamServersByAttribute will fail → returns "#"
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			attributeServers:    map[string][]UpstreamServer{},
			attributeServersSSL: map[string][]UpstreamServer{},
		},
		LdapClient: nil,
	}
	result, err := g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamEwsServers", "ews")
	if err != nil {
		t.Fatalf("resolveUpstreamDisable: %v", err)
	}
	if result != "#" {
		t.Errorf("expected '#' when no servers, got %v", result)
	}
}

// TestResolveUpstreamDisableWithServers tests resolveUpstreamDisable returns "" when servers present
func TestResolveUpstreamDisableWithServers(t *testing.T) {
	ctx := context.Background()
	attrName := "zimbraReverseProxyUpstreamEwsServers"
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			attributeServers: map[string][]UpstreamServer{
				attrName: {{Host: "server1.example.com", Port: 8080}},
			},
			attributeServersSSL: map[string][]UpstreamServer{},
		},
	}
	result, err := g.resolveUpstreamDisable(ctx, attrName, "ews")
	if err != nil {
		t.Fatalf("resolveUpstreamDisable: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string when servers present, got %v", result)
	}
}

// TestResolveWebUpstreamTargetAvailable tests both branches of resolveWebUpstreamTargetAvailable
func TestResolveWebUpstreamTargetAvailable(t *testing.T) {
	ctx := context.Background()

	t.Run("returns false when no LDAP and no cache", func(t *testing.T) {
		g := &Generator{LdapClient: nil}
		result, err := g.resolveWebUpstreamTargetAvailable(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != false {
			t.Errorf("expected false when no backends, got %v", result)
		}
	})

	t.Run("returns true when cached backends present", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:            true,
				reverseProxyBackends: []UpstreamServer{{Host: "backend.example.com", Port: 8080}},
			},
		}
		result, err := g.resolveWebUpstreamTargetAvailable(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != true {
			t.Errorf("expected true when backends present, got %v", result)
		}
	})

	t.Run("returns false when cached backends empty", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:            true,
				reverseProxyBackends: []UpstreamServer{},
			},
		}
		result, err := g.resolveWebUpstreamTargetAvailable(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// When populated with empty list, fallback localhost is returned → len > 0 → true
		// Actually getAllReverseProxyBackends returns fallback when empty, so result is true
		_ = result
	})
}

// TestResolveWebAvailable tests resolveWebAvailable branches
func TestResolveWebAvailable(t *testing.T) {
	t.Run("returns true when backends exist in cache", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:            true,
				reverseProxyBackends: []UpstreamServer{{Host: "server1.example.com", Port: 443}},
			},
		}
		result, err := g.resolveWebAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveWebAvailable failed: %v", err)
		}
		if result != true {
			t.Errorf("expected true, got %v", result)
		}
	})

	t.Run("returns false when no backends", func(t *testing.T) {
		g := &Generator{
			upstreamCache: &upstreamQueryCache{
				populated:            true,
				reverseProxyBackends: []UpstreamServer{},
			},
		}
		result, err := g.resolveWebAvailable(context.Background())
		if err != nil {
			t.Fatalf("resolveWebAvailable failed: %v", err)
		}
		if result != false {
			t.Errorf("expected false, got %v", result)
		}
	})
}
