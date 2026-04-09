// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/state"
	"slices"
	"testing"
	"time"
)

func TestInvalidateLDAPCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	// Create a cache instance
	cacheInstance := cache.New(ctx, false)

	// Populate cache with LDAP test data
	cacheInstance.GetCachedConfig(ctx, "ldap:domains", func() (any, error) {
		return []string{"example.com", "test.com"}, nil
	})
	cacheInstance.GetCachedConfig(ctx, "ldap:servers:mailbox", func() (any, error) {
		return []string{"server1", "server2"}, nil
	})
	cacheInstance.GetCachedConfig(ctx, "non-ldap:data", func() (any, error) {
		return "should remain", nil
	})

	initialKeys := cacheInstance.GetCacheKeys()
	t.Logf("Initial cache keys: %v", initialKeys)

	if len(initialKeys) != 3 {
		t.Fatalf("Expected 3 initial cache entries, got %d", len(initialKeys))
	}

	// Create ConfigManager with cache
	mainCfg := &config.Config{BaseDir: "/tmp", Hostname: "test"}
	appState := state.NewState()
	cm := NewConfigManager(ctx, mainCfg, appState, nil, cacheInstance)

	// Test cache invalidation
	t.Log("Calling InvalidateLDAPCache...")
	cm.InvalidateLDAPCache(ctx)

	// Wait a bit for async operations
	time.Sleep(50 * time.Millisecond)

	// Check LDAP cache is cleared but non-LDAP data remains
	keys := cacheInstance.GetCacheKeys()
	t.Logf("Cache keys after invalidation: %v", keys)

	// Verify LDAP entries are gone
	for _, key := range keys {
		if key == "ldap:domains" || key == "ldap:servers:mailbox" {
			t.Errorf("LDAP cache entry %s still present after invalidation", key)
		}
	}

	// Verify non-LDAP entry remains
	hasNonLDAP := slices.Contains(keys, "non-ldap:data")
	if !hasNonLDAP {
		t.Error("Non-LDAP cache entry was incorrectly cleared")
	}

	t.Log("✓ Cache invalidation successful")
}

func TestInvalidateLDAPCache_NilCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	// Create ConfigManager without cache
	mainCfg := &config.Config{BaseDir: "/tmp", Hostname: "test"}
	appState := state.NewState()
	cm := NewConfigManager(ctx, mainCfg, appState, nil, nil)

	// Should not panic with nil cache
	cm.InvalidateLDAPCache(ctx)
	t.Log("✓ InvalidateLDAPCache handles nil cache gracefully")
}
