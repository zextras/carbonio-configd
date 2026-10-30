// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"sync"
	"testing"
)

// TestNewConfigMap tests ConfigMap initialization
func TestNewConfigMap(t *testing.T) {
	cm := NewConfigMap()
	if cm == nil {
		t.Fatal("NewConfigMap returned nil")
	}
	if cm.Len() != 0 {
		t.Errorf("NewConfigMap should create empty map, got len=%d", cm.Len())
	}
}

// TestConfigMapGet tests Get method for present and absent keys
func TestConfigMapGet(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantOk    bool
	}{
		{
			name:      "present key",
			key:       "key1",
			wantValue: "value1",
			wantOk:    true,
		},
		{
			name:      "absent key",
			key:       "nonexistent",
			wantValue: "",
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := cm.Get(tt.key)
			if ok != tt.wantOk {
				t.Errorf("expected ok=%v, got %v", tt.wantOk, ok)
			}
			if val != tt.wantValue {
				t.Errorf("expected value %q, got %q", tt.wantValue, val)
			}
		})
	}
}

// TestConfigMapGetOr tests GetOr method with default values
func TestConfigMapGetOr(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")

	tests := []struct {
		name     string
		key      string
		def      string
		expected string
	}{
		{
			name:     "present key returns value",
			key:      "key1",
			def:      "default",
			expected: "value1",
		},
		{
			name:     "absent key returns default",
			key:      "nonexistent",
			def:      "default",
			expected: "default",
		},
		{
			name:     "absent key with empty default",
			key:      "nonexistent",
			def:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cm.GetOr(tt.key, tt.def)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestConfigMapSet tests Set method
func TestConfigMapSet(t *testing.T) {
	cm := NewConfigMap()

	cm.Set("key1", "value1")
	if val, ok := cm.Get("key1"); !ok || val != "value1" {
		t.Errorf("Set failed: expected value1, got %q (ok=%v)", val, ok)
	}

	// Overwrite existing key
	cm.Set("key1", "value2")
	if val, ok := cm.Get("key1"); !ok || val != "value2" {
		t.Errorf("Set overwrite failed: expected value2, got %q", val)
	}

	// Set multiple keys
	cm.Set("key2", "value2")
	cm.Set("key3", "value3")
	if cm.Len() != 3 {
		t.Errorf("expected len=3, got %d", cm.Len())
	}
}

// TestConfigMapDelete tests Delete method
func TestConfigMapDelete(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")
	cm.Set("key2", "value2")

	if cm.Len() != 2 {
		t.Errorf("expected len=2, got %d", cm.Len())
	}

	cm.Delete("key1")
	if cm.Len() != 1 {
		t.Errorf("after delete, expected len=1, got %d", cm.Len())
	}

	if _, ok := cm.Get("key1"); ok {
		t.Error("key1 should be deleted")
	}

	if val, ok := cm.Get("key2"); !ok || val != "value2" {
		t.Error("key2 should still exist")
	}

	// Delete non-existent key should not panic
	cm.Delete("nonexistent")
	if cm.Len() != 1 {
		t.Errorf("delete non-existent should not change len, got %d", cm.Len())
	}
}

// TestConfigMapLen tests Len method
func TestConfigMapLen(t *testing.T) {
	cm := NewConfigMap()

	if cm.Len() != 0 {
		t.Errorf("new map should have len=0, got %d", cm.Len())
	}

	cm.Set("key1", "value1")
	if cm.Len() != 1 {
		t.Errorf("after 1 set, expected len=1, got %d", cm.Len())
	}

	cm.Set("key2", "value2")
	cm.Set("key3", "value3")
	if cm.Len() != 3 {
		t.Errorf("after 3 sets, expected len=3, got %d", cm.Len())
	}

	cm.Delete("key2")
	if cm.Len() != 2 {
		t.Errorf("after delete, expected len=2, got %d", cm.Len())
	}
}

// TestConfigMapSnapshot tests Snapshot returns a copy
func TestConfigMapSnapshot(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")
	cm.Set("key2", "value2")

	snap := cm.Snapshot()

	// Verify snapshot contains the same data
	if len(snap) != 2 {
		t.Errorf("snapshot should have len=2, got %d", len(snap))
	}
	if snap["key1"] != "value1" || snap["key2"] != "value2" {
		t.Errorf("snapshot data mismatch: %v", snap)
	}

	// Mutate snapshot and verify original is unchanged
	snap["key1"] = "modified"
	snap["key3"] = "new"

	if val, _ := cm.Get("key1"); val != "value1" {
		t.Error("original map should not be affected by snapshot mutation")
	}
	if _, ok := cm.Get("key3"); ok {
		t.Error("original map should not have new keys from snapshot mutation")
	}

	// Verify snapshot is still correct
	if snap["key1"] != "modified" || snap["key3"] != "new" {
		t.Error("snapshot mutation should work")
	}
}

// TestConfigMapReplace tests Replace method
func TestConfigMapReplace(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")
	cm.Set("key2", "value2")

	newMap := map[string]string{
		"keyA": "valueA",
		"keyB": "valueB",
	}

	cm.Replace(newMap)

	if cm.Len() != 2 {
		t.Errorf("after replace, expected len=2, got %d", cm.Len())
	}

	if val, ok := cm.Get("keyA"); !ok || val != "valueA" {
		t.Error("replaced map should have keyA")
	}
	if _, ok := cm.Get("key1"); ok {
		t.Error("old keys should be gone after replace")
	}
}

// TestConfigMapReplaceNil tests Replace with nil clears the map
func TestConfigMapReplaceNil(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")
	cm.Set("key2", "value2")

	cm.Replace(nil)

	if cm.Len() != 0 {
		t.Errorf("replace(nil) should clear map, got len=%d", cm.Len())
	}

	if _, ok := cm.Get("key1"); ok {
		t.Error("all keys should be gone after replace(nil)")
	}
}

// TestConfigMapClear tests Clear method
func TestConfigMapClear(t *testing.T) {
	cm := NewConfigMap()
	cm.Set("key1", "value1")
	cm.Set("key2", "value2")
	cm.Set("key3", "value3")

	if cm.Len() != 3 {
		t.Errorf("before clear, expected len=3, got %d", cm.Len())
	}

	cm.Clear()

	if cm.Len() != 0 {
		t.Errorf("after clear, expected len=0, got %d", cm.Len())
	}

	if _, ok := cm.Get("key1"); ok {
		t.Error("all keys should be gone after clear")
	}

	// Should be able to use after clear
	cm.Set("newkey", "newvalue")
	if val, ok := cm.Get("newkey"); !ok || val != "newvalue" {
		t.Error("should be able to set after clear")
	}
}

// TestConfigMapConcurrent tests concurrent access with race detector
func TestConfigMapConcurrent(t *testing.T) {
	cm := NewConfigMap()
	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch goroutines doing concurrent Set/Get/Delete/Snapshot
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := "key" + string(rune(j%10))
				value := "value" + string(rune(id))

				// Mix of operations
				cm.Set(key, value)
				cm.Get(key)
				if j%3 == 0 {
					cm.Delete(key)
				}
				if j%5 == 0 {
					_ = cm.Snapshot()
				}
				if j%7 == 0 {
					_ = cm.Len()
				}
			}
		}(i)
	}

	wg.Wait()

	// Final state should be consistent
	finalLen := cm.Len()
	if finalLen < 0 {
		t.Errorf("final len should be non-negative, got %d", finalLen)
	}

	// Should be able to snapshot without panic
	snap := cm.Snapshot()
	if snap == nil {
		t.Error("snapshot should not be nil")
	}
}
