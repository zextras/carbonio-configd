// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package cache provides in-memory caching functionality for configd configuration data.
// It supports TTL-based expiration, change detection using MD5 hashing, and cache invalidation
// based on configuration changes. The cache improves performance by reducing redundant
// configuration file reads and external command executions.
package cache

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 used for non-cryptographic checksumming only
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	errs "github.com/zextras/carbonio-configd/internal/errors"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// MemoryCacheEntry represents an in-memory cache entry
type MemoryCacheEntry struct {
	Data      any
	Timestamp time.Time
	Hash      string
	TTL       int
}

// ConfigCache represents the pure in-memory configuration caching system.
//
// Note: context is intentionally not stored on the struct. Operation APIs
// accept a context.Context so the caller controls cancellation/tracing. A
// dedicated stop channel drives the cleanup goroutine lifecycle.
type ConfigCache struct {
	mutex          sync.RWMutex
	memoryCache    map[string]*MemoryCacheEntry // In-memory cache
	memoryCacheTTL time.Duration                // TTL for memory cache entries
	maxMemoryItems int                          // Maximum items in memory cache
	skipCache      bool                         // Global flag to skip caching
	stop           chan struct{}                // Closed by Stop() to terminate cleanup goroutine
	stopOnce       sync.Once                    // Ensures stop is closed only once
}

// New creates a new pure in-memory configuration cache.
// The provided parent context is used solely to scope the cleanup goroutine
// (it is not stored on the struct). Use Stop() to tear the cache down explicitly.
func New(parent context.Context, skipCache bool) *ConfigCache {
	cache := &ConfigCache{
		memoryCache:    make(map[string]*MemoryCacheEntry),
		memoryCacheTTL: time.Duration(config.CacheTTL) * time.Second, // Use config TTL
		maxMemoryItems: 1000,                                         // Higher limit since we're memory-only
		skipCache:      skipCache,                                    // Set skipCache during initialization
		stop:           make(chan struct{}),
	}

	// Start cleanup goroutine for memory cache. The goroutine uses a bounded
	// background context for its own log lines but honors both the parent
	// context cancellation and the explicit stop signal.
	go cache.cleanupMemoryCache(parent)

	if !skipCache {
		logger.DebugContext(backgroundCtx(), "In-memory configuration cache initialized")
	}

	return cache
}

// Stop stops the cache cleanup goroutine and releases resources.
// Safe to call multiple times.
func (cc *ConfigCache) Stop() {
	cc.stopOnce.Do(func() {
		close(cc.stop)
		logger.DebugContext(backgroundCtx(), "ConfigCache stopped")
	})
}

// backgroundCtx returns a background context tagged with the cache component,
// used for log lines emitted from contexts without a caller-supplied ctx
// (e.g. the cleanup goroutine).
func backgroundCtx() context.Context {
	return logger.ContextWithComponent(context.Background(), "cache")
}

// cleanupMemoryCache periodically cleans expired entries from memory cache.
// It terminates when the parent context is cancelled or Stop() is invoked.
func (cc *ConfigCache) cleanupMemoryCache(parent context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := backgroundCtx()

	for {
		select {
		case <-ticker.C:
			cc.runCleanup(ctx)
		case <-cc.stop:
			logger.DebugContext(ctx, "Memory cache cleanup goroutine stopped")

			return
		case <-parent.Done():
			logger.DebugContext(ctx, "Memory cache cleanup goroutine stopped")

			return
		}
	}
}

// runCleanup performs a single cleanup pass: removing expired entries and
// evicting oldest entries if the cache exceeds its configured max size.
func (cc *ConfigCache) runCleanup(ctx context.Context) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	now := time.Now()
	expired := 0

	for key, entry := range cc.memoryCache {
		if now.Sub(entry.Timestamp) > time.Duration(entry.TTL)*time.Second {
			delete(cc.memoryCache, key)

			expired++
		}
	}

	// If memory cache is too large, remove oldest entries using an O(n log n)
	// sort instead of the previous O(n²) bubble sort.
	if len(cc.memoryCache) > cc.maxMemoryItems {
		type keyEntry struct {
			key   string
			entry *MemoryCacheEntry
		}

		entries := make([]keyEntry, 0, len(cc.memoryCache))
		for k, v := range cc.memoryCache {
			entries = append(entries, keyEntry{k, v})
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].entry.Timestamp.Before(entries[j].entry.Timestamp)
		})

		toRemove := len(cc.memoryCache) - cc.maxMemoryItems
		for i := range toRemove {
			delete(cc.memoryCache, entries[i].key)
		}

		logger.DebugContext(ctx, "Memory cache eviction",
			"removed_count", toRemove)
	}

	if expired > 0 {
		logger.DebugContext(ctx, "Memory cache cleanup",
			"expired_count", expired)
	}
}

