// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpstreamCacheInitialization verifies upstream cache is initialized
func TestUpstreamCacheInitialization(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  "/tmp/test",
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator failed: %v", err)
	}

	if gen.upstreamCache == nil {
		t.Fatal("Expected upstream cache to be initialized, got nil")
	}

	if gen.upstreamCache.populated {
		t.Error("Expected cache to be unpopulated initially")
	}
}

// TestGetAllReverseProxyBackendsCaching verifies caching behavior
func TestGetAllReverseProxyBackendsCaching(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "zmprov")
	callCountPath := filepath.Join(tmpDir, "call_count.txt")

	// Create a mock zmprov that counts calls
	script := fmt.Sprintf(`#!/bin/bash
# Increment call counter
if [ -f %s ]; then
    count=$(cat %s)
    count=$((count + 1))
else
    count=1
fi
echo $count > %s

# Output minimal server data
cat <<EOF
# name mail.example.com
zimbraServiceHostname: mail.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080
zimbraMailSSLPort: 8443
EOF
exit 0
`, callCountPath, callCountPath, callCountPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	// Override zmprov path for testing
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	// Symlink our mock to zmprov name
	zmprovPath := filepath.Join(tmpDir, "zmprov")
	if err := os.Rename(scriptPath, zmprovPath); err != nil {
		t.Fatalf("Failed to rename script: %v", err)
	}

	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "proxy.example.com",
	}

	gen := &Generator{
		Config:        cfg,
		upstreamCache: &upstreamQueryCache{},
		LocalConfig:   &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:  &config.GlobalConfig{Data: make(map[string]string)},
		ServerConfig: &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		},
	}

	// First call - should hit zmprov
	servers1, err := gen.getAllReverseProxyBackends(context.Background())
	if err != nil {
		// Skip test if zmprov is not available or LDAP not initialized
		if strings.Contains(err.Error(), "no such file or directory") ||
			strings.Contains(err.Error(), "native LDAP client not initialized") {
			t.Skip("Skipping test - requires zmprov command or LDAP")
		}
		t.Fatalf("First getAllReverseProxyBackends failed: %v", err)
	}

	if len(servers1) == 0 {
		t.Fatal("Expected servers from first call, got none")
	}

	// Check call count
	callData1, _ := os.ReadFile(callCountPath)
	callCount1 := strings.TrimSpace(string(callData1))
	if callCount1 != "1" {
		t.Errorf("Expected 1 call after first query, got %s", callCount1)
	}

	// Second call - should use cache
	servers2, err := gen.getAllReverseProxyBackends(context.Background())
	if err != nil {
		t.Fatalf("Second getAllReverseProxyBackends failed: %v", err)
	}

	if len(servers2) != len(servers1) {
		t.Errorf("Expected same number of servers from cache, got %d vs %d", len(servers2), len(servers1))
	}

	// Call count should still be 1 (cache hit)
	callData2, _ := os.ReadFile(callCountPath)
	callCount2 := strings.TrimSpace(string(callData2))
	if callCount2 != "1" {
		t.Errorf("Expected still 1 call after second query (cache hit), got %s", callCount2)
	}

	// Third call - should still use cache
	servers3, err := gen.getAllReverseProxyBackends(context.Background())
	if err != nil {
		t.Fatalf("Third getAllReverseProxyBackends failed: %v", err)
	}

	if len(servers3) != len(servers1) {
		t.Errorf("Expected same number of servers from cache, got %d vs %d", len(servers3), len(servers1))
	}

	// Call count should still be 1
	callData3, _ := os.ReadFile(callCountPath)
	callCount3 := strings.TrimSpace(string(callData3))
	if callCount3 != "1" {
		t.Errorf("Expected still 1 call after third query (cache hit), got %s", callCount3)
	}
}

// TestGetAllReverseProxyBackendsSSLCaching verifies SSL backend caching
func TestGetAllReverseProxyBackendsSSLCaching(t *testing.T) {
	tmpDir := t.TempDir()
	callCountPath := filepath.Join(tmpDir, "ssl_call_count.txt")
	scriptPath := filepath.Join(tmpDir, "zmprov")

	// Create a mock zmprov that counts calls
	script := fmt.Sprintf(`#!/bin/bash
# Increment call counter
if [ -f %s ]; then
    count=$(cat %s)
    count=$((count + 1))
else
    count=1
fi
echo $count > %s

# Output minimal server data
cat <<EOF
# name mail.example.com
zimbraServiceHostname: mail.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: https
zimbraMailPort: 8080
zimbraMailSSLPort: 8443
EOF
exit 0
`, callCountPath, callCountPath, callCountPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	// Override PATH
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "proxy.example.com",
	}

	gen := &Generator{
		Config:        cfg,
		upstreamCache: &upstreamQueryCache{},
		LocalConfig:   &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:  &config.GlobalConfig{Data: make(map[string]string)},
		ServerConfig: &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		},
	}

	// First call
	_, err := gen.getAllReverseProxyBackendsSSL(context.Background())
	if err != nil {
		// Skip test if zmprov is not available or LDAP not initialized
		if strings.Contains(err.Error(), "no such file or directory") ||
			strings.Contains(err.Error(), "native LDAP client not initialized") {
			t.Skip("Skipping test - requires zmprov command or LDAP")
		}
		t.Fatalf("First getAllReverseProxyBackendsSSL failed: %v", err)
	}

	// Second call - should use cache
	_, err = gen.getAllReverseProxyBackendsSSL(context.Background())
	if err != nil {
		t.Fatalf("Second getAllReverseProxyBackendsSSL failed: %v", err)
	}

	// Verify only one call was made
	callData, _ := os.ReadFile(callCountPath)
	callCount := strings.TrimSpace(string(callData))
	if callCount != "1" {
		t.Errorf("Expected 1 call total (cache hit on second), got %s", callCount)
	}
}

