// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestNew verifies cache initialization
func TestNew(t *testing.T) {
	t.Run("cache_enabled", func(t *testing.T) {
		ctx := context.Background()
		cache := New(ctx, false)
		defer cache.Stop()

		if cache == nil {
			t.Fatal("Expected cache to be created")
		}

		if cache.memoryCache == nil {
			t.Fatal("Expected memoryCache to be initialized")
		}

		if cache.skipCache {
			t.Error("Expected skipCache to be false")
		}

		if cache.maxMemoryItems != 20 {
			t.Errorf("Expected maxMemoryItems=20, got %d", cache.maxMemoryItems)
		}
	})

	t.Run("cache_disabled", func(t *testing.T) {
		ctx := context.Background()
		cache := New(ctx, true)
		defer cache.Stop()

		if !cache.skipCache {
			t.Error("Expected skipCache to be true")
		}
	})
}

// TestStop verifies cache cleanup
func TestStop(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)

	cache.Stop()

	// Verify stop channel is closed
	select {
	case <-cache.stop:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected stop channel to be closed")
	}

	// Stop is idempotent
	cache.Stop()
}

// TestSetSkipCache verifies skipCache flag can be changed
func TestSetSkipCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Initially false
	if cache.skipCache {
		t.Error("Expected skipCache to be false initially")
	}

	// Set to true
	cache.SetSkipCache(true)
	if !cache.skipCache {
		t.Error("Expected skipCache to be true after SetSkipCache(true)")
	}

	// Set back to false
	cache.SetSkipCache(false)
	if cache.skipCache {
		t.Error("Expected skipCache to be false after SetSkipCache(false)")
	}
}

// TestGenerateHash verifies hash generation
func TestGenerateHash(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("consistent_hash", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		hash1 := cache.generateHash(data)
		hash2 := cache.generateHash(data)

		if hash1 != hash2 {
			t.Errorf("Expected consistent hashes, got %s and %s", hash1, hash2)
		}

		if hash1 == "" {
			t.Error("Expected non-empty hash")
		}
	})

	t.Run("different_data_different_hash", func(t *testing.T) {
		data1 := map[string]string{"key": "value1"}
		data2 := map[string]string{"key": "value2"}

		hash1 := cache.generateHash(data1)
		hash2 := cache.generateHash(data2)

		if hash1 == hash2 {
			t.Error("Expected different hashes for different data")
		}
	})

	t.Run("unmarshalable_data", func(t *testing.T) {
		// Channels cannot be marshaled to JSON
		data := make(chan int)
		hash := cache.generateHash(data)

		if hash != "" {
			t.Error("Expected empty hash for unmarshalable data")
		}
	})
}

// TestGetFromMemoryCache verifies memory cache retrieval
func TestGetFromMemoryCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("cache_miss", func(t *testing.T) {
		entry, found := cache.getFromMemoryCache("nonexistent")
		if found {
			t.Error("Expected cache miss for nonexistent key")
		}
		if entry != nil {
			t.Error("Expected nil entry for cache miss")
		}
	})

	t.Run("cache_hit_valid", func(t *testing.T) {
		// Manually add entry (hash must be at least 8 chars for logging)
		cache.setMemoryCache(ctx, "testkey", "testdata", "testhash123456")

		entry, found := cache.getFromMemoryCache("testkey")
		if !found {
			t.Error("Expected cache hit")
		}
		if entry == nil {
			t.Fatal("Expected non-nil entry")
		}
		if entry.Data != "testdata" {
			t.Errorf("Expected data='testdata', got %v", entry.Data)
		}
		if entry.Hash != "testhash123456" {
			t.Errorf("Expected hash='testhash123456', got %s", entry.Hash)
		}
	})

	t.Run("cache_hit_expired", func(t *testing.T) {
		// Add entry with past timestamp
		cache.mutex.Lock()
		cache.memoryCache["expired"] = &MemoryCacheEntry{
			Data:      "olddata",
			Timestamp: time.Now().Add(-1 * time.Hour),
			Hash:      "oldhash12345678",
			TTL:       1, // 1 second TTL
		}
		cache.mutex.Unlock()

		entry, found := cache.getFromMemoryCache("expired")
		if found {
			t.Error("Expected cache miss for expired entry")
		}
		if entry != nil {
			t.Error("Expected nil entry for expired cache")
		}
	})
}

