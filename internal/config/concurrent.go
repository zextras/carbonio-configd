// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"maps"
	"sync"
)

// ConfigMap is a string→string map with built-in RWMutex protection.
// Zero value is NOT usable; construct via NewConfigMap.
//
//nolint:revive // historical name; renaming to Map is a separate breaking change.
type ConfigMap struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewConfigMap returns an empty, ready-to-use ConfigMap.
func NewConfigMap() *ConfigMap { return &ConfigMap{m: make(map[string]string)} }

// NewConfigMapFrom wraps src as a ConfigMap. A nil src is treated as empty.
// The supplied map is taken by reference; callers must not retain it.
func NewConfigMapFrom(src map[string]string) *ConfigMap {
	if src == nil {
		src = make(map[string]string)
	}

	return &ConfigMap{m: src}
}

// Get returns the value for key and whether it was present.
func (c *ConfigMap) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.m[key]

	return v, ok
}

// GetOr returns the value for key, or def if the key is absent.
func (c *ConfigMap) GetOr(key, def string) string {
	if v, ok := c.Get(key); ok {
		return v
	}

	return def
}

// Set assigns value to key.
func (c *ConfigMap) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m[key] = value
}

// Delete removes key from the map.
func (c *ConfigMap) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.m, key)
}

// Len returns the number of entries.
func (c *ConfigMap) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.m)
}

// Snapshot returns a copy of the underlying map, safe to mutate.
func (c *ConfigMap) Snapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]string, len(c.m))
	maps.Copy(out, c.m)

	return out
}

// Replace swaps the backing map for src. A nil src clears the map.
func (c *ConfigMap) Replace(src map[string]string) {
	if src == nil {
		src = make(map[string]string)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.m = src
}

// Clear removes all entries.
func (c *ConfigMap) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m = make(map[string]string)
}

// SectionMap is a string→*MtaConfigSection map with RWMutex protection.
// Sections themselves are immutable after ParseMtaConfig — do not mutate
// returned *MtaConfigSection values.
type SectionMap struct {
	mu sync.RWMutex
	m  map[string]*MtaConfigSection
}

// NewSectionMap returns an empty SectionMap.
func NewSectionMap() *SectionMap {
	return &SectionMap{m: make(map[string]*MtaConfigSection)}
}

// Get returns the section for name and whether it was present.
func (s *SectionMap) Get(name string) (*MtaConfigSection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.m[name]

	return v, ok
}

// Set assigns sec to name.
func (s *SectionMap) Set(name string, sec *MtaConfigSection) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[name] = sec
}

// Len returns the number of sections.
func (s *SectionMap) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.m)
}

// Snapshot returns a shallow copy of the section map.
func (s *SectionMap) Snapshot() map[string]*MtaConfigSection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]*MtaConfigSection, len(s.m))
	maps.Copy(out, s.m)

	return out
}

// Replace swaps the backing map for src. A nil src clears the map.
func (s *SectionMap) Replace(src map[string]*MtaConfigSection) {
	if src == nil {
		src = make(map[string]*MtaConfigSection)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.m = src
}
