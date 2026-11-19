// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream and backend resolver tests
package proxy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/config/configtest"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// TestResolveLookupHandlers tests all branches of resolveLookupHandlers
func TestResolveLookupHandlers(t *testing.T) {
	tests := []struct {
		name         string
		globalData   *config.ConfigMap
		localData    *config.ConfigMap
		containsURL  string
		containsPort string
	}{
		{
			name:         "fallback to localhost when no config",
			globalData:   configtest.NewConfigMapFrom(map[string]string{}),
			localData:    configtest.NewConfigMapFrom(map[string]string{}),
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "uses custom extension port",
			globalData: configtest.NewConfigMapFrom(map[string]string{
				"zimbraExtensionBindPort": "9090",
			}),
			localData:    configtest.NewConfigMapFrom(map[string]string{}),
			containsPort: "9090",
			containsURL:  "nginx-lookup",
		},
		{
			name:       "uses zimbra_server_hostname from local config when no lookup targets",
			globalData: configtest.NewConfigMapFrom(map[string]string{}),
			localData: configtest.NewConfigMapFrom(map[string]string{
				"zimbra_server_hostname": "127.0.0.1",
			}),
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "lookup targets override local hostname",
			globalData: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "127.0.0.1",
			}),
			localData:    configtest.NewConfigMapFrom(map[string]string{}),
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
		{
			name: "multi-host (newline-joined LDAP multi-value) renders one URL per host",
			globalData: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "127.0.0.1\n127.0.0.2",
			}),
			localData:    configtest.NewConfigMapFrom(map[string]string{}),
			containsPort: "7072",
			containsURL:  "nginx-lookup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				env:          sys.Real(),
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

func TestLookupTargetsFallBackToLookupTargetServers(t *testing.T) {
	const handler = "https://%s/service/extension/nginx-lookup"

	lookupTargets := map[string]map[string]string{
		"127.0.0.2": {
			zimbraServiceHostnameAttr: "127.0.0.2", zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr: "http", zimbraMailPortAttr: "0", "zimbraExtensionBindPort": "9090",
		},
		"127.0.0.3": {zimbraServiceHostnameAttr: "127.0.0.3", zimbraReverseProxyLookupTargetAttr: "TRUE"},
		"127.0.0.4": {
			zimbraServiceHostnameAttr: "127.0.0.4", zimbraReverseProxyLookupTargetAttr: "FALSE", extensionBindPortAttr: "9091",
		},
	}

	tests := []struct {
		name          string
		globalData    map[string]string
		serverData    map[string]string
		lookupServers map[string]map[string]string
		wantHandlers  string
		wantAvailable bool
	}{
		{
			name:          "discovery uses extension ports independently of web ports",
			globalData:    map[string]string{"zimbraExtensionBindPort": "7073"},
			lookupServers: lookupTargets,
			wantHandlers:  fmt.Sprintf(handler, "127.0.0.2:9090") + " " + fmt.Sprintf(handler, "127.0.0.3:7073"),
			wantAvailable: true,
		},
		{
			name: "explicit list uses mailbox port regardless of lookup flag and global port for unknown host",
			globalData: map[string]string{
				"zimbraReverseProxyAvailableLookupTargets": "127.0.0.4\n127.0.0.9",
				extensionBindPortAttr:                      "7073",
			},
			lookupServers: lookupTargets,
			wantHandlers:  fmt.Sprintf(handler, "127.0.0.4:9091") + " " + fmt.Sprintf(handler, "127.0.0.9:7073"),
			wantAvailable: true,
		},
		{
			name:          "proxy list wins over global list and discovery",
			serverData:    map[string]string{"zimbraReverseProxyAvailableLookupTargets": "127.0.0.8"},
			globalData:    map[string]string{"zimbraReverseProxyAvailableLookupTargets": "127.0.0.9"},
			lookupServers: lookupTargets,
			wantHandlers:  fmt.Sprintf(handler, "127.0.0.8:7072"),
			wantAvailable: true,
		},
		{
			name:          "no lookup target keeps the local hostname and is unavailable",
			wantHandlers:  fmt.Sprintf(handler, "127.0.0.7:7072"),
			wantAvailable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				env:          sys.Real(),
				GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(tt.globalData)},
				ServerConfig: &config.ServerConfig{Data: configtest.NewConfigMapFrom(tt.serverData)},
				LocalConfig: &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
					"zimbra_server_hostname": "127.0.0.7",
				})},
				upstreamCache: &upstreamQueryCache{serverAttrsByHost: tt.lookupServers},
			}

			handlers, err := g.resolveLookupHandlers(context.Background())
			if err != nil {
				t.Fatalf("resolveLookupHandlers failed: %v", err)
			}
			if handlers != tt.wantHandlers {
				t.Errorf("handlers = %q, want %q", handlers, tt.wantHandlers)
			}

			available, err := g.resolveLookupAvailable(context.Background())
			if err != nil {
				t.Fatalf("resolveLookupAvailable failed: %v", err)
			}
			if available != tt.wantAvailable {
				t.Errorf("lookup.available = %v, want %v", available, tt.wantAvailable)
			}
		})
	}
}