// TestSetMemoryCache verifies memory cache storage
func TestSetMemoryCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	cache.setMemoryCache(ctx, "key1", "data1", "hash1234567890")

	cache.mutex.RLock()
	entry, exists := cache.memoryCache["key1"]
	cache.mutex.RUnlock()

	if !exists {
		t.Fatal("Expected entry to be stored in cache")
	}

	if entry.Data != "data1" {
		t.Errorf("Expected data='data1', got %v", entry.Data)
	}

	if entry.Hash != "hash1234567890" {
		t.Errorf("Expected hash='hash1234567890', got %s", entry.Hash)
	}

	if entry.TTL != config.CacheTTL {
		t.Errorf("Expected TTL=%d, got %d", config.CacheTTL, entry.TTL)
	}

	// Verify timestamp is recent
	age := time.Since(entry.Timestamp)
	if age > time.Second {
		t.Errorf("Expected recent timestamp, got age=%v", age)
	}
}

// TestIsCacheValid verifies cache validity checking
func TestIsCacheValid(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("nil_cached_data", func(t *testing.T) {
		if cache.IsCacheValid(nil) {
			t.Error("Expected false for nil cached data")
		}
	})

	t.Run("valid_cached_data", func(t *testing.T) {
		data := &config.CachedData{
			Timestamp: time.Now(),
			TTL:       3600, // 1 hour
		}
		if !cache.IsCacheValid(data) {
			t.Error("Expected true for valid cached data")
		}
	})

	t.Run("expired_cached_data", func(t *testing.T) {
		data := &config.CachedData{
			Timestamp: time.Now().Add(-2 * time.Hour),
			TTL:       3600, // 1 hour
		}
		if cache.IsCacheValid(data) {
			t.Error("Expected false for expired cached data")
		}
	})
}

// TestGetCachedConfig verifies basic cache retrieval
func TestGetCachedConfig(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	callCount := 0
	fetchFunc := func() (any, error) {
		callCount++
		return fmt.Sprintf("data_%d", callCount), nil
	}

	t.Run("cache_miss_then_hit", func(t *testing.T) {
		callCount = 0

		// First call - cache miss
		data1, err := cache.GetCachedConfig(ctx, "key1", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if data1 != "data_1" {
			t.Errorf("Expected data_1, got %v", data1)
		}
		if callCount != 1 {
			t.Errorf("Expected 1 fetch call, got %d", callCount)
		}

		// Second call - cache hit
		data2, err := cache.GetCachedConfig(ctx, "key1", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if data2 != "data_1" {
			t.Errorf("Expected data_1 from cache, got %v", data2)
		}
		if callCount != 1 {
			t.Errorf("Expected no additional fetch calls, got %d total", callCount)
		}
	})

	t.Run("fetch_error", func(t *testing.T) {
		errorFetch := func() (any, error) {
			return nil, errors.New("fetch failed")
		}

		_, err := cache.GetCachedConfig(ctx, "error_key", errorFetch)
		if err == nil {
			t.Error("Expected error from fetch failure")
		}
	})

	t.Run("skip_cache_enabled", func(t *testing.T) {
		cacheSkip := New(ctx, true)
		defer cacheSkip.Stop()

		callCount = 0

		// First call
		data1, err := cacheSkip.GetCachedConfig(ctx, "key2", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("Expected 1 fetch call, got %d", callCount)
		}

		// Second call - should fetch again since cache is skipped
		data2, err := cacheSkip.GetCachedConfig(ctx, "key2", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("Expected 2 fetch calls with skip cache, got %d", callCount)
		}
		if data1 == data2 {
			t.Error("Expected different data on each call with skip cache")
		}
	})
}

// TestGetCachedConfigWithChangeDetection verifies change detection
func TestGetCachedConfigWithChangeDetection(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("first_fetch_is_changed", func(t *testing.T) {
		fetchFunc := func() (any, error) {
			return "initial_data", nil
		}

		data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "key1", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if data != "initial_data" {
			t.Errorf("Expected 'initial_data', got %v", data)
		}
		if !changed {
			t.Error("Expected changed=true for first fetch")
		}
	})

	t.Run("unchanged_data", func(t *testing.T) {
		fetchFunc := func() (any, error) {
			return "same_data", nil
		}

		// First call
		_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, "key2", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Second call with same data
		data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "key2", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if data != "same_data" {
			t.Errorf("Expected 'same_data', got %v", data)
		}
		if changed {
			t.Error("Expected changed=false for unchanged data")
		}
	})

	t.Run("changed_data", func(t *testing.T) {
		dataValue := "original"
		fetchFunc := func() (any, error) {
			return dataValue, nil
		}

		// First call populates the cache.
		_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, "key3", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Force the cached entry to be expired so the next call performs a
		// fresh fetch and compares the new hash with the stored one.
		cache.mutex.Lock()
		if entry, ok := cache.memoryCache["key3"]; ok {
			entry.Timestamp = time.Now().Add(-2 * time.Hour)
			entry.TTL = 1
		}
		cache.mutex.Unlock()

		// Change data and fetch again
		dataValue = "modified"
		data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "key3", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if data != "modified" {
			t.Errorf("Expected 'modified', got %v", data)
		}
		if !changed {
			t.Error("Expected changed=true for modified data")
		}
	})

	t.Run("fetch_error_returns_cached", func(t *testing.T) {
		successFetch := func() (any, error) {
			return "cached_value", nil
		}

		// First call to populate cache
		_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, "key4", successFetch)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Second call with error - should return cached data
		errorFetch := func() (any, error) {
			return nil, errors.New("network error")
		}

		data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "key4", errorFetch)
		if err != nil {
			t.Errorf("Expected no error when returning cached data, got: %v", err)
		}
		if data != "cached_value" {
			t.Errorf("Expected cached_value, got %v", data)
		}
		if changed {
			t.Error("Expected changed=false when using cached data")
		}
	})

	t.Run("skip_cache_enabled", func(t *testing.T) {
		cacheSkip := New(ctx, true)
		defer cacheSkip.Stop()

		fetchFunc := func() (any, error) {
			return "skip_data", nil
		}

		data, changed, err := cacheSkip.GetCachedConfigWithChangeDetection(ctx, "key5", fetchFunc)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !changed {
			t.Error("Expected changed=true with skip cache")
		}
		if data != "skip_data" {
			t.Errorf("Expected 'skip_data', got %v", data)
		}
	})
}

