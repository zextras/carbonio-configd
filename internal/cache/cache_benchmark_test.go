// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cache

import (
	"testing"
)

func BenchmarkCache_GetCachedConfig_New(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Mock fetch function
	fetchFunc := func() (any, error) {
		return "benchmark_value", nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "benchmark_key"
			_, err := cache.GetCachedConfig(ctx, key, fetchFunc)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}

func BenchmarkCache_GetCachedConfigWithChangeDetection_New(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Mock fetch function
	fetchFunc := func() (any, error) {
		return "benchmark_value", nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "benchmark_key"
			_, _, err := cache.GetCachedConfigWithChangeDetection(ctx, key, fetchFunc)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}

func BenchmarkCache_InvalidateRelatedCache(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Pre-populate cache with some entries
	fetchFunc := func() (any, error) {
		return "config_value", nil
	}

	for range 100 {
		key := "postfix_config_test"
		_, err := cache.GetCachedConfig(ctx, key, fetchFunc)
		if err != nil {
			b.Fatal(err)
		}
	}

	for b.Loop() {
		cache.InvalidateRelatedCache("postfix")
	}
}

func BenchmarkCache_GetMemoryCacheStats(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	for b.Loop() {
		cache.GetMemoryCacheStats()
	}
}

func BenchmarkCache_GetCacheKeys(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Pre-populate cache
	fetchFunc := func() (any, error) {
		return "config_value", nil
	}

	for range 100 {
		key := "config_key"
		_, err := cache.GetCachedConfig(ctx, key, fetchFunc)
		if err != nil {
			b.Fatal(err)
		}
	}

	for b.Loop() {
		cache.GetCacheKeys()
	}
}

func BenchmarkCache_ClearCache(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Pre-populate cache
	fetchFunc := func() (any, error) {
		return "config_value", nil
	}

	for range 100 {
		key := "config_key"
		_, err := cache.GetCachedConfig(ctx, key, fetchFunc)
		if err != nil {
			b.Fatal(err)
		}
	}

	for b.Loop() {
		err := cache.ClearCache()
		if err != nil {
			b.Fatal(err)
		}
		// Re-populate for next iteration
		_, err = cache.GetCachedConfig(ctx, "config_key", fetchFunc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCache_ConcurrentOperations(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	// Mock fetch function
	fetchFunc := func() (any, error) {
		return "concurrent_value", nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				// Cache operation
				key := "concurrent_key"
				_, err := cache.GetCachedConfig(ctx, key, fetchFunc)
				if err != nil {
					b.Fatal(err)
				}
			} else {
				// Stats operation
				cache.GetMemoryCacheStats()
			}
			i++
		}
	})
}

func BenchmarkCache_LoadCache_Miss(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cache.LoadCache("non_existent_key")
			if err == nil {
				b.Fatal("Expected error for non-existent key")
			}
		}
	})
}

func BenchmarkCache_IsCacheValid(b *testing.B) {
	// Setup cache
	ctx := b.Context()
	cache := New(ctx, false)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.IsCacheValid(nil) // Pass nil to test the basic logic
		}
	})
}