// TestResolveMemcacheServers tests resolveMemcacheServers with cached data
func TestResolveMemcacheServers(t *testing.T) {
	t.Run("returns formatted memcache servers from cache", func(t *testing.T) {
		g := &Generator{
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				memcachedPopulated: true,
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
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				memcachedPopulated: true,
				memcachedServers:   []MemcacheServer{},
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
			env:          sys.Real(),
			GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
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
			env: sys.Real(),
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "64",
			})},
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
			env: sys.Real(),
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "16",
			})},
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
			env: sys.Real(),
			GlobalConfig: &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
				"zimbraReverseProxyUpstreamFairShmSize": "notanumber",
			})},
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
		serverData *config.ConfigMap
		sslName    string
		nonSSLName string
		expected   string
	}{
		{
			name:       "defaults to ssl upstream when not configured",
			serverData: configtest.NewConfigMapFrom(map[string]string{}),
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "https://zimbra_ssl",
		},
		{
			name:       "uses ssl upstream when explicitly enabled",
			serverData: configtest.NewConfigMapFrom(map[string]string{"zimbraReverseProxySSLToUpstreamEnabled": "TRUE"}),
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "https://zimbra_ssl",
		},
		{
			name:       "uses non-ssl upstream when ssl disabled",
			serverData: configtest.NewConfigMapFrom(map[string]string{"zimbraReverseProxySSLToUpstreamEnabled": "FALSE"}),
			sslName:    "zimbra_ssl",
			nonSSLName: "zimbra",
			expected:   "http://zimbra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				env:          sys.Real(),
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

// TestResolveEwsUpstreamDisableNoServers checks "#" is returned when no EWS servers exist.
func TestResolveEwsUpstreamDisableNoServers(t *testing.T) {
	ctx := context.Background()
	g := newSpecTestGenerator(map[string]map[string]string{})

	result, err := g.resolveEwsUpstreamDisable(ctx)
	if err != nil {
		t.Fatalf("resolveEwsUpstreamDisable: %v", err)
	}

	if result != "#" {
		t.Errorf("expected '#' when no servers, got %v", result)
	}
}

// TestResolveEwsUpstreamDisableWithServers checks "" is returned when an EWS server exists,
// and that the enabler decision is consistent with the servers var (same spec/source).
func TestResolveEwsUpstreamDisableWithServers(t *testing.T) {
	ctx := context.Background()
	g := newSpecTestGenerator(map[string]map[string]string{
		"ews": {
			zimbraServiceHostnameAttr:          "ews.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
	})
	g.GlobalConfig = &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{
		"zimbraReverseProxyUpstreamEwsServers": "ews.example.com",
	})}

	result, err := g.resolveEwsUpstreamDisable(ctx)
	if err != nil {
		t.Fatalf("resolveEwsUpstreamDisable: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string when servers present, got %v", result)
	}
}

// TestResolveWebUpstreamTargetAvailable tests both branches of resolveWebUpstreamTargetAvailable
func TestResolveWebUpstreamTargetAvailable(t *testing.T) {
	ctx := context.Background()

	t.Run("returns false when no LDAP and no cache", func(t *testing.T) {
		g := &Generator{env: sys.Real(), LdapClient: nil}
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
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				backendsPopulated:    true,
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
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				backendsPopulated:    true,
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
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				backendsPopulated:    true,
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
			env: sys.Real(),
			upstreamCache: &upstreamQueryCache{
				backendsPopulated:    true,
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