// TestClearCache verifies cache clearing
func TestClearCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Populate cache
	cache.setMemoryCache(ctx, "key1", "data1", "hash1234567890")
	cache.setMemoryCache(ctx, "key2", "data2", "hash2234567890")
	cache.setMemoryCache(ctx, "key3", "data3", "hash3234567890")

	// Verify entries exist
	cache.mutex.RLock()
	count := len(cache.memoryCache)
	cache.mutex.RUnlock()
	if count != 3 {
		t.Errorf("Expected 3 entries, got %d", count)
	}

	// Clear cache
	err := cache.ClearCache()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify cache is empty
	cache.mutex.RLock()
	count = len(cache.memoryCache)
	cache.mutex.RUnlock()
	if count != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", count)
	}
}

// TestInvalidateRelatedCache verifies cache invalidation
func TestInvalidateRelatedCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Populate cache with various keys
	cache.setMemoryCache(ctx, "serverconfig", "data1", "hash1234567890")
	cache.setMemoryCache(ctx, "globalconfig", "data2", "hash2234567890")
	cache.setMemoryCache(ctx, "enabledservices", "data3", "hash3234567890")
	cache.setMemoryCache(ctx, "localconfig", "data4", "hash4234567890")
	cache.setMemoryCache(ctx, "other", "data5", "hash5234567890")

	t.Run("invalidate_mta_service", func(t *testing.T) {
		cache.InvalidateRelatedCache("mta")

		// Should invalidate serverconfig, globalconfig, enabledservices
		_, found := cache.getFromMemoryCache("serverconfig")
		if found {
			t.Error("Expected serverconfig to be invalidated")
		}
		_, found = cache.getFromMemoryCache("globalconfig")
		if found {
			t.Error("Expected globalconfig to be invalidated")
		}
		_, found = cache.getFromMemoryCache("enabledservices")
		if found {
			t.Error("Expected enabledservices to be invalidated")
		}
	})

	t.Run("invalidate_unknown_service", func(t *testing.T) {
		// Re-populate
		cache.setMemoryCache(ctx, "serverconfig", "data1", "hash1234567890")
		cache.setMemoryCache(ctx, "globalconfig", "data2", "hash2234567890")
		cache.setMemoryCache(ctx, "enabledservices", "data3", "hash3234567890")
		cache.setMemoryCache(ctx, "localconfig", "data4", "hash4234567890")

		cache.InvalidateRelatedCache("unknown_service")

		// Should invalidate all config caches
		_, found := cache.getFromMemoryCache("serverconfig")
		if found {
			t.Error("Expected serverconfig to be invalidated")
		}
		_, found = cache.getFromMemoryCache("localconfig")
		if found {
			t.Error("Expected localconfig to be invalidated for unknown service")
		}
	})
}