// TestClearUpstreamCache verifies cache invalidation
func TestClearUpstreamCache(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  "/tmp/test",
		Hostname: "test.example.com",
	}

	gen := &Generator{
		Config:        cfg,
		upstreamCache: &upstreamQueryCache{},
		LocalConfig:   &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:  &config.GlobalConfig{Data: make(map[string]string)},
		ServerConfig: &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		},
	}

	// Populate cache
	gen.upstreamCache.reverseProxyBackends = []UpstreamServer{{Host: "test1", Port: 8080}}
	gen.upstreamCache.reverseProxyBackendsSSL = []UpstreamServer{{Host: "test2", Port: 8443}}
	gen.upstreamCache.memcachedServers = []MemcacheServer{{Hostname: "mc1", Port: 11211}}
	gen.upstreamCache.populated = true

	// Verify populated
	if !gen.upstreamCache.populated {
		t.Fatal("Cache should be populated before clear")
	}

	// Clear cache
	gen.ClearUpstreamCache(context.Background())

	// Verify cleared
	if gen.upstreamCache.populated {
		t.Error("Cache should be unpopulated after clear")
	}

	if gen.upstreamCache.reverseProxyBackends != nil {
		t.Error("reverseProxyBackends should be nil after clear")
	}

	if gen.upstreamCache.reverseProxyBackendsSSL != nil {
		t.Error("reverseProxyBackendsSSL should be nil after clear")
	}

	if gen.upstreamCache.memcachedServers != nil {
		t.Error("memcachedServers should be nil after clear")
	}
}

// TestReloadConfigurationInvalidatesCache verifies reload clears cache
func TestReloadConfigurationInvalidatesCache(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  "/tmp/test",
		Hostname: "test.example.com",
	}

	gen := &Generator{
		Config:        cfg,
		upstreamCache: &upstreamQueryCache{},
		LocalConfig:   &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:  &config.GlobalConfig{Data: make(map[string]string)},
		ServerConfig: &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		},
		Variables: make(map[string]*Variable),
	}

	// Populate cache
	gen.upstreamCache.reverseProxyBackends = []UpstreamServer{{Host: "test1", Port: 8080}}
	gen.upstreamCache.populated = true

	// Reload configuration
	newLocal := &config.LocalConfig{Data: map[string]string{"key": "value"}}
	newGlobal := &config.GlobalConfig{Data: map[string]string{"key": "value"}}
	newServer := &config.ServerConfig{
		Data:          map[string]string{"key": "value"},
		ServiceConfig: make(map[string]string),
	}

	err := gen.ReloadConfiguration(context.Background(), newLocal, newGlobal, newServer)
	if err != nil {
		// Variables may not resolve in test env, but cache should still be cleared
		t.Logf("ReloadConfiguration error (expected in test): %v", err)
	}

	// Verify cache was cleared
	if gen.upstreamCache.populated {
		t.Error("Cache should be cleared after configuration reload")
	}

	if gen.upstreamCache.reverseProxyBackends != nil {
		t.Error("reverseProxyBackends should be nil after reload")
	}
}

// TestMemcachedServersCaching verifies memcached server caching
func TestMemcachedServersCaching(t *testing.T) {
	tmpDir := t.TempDir()
	callCountPath := filepath.Join(tmpDir, "mc_call_count.txt")
	scriptPath := filepath.Join(tmpDir, "zmprov")

	// Create a mock zmprov that counts calls
	script := fmt.Sprintf(`#!/bin/bash
# Increment call counter
if [ -f %s ]; then
    count=$(cat %s)
    count=$((count + 1))
else
    count=1
fi
echo $count > %s

# Output minimal server data with memcached
cat <<EOF
# name mc.example.com
zimbraServiceHostname: mc.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 11211
EOF
exit 0
`, callCountPath, callCountPath, callCountPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	// Override PATH
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+oldPath)
	defer os.Setenv("PATH", oldPath)

	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "proxy.example.com",
	}

	gen := &Generator{
		Config:        cfg,
		upstreamCache: &upstreamQueryCache{},
		LocalConfig:   &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:  &config.GlobalConfig{Data: make(map[string]string)},
		ServerConfig: &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		},
	}

	// First call
	servers1, err := gen.getAllMemcachedServers(context.Background())
	if err != nil {
		// Skip test if zmprov is not available or LDAP not initialized
		if strings.Contains(err.Error(), "no such file or directory") ||
			strings.Contains(err.Error(), "native LDAP client not initialized") {
			t.Skip("Skipping test - requires zmprov command or LDAP")
		}
		t.Fatalf("First getAllMemcachedServers failed: %v", err)
	}

	if len(servers1) == 0 {
		t.Fatal("Expected memcached servers from first call, got none")
	}

	// Second call - should use cache
	servers2, err := gen.getAllMemcachedServers(context.Background())
	if err != nil {
		t.Fatalf("Second getAllMemcachedServers failed: %v", err)
	}

	if len(servers2) != len(servers1) {
		t.Errorf("Expected same number of servers from cache, got %d vs %d", len(servers2), len(servers1))
	}

	// Verify only one call was made
	callData, _ := os.ReadFile(callCountPath)
	callCount := strings.TrimSpace(string(callData))
	if callCount != "1" {
		t.Errorf("Expected 1 call total (cache hit on second), got %s", callCount)
	}
}
