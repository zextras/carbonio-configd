// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"maps"
	"sync"
)

// syncMap is a generic comparable-keyed map guarded by an RWMutex. It backs
// ConfigMap and SectionMap, each of which exposes only the subset of
// methods it needs.
type syncMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

// newSyncMap wraps src in a ready-to-use syncMap. A nil src is treated as empty.
func newSyncMap[K comparable, V any](src map[K]V) *syncMap[K, V] {
	if src == nil {
		src = make(map[K]V)
	}

	return &syncMap[K, V]{m: src}
}

func (s *syncMap[K, V]) get(key K) (V, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.m[key]

	return v, ok
}

func (s *syncMap[K, V]) set(key K, value V) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[key] = value
}

func (s *syncMap[K, V]) delete(key K) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.m, key)
}

func (s *syncMap[K, V]) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.m)
}

func (s *syncMap[K, V]) snapshot() map[K]V {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[K]V, len(s.m))
	maps.Copy(out, s.m)

	return out
}

func (s *syncMap[K, V]) replace(src map[K]V) {
	if src == nil {
		src = make(map[K]V)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.m = src
}

func (s *syncMap[K, V]) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m = make(map[K]V)
}

// ConfigMap is a string→string map with built-in RWMutex protection.
// Zero value is NOT usable; construct via NewConfigMap.
//
//nolint:revive // historical name; renaming to Map is a separate breaking change.
type ConfigMap struct {
	sm *syncMap[string, string]
}

// NewConfigMap returns an empty, ready-to-use ConfigMap.
func NewConfigMap() *ConfigMap { return &ConfigMap{sm: newSyncMap[string, string](nil)} }

// NewConfigMapFrom wraps src as a ConfigMap. A nil src is treated as empty.
// The supplied map is taken by reference; callers must not retain it.
func NewConfigMapFrom(src map[string]string) *ConfigMap {
	return &ConfigMap{sm: newSyncMap(src)}
}

// Get returns the value for key and whether it was present.
func (c *ConfigMap) Get(key string) (string, bool) { return c.sm.get(key) }

// GetOr returns the value for key, or def if the key is absent.
func (c *ConfigMap) GetOr(key, def string) string {
	if v, ok := c.Get(key); ok {
		return v
	}

	return def
}

// Set assigns value to key.
func (c *ConfigMap) Set(key, value string) { c.sm.set(key, value) }

// Delete removes key from the map.
func (c *ConfigMap) Delete(key string) { c.sm.delete(key) }

// Len returns the number of entries.
func (c *ConfigMap) Len() int { return c.sm.len() }

// Snapshot returns a copy of the underlying map, safe to mutate.
func (c *ConfigMap) Snapshot() map[string]string { return c.sm.snapshot() }

// Replace swaps the backing map for src. A nil src clears the map.
func (c *ConfigMap) Replace(src map[string]string) { c.sm.replace(src) }

// Clear removes all entries.
func (c *ConfigMap) Clear() { c.sm.clear() }

// SectionMap is a string→*MtaConfigSection map with RWMutex protection.
// Sections themselves are immutable after ParseMtaConfig — do not mutate
// returned *MtaConfigSection values.
type SectionMap struct {
	sm *syncMap[string, *MtaConfigSection]
}

// NewSectionMap returns an empty SectionMap.
func NewSectionMap() *SectionMap {
	return &SectionMap{sm: newSyncMap[string, *MtaConfigSection](nil)}
}

// Get returns the section for name and whether it was present.
func (s *SectionMap) Get(name string) (*MtaConfigSection, bool) { return s.sm.get(name) }

// Set assigns sec to name.
func (s *SectionMap) Set(name string, sec *MtaConfigSection) { s.sm.set(name, sec) }

// Len returns the number of sections.
func (s *SectionMap) Len() int { return s.sm.len() }

// Snapshot returns a shallow copy of the section map.
func (s *SectionMap) Snapshot() map[string]*MtaConfigSection { return s.sm.snapshot() }

// Replace swaps the backing map for src. A nil src clears the map.
func (s *SectionMap) Replace(src map[string]*MtaConfigSection) { s.sm.replace(src) }