// generateHash generates MD5 hash of the data for change detection
func (cc *ConfigCache) generateHash(data any) string {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return ""
	}

	hash := md5.Sum(jsonData) //nolint:gosec // MD5 used for non-cryptographic checksumming only

	return fmt.Sprintf("%x", hash)
}

// getFromMemoryCache retrieves data from in-memory cache
func (cc *ConfigCache) getFromMemoryCache(key string) (*MemoryCacheEntry, bool) {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	entry, exists := cc.memoryCache[key]
	if !exists {
		return nil, false
	}

	// Check if memory cache entry is still valid
	age := time.Since(entry.Timestamp)
	if age > time.Duration(entry.TTL)*time.Second {
		return nil, false
	}

	return entry, true
}

// setMemoryCache stores data in in-memory cache
func (cc *ConfigCache) setMemoryCache(ctx context.Context, key string, data any, hash string) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cc.memoryCache[key] = &MemoryCacheEntry{
		Data:      data,
		Timestamp: time.Now(),
		Hash:      hash,
		TTL:       config.CacheTTL,
	}

	logger.DebugContext(ctx, "Memory cache stored",
		"key", key,
		"hash_prefix", hashPrefix(hash),
		"ttl_seconds", config.CacheTTL)
}

// hashPrefix returns a short prefix of a hash for safe logging, guarding
// against short hashes (e.g. an empty string from a marshal failure).
func hashPrefix(hash string) string {
	const prefixLen = 8
	if len(hash) < prefixLen {
		return hash
	}

	return hash[:prefixLen]
}

// SetSkipCache sets the skipCache flag
func (cc *ConfigCache) SetSkipCache(skip bool) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cc.skipCache = skip
}

// IsCacheValid checks if cached data is still valid (for API compatibility)
func (cc *ConfigCache) IsCacheValid(cachedData *config.CachedData) bool {
	if cachedData == nil {
		return false
	}

	age := time.Since(cachedData.Timestamp).Seconds()

	return age < float64(cachedData.TTL)
}

// GetCachedConfig retrieves configuration with lightning-fast in-memory cache.
// When ctx is nil, a background context is used for internal logging.
func (cc *ConfigCache) GetCachedConfig(ctx context.Context, key string, fetchFunc func() (any, error)) (any, error) {
	if ctx == nil {
		ctx = backgroundCtx()
	}

	// Check in-memory cache first
	if !cc.skipCache {
		if memEntry, found := cc.getFromMemoryCache(key); found {
			age := time.Since(memEntry.Timestamp).Seconds()
			logger.DebugContext(ctx, "Memory cache hit",
				"key", key,
				"age_seconds", age,
				"hash_prefix", hashPrefix(memEntry.Hash))

			return memEntry.Data, nil
		}

		logger.DebugContext(ctx, "Memory cache miss",
			"key", key)
	}

	// Fetch fresh data
	logger.DebugContext(ctx, "Fetching fresh data",
		"key", key)

	freshData, err := fetchFunc()
	if err != nil {
		return nil, errs.WrapCache("fetch", key, err)
	}

	// Generate hash for change detection
	newHash := cc.generateHash(freshData)

	// Store in memory cache
	if !cc.skipCache {
		logger.DebugContext(ctx, "Data fetched and cached",
			"key", key,
			"hash", hashPrefix(newHash))

		cc.setMemoryCache(ctx, key, freshData, newHash)
	}

	return freshData, nil
}