// TestLoadCache verifies cache loading
func TestLoadCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("cache_miss", func(t *testing.T) {
		_, err := cache.LoadCache("nonexistent")
		if err == nil {
			t.Error("Expected error for nonexistent key")
		}
	})

	t.Run("cache_hit", func(t *testing.T) {
		cache.setMemoryCache(ctx, "loadkey", "loaddata", "loadhash123456")

		data, err := cache.LoadCache("loadkey")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if data.Data != "loaddata" {
			t.Errorf("Expected data='loaddata', got %v", data.Data)
		}
		if data.Hash != "loadhash123456" {
			t.Errorf("Expected hash='loadhash123456', got %s", data.Hash)
		}
		if data.TTL != config.CacheTTL {
			t.Errorf("Expected TTL=%d, got %d", config.CacheTTL, data.TTL)
		}
	})
}

// TestGetMemoryCacheStats verifies stats retrieval
func TestGetMemoryCacheStats(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("empty_cache", func(t *testing.T) {
		stats := cache.GetMemoryCacheStats()

		if stats["entries"] != 0 {
			t.Errorf("Expected 0 entries, got %v", stats["entries"])
		}
		if stats["max_entries"] != 20 {
			t.Errorf("Expected max_entries=20, got %v", stats["max_entries"])
		}
	})

	t.Run("populated_cache", func(t *testing.T) {
		cache.setMemoryCache(ctx, "key1", "data1", "hash1234567890")
		cache.setMemoryCache(ctx, "key2", "data2", "hash2234567890")
		cache.setMemoryCache(ctx, "key3", "data3", "hash3234567890")

		stats := cache.GetMemoryCacheStats()

		if stats["entries"] != 3 {
			t.Errorf("Expected 3 entries, got %v", stats["entries"])
		}

		// Check that stats include age information
		if _, ok := stats["oldest_entry_age_seconds"]; !ok {
			t.Error("Expected oldest_entry_age_seconds in stats")
		}
		if _, ok := stats["newest_entry_age_seconds"]; !ok {
			t.Error("Expected newest_entry_age_seconds in stats")
		}
		if _, ok := stats["total_size_bytes"]; !ok {
			t.Error("Expected total_size_bytes in stats")
		}
	})

	t.Run("cache_with_expired_entries", func(t *testing.T) {
		// Add expired entry
		cache.mutex.Lock()
		cache.memoryCache["expired"] = &MemoryCacheEntry{
			Data:      "olddata",
			Timestamp: time.Now().Add(-2 * time.Hour),
			Hash:      "oldhash12345678",
			TTL:       1, // 1 second
		}
		cache.mutex.Unlock()

		stats := cache.GetMemoryCacheStats()

		expired := stats["expired_entries"].(int)
		if expired < 1 {
			t.Error("Expected at least 1 expired entry")
		}
	})
}

// TestSetMemoryCacheConfig verifies cache configuration
func TestSetMemoryCacheConfig(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	newTTL := 300 * time.Second
	newMax := 500

	cache.SetMemoryCacheConfig(newTTL, newMax)

	cache.mutex.RLock()
	actualTTL := cache.memoryCacheTTL
	actualMax := cache.maxMemoryItems
	cache.mutex.RUnlock()

	if actualTTL != newTTL {
		t.Errorf("Expected TTL=%v, got %v", newTTL, actualTTL)
	}
	if actualMax != newMax {
		t.Errorf("Expected max=%d, got %d", newMax, actualMax)
	}
}

