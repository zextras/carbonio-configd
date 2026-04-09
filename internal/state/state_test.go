// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/config"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestComputeStringMD5 verifies MD5 computation for strings
func TestComputeStringMD5(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "empty string",
			content:  "",
			expected: "d41d8cd98f00b204e9800998ecf8427e",
		},
		{
			name:     "simple string",
			content:  "hello world",
			expected: "5eb63bbbe01eeed093cb22bb8f5acdc3",
		},
		{
			name:     "multiline string",
			content:  "line1\nline2\nline3",
			expected: "81facad50c8e6244de64a98cf4f56f77",
		},
		{
			name:     "config-like string",
			content:  "mynetworks = 127.0.0.0/8, 10.0.0.0/8",
			expected: "8e8652894d5c3b10b6a39d728f22d3b2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeStringMD5(tt.content)
			if result != tt.expected {
				t.Errorf("ComputeStringMD5(%q) = %s, want %s", tt.content, result, tt.expected)
			}
		})
	}
}

// TestComputeFileMD5 verifies MD5 computation for files
func TestComputeFileMD5(t *testing.T) {
	// Create temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.conf")

	content := "test configuration content\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Compute MD5
	md5hash, err := ComputeFileMD5(context.Background(), testFile)
	if err != nil {
		t.Fatalf("ComputeFileMD5() error = %v", err)
	}

	// Verify MD5 is non-empty and correct length (32 hex chars)
	if len(md5hash) != 32 {
		t.Errorf("MD5 hash length = %d, want 32", len(md5hash))
	}

	// Compute again - should be identical
	md5hash2, err := ComputeFileMD5(context.Background(), testFile)
	if err != nil {
		t.Fatalf("ComputeFileMD5() second call error = %v", err)
	}

	if md5hash != md5hash2 {
		t.Errorf("MD5 hashes differ on identical file: %s != %s", md5hash, md5hash2)
	}

	// Modify file and verify hash changes
	newContent := "modified configuration content\n"
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	md5hash3, err := ComputeFileMD5(context.Background(), testFile)
	if err != nil {
		t.Fatalf("ComputeFileMD5() after modification error = %v", err)
	}

	if md5hash == md5hash3 {
		t.Errorf("MD5 hash should differ after file modification")
	}
}

// TestComputeFileMD5_NonExistent verifies error handling for non-existent files
func TestComputeFileMD5_NonExistent(t *testing.T) {
	_, err := ComputeFileMD5(context.Background(), "/nonexistent/file/path")
	if err == nil {
		t.Error("ComputeFileMD5() should return error for non-existent file")
	}
}

// TestState_FileMD5Cache tests MD5 cache operations
func TestState_FileMD5Cache(t *testing.T) {
	s := NewState()

	filepath := "/opt/zextras/conf/postfix_main.cf"
	md5hash := "abc123def456"

	// Initially empty
	if got := s.GetFileMD5(filepath); got != "" {
		t.Errorf("GetFileMD5() initially = %q, want empty string", got)
	}

	// Set MD5
	s.SetFileMD5(filepath, md5hash)

	// Retrieve MD5
	if got := s.GetFileMD5(filepath); got != md5hash {
		t.Errorf("GetFileMD5() = %q, want %q", got, md5hash)
	}

	// Update MD5
	newMD5 := "789ghi012jkl"
	s.SetFileMD5(filepath, newMD5)

	if got := s.GetFileMD5(filepath); got != newMD5 {
		t.Errorf("GetFileMD5() after update = %q, want %q", got, newMD5)
	}
}

// TestState_FileHasChanged tests file change detection logic
func TestState_FileHasChanged(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.conf")

	// Create initial file
	content := "initial content\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	s := NewState()

	// Test 1: No cached MD5 - should be considered changed
	if !s.FileHasChanged(context.Background(), testFile) {
		t.Error("FileHasChanged() with no cached MD5 should return true")
	}

	// Test 2: Cache the MD5
	if err := s.UpdateFileMD5(context.Background(), testFile); err != nil {
		t.Fatalf("UpdateFileMD5() error = %v", err)
	}

	// Test 3: File unchanged - should not be changed
	if s.FileHasChanged(context.Background(), testFile) {
		t.Error("FileHasChanged() with matching MD5 should return false")
	}

	// Test 4: Modify file - should be changed
	newContent := "modified content\n"
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	if !s.FileHasChanged(context.Background(), testFile) {
		t.Error("FileHasChanged() after modification should return true")
	}

	// Test 5: Update cache again
	if err := s.UpdateFileMD5(context.Background(), testFile); err != nil {
		t.Fatalf("UpdateFileMD5() error = %v", err)
	}

	// Test 6: File should not be changed after cache update
	if s.FileHasChanged(context.Background(), testFile) {
		t.Error("FileHasChanged() after cache update should return false")
	}
}

// TestState_FileHasChanged_NonExistent tests behavior with non-existent files
func TestState_FileHasChanged_NonExistent(t *testing.T) {
	s := NewState()

	// Non-existent file should be considered changed
	if !s.FileHasChanged(context.Background(), "/nonexistent/file") {
		t.Error("FileHasChanged() for non-existent file should return true")
	}
}