// GetCachedConfigWithChangeDetection returns cached data without invoking the
// backing fetch when the cached entry is still valid. On cache miss or when
// the stored entry is expired, it fetches fresh data and reports whether the
// content hash changed compared to the previously cached hash.
func (cc *ConfigCache) GetCachedConfigWithChangeDetection(ctx context.Context, key string,
	fetchFunc func() (any, error)) (data any, changed bool, err error) {
	if ctx == nil {
		ctx = backgroundCtx()
	}

	if cc.skipCache {
		return cc.fetchAndCache(ctx, key, fetchFunc, true, "")
	}

	if memEntry, found := cc.getFromMemoryCache(key); found {
		age := time.Since(memEntry.Timestamp).Seconds()
		logger.DebugContext(ctx, "Memory cache hit",
			"key", key,
			"age_seconds", age,
			"hash_prefix", hashPrefix(memEntry.Hash))

		// Valid cached entry: honor the cache and skip the backing fetch.
		return memEntry.Data, false, nil
	}

	// Entry missing or expired: fetch fresh data and compare with the
	// previously stored hash (if any) to report whether content changed.
	previousHash := cc.previousHash(key)

	logger.DebugContext(ctx, "Memory cache miss",
		"key", key)

	data, _, err = cc.fetchAndCache(ctx, key, fetchFunc, true, previousHash)
	if err != nil {
		return nil, false, err
	}

	newHash := cc.generateHash(data)
	changed = previousHash == "" || previousHash != newHash

	if !changed {
		logger.DebugContext(ctx, "Data unchanged after refresh",
			"key", key)
	} else if previousHash != "" {
		logger.InfoContext(ctx, "Data changed",
			"key", key,
			"old_hash_prefix", hashPrefix(previousHash),
			"new_hash_prefix", hashPrefix(newHash))
	}

	return data, changed, nil
}

// previousHash returns the hash of the currently stored cache entry for key,
// ignoring TTL validity (so hash comparisons survive expiry). Returns empty
// string when no entry is present.
func (cc *ConfigCache) previousHash(key string) string {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	if entry, ok := cc.memoryCache[key]; ok {
		return entry.Hash
	}

	return ""
}

// fetchAndCache is a helper function to fetch and optionally cache data
func (cc *ConfigCache) fetchAndCache(
	ctx context.Context,
	key string,
	fetchFunc func() (any, error),
	isChanged bool,
	_ string,
) (data any, changed bool, err error) {
	// Fetch fresh data
	logger.DebugContext(ctx, "Fetching fresh data",
		"key", key)

	freshData, err := fetchFunc()
	if err != nil {
		return nil, false, errs.WrapCache("fetch", key, err)
	}

	// Generate hash and store in cache
	newHash := cc.generateHash(freshData)
	if !cc.skipCache {
		logger.DebugContext(ctx, "Data fetched and cached",
			"key", key,
			"hash", hashPrefix(newHash))

		cc.setMemoryCache(ctx, key, freshData, newHash)
	}

	return freshData, isChanged, nil // New data, so it's considered "changed"
}

// ClearCache removes all memory cache entries
func (cc *ConfigCache) ClearCache() error {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	// Clear memory cache
	memoryEntries := len(cc.memoryCache)
	cc.memoryCache = make(map[string]*MemoryCacheEntry)

	logger.DebugContext(backgroundCtx(), "Memory cache cleared",
		"removed_entries", memoryEntries)

	return nil
}

// InvalidateRelatedCache clears memory cache entries related to a service
func (cc *ConfigCache) InvalidateRelatedCache(configKey string) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	// Map of services to their related cache keys
	var cacheKeys []string

	switch strings.ToLower(configKey) {
	case "mta", "postfix", "proxy", "nginx", "ldap", "slapd",
		"antispam", "spamassassin", "antivirus", "clamav",
		"cbpolicyd", "policyd", "sasl", "saslauthd",
		"amavis", "amavisd", "opendkim", "dkim",
		"mailbox", "mailboxd", "zimbra":
		// Service-related configs should invalidate all configuration caches
		cacheKeys = []string{"serverconfig", "globalconfig", "enabledservices"}
	default:
		// For unknown configs, invalidate all caches to be safe
		cacheKeys = []string{"serverconfig", "globalconfig", "enabledservices", "localconfig"}
	}

	invalidated := 0
	ctx := backgroundCtx()

	for _, key := range cacheKeys {
		if _, exists := cc.memoryCache[key]; exists {
			delete(cc.memoryCache, key)

			invalidated++

			logger.DebugContext(ctx, "Memory cache invalidated",
				"key", key)
		}
	}

	if invalidated > 0 {
		logger.DebugContext(ctx, "Invalidated cache entries for config change",
			"invalidated_count", invalidated,
			"config_key", configKey)
	}
}