// TestGetCacheKeys verifies key retrieval
func TestGetCacheKeys(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("empty_cache", func(t *testing.T) {
		keys := cache.GetCacheKeys()
		if len(keys) != 0 {
			t.Errorf("Expected 0 keys, got %d", len(keys))
		}
	})

	t.Run("populated_cache", func(t *testing.T) {
		cache.setMemoryCache(ctx, "key1", "data1", "hash1234567890")
		cache.setMemoryCache(ctx, "key2", "data2", "hash2234567890")
		cache.setMemoryCache(ctx, "key3", "data3", "hash3234567890")

		keys := cache.GetCacheKeys()
		if len(keys) != 3 {
			t.Errorf("Expected 3 keys, got %d", len(keys))
		}

		// Verify all keys are present
		keyMap := make(map[string]bool)
		for _, k := range keys {
			keyMap[k] = true
		}

		for _, expectedKey := range []string{"key1", "key2", "key3"} {
			if !keyMap[expectedKey] {
				t.Errorf("Expected key %s to be in cache keys", expectedKey)
			}
		}
	})
}

// TestInvalidateCacheByPrefix verifies prefix-based invalidation
func TestInvalidateCacheByPrefix(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Populate cache
	cache.setMemoryCache(ctx, "server:config1", "data1", "hash1234567890")
	cache.setMemoryCache(ctx, "server:config2", "data2", "hash2234567890")
	cache.setMemoryCache(ctx, "global:config1", "data3", "hash3234567890")
	cache.setMemoryCache(ctx, "other", "data4", "hash4234567890")

	t.Run("invalidate_server_prefix", func(t *testing.T) {
		count := cache.InvalidateCacheByPrefix("server:")
		if count != 2 {
			t.Errorf("Expected 2 invalidations, got %d", count)
		}

		// Verify server: keys are gone
		_, found := cache.getFromMemoryCache("server:config1")
		if found {
			t.Error("Expected server:config1 to be invalidated")
		}
		_, found = cache.getFromMemoryCache("server:config2")
		if found {
			t.Error("Expected server:config2 to be invalidated")
		}

		// Verify other keys remain
		_, found = cache.getFromMemoryCache("global:config1")
		if !found {
			t.Error("Expected global:config1 to remain")
		}
		_, found = cache.getFromMemoryCache("other")
		if !found {
			t.Error("Expected other to remain")
		}
	})

	t.Run("no_matching_prefix", func(t *testing.T) {
		count := cache.InvalidateCacheByPrefix("nonexistent:")
		if count != 0 {
			t.Errorf("Expected 0 invalidations, got %d", count)
		}
	})
}

// TestWarmCache verifies cache warming
func TestWarmCache(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	t.Run("successful_warmup", func(t *testing.T) {
		warmupConfigs := map[string]func() (any, error){
			"config1": func() (any, error) { return "data1", nil },
			"config2": func() (any, error) { return "data2", nil },
			"config3": func() (any, error) { return "data3", nil },
		}

		err := cache.WarmCache(ctx, warmupConfigs)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Verify all configs are cached
		for key := range warmupConfigs {
			_, found := cache.getFromMemoryCache(key)
			if !found {
				t.Errorf("Expected %s to be cached", key)
			}
		}

		stats := cache.GetMemoryCacheStats()
		if stats["entries"] != 3 {
			t.Errorf("Expected 3 cached entries, got %v", stats["entries"])
		}
	})

	t.Run("partial_failure", func(t *testing.T) {
		cache.ClearCache() // Clear previous entries

		warmupConfigs := map[string]func() (any, error){
			"success1": func() (any, error) { return "data1", nil },
			"failure":  func() (any, error) { return nil, errors.New("fetch failed") },
			"success2": func() (any, error) { return "data2", nil },
		}

		err := cache.WarmCache(ctx, warmupConfigs)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// Verify successful configs are cached
		_, found := cache.getFromMemoryCache("success1")
		if !found {
			t.Error("Expected success1 to be cached")
		}
		_, found = cache.getFromMemoryCache("success2")
		if !found {
			t.Error("Expected success2 to be cached")
		}

		// Verify failed config is not cached
		_, found = cache.getFromMemoryCache("failure")
		if found {
			t.Error("Expected failure not to be cached")
		}
	})
}