// TestState_ShouldRewriteSection tests section rewrite decision logic
func TestState_ShouldRewriteSection(t *testing.T) {
	tests := []struct {
		name           string
		firstRun       bool
		sectionChanged bool
		forced         bool
		requested      bool
		want           bool
	}{
		{
			name:     "first run always rewrites",
			firstRun: true,
			want:     true,
		},
		{
			name:           "section changed triggers rewrite",
			firstRun:       false,
			sectionChanged: true,
			want:           true,
		},
		{
			name:     "forced config triggers rewrite",
			firstRun: false,
			forced:   true,
			want:     true,
		},
		{
			name:      "requested config triggers rewrite",
			firstRun:  false,
			requested: true,
			want:      true,
		},
		{
			name:     "no change, no force, no request - skip rewrite",
			firstRun: false,
			want:     false,
		},
		{
			name:           "all conditions met",
			firstRun:       false,
			sectionChanged: true,
			forced:         true,
			requested:      true,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewState()
			s.FirstRun = tt.firstRun

			sectionName := "mta"
			section := &config.MtaConfigSection{
				Name:    sectionName,
				Changed: tt.sectionChanged,
			}

			if tt.forced {
				s.ForcedConfig[sectionName] = "1"
			}
			if tt.requested {
				s.RequestedConfig[sectionName] = "1"
			}

			got := s.ShouldRewriteSection(context.Background(), sectionName, section)
			if got != tt.want {
				t.Errorf("ShouldRewriteSection() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestState_ShouldRewriteSection_NilSection tests behavior with nil section
func TestState_ShouldRewriteSection_NilSection(t *testing.T) {
	s := NewState()
	s.FirstRun = false

	// With nil section and no forced/requested, should return false
	if s.ShouldRewriteSection(context.Background(), "mta", nil) {
		t.Error("ShouldRewriteSection() with nil section and no force should return false")
	}

	// With forced config, should still return true even with nil section
	s.ForcedConfig["mta"] = "1"
	if !s.ShouldRewriteSection(context.Background(), "mta", nil) {
		t.Error("ShouldRewriteSection() with nil section but forced should return true")
	}
}

// TestState_ClearFileCache tests file cache clearing
func TestState_ClearFileCache(t *testing.T) {
	s := NewState()

	// Populate cache
	s.FileCache["key1"] = "value1"
	s.FileCache["key2"] = "value2"

	if len(s.FileCache) != 2 {
		t.Errorf("FileCache should have 2 entries, got %d", len(s.FileCache))
	}

	// Clear cache
	s.ClearFileCache(context.Background())

	if len(s.FileCache) != 0 {
		t.Errorf("FileCache should be empty after clear, got %d entries", len(s.FileCache))
	}
}

// TestState_ResetConfigs tests forced and requested config reset
func TestState_ResetConfigs(t *testing.T) {
	s := NewState()

	// Populate maps
	s.ForcedConfig["mta"] = "1"
	s.ForcedConfig["proxy"] = "1"
	s.RequestedConfig["ldap"] = "1"

	// Reset forced
	s.ResetForcedConfig(context.Background())
	if len(s.ForcedConfig) != 0 {
		t.Errorf("ForcedConfig should be empty after reset, got %d entries", len(s.ForcedConfig))
	}

	// RequestedConfig should still have entries
	if len(s.RequestedConfig) != 1 {
		t.Errorf("RequestedConfig should still have 1 entry, got %d", len(s.RequestedConfig))
	}

	// Reset requested
	s.ResetRequestedConfig(context.Background())
	if len(s.RequestedConfig) != 0 {
		t.Errorf("RequestedConfig should be empty after reset, got %d entries", len(s.RequestedConfig))
	}
}

// TestState_IsFalseValue tests false value detection
func TestState_IsFalseValue(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", true},
		{"no", true},
		{"NO", true},
		{"No", true},
		{"false", true},
		{"FALSE", true},
		{"False", true},
		{"0", true},
		{"00", true},
		{"yes", false},
		{"true", false},
		{"1", false},
		{"enabled", false},
		{"anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			got := IsFalseValue(tt.val)
			if got != tt.want {
				t.Errorf("IsFalseValue(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

// TestState_IsTrueValue tests true value detection
func TestState_IsTrueValue(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"no", false},
		{"false", false},
		{"0", false},
		{"yes", true},
		{"YES", true},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"enabled", true},
		{"anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			got := IsTrueValue(tt.val)
			if got != tt.want {
				t.Errorf("IsTrueValue(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

// TestState_UpdateFileMD5_Error tests error handling in UpdateFileMD5
func TestState_UpdateFileMD5_Error(t *testing.T) {
	s := NewState()

	err := s.UpdateFileMD5(context.Background(), "/nonexistent/file")
	if err == nil {
		t.Error("UpdateFileMD5() should return error for non-existent file")
	}
}

// TestNewState verifies proper state initialization
func TestNewState(t *testing.T) {
	s := NewState()

	// Verify maps are initialized
	if s.ChangedKeys == nil {
		t.Error("ChangedKeys should be initialized")
	}
	if s.LastVals == nil {
		t.Error("LastVals should be initialized")
	}
	if s.ForcedConfig == nil {
		t.Error("ForcedConfig should be initialized")
	}
	if s.RequestedConfig == nil {
		t.Error("RequestedConfig should be initialized")
	}
	if s.FileCache == nil {
		t.Error("FileCache should be initialized")
	}
	if s.FileMD5Cache == nil {
		t.Error("FileMD5Cache should be initialized")
	}
	if s.WatchdogProcess == nil {
		t.Error("WatchdogProcess should be initialized")
	}

	// Verify default values
	if !s.FirstRun {
		t.Error("FirstRun should be true initially")
	}
	if s.MaxFailedRestarts != 3 {
		t.Errorf("MaxFailedRestarts = %d, want 3", s.MaxFailedRestarts)
	}

	// Verify config objects are initialized
	if s.LocalConfig == nil {
		t.Error("LocalConfig should be initialized")
	}
	if s.GlobalConfig == nil {
		t.Error("GlobalConfig should be initialized")
	}
	if s.MiscConfig == nil {
		t.Error("MiscConfig should be initialized")
	}
	if s.ServerConfig == nil {
		t.Error("ServerConfig should be initialized")
	}
	if s.MtaConfig == nil {
		t.Error("MtaConfig should be initialized")
	}

	// Verify CurrentActions maps are initialized
	if s.CurrentActions.Rewrites == nil {
		t.Error("CurrentActions.Rewrites should be initialized")
	}
	if s.CurrentActions.Restarts == nil {
		t.Error("CurrentActions.Restarts should be initialized")
	}
	if s.CurrentActions.Postconf == nil {
		t.Error("CurrentActions.Postconf should be initialized")
	}
	if s.CurrentActions.Postconfd == nil {
		t.Error("CurrentActions.Postconfd should be initialized")
	}
	if s.CurrentActions.Services == nil {
		t.Error("CurrentActions.Services should be initialized")
	}
	if s.CurrentActions.Ldap == nil {
		t.Error("CurrentActions.Ldap should be initialized")
	}

	// Verify PreviousActions maps are initialized
	if s.PreviousActions.Rewrites == nil {
		t.Error("PreviousActions.Rewrites should be initialized")
	}
	if s.PreviousActions.Config == nil {
		t.Error("PreviousActions.Config should be initialized")
	}
	if s.PreviousActions.Restarts == nil {
		t.Error("PreviousActions.Restarts should be initialized")
	}
	if s.PreviousActions.Postconf == nil {
		t.Error("PreviousActions.Postconf should be initialized")
	}
	if s.PreviousActions.Postconfd == nil {
		t.Error("PreviousActions.Postconfd should be initialized")
	}
	if s.PreviousActions.Services == nil {
		t.Error("PreviousActions.Services should be initialized")
	}
	if s.PreviousActions.Ldap == nil {
		t.Error("PreviousActions.Ldap should be initialized")
	}
}

// TestState_SetForcedConfig tests forced config addition
func TestState_SetForcedConfig(t *testing.T) {
	s := NewState()

	// Initially empty
	if len(s.ForcedConfig) != 0 {
		t.Error("ForcedConfig should be initially empty")
	}

	// Add forced config
	s.SetForcedConfig(context.Background(), "mta")
	if val, ok := s.ForcedConfig["mta"]; !ok || val != "1" {
		t.Error("SetForcedConfig should add mta section")
	}

	// Add another
	s.SetForcedConfig(context.Background(), "proxy")
	if len(s.ForcedConfig) != 2 {
		t.Errorf("ForcedConfig should have 2 entries, got %d", len(s.ForcedConfig))
	}
}

// TestState_SetRequestedConfig tests requested config addition
func TestState_SetRequestedConfig(t *testing.T) {
	s := NewState()

	// Initially empty
	if len(s.RequestedConfig) != 0 {
		t.Error("RequestedConfig should be initially empty")
	}

	// Add requested config
	s.SetRequestedConfig(context.Background(), "ldap")
	if val, ok := s.RequestedConfig["ldap"]; !ok || val != "ldap" {
		t.Error("SetRequestedConfig should add ldap section")
	}

	// Add another
	s.SetRequestedConfig(context.Background(), "mailbox")
	if len(s.RequestedConfig) != 2 {
		t.Errorf("RequestedConfig should have 2 entries, got %d", len(s.RequestedConfig))
	}
}

// TestState_CompareActions tests action comparison logic
func TestState_CompareActions(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(s *State)
		want   bool
		reason string
	}{
		{
			name: "identical empty states",
			setup: func(s *State) {
				// Nothing to set up - both are empty
			},
			want:   false,
			reason: "empty current and previous should be equal",
		},
		{
			name: "different rewrites",
			setup: func(s *State) {
				s.CurrentActions.Rewrites["mta"] = config.RewriteEntry{
					Value: "conf/postfix_main.cf",
					Mode:  "0644",
				}
			},
			want:   true,
			reason: "rewrites differ",
		},
		{
			name: "different postconf",
			setup: func(s *State) {
				s.CurrentActions.Postconf["mynetworks"] = "127.0.0.0/8"
			},
			want:   true,
			reason: "postconf differs",
		},
		{
			name: "different postconfd",
			setup: func(s *State) {
				s.CurrentActions.Postconfd["removed_key"] = "1"
			},
			want:   true,
			reason: "postconfd differs",
		},
		{
			name: "different services",
			setup: func(s *State) {
				s.CurrentActions.Services["mta"] = "running"
			},
			want:   true,
			reason: "services differ",
		},
		{
			name: "different ldap",
			setup: func(s *State) {
				s.CurrentActions.Ldap["zimbraSSLProtocols"] = "TLSv1.2 TLSv1.3"
			},
			want:   true,
			reason: "ldap differs",
		},
		{
			name: "different proxygen",
			setup: func(s *State) {
				s.CurrentActions.Proxygen = true
			},
			want:   true,
			reason: "proxygen differs",
		},
		{
			name: "identical after copy",
			setup: func(s *State) {
				s.CurrentActions.Postconf["key1"] = "value1"
				s.CurrentActions.Services["mta"] = "running"
				s.CurrentActions.Proxygen = true

				// Copy to previous
				s.SaveCurrentToPrevious(context.Background())
			},
			want:   false,
			reason: "identical current and previous after copy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewState()
			tt.setup(s)

			got := s.CompareActions()
			if got != tt.want {
				t.Errorf("CompareActions() = %v, want %v (reason: %s)", got, tt.want, tt.reason)
			}
		})
	}
}

// TestState_SaveCurrentToPrevious tests state copying
func TestState_SaveCurrentToPrevious(t *testing.T) {
	s := NewState()

	// Populate current actions
	s.CurrentActions.Rewrites["mta"] = config.RewriteEntry{
		Value: "conf/postfix_main.cf",
		Mode:  "0644",
	}
	s.CurrentActions.Postconf["mynetworks"] = "127.0.0.0/8"
	s.CurrentActions.Postconfd["old_key"] = "1"
	s.CurrentActions.Services["mta"] = "running"
	s.CurrentActions.Ldap["zimbraSSLProtocols"] = "TLSv1.2 TLSv1.3"
	s.CurrentActions.Proxygen = true

	// Save to previous
	s.SaveCurrentToPrevious(context.Background())

	// Verify previous now matches current
	if len(s.PreviousActions.Rewrites) != len(s.CurrentActions.Rewrites) {
		t.Error("PreviousActions.Rewrites length mismatch after save")
	}
	if len(s.PreviousActions.Postconf) != len(s.CurrentActions.Postconf) {
		t.Error("PreviousActions.Postconf length mismatch after save")
	}
	if len(s.PreviousActions.Postconfd) != len(s.CurrentActions.Postconfd) {
		t.Error("PreviousActions.Postconfd length mismatch after save")
	}
	if len(s.PreviousActions.Services) != len(s.CurrentActions.Services) {
		t.Error("PreviousActions.Services length mismatch after save")
	}
	if len(s.PreviousActions.Ldap) != len(s.CurrentActions.Ldap) {
		t.Error("PreviousActions.Ldap length mismatch after save")
	}
	if s.PreviousActions.Proxygen != s.CurrentActions.Proxygen {
		t.Error("PreviousActions.Proxygen mismatch after save")
	}

	// Verify deep copy (modifying current doesn't affect previous)
	s.CurrentActions.Postconf["new_key"] = "new_value"
	if _, ok := s.PreviousActions.Postconf["new_key"]; ok {
		t.Error("Modifying current should not affect previous (not a deep copy)")
	}

	// CompareActions should now return true (current differs from previous)
	if !s.CompareActions() {
		t.Error("CompareActions() should return true after adding new key to current")
	}
}

// TestState_CompareActions_AllFields tests comprehensive comparison
func TestState_SnapshotCompileActions(t *testing.T) {
	s := NewState()
	s.RequestedConfig["proxy"] = "proxy"
	s.ForcedConfig["mta"] = "1"
	s.FirstRun = true
	s.ServerConfig.ServiceConfig["mta"] = "TRUE"
	section := &config.MtaConfigSection{Name: "proxy"}
	s.MtaConfig.Sections["proxy"] = section

	snapshot := s.SnapshotCompileActions()

	if snapshot.RequestedConfig["proxy"] != "proxy" {
		t.Fatalf("requested config not captured in snapshot")
	}
	if len(s.RequestedConfig) != 0 {
		t.Fatalf("requested config should be cleared after snapshot")
	}
	if snapshot.ForcedConfig["mta"] != "1" {
		t.Fatalf("forced config not captured in snapshot")
	}
	if !snapshot.FirstRun {
		t.Fatalf("firstRun not captured in snapshot")
	}
	if snapshot.ServiceConfig["mta"] != "TRUE" {
		t.Fatalf("service config not captured in snapshot")
	}
	if snapshot.MtaSections["proxy"] != section {
		t.Fatalf("mta sections not captured in snapshot")
	}
}

func TestState_ConcurrentRequestedConfigAndSnapshots(t *testing.T) {
	s := NewState()
	const workers = 20
	const iterations = 100

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				s.AddRequestedConfigs(context.Background(), []string{fmt.Sprintf("section-%d-%d", id, j)})
				_ = s.SnapshotCompileActions()
			}
		}(i)
	}
	wg.Wait()
}

func TestState_CompareActions_AllFields(t *testing.T) {
	s := NewState()

	// Populate both current and previous with different values
	s.CurrentActions.Rewrites["mta"] = config.RewriteEntry{
		Value: "conf/postfix_main.cf",
		Mode:  "0644",
	}
	s.PreviousActions.Rewrites["mta"] = config.RewriteEntry{
		Value: "conf/postfix_main.cf",
		Mode:  "0600", // Different mode
	}

	// Should detect difference
	if !s.CompareActions() {
		t.Error("CompareActions() should detect rewrite mode difference")
	}

	// Make them identical
	s.PreviousActions.Rewrites["mta"] = s.CurrentActions.Rewrites["mta"]

	// Should now be equal
	if s.CompareActions() {
		t.Error("CompareActions() should return false when states are identical")
	}
}

// TestState_SetSleepTimer tests sleep timer set and get.
func TestState_SetSleepTimer(t *testing.T) {
	s := NewState()

	if got := s.GetSleepTimer(); got != 0 {
		t.Errorf("GetSleepTimer() initially = %v, want 0", got)
	}

	s.SetSleepTimer(1.5)
	if got := s.GetSleepTimer(); got != 1.5 {
		t.Errorf("GetSleepTimer() = %v, want 1.5", got)
	}

	s.SetSleepTimer(0)
	if got := s.GetSleepTimer(); got != 0 {
		t.Errorf("GetSleepTimer() after reset = %v, want 0", got)
	}
}

// TestState_SetFirstRun tests toggling the first-run flag.
func TestState_SetFirstRun(t *testing.T) {
	s := NewState()

	if !s.FirstRun {
		t.Error("FirstRun should be true initially")
	}

	s.SetFirstRun(false)
	if s.FirstRun {
		t.Error("FirstRun should be false after SetFirstRun(false)")
	}

	s.SetFirstRun(true)
	if !s.FirstRun {
		t.Error("FirstRun should be true after SetFirstRun(true)")
	}
}

// TestState_GetCurrentRewriteKeys tests retrieval of rewrite keys.
func TestState_GetCurrentRewriteKeys(t *testing.T) {
	s := NewState()

	keys := s.GetCurrentRewriteKeys()
	if len(keys) != 0 {
		t.Errorf("GetCurrentRewriteKeys() initially = %v, want empty", keys)
	}

	s.CurrentActions.Rewrites["mta"] = config.RewriteEntry{Value: "conf/postfix_main.cf", Mode: "0644"}
	s.CurrentActions.Rewrites["ldap"] = config.RewriteEntry{Value: "conf/ldap.cf", Mode: "0600"}

	keys = s.GetCurrentRewriteKeys()
	if len(keys) != 2 {
		t.Errorf("GetCurrentRewriteKeys() len = %d, want 2", len(keys))
	}
}

// TestState_SetConfigs tests replacing all config references.
func TestState_SetConfigs(t *testing.T) {
	s := NewState()

	lc := &config.LocalConfig{Data: map[string]string{"key": "val"}}
	gc := &config.GlobalConfig{Data: map[string]string{"gkey": "gval"}}
	mc := &config.MiscConfig{Data: map[string]string{"mkey": "mval"}}
	sc := &config.ServerConfig{Data: map[string]string{}, ServiceConfig: map[string]string{"svc": "TRUE"}}
	mtac := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{}}

	s.SetConfigs(lc, gc, mc, sc, mtac)

	if s.LocalConfig != lc {
		t.Error("LocalConfig not set correctly")
	}
	if s.GlobalConfig != gc {
		t.Error("GlobalConfig not set correctly")
	}
	if s.MiscConfig != mc {
		t.Error("MiscConfig not set correctly")
	}
	if s.ServerConfig != sc {
		t.Error("ServerConfig not set correctly")
	}
	if s.MtaConfig != mtac {
		t.Error("MtaConfig not set correctly")
	}
}

// TestState_CurRewrites tests adding and retrieving rewrite entries.
func TestState_CurRewrites(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Retrieve when empty - should return zero value
	got := s.CurRewrites(ctx, "mta", nil)
	if got != (config.RewriteEntry{}) {
		t.Errorf("CurRewrites() with nil on empty map = %v, want zero value", got)
	}

	// Add an entry
	entry := &config.RewriteEntry{Value: "conf/postfix_main.cf", Mode: "0644"}
	got = s.CurRewrites(ctx, "mta", entry)
	if got.Value != entry.Value || got.Mode != entry.Mode {
		t.Errorf("CurRewrites() after add = %v, want %v", got, *entry)
	}

	// Retrieve without setting
	got2 := s.CurRewrites(ctx, "mta", nil)
	if got2 != got {
		t.Errorf("CurRewrites() retrieve = %v, want %v", got2, got)
	}
}

// TestState_DelRewrite tests deletion of rewrite entries.
func TestState_DelRewrite(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	entry := &config.RewriteEntry{Value: "conf/postfix_main.cf", Mode: "0644"}
	s.CurRewrites(ctx, "mta", entry)

	if _, ok := s.CurrentActions.Rewrites["mta"]; !ok {
		t.Fatal("Rewrite entry should exist before delete")
	}

	s.DelRewrite("mta")

	if _, ok := s.CurrentActions.Rewrites["mta"]; ok {
		t.Error("Rewrite entry should be deleted")
	}

	// Deleting non-existent key should not panic
	s.DelRewrite("nonexistent")
}

// TestState_CurRestarts tests setting restart action values.
func TestState_CurRestarts(t *testing.T) {
	s := NewState()

	s.CurRestarts("mta", 1)
	if val, ok := s.CurrentActions.Restarts["mta"]; !ok || val != 1 {
		t.Errorf("CurRestarts() = %v, want 1", val)
	}

	s.CurRestarts("mta", -1)
	if val := s.CurrentActions.Restarts["mta"]; val != -1 {
		t.Errorf("CurRestarts() after update = %v, want -1", val)
	}
}

// TestState_DelRestart tests deletion of restart entries.
func TestState_DelRestart(t *testing.T) {
	s := NewState()

	s.CurRestarts("mta", 1)
	s.DelRestart("mta")

	if _, ok := s.CurrentActions.Restarts["mta"]; ok {
		t.Error("Restart entry should be deleted")
	}

	// Deleting non-existent key should not panic
	s.DelRestart("nonexistent")
}

// TestState_CurLdap tests adding and retrieving LDAP configuration changes.
func TestState_CurLdap(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Retrieve when empty
	got := s.CurLdap(ctx, "zimbraSSLProtocols", "")
	if got != "" {
		t.Errorf("CurLdap() on empty = %q, want empty", got)
	}

	// Add an entry
	got = s.CurLdap(ctx, "zimbraSSLProtocols", "TLSv1.2 TLSv1.3")
	if got != "TLSv1.2 TLSv1.3" {
		t.Errorf("CurLdap() after add = %q, want TLSv1.2 TLSv1.3", got)
	}

	// Retrieve without adding
	got2 := s.CurLdap(ctx, "zimbraSSLProtocols", "")
	if got2 != "TLSv1.2 TLSv1.3" {
		t.Errorf("CurLdap() retrieve = %q, want TLSv1.2 TLSv1.3", got2)
	}
}

// TestState_DelLdap tests deletion of LDAP configuration changes.
func TestState_DelLdap(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	s.CurLdap(ctx, "zimbraSSLProtocols", "TLSv1.2")
	s.DelLdap("zimbraSSLProtocols")

	if _, ok := s.CurrentActions.Ldap["zimbraSSLProtocols"]; ok {
		t.Error("LDAP entry should be deleted")
	}

	// Deleting non-existent key should not panic
	s.DelLdap("nonexistent")
}

// TestState_CurPostconf tests adding and retrieving postconf changes.
func TestState_CurPostconf(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Retrieve when empty
	got := s.CurPostconf(ctx, "mynetworks", "")
	if got != "" {
		t.Errorf("CurPostconf() on empty = %q, want empty", got)
	}

	// Add an entry
	got = s.CurPostconf(ctx, "mynetworks", "127.0.0.0/8")
	if got != "127.0.0.0/8" {
		t.Errorf("CurPostconf() after add = %q, want 127.0.0.0/8", got)
	}

	// Newlines should be converted to spaces
	got = s.CurPostconf(ctx, "smtpd_relay_restrictions", "permit_mynetworks\npermit_sasl_authenticated")
	if got != "permit_mynetworks permit_sasl_authenticated" {
		t.Errorf("CurPostconf() newline conversion = %q, want spaces", got)
	}

	// Retrieve without adding
	got2 := s.CurPostconf(ctx, "mynetworks", "")
	if got2 != "127.0.0.0/8" {
		t.Errorf("CurPostconf() retrieve = %q, want 127.0.0.0/8", got2)
	}
}

// TestState_ClearPostconf tests clearing all postconf changes.
func TestState_ClearPostconf(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	s.CurPostconf(ctx, "mynetworks", "127.0.0.0/8")
	s.CurPostconf(ctx, "myhostname", "mail.example.com")

	if len(s.CurrentActions.Postconf) != 2 {
		t.Fatalf("Expected 2 postconf entries, got %d", len(s.CurrentActions.Postconf))
	}

	s.ClearPostconf()

	if len(s.CurrentActions.Postconf) != 0 {
		t.Errorf("Postconf should be empty after ClearPostconf, got %d entries", len(s.CurrentActions.Postconf))
	}
}

// TestState_CurPostconfd tests adding and retrieving postconfd changes.
func TestState_CurPostconfd(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Retrieve when empty
	got := s.CurPostconfd(ctx, "removed_key", "")
	if got != "" {
		t.Errorf("CurPostconfd() on empty = %q, want empty", got)
	}

	// Add an entry
	got = s.CurPostconfd(ctx, "removed_key", "1")
	if got != "1" {
		t.Errorf("CurPostconfd() after add = %q, want 1", got)
	}

	// Newlines should be converted to spaces
	got = s.CurPostconfd(ctx, "multi_line", "val1\nval2")
	if got != "val1 val2" {
		t.Errorf("CurPostconfd() newline conversion = %q, want spaces", got)
	}
}

// TestState_ClearPostconfd tests clearing all postconfd changes.
func TestState_ClearPostconfd(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	s.CurPostconfd(ctx, "removed_key", "1")
	s.CurPostconfd(ctx, "other_key", "2")

	if len(s.CurrentActions.Postconfd) != 2 {
		t.Fatalf("Expected 2 postconfd entries, got %d", len(s.CurrentActions.Postconfd))
	}

	s.ClearPostconfd()

	if len(s.CurrentActions.Postconfd) != 0 {
		t.Errorf("Postconfd should be empty after ClearPostconfd, got %d entries", len(s.CurrentActions.Postconfd))
	}
}

// TestState_CurServices tests setting and retrieving service statuses.
func TestState_CurServices(t *testing.T) {
	s := NewState()

	// Retrieve when empty
	got := s.CurServices("mta", "")
	if got != "" {
		t.Errorf("CurServices() on empty = %q, want empty", got)
	}

	// Set status
	got = s.CurServices("mta", "running")
	if got != "running" {
		t.Errorf("CurServices() after set = %q, want running", got)
	}

	// Retrieve without setting
	got2 := s.CurServices("mta", "")
	if got2 != "running" {
		t.Errorf("CurServices() retrieve = %q, want running", got2)
	}

	// Update status
	got = s.CurServices("mta", "stopped")
	if got != "stopped" {
		t.Errorf("CurServices() after update = %q, want stopped", got)
	}
}

// TestState_PrevServices tests setting and retrieving previous service statuses.
func TestState_PrevServices(t *testing.T) {
	s := NewState()

	// Retrieve when empty
	got := s.PrevServices("mta", "")
	if got != "" {
		t.Errorf("PrevServices() on empty = %q, want empty", got)
	}

	// Set status
	got = s.PrevServices("mta", "running")
	if got != "running" {
		t.Errorf("PrevServices() after set = %q, want running", got)
	}

	// Retrieve without setting
	got2 := s.PrevServices("mta", "")
	if got2 != "running" {
		t.Errorf("PrevServices() retrieve = %q, want running", got2)
	}
}

// TestState_Proxygen tests setting and returning the proxygen flag.
func TestState_Proxygen(t *testing.T) {
	s := NewState()

	if got := s.Proxygen(false); got {
		t.Error("Proxygen(false) should return false")
	}

	if got := s.Proxygen(true); !got {
		t.Error("Proxygen(true) should return true")
	}

	if got := s.Proxygen(false); got {
		t.Error("Proxygen(false) should return false after reset")
	}
}

// TestState_ResetChangedKeys tests clearing changed keys for a section.
func TestState_ResetChangedKeys(t *testing.T) {
	s := NewState()

	s.ChangedKeys["mta"] = []string{"key1", "key2"}

	s.ResetChangedKeys("mta")

	if keys := s.ChangedKeys["mta"]; len(keys) != 0 {
		t.Errorf("ChangedKeys[mta] should be empty after reset, got %v", keys)
	}

	// Resetting a non-existent section should not panic
	s.ResetChangedKeys("nonexistent")
}

// TestState_ChangedKeysForSection tests adding and retrieving changed keys per section.
func TestState_ChangedKeysForSection(t *testing.T) {
	s := NewState()

	// Retrieve when empty - should initialize
	keys := s.ChangedKeysForSection("mta", "")
	if len(keys) != 0 {
		t.Errorf("ChangedKeysForSection() on empty = %v, want empty slice", keys)
	}

	// Add a key
	keys = s.ChangedKeysForSection("mta", "mynetworks")
	if len(keys) != 1 || keys[0] != "mynetworks" {
		t.Errorf("ChangedKeysForSection() after add = %v, want [mynetworks]", keys)
	}

	// Add another key
	keys = s.ChangedKeysForSection("mta", "myhostname")
	if len(keys) != 2 {
		t.Errorf("ChangedKeysForSection() after second add = %v, want 2 items", keys)
	}

	// Retrieve without adding
	keys2 := s.ChangedKeysForSection("mta", "")
	if len(keys2) != 2 {
		t.Errorf("ChangedKeysForSection() retrieve = %v, want 2 items", keys2)
	}

	// Different section is independent
	keys3 := s.ChangedKeysForSection("proxy", "")
	if len(keys3) != 0 {
		t.Errorf("ChangedKeysForSection() for different section = %v, want empty", keys3)
	}
}

// TestState_LastVal tests storing and retrieving last configuration values.
func TestState_LastVal(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Retrieve when not set - should return empty
	got := s.LastVal(ctx, "mta", "VAR", "mynetworks", "")
	if got != "" {
		t.Errorf("LastVal() on empty = %q, want empty", got)
	}

	// Store a value
	got = s.LastVal(ctx, "mta", "VAR", "mynetworks", "127.0.0.0/8")
	if got != "127.0.0.0/8" {
		t.Errorf("LastVal() after store = %q, want 127.0.0.0/8", got)
	}

	// Retrieve without updating
	got2 := s.LastVal(ctx, "mta", "VAR", "mynetworks", "")
	if got2 != "127.0.0.0/8" {
		t.Errorf("LastVal() retrieve = %q, want 127.0.0.0/8", got2)
	}

	// Different section/type/key are independent
	got3 := s.LastVal(ctx, "ldap", "LOCAL", "zimbraSSLProtocols", "")
	if got3 != "" {
		t.Errorf("LastVal() different section = %q, want empty", got3)
	}

	// Update value
	got4 := s.LastVal(ctx, "mta", "VAR", "mynetworks", "10.0.0.0/8")
	if got4 != "10.0.0.0/8" {
		t.Errorf("LastVal() after update = %q, want 10.0.0.0/8", got4)
	}
}

// TestState_DelVal tests removal of last value entries.
func TestState_DelVal(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	s.LastVal(ctx, "mta", "VAR", "mynetworks", "127.0.0.0/8")

	s.DelVal("mta", "VAR", "mynetworks")

	got := s.LastVal(ctx, "mta", "VAR", "mynetworks", "")
	if got != "" {
		t.Errorf("LastVal() after DelVal = %q, want empty", got)
	}

	// Deleting from non-existent section/type should not panic
	s.DelVal("nonexistent", "VAR", "key")
	s.DelVal("mta", "nonexistent", "key")
}

// TestState_GetSetDelWatchdog tests watchdog tracking operations.
func TestState_GetSetDelWatchdog(t *testing.T) {
	s := NewState()

	// Initially not set - should return nil
	if got := s.GetWatchdog("antivirus"); got != nil {
		t.Errorf("GetWatchdog() initially = %v, want nil", got)
	}

	// Set to true
	s.SetWatchdog("antivirus", true)
	got := s.GetWatchdog("antivirus")
	if got == nil {
		t.Fatal("GetWatchdog() should return non-nil after SetWatchdog")
	}
	if !*got {
		t.Error("GetWatchdog() should return true")
	}

	// Set to false
	s.SetWatchdog("antivirus", false)
	got = s.GetWatchdog("antivirus")
	if got == nil {
		t.Fatal("GetWatchdog() should return non-nil after SetWatchdog(false)")
	}
	if *got {
		t.Error("GetWatchdog() should return false")
	}

	// Delete
	s.DelWatchdog("antivirus")
	if got := s.GetWatchdog("antivirus"); got != nil {
		t.Errorf("GetWatchdog() after delete = %v, want nil", got)
	}

	// Deleting non-existent should not panic
	s.DelWatchdog("nonexistent")
}

// TestState_CompileDependencyRestarts tests dependency restart compilation.
func TestState_CompileDependencyRestarts(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	// Section with dependencies
	s.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Name: "mta",
		Depends: map[string]bool{
			"antispam": true,
			"amavis":   true,
		},
	}

	restarts := make(map[string]int)
	curRestarts := func(service string, actionValue int) {
		restarts[service] = actionValue
	}
	lookupConfig := func(cfgType, key string) (string, error) {
		if key == "antispam" {
			return "TRUE", nil
		}
		return "FALSE", nil
	}

	s.CompileDependencyRestarts(ctx, "mta", lookupConfig, curRestarts)

	// antispam is enabled (TRUE) -> should get a restart
	if val, ok := restarts["antispam"]; !ok || val != -1 {
		t.Errorf("restarts[antispam] = %v, want -1", restarts["antispam"])
	}

	// amavis is special-cased -> should always get a restart regardless of lookup
	if val, ok := restarts["amavis"]; !ok || val != -1 {
		t.Errorf("restarts[amavis] = %v, want -1", restarts["amavis"])
	}
}

// TestState_CompileDependencyRestarts_NoSection tests with a missing section.
func TestState_CompileDependencyRestarts_NoSection(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	restarts := make(map[string]int)
	curRestarts := func(service string, actionValue int) {
		restarts[service] = actionValue
	}
	lookupConfig := func(cfgType, key string) (string, error) {
		return "TRUE", nil
	}

	// Section does not exist - should be a no-op
	s.CompileDependencyRestarts(ctx, "nonexistent", lookupConfig, curRestarts)

	if len(restarts) != 0 {
		t.Errorf("Expected no restarts for missing section, got %v", restarts)
	}
}

// TestState_CompileDependencyRestarts_LookupError tests error handling during lookup.
func TestState_CompileDependencyRestarts_LookupError(t *testing.T) {
	s := NewState()
	ctx := context.Background()

	s.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Name: "mta",
		Depends: map[string]bool{
			"broken-service": true,
		},
	}

	restarts := make(map[string]int)
	curRestarts := func(service string, actionValue int) {
		restarts[service] = actionValue
	}
	lookupConfig := func(cfgType, key string) (string, error) {
		return "", fmt.Errorf("lookup error")
	}

	// Should not panic on error - just skip that dependency
	s.CompileDependencyRestarts(ctx, "mta", lookupConfig, curRestarts)

	if len(restarts) != 0 {
		t.Errorf("Expected no restarts on lookup error, got %v", restarts)
	}
}

// TestState_MultipleOperations tests realistic state management workflow
func TestState_MultipleOperations(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "postfix_main.cf")

	// Create initial config file
	if err := os.WriteFile(configFile, []byte("mynetworks = 127.0.0.0/8\n"), 0644); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	s := NewState()

	// Set FirstRun to false to avoid triggering rewrite on first run
	s.FirstRun = false

	// First run: file is new
	if !s.FileHasChanged(context.Background(), configFile) {
		t.Error("New file should be detected as changed")
	}

	// Update cache
	if err := s.UpdateFileMD5(context.Background(), configFile); err != nil {
		t.Fatalf("UpdateFileMD5 failed: %v", err)
	}

	// File should not be changed now
	if s.FileHasChanged(context.Background(), configFile) {
		t.Error("File should not be changed after cache update")
	}

	// Force rewrite for mta section
	s.SetForcedConfig(context.Background(), "mta")
	if !s.ShouldRewriteSection(context.Background(), "mta", nil) {
		t.Error("Forced section should trigger rewrite")
	}

	// Reset forced config
	s.ResetForcedConfig(context.Background())
	if s.ShouldRewriteSection(context.Background(), "mta", nil) {
		t.Error("After reset, forced section should not trigger rewrite")
	}

	// Request specific section
	s.SetRequestedConfig(context.Background(), "proxy")
	if !s.ShouldRewriteSection(context.Background(), "proxy", nil) {
		t.Error("Requested section should trigger rewrite")
	}

	// Populate current actions
	s.CurrentActions.Postconf["key1"] = "value1"
	s.CurrentActions.Services["mta"] = "running"

	// Should differ from empty previous
	if !s.CompareActions() {
		t.Error("Current with data should differ from empty previous")
	}

	// Save current to previous
	s.SaveCurrentToPrevious(context.Background())

	// Should now be identical
	if s.CompareActions() {
		t.Error("After save, current and previous should be identical")
	}

	// Modify file
	if err := os.WriteFile(configFile, []byte("mynetworks = 127.0.0.0/8, 10.0.0.0/8\n"), 0644); err != nil {
		t.Fatalf("Failed to modify config file: %v", err)
	}

	// Should detect change
	if !s.FileHasChanged(context.Background(), configFile) {
		t.Error("Modified file should be detected as changed")
	}

	// Clear FirstRun flag
	s.FirstRun = false

	// With changed section
	section := &config.MtaConfigSection{Name: "mta", Changed: true}
	if !s.ShouldRewriteSection(context.Background(), "mta", section) {
		t.Error("Changed section should trigger rewrite")
	}
}