// LoadCache loads cached data for external access (for API compatibility)
func (cc *ConfigCache) LoadCache(key string) (*config.CachedData, error) {
	if memEntry, found := cc.getFromMemoryCache(key); found {
		// Convert memory cache entry to CachedData format for compatibility
		return &config.CachedData{
			Data:      memEntry.Data,
			Timestamp: memEntry.Timestamp,
			Hash:      memEntry.Hash,
			TTL:       memEntry.TTL,
		}, nil
	}

	return nil, errs.WrapCache("get", key, fmt.Errorf(errs.ErrCacheEntry))
}

// GetMemoryCacheStats returns statistics about the memory cache
func (cc *ConfigCache) GetMemoryCacheStats() map[string]any {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	stats := map[string]any{
		"entries":     len(cc.memoryCache),
		"max_entries": cc.maxMemoryItems,
		"ttl_seconds": cc.memoryCacheTTL.Seconds(),
	}

	// Count expired entries
	now := time.Now()
	expired := 0
	totalSize := 0
	oldestAge := time.Duration(0)
	newestAge := time.Duration(0)

	for _, entry := range cc.memoryCache {
		age := now.Sub(entry.Timestamp)
		if age > time.Duration(entry.TTL)*time.Second {
			expired++
		}

		// Calculate approximate size (rough estimate)
		if data, err := json.Marshal(entry.Data); err == nil {
			totalSize += len(data)
		}

		// Track age statistics
		if oldestAge == 0 || age > oldestAge {
			oldestAge = age
		}

		if newestAge == 0 || age < newestAge {
			newestAge = age
		}
	}

	stats["expired_entries"] = expired
	stats["total_size_bytes"] = totalSize
	stats["oldest_entry_age_seconds"] = oldestAge.Seconds()
	stats["newest_entry_age_seconds"] = newestAge.Seconds()

	return stats
}

// SetMemoryCacheConfig allows tuning memory cache parameters
func (cc *ConfigCache) SetMemoryCacheConfig(ttl time.Duration, maxItems int) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cc.memoryCacheTTL = ttl
	cc.maxMemoryItems = maxItems

	logger.DebugContext(backgroundCtx(), "Memory cache config updated",
		"ttl", ttl,
		"max_items", maxItems)
}

// GetCacheKeys returns all currently cached keys (useful for debugging)
func (cc *ConfigCache) GetCacheKeys() []string {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	keys := make([]string, 0, len(cc.memoryCache))
	for key := range cc.memoryCache {
		keys = append(keys, key)
	}

	return keys
}

// InvalidateCacheByPrefix removes all cache entries with the given prefix
func (cc *ConfigCache) InvalidateCacheByPrefix(prefix string) int {
	cc.mutex.Lock()

	invalidated := 0
	ctx := backgroundCtx()

	for key := range cc.memoryCache {
		if strings.HasPrefix(key, prefix) {
			delete(cc.memoryCache, key)
			logger.DebugContext(ctx, "Memory cache invalidated",
				"key", key)

			invalidated++
		}
	}

	cc.mutex.Unlock()

	if invalidated > 0 {
		logger.DebugContext(ctx, "Invalidated cache entries with prefix",
			"invalidated_count", invalidated,
			"prefix", prefix)
	}

	return invalidated
}

// WarmCache pre-loads cache with data using provided fetch functions
func (cc *ConfigCache) WarmCache(ctx context.Context, warmupConfigs map[string]func() (any, error)) error {
	if ctx == nil {
		ctx = backgroundCtx()
	}

	logger.DebugContext(ctx, "Warming memory cache",
		"config_count", len(warmupConfigs))

	warmed := 0

	for key, fetchFunc := range warmupConfigs {
		if _, err := cc.GetCachedConfig(ctx, key, fetchFunc); err != nil {
			logger.WarnContext(ctx, "Failed to warm cache",
				"key", key,
				"error", err)
		} else {
			warmed++
		}
	}

	stats := cc.GetMemoryCacheStats()
	logger.DebugContext(ctx, "Cache warmed",
		"loaded", warmed,
		"requested", len(warmupConfigs),
		"total_entries", stats["entries"])

	return nil
}