// TestCleanupMemoryCache verifies automatic cleanup
func TestCleanupMemoryCache(t *testing.T) {
	t.Run("cleanup_stops_on_context_cancel", func(t *testing.T) {
		ctx := context.Background()
		cache := New(ctx, false)

		// Stop the cache
		cache.Stop()

		// Wait a bit to ensure cleanup goroutine has stopped
		time.Sleep(100 * time.Millisecond)

		// Verify stop channel is closed
		select {
		case <-cache.stop:
			// Expected
		default:
			t.Error("Expected stop channel to be closed")
		}
	})
}

// TestRunCleanup_ExpiresEntries calls runCleanup directly to cover the 0% branch.
func TestRunCleanup_ExpiresEntries(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Add a valid entry and an already-expired entry.
	cache.setMemoryCache(ctx, "fresh", "data", "hash1234567890ab")

	cache.mutex.Lock()
	cache.memoryCache["stale"] = &MemoryCacheEntry{
		Data:      "old",
		Timestamp: time.Now().Add(-2 * time.Hour),
		Hash:      "oldhash1234567890",
		TTL:       1, // 1 second — clearly expired
	}
	cache.mutex.Unlock()

	// Directly invoke runCleanup.
	cache.runCleanup(ctx)

	// Stale entry must be gone.
	cache.mutex.RLock()
	_, staleExists := cache.memoryCache["stale"]
	_, freshExists := cache.memoryCache["fresh"]
	cache.mutex.RUnlock()

	if staleExists {
		t.Error("Expected stale entry to be evicted by runCleanup")
	}
	if !freshExists {
		t.Error("Expected fresh entry to survive runCleanup")
	}
}

// TestRunCleanup_EvictsOldestWhenOverLimit exercises the eviction path inside
// runCleanup when the cache exceeds maxMemoryItems.
func TestRunCleanup_EvictsOldestWhenOverLimit(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	// Lower the limit so we can trigger eviction cheaply.
	cache.SetMemoryCacheConfig(time.Hour, 3)

	// Insert 5 entries with distinct timestamps (oldest first).
	base := time.Now().Add(-10 * time.Minute)
	cache.mutex.Lock()
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key%d", i)
		cache.memoryCache[key] = &MemoryCacheEntry{
			Data:      key,
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Hash:      fmt.Sprintf("hash%d12345678", i),
			TTL:       3600,
		}
	}
	cache.mutex.Unlock()

	cache.runCleanup(ctx)

	cache.mutex.RLock()
	remaining := len(cache.memoryCache)
	cache.mutex.RUnlock()

	if remaining != 3 {
		t.Errorf("Expected 3 entries after eviction, got %d", remaining)
	}
}

// TestRunCleanup_ViaParentContextCancel exercises the parent-context-cancel branch
// of cleanupMemoryCache.
func TestRunCleanup_ViaParentContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cache := New(ctx, false)

	// Cancel the parent context to stop the goroutine via the parent.Done() branch.
	cancel()

	// Give the goroutine a moment to exit.
	time.Sleep(50 * time.Millisecond)

	// The stop channel should still be open (we used ctx cancellation, not Stop()).
	select {
	case <-cache.stop:
		t.Error("Expected stop channel to still be open when stopped via context")
	default:
		// Expected: goroutine exited via parent.Done(), not via stop channel.
	}

	cache.Stop() // cleanup
}

// TestHashPrefix_ShortHash exercises the short-hash branch of hashPrefix.
func TestHashPrefix_ShortHash(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"abc", "abc"},
		{"1234567", "1234567"},          // 7 chars — below the 8-char threshold
		{"12345678", "12345678"},        // exactly 8 chars — returns full string
		{"123456789abcdef", "12345678"}, // longer — truncated to 8
	}

	for _, tc := range cases {
		got := hashPrefix(tc.input)
		if got != tc.expected {
			t.Errorf("hashPrefix(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// TestGetCachedConfigWithChangeDetection_UnchangedAfterExpiry exercises the
// "data unchanged after refresh" log branch (previousHash == newHash after expiry).
func TestGetCachedConfigWithChangeDetection_UnchangedAfterExpiry(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	fetchFunc := func() (any, error) {
		return "stable_data", nil
	}

	// First fetch — populates cache.
	_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, "stable_key", fetchFunc)
	if err != nil {
		t.Fatalf("First fetch error: %v", err)
	}

	// Expire the entry so the next call performs a fresh fetch.
	cache.mutex.Lock()
	if entry, ok := cache.memoryCache["stable_key"]; ok {
		entry.Timestamp = time.Now().Add(-2 * time.Hour)
		entry.TTL = 1
	}
	cache.mutex.Unlock()

	// Second fetch returns same data — hash should match, changed=false.
	data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "stable_key", fetchFunc)
	if err != nil {
		t.Fatalf("Second fetch error: %v", err)
	}
	if data != "stable_data" {
		t.Errorf("Expected 'stable_data', got %v", data)
	}
	if changed {
		t.Error("Expected changed=false when hash is identical after expiry")
	}
}

// TestGetCachedConfig_NilContext exercises the nil-context fallback in GetCachedConfig.
func TestGetCachedConfig_NilContext(t *testing.T) {
	cache := New(context.Background(), false)
	defer cache.Stop()

	data, err := cache.GetCachedConfig(context.TODO(), "nil_ctx_key", func() (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if data != "ok" {
		t.Errorf("Expected 'ok', got %v", data)
	}
}

// TestGetCachedConfigWithChangeDetection_NilContext exercises the nil-context fallback
// in GetCachedConfigWithChangeDetection.
func TestGetCachedConfigWithChangeDetection_NilContext(t *testing.T) {
	cache := New(context.Background(), false)
	defer cache.Stop()

	data, changed, err := cache.GetCachedConfigWithChangeDetection(context.TODO(), "nil_cd_key", func() (any, error) {
		return "value", nil
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if data != "value" {
		t.Errorf("Expected 'value', got %v", data)
	}
	if !changed {
		t.Error("Expected changed=true on first fetch")
	}
}

// TestGetCachedConfigWithChangeDetection_ChangedAfterExpiry exercises the
// "Data changed" log branch where previousHash != "" and differs from newHash.
func TestGetCachedConfigWithChangeDetection_ChangedAfterExpiry(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, false)
	defer cache.Stop()

	callCount := 0
	fetchFunc := func() (any, error) {
		callCount++
		return fmt.Sprintf("data_v%d", callCount), nil
	}

	// First fetch populates cache.
	_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, "change_key", fetchFunc)
	if err != nil {
		t.Fatalf("First fetch error: %v", err)
	}

	// Expire the entry so the next call fetches fresh and compares hashes.
	cache.mutex.Lock()
	if entry, ok := cache.memoryCache["change_key"]; ok {
		entry.Timestamp = time.Now().Add(-2 * time.Hour)
		entry.TTL = 1
	}
	cache.mutex.Unlock()

	// Second fetch with different data — triggers the "Data changed" log line
	// (previousHash != "" && previousHash != newHash).
	data, changed, err := cache.GetCachedConfigWithChangeDetection(ctx, "change_key", fetchFunc)
	if err != nil {
		t.Fatalf("Second fetch error: %v", err)
	}
	if !changed {
		t.Error("Expected changed=true when data is different after expiry")
	}
	if data != "data_v2" {
		t.Errorf("Expected 'data_v2', got %v", data)
	}
}

// TestFetchAndCache_SkipCacheEnabled exercises the skipCache=true path inside
// fetchAndCache (data fetched but not stored).
func TestFetchAndCache_SkipCacheEnabled(t *testing.T) {
	ctx := context.Background()
	cache := New(ctx, true) // skipCache=true
	defer cache.Stop()

	callCount := 0
	fetchFunc := func() (any, error) {
		callCount++
		return "data", nil
	}

	// With skipCache the entry is never stored; each call fetches fresh.
	data, changed, _, err := cache.fetchAndCache(ctx, "skip_key", fetchFunc, true, "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if data != "data" {
		t.Errorf("Expected 'data', got %v", data)
	}
	if !changed {
		t.Error("Expected changed=true from fetchAndCache")
	}

	// Verify nothing was stored.
	_, found := cache.getFromMemoryCache("skip_key")
	if found {
		t.Error("Expected no cache entry when skipCache=true")
	}
}
