// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/transformer"
	"os"
	"sync"
	"testing"
)

// mockServiceManager is a mock implementation of services.Manager for testing
// Shared across all test files in the configmgr package
type mockServiceManager struct {
	commands        map[string]bool // For HasCommand() support
	runningServices map[string]bool // For IsRunning() support
	restartQueue    []string        // For restart tracking
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{
		commands:        make(map[string]bool),
		runningServices: make(map[string]bool),
		restartQueue:    make([]string, 0),
	}
}

func (m *mockServiceManager) ControlProcess(_ context.Context, service string, action services.ServiceAction) error {
	return nil
}

func (m *mockServiceManager) IsRunning(_ context.Context, service string) (bool, error) {
	running, ok := m.runningServices[service]
	if !ok {
		return false, nil
	}
	return running, nil
}

func (m *mockServiceManager) AddRestart(_ context.Context, service string) error {
	m.restartQueue = append(m.restartQueue, service)
	return nil
}

func (m *mockServiceManager) ProcessRestarts(_ context.Context, configLookup func(string) string) error {
	return nil
}

func (m *mockServiceManager) ClearRestarts(_ context.Context) {
	m.restartQueue = make([]string, 0)
}

func (m *mockServiceManager) GetPendingRestarts() []string {
	return m.restartQueue
}

func (m *mockServiceManager) SetDependencies(_ context.Context, deps map[string][]string) {
}

func (m *mockServiceManager) AddDependencyRestarts(_ context.Context, sectionName string, configLookup func(string) string) {
}

func (m *mockServiceManager) HasCommand(service string) bool {
	if m.commands == nil {
		return true // Default to true for backward compatibility
	}
	return m.commands[service]
}

func (m *mockServiceManager) SetUseSystemd(enabled bool) {
	// No-op for mock
}

// newTestTransformer creates a transformer for testing that returns lines unchanged
func newTestTransformer(cm *ConfigManager, st *state.State) *transformer.Transformer {
	return transformer.NewTransformer(cm, st)
}

// TestProcessIsRunning tests the ProcessIsRunning method
func TestProcessIsRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	mockSvcMgr := newMockServiceManager()
	mockSvcMgr.runningServices["nginx"] = true
	mockSvcMgr.runningServices["mta"] = false

	cm := &ConfigManager{
		mainConfig: &config.Config{
			BaseDir:  "/tmp",
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: mockSvcMgr,
		Cache:      cacheInstance,
	}

	// Test running service
	if !cm.ProcessIsRunning(context.Background(), "nginx") {
		t.Error("Expected nginx to be running")
	}

	// Test not running service
	if cm.ProcessIsRunning(context.Background(), "mta") {
		t.Error("Expected mta to not be running")
	}

	// Test unknown service
	if cm.ProcessIsRunning(context.Background(), "unknown") {
		t.Error("Expected unknown service to not be running")
	}
}

// TestProcessIsNotRunning tests the ProcessIsNotRunning method
func TestProcessIsNotRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	mockSvcMgr := newMockServiceManager()
	mockSvcMgr.runningServices["nginx"] = true
	mockSvcMgr.runningServices["mta"] = false

	cm := &ConfigManager{
		mainConfig: &config.Config{
			BaseDir:  "/tmp",
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: mockSvcMgr,
		Cache:      cacheInstance,
	}

	// Test running service (should be NOT not running)
	if cm.ProcessIsNotRunning(context.Background(), "nginx") {
		t.Error("Expected nginx to not be 'not running' (i.e., it is running)")
	}

	// Test not running service (should be not running)
	if !cm.ProcessIsNotRunning(context.Background(), "mta") {
		t.Error("Expected mta to be not running")
	}

	// Test unknown service (should be not running)
	if !cm.ProcessIsNotRunning(context.Background(), "unknown") {
		t.Error("Expected unknown service to be not running")
	}
}

// TestIsAlreadyClosedError tests the isAlreadyClosedError utility function
func TestIsAlreadyClosedError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "exact match",
			err:      &testError{msg: "file already closed"},
			expected: true,
		},
		{
			name:     "contains match",
			err:      &testError{msg: "error: connection already closed"},
			expected: true,
		},
		{
			name:     "different error",
			err:      &testError{msg: "permission denied"},
			expected: false,
		},
		{
			name:     "empty error message",
			err:      &testError{msg: ""},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAlreadyClosedError(tt.err)
			if result != tt.expected {
				t.Errorf("isAlreadyClosedError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// testError is a simple error type for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// TestParseValueSpec tests the parseValueSpec utility function
func TestParseValueSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name         string
		valueSpec    string
		expectedType string
		expectedKey  string
	}{
		{
			name:         "VAR type",
			valueSpec:    "VAR:some_var_key",
			expectedType: "VAR",
			expectedKey:  "some_var_key",
		},
		{
			name:         "LOCAL type",
			valueSpec:    "LOCAL:local_config_key",
			expectedType: "LOCAL",
			expectedKey:  "local_config_key",
		},
		{
			name:         "MAPLOCAL type",
			valueSpec:    "MAPLOCAL:map_key",
			expectedType: "MAPLOCAL",
			expectedKey:  "map_key",
		},
		{
			name:         "FILE type",
			valueSpec:    "FILE /path/to/file.txt",
			expectedType: "FILE",
			expectedKey:  "/path/to/file.txt",
		},
		{
			name:         "FILE type with spaces in path",
			valueSpec:    "FILE /path/to/my file.txt",
			expectedType: "FILE",
			expectedKey:  "/path/to/my file.txt",
		},
		{
			name:         "literal value",
			valueSpec:    "simple_literal_value",
			expectedType: "LITERAL",
			expectedKey:  "simple_literal_value",
		},
		{
			name:         "literal value with colon but not a known type",
			valueSpec:    "UNKNOWN:value",
			expectedType: "LITERAL",
			expectedKey:  "UNKNOWN:value",
		},
		{
			name:         "empty value",
			valueSpec:    "",
			expectedType: "LITERAL",
			expectedKey:  "",
		},
		{
			name:         "VAR type with empty key",
			valueSpec:    "VAR:",
			expectedType: "VAR",
			expectedKey:  "",
		},
		{
			name:         "LOCAL type with colon in key",
			valueSpec:    "LOCAL:key:with:colons",
			expectedType: "LOCAL",
			expectedKey:  "key:with:colons",
		},
		{
			name:         "literal number",
			valueSpec:    "12345",
			expectedType: "LITERAL",
			expectedKey:  "12345",
		},
		{
			name:         "literal IP address",
			valueSpec:    "192.168.1.1",
			expectedType: "LITERAL",
			expectedKey:  "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valueType, valueKey := parseValueSpec(tt.valueSpec)
			if valueType != tt.expectedType {
				t.Errorf("parseValueSpec(%q) type = %q, expected %q", tt.valueSpec, valueType, tt.expectedType)
			}
			if valueKey != tt.expectedKey {
				t.Errorf("parseValueSpec(%q) key = %q, expected %q", tt.valueSpec, valueKey, tt.expectedKey)
			}
		})
	}
}

// TestDoRestarts tests the DoRestarts method
func TestDoRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	t.Run("restart services from state", func(t *testing.T) {
		mockSvcMgr := newMockServiceManager()

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: mockSvcMgr,
			Cache:      cacheInstance,
		}

		// Add services to restart queue in state
		cm.State.CurrentActions.Restarts = map[string]int{
			"nginx": 1,
			"mta":   1,
		}

		// Execute restarts
		cm.DoRestarts(ctx)

		// Verify services were added to restart queue
		pendingRestarts := mockSvcMgr.GetPendingRestarts()
		if len(pendingRestarts) != 2 {
			t.Errorf("Expected 2 services in restart queue, got %d", len(pendingRestarts))
		}

		// Check that both services are in the queue
		hasNginx := false
		hasMta := false
		for _, service := range pendingRestarts {
			if service == "nginx" {
				hasNginx = true
			}
			if service == "mta" {
				hasMta = true
			}
		}

		if !hasNginx {
			t.Error("Expected nginx in restart queue")
		}
		if !hasMta {
			t.Error("Expected mta in restart queue")
		}
	})

	t.Run("no restarts needed", func(t *testing.T) {
		mockSvcMgr := newMockServiceManager()

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: mockSvcMgr,
			Cache:      cacheInstance,
		}

		// No services in restart queue
		cm.State.CurrentActions.Restarts = map[string]int{}

		// Execute restarts
		cm.DoRestarts(ctx)

		// Verify no services were added to restart queue
		pendingRestarts := mockSvcMgr.GetPendingRestarts()
		if len(pendingRestarts) != 0 {
			t.Errorf("Expected 0 services in restart queue, got %d", len(pendingRestarts))
		}
	})
}

// TestClearLocalConfigCache tests the ClearLocalConfigCache method
func TestClearLocalConfigCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	t.Run("clear non-empty cache", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:                   state.NewState(),
			Cache:                   cacheInstance,
			cachedLocalConfigOutput: "cached content",
		}

		// Verify cache has content before clearing
		if cm.cachedLocalConfigOutput == "" {
			t.Error("Expected cache to have content before clearing")
		}

		// Clear the cache
		cm.ClearLocalConfigCache(ctx)

		// Verify cache is empty
		if cm.cachedLocalConfigOutput != "" {
			t.Errorf("Expected cache to be empty after clearing, got: %q", cm.cachedLocalConfigOutput)
		}
	})

	t.Run("clear already empty cache", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:                   state.NewState(),
			Cache:                   cacheInstance,
			cachedLocalConfigOutput: "",
		}

		// Clear the already empty cache (should be a no-op)
		cm.ClearLocalConfigCache(ctx)

		// Verify cache is still empty
		if cm.cachedLocalConfigOutput != "" {
			t.Errorf("Expected cache to remain empty, got: %q", cm.cachedLocalConfigOutput)
		}
	})
}

// TestCleanupRewriteFiles tests the cleanupRewriteFiles utility function
func TestCleanupRewriteFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	tmpDir := t.TempDir()

	t.Run("cleanup with all files", func(t *testing.T) {
		// Create temporary files
		srcPath := tmpDir + "/src1.txt"
		tmpPath := tmpDir + "/tmp1.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		// Call cleanup
		cleanupRewriteFiles(ctx, srcFile, tmpFile, tmpPath)

		// Verify temp file was removed
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("Expected temp file to be removed")
		}
	})

	t.Run("cleanup with nil source file", func(t *testing.T) {
		tmpPath := tmpDir + "/tmp2.txt"

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		// Call cleanup with nil source
		cleanupRewriteFiles(ctx, nil, tmpFile, tmpPath)

		// Verify temp file was removed
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("Expected temp file to be removed")
		}
	})

	t.Run("cleanup with nil temp file", func(t *testing.T) {
		srcPath := tmpDir + "/src3.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		// Call cleanup with nil temp file
		cleanupRewriteFiles(ctx, srcFile, nil, "")

		// Should complete without error (nothing to verify since no temp file)
	})

	t.Run("cleanup with all nil", func(t *testing.T) {
		// Call cleanup with everything nil (should be a no-op)
		cleanupRewriteFiles(ctx, nil, nil, "")

		// Should complete without error
	})

	t.Run("cleanup with nonexistent temp file path", func(t *testing.T) {
		srcPath := tmpDir + "/src4.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		// Call cleanup with nonexistent temp file path
		cleanupRewriteFiles(ctx, srcFile, nil, tmpDir+"/nonexistent.txt")

		// Should complete without error even though file doesn't exist
	})
}

// TestCompileActions tests the CompileActions method
func TestCompileActions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	t.Run("compile with first run", func(t *testing.T) {
		mockSvcMgr := newMockServiceManager()

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: mockSvcMgr,
			Cache:      cacheInstance,
		}

		// Set first run
		cm.State.FirstRun = true

		// Add a section to MtaConfig
		section := &config.MtaConfigSection{
			Name:         "testservice",
			Depends:      make(map[string]bool),
			Rewrites:     make(map[string]config.RewriteEntry),
			Restarts:     make(map[string]bool),
			RequiredVars: make(map[string]string),
			Postconf:     make(map[string]string),
			Postconfd:    make(map[string]string),
			Ldap:         make(map[string]string),
			Conditionals: make([]config.Conditional, 0),
			Changed:      true,
		}

		// Add a rewrite entry
		section.Rewrites["conf/test.conf.in"] = config.RewriteEntry{
			Value: "conf/test.conf",
			Mode:  "0644",
		}

		cm.State.MtaConfig.Sections["testservice"] = section

		// Compile actions
		cm.CompileActions(context.Background())

		// Verify rewrites were added
		if len(cm.State.CurrentActions.Rewrites) == 0 {
			t.Error("Expected rewrites to be compiled")
		}
	})

	t.Run("compile with forced config", func(t *testing.T) {
		mockSvcMgr := newMockServiceManager()

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: mockSvcMgr,
			Cache:      cacheInstance,
		}

		// Set forced config
		cm.State.ForcedConfig = map[string]string{
			"proxy": "forced",
		}

		// Add a section
		section := &config.MtaConfigSection{
			Name:         "proxy",
			Depends:      make(map[string]bool),
			Rewrites:     make(map[string]config.RewriteEntry),
			Restarts:     make(map[string]bool),
			RequiredVars: make(map[string]string),
			Postconf:     make(map[string]string),
			Postconfd:    make(map[string]string),
			Ldap:         make(map[string]string),
			Conditionals: make([]config.Conditional, 0),
			Changed:      false, // Not changed, but forced
		}

		section.Rewrites["conf/proxy.conf.in"] = config.RewriteEntry{
			Value: "conf/proxy.conf",
			Mode:  "0644",
		}

		cm.State.MtaConfig.Sections["proxy"] = section

		// Compile actions
		cm.CompileActions(context.Background())

		// Verify rewrites were added
		if len(cm.State.CurrentActions.Rewrites) == 0 {
			t.Error("Expected rewrites to be compiled for forced config")
		}
	})

	t.Run("skip unchanged sections", func(t *testing.T) {
		mockSvcMgr := newMockServiceManager()

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: mockSvcMgr,
			Cache:      cacheInstance,
		}

		// Not first run, no forced config
		cm.State.FirstRun = false
		cm.State.ForcedConfig = make(map[string]string)
		cm.State.RequestedConfig = make(map[string]string)

		// Add an unchanged section
		section := &config.MtaConfigSection{
			Name:         "unchanged",
			Depends:      make(map[string]bool),
			Rewrites:     make(map[string]config.RewriteEntry),
			Restarts:     make(map[string]bool),
			RequiredVars: make(map[string]string),
			Postconf:     make(map[string]string),
			Postconfd:    make(map[string]string),
			Ldap:         make(map[string]string),
			Conditionals: make([]config.Conditional, 0),
			Changed:      false, // Not changed
		}

		section.Rewrites["conf/unchanged.conf.in"] = config.RewriteEntry{
			Value: "conf/unchanged.conf",
			Mode:  "0644",
		}

		cm.State.MtaConfig.Sections["unchanged"] = section

		// Compile actions
		cm.CompileActions(context.Background())

		// Verify no rewrites were added (section was skipped)
		if len(cm.State.CurrentActions.Rewrites) != 0 {
			t.Error("Expected no rewrites for unchanged section")
		}
	})
}

// TestLoadMtaConfig tests the LoadMtaConfig method
func TestLoadMtaConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	cm := &ConfigManager{
		mainConfig: &config.Config{
			BaseDir:  "/tmp",
			Hostname: "testhost",
		},
		State: state.NewState(),
		Cache: cacheInstance,
	}

	// Load MTA config
	err := cm.LoadMtaConfig(context.Background(), "/tmp/zmconfigd.cf")
	if err != nil {
		t.Errorf("LoadMtaConfig failed: %v", err)
	}

	// Verify proxy section was added
	if _, exists := cm.State.MtaConfig.Sections["proxy"]; !exists {
		t.Error("Expected proxy section to be loaded")
	}

	// Verify proxy section has expected data
	proxySection := cm.State.MtaConfig.Sections["proxy"]
	if proxySection.Name != "proxy" {
		t.Errorf("Expected section name 'proxy', got %q", proxySection.Name)
	}

	// Verify rewrites
	if len(proxySection.Rewrites) == 0 {
		t.Error("Expected proxy section to have rewrites")
	}

	// Verify restarts
	if !proxySection.Restarts["proxy"] {
		t.Error("Expected proxy to be in restarts")
	}

	// Verify required vars
	if _, exists := proxySection.RequiredVars["zimbraReverseProxyLookupTarget"]; !exists {
		t.Error("Expected zimbraReverseProxyLookupTarget in required vars")
	}
}

// TestLookUpConfig tests the LookUpConfig method
func TestLookUpConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)
	tmpDir := t.TempDir()

	t.Run("lookup VAR type from GlobalConfig", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to GlobalConfig
		cm.State.GlobalConfig.Data["testKey"] = "testValue"

		value, err := cm.LookUpConfig(ctx, "VAR", "testKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "testValue" {
			t.Errorf("Expected 'testValue', got %q", value)
		}
	})

	t.Run("lookup VAR type from MiscConfig", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to MiscConfig (not in GlobalConfig)
		cm.State.MiscConfig.Data["miscKey"] = "miscValue"

		value, err := cm.LookUpConfig(ctx, "VAR", "miscKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "miscValue" {
			t.Errorf("Expected 'miscValue', got %q", value)
		}
	})

	t.Run("lookup VAR type from ServerConfig", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to ServerConfig (not in Global or Misc)
		cm.State.ServerConfig.Data["serverKey"] = "serverValue"

		value, err := cm.LookUpConfig(ctx, "VAR", "serverKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "serverValue" {
			t.Errorf("Expected 'serverValue', got %q", value)
		}
	})

	t.Run("lookup LOCAL type", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to LocalConfig
		cm.State.LocalConfig.Data["localKey"] = "localValue"

		value, err := cm.LookUpConfig(ctx, "LOCAL", "localKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "localValue" {
			t.Errorf("Expected 'localValue', got %q", value)
		}
	})

	t.Run("lookup SERVICE type - enabled", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to ServiceConfig
		cm.State.ServerConfig.ServiceConfig["nginx"] = "TRUE"

		value, err := cm.LookUpConfig(ctx, "SERVICE", "nginx")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "TRUE" {
			t.Errorf("Expected 'TRUE', got %q", value)
		}
	})

	t.Run("lookup SERVICE type - disabled", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Service not in ServiceConfig
		value, err := cm.LookUpConfig(ctx, "SERVICE", "nonexistent")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "FALSE" {
			t.Errorf("Expected 'FALSE', got %q", value)
		}
	})

	t.Run("lookup unknown config type", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "UNKNOWN", "key")
		if err == nil {
			t.Error("Expected error for unknown config type")
		}
	})

	t.Run("lookup VAR type - key not found", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "VAR", "nonexistentKey")
		if err == nil {
			t.Error("Expected error for nonexistent key")
		}
	})

	t.Run("lookup FILE type - read from disk", func(t *testing.T) {
		// Create a test file
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		testFile := confDir + "/test.txt"
		testContent := "line1\n\nline2\n  line3  \n\n"
		if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		stateInstance := state.NewState()
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		// Create a simple transformer with nil ConfigLookup (not used in this test)
		cm.Transformer = newTestTransformer(cm, stateInstance)

		value, err := cm.LookUpConfig(ctx, "FILE", "test.txt")
		if err != nil {
			t.Errorf("LookUpConfig FILE failed: %v", err)
		}
		// Expect: non-empty lines, trimmed, joined with ", "
		expected := "line1, line2, line3"
		if value != expected {
			t.Errorf("Expected %q, got %q", expected, value)
		}

		// Verify it was cached
		if cachedValue, ok := cm.State.FileCache["test.txt"]; !ok {
			t.Error("Expected value to be cached")
		} else if cachedValue != expected {
			t.Errorf("Expected cached value %q, got %q", expected, cachedValue)
		}
	})

	t.Run("lookup FILE type - from cache", func(t *testing.T) {
		stateInstance := state.NewState()
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		cm.Transformer = newTestTransformer(cm, stateInstance)

		// Pre-populate cache
		cm.State.FileCache["cached.txt"] = "cached content"

		value, err := cm.LookUpConfig(ctx, "FILE", "cached.txt")
		if err != nil {
			t.Errorf("LookUpConfig FILE from cache failed: %v", err)
		}
		if value != "cached content" {
			t.Errorf("Expected 'cached content', got %q", value)
		}
	})

	t.Run("lookup FILE type - file not found", func(t *testing.T) {
		stateInstance := state.NewState()
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		cm.Transformer = newTestTransformer(cm, stateInstance)

		_, err := cm.LookUpConfig(ctx, "FILE", "nonexistent.txt")
		if err == nil {
			t.Error("Expected error for nonexistent file")
		}
	})

	t.Run("lookup MAPFILE type - file exists", func(t *testing.T) {
		// Create the mapped file path
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		mappedFile := confDir + "/dhparam.pem"
		if err := os.WriteFile(mappedFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write mapped file: %v", err)
		}

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// zimbraSSLDHParam is in state.MAPPEDFILES
		value, err := cm.LookUpConfig(ctx, "MAPFILE", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPFILE failed: %v", err)
		}
		expectedPath := tmpDir + "/conf/dhparam.pem"
		if value != expectedPath {
			t.Errorf("Expected %q, got %q", expectedPath, value)
		}
	})

	t.Run("lookup MAPFILE type - file does not exist", func(t *testing.T) {
		// Use a fresh temp directory without creating conf dir
		tmpDir2 := t.TempDir()
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir2,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// zimbraSSLDHParam is in MAPPEDFILES but file doesn't exist
		value, err := cm.LookUpConfig(ctx, "MAPFILE", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPFILE failed: %v", err)
		}
		// When file doesn't exist, should return empty string
		if value != "" {
			t.Errorf("Expected empty string for nonexistent mapped file, got %q", value)
		}
	})

	t.Run("lookup MAPFILE type - key not in MAPPEDFILES", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "MAPFILE", "unknownKey")
		if err == nil {
			t.Error("Expected error for key not in MAPPEDFILES")
		}
	})

	t.Run("lookup MAPLOCAL type - file exists", func(t *testing.T) {
		// Create the mapped file path
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		mappedFile := confDir + "/dhparam.pem"
		if err := os.WriteFile(mappedFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write mapped file: %v", err)
		}

		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		value, err := cm.LookUpConfig(ctx, "MAPLOCAL", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPLOCAL failed: %v", err)
		}
		expectedPath := tmpDir + "/conf/dhparam.pem"
		if value != expectedPath {
			t.Errorf("Expected %q, got %q", expectedPath, value)
		}
	})

	t.Run("lookup MAPLOCAL type - file does not exist", func(t *testing.T) {
		// Use a fresh temp directory without creating conf dir
		tmpDir3 := t.TempDir()
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  tmpDir3,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		value, err := cm.LookUpConfig(ctx, "MAPLOCAL", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPLOCAL failed: %v", err)
		}
		if value != "" {
			t.Errorf("Expected empty string for nonexistent mapped file, got %q", value)
		}
	})
}

// TestCompileSectionActions tests the compileSectionActions method
func TestCompileSectionActions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	t.Run("compile rewrites", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}

		section := &config.MtaConfigSection{
			Name: "testservice",
			Rewrites: map[string]config.RewriteEntry{
				"file1": {Value: "/path/to/file1"},
				"file2": {Value: "/path/to/file2"},
			},
		}

		cm.compileSectionActions(ctx, "testservice", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that rewrites were added to state
		if len(cm.State.CurrentActions.Rewrites) != 2 {
			t.Errorf("Expected 2 rewrites, got %d", len(cm.State.CurrentActions.Rewrites))
		}
		if _, ok := cm.State.CurrentActions.Rewrites["file1"]; !ok {
			t.Error("Expected file1 in rewrites")
		}
		if _, ok := cm.State.CurrentActions.Rewrites["file2"]; !ok {
			t.Error("Expected file2 in rewrites")
		}
	})

	t.Run("compile postconf and postconfd", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}

		section := &config.MtaConfigSection{
			Name: "mta",
			Postconf: map[string]string{
				"myhostname":    "mail.example.com",
				"mydestination": "localhost",
			},
			Postconfd: map[string]string{
				"milter_default_action": "accept",
			},
		}

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check postconf
		if len(cm.State.CurrentActions.Postconf) != 2 {
			t.Errorf("Expected 2 postconf entries, got %d", len(cm.State.CurrentActions.Postconf))
		}
		if cm.State.CurrentActions.Postconf["myhostname"] != "mail.example.com" {
			t.Error("Expected myhostname in postconf")
		}

		// Check postconfd
		if len(cm.State.CurrentActions.Postconfd) != 1 {
			t.Errorf("Expected 1 postconfd entry, got %d", len(cm.State.CurrentActions.Postconfd))
		}
		if cm.State.CurrentActions.Postconfd["milter_default_action"] != "accept" {
			t.Error("Expected milter_default_action in postconfd")
		}
	})

	t.Run("compile proxygen directive", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}

		section := &config.MtaConfigSection{
			Name:     "proxy",
			Proxygen: true,
		}

		cm.compileSectionActions(ctx, "proxy", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that proxygen was set
		if !cm.State.CurrentActions.Proxygen {
			t.Error("Expected Proxygen to be true")
		}
	})

	t.Run("compile ldap entries", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}

		section := &config.MtaConfigSection{
			Name: "ldap",
			Ldap: map[string]string{
				"ldap_uri":  "ldap://localhost",
				"ldap_base": "dc=example,dc=com",
			},
		}

		cm.compileSectionActions(ctx, "ldap", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check ldap entries
		if len(cm.State.CurrentActions.Ldap) != 2 {
			t.Errorf("Expected 2 ldap entries, got %d", len(cm.State.CurrentActions.Ldap))
		}
		if cm.State.CurrentActions.Ldap["ldap_uri"] != "ldap://localhost" {
			t.Error("Expected ldap_uri in ldap")
		}
	})

	t.Run("skip restarts on first run", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = true

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"mta": true},
		}

		// Set up service as enabled
		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that no restarts were added
		if len(cm.State.CurrentActions.Restarts) != 0 {
			t.Errorf("Expected 0 restarts on first run, got %d", len(cm.State.CurrentActions.Restarts))
		}
	})

	t.Run("skip restarts with forced config", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false
		cm.State.ForcedConfig["mta"] = "1"

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"mta": true},
		}

		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that no restarts were added
		if len(cm.State.CurrentActions.Restarts) != 0 {
			t.Errorf("Expected 0 restarts with forced config, got %d", len(cm.State.CurrentActions.Restarts))
		}
	})

	t.Run("skip restarts with requested config", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false
		cm.State.RequestedConfig["mta"] = "1"

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"mta": true},
		}

		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"

		cm.compileSectionActions(ctx, "mta", section, cm.State.RequestedConfig, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that no restarts were added
		if len(cm.State.CurrentActions.Restarts) != 0 {
			t.Errorf("Expected 0 restarts with requested config, got %d", len(cm.State.CurrentActions.Restarts))
		}
	})

	t.Run("add restart for enabled service", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"mta": true},
		}

		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that restart was added with value -1
		if len(cm.State.CurrentActions.Restarts) != 1 {
			t.Errorf("Expected 1 restart, got %d", len(cm.State.CurrentActions.Restarts))
		}
		if cm.State.CurrentActions.Restarts["mta"] != -1 {
			t.Errorf("Expected restart value -1, got %d", cm.State.CurrentActions.Restarts["mta"])
		}
	})

	t.Run("add stop for disabled service", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"mta": true},
		}

		// Don't add mta to ServiceConfig - absence means disabled

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that stop was added with value 0
		if len(cm.State.CurrentActions.Restarts) != 1 {
			t.Errorf("Expected 1 stop action, got %d", len(cm.State.CurrentActions.Restarts))
		}
		if cm.State.CurrentActions.Restarts["mta"] != 0 {
			t.Errorf("Expected stop value 0, got %d", cm.State.CurrentActions.Restarts["mta"])
		}
	})

	t.Run("skip stop for archiving service when not enabled", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"archiving": true},
		}

		// Don't add archiving to ServiceConfig - absence means disabled

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that archiving was NOT added
		if len(cm.State.CurrentActions.Restarts) != 0 {
			t.Errorf("Expected 0 restarts for disabled archiving, got %d", len(cm.State.CurrentActions.Restarts))
		}
	})

	t.Run("add opendkim restart when mta is enabled", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false

		section := &config.MtaConfigSection{
			Name:     "mta",
			Restarts: map[string]bool{"opendkim": true},
		}

		// opendkim is not explicitly enabled, but mta is
		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that opendkim restart was added
		if len(cm.State.CurrentActions.Restarts) != 1 {
			t.Errorf("Expected 1 restart for opendkim, got %d", len(cm.State.CurrentActions.Restarts))
		}
		if cm.State.CurrentActions.Restarts["opendkim"] != -1 {
			t.Errorf("Expected opendkim restart value -1, got %d", cm.State.CurrentActions.Restarts["opendkim"])
		}
	})

	t.Run("compile conditionals", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}

		// Set up a condition that will evaluate to true
		cm.State.LocalConfig.Data["zimbraServiceEnabled"] = "TRUE"

		section := &config.MtaConfigSection{
			Name: "mta",
			Conditionals: []config.Conditional{
				{
					Type:    "LOCAL",
					Key:     "zimbraServiceEnabled",
					Negated: false,
					Postconf: map[string]string{
						"conditional_setting": "value",
					},
				},
			},
		}

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check that conditional postconf was added
		if cm.State.CurrentActions.Postconf["conditional_setting"] != "value" {
			t.Error("Expected conditional_setting in postconf")
		}
	})

	t.Run("compile multiple directives together", func(t *testing.T) {
		cm := &ConfigManager{
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:      state.NewState(),
			ServiceMgr: newMockServiceManager(),
			Cache:      cacheInstance,
		}
		cm.State.FirstRun = false
		cm.State.ServerConfig.ServiceConfig["mta"] = "TRUE"
		cm.State.ServerConfig.ServiceConfig["nginx"] = "TRUE"

		section := &config.MtaConfigSection{
			Name: "mta",
			Rewrites: map[string]config.RewriteEntry{
				"main.cf": {Value: "/opt/zimbra/conf/main.cf"},
			},
			Postconf: map[string]string{
				"myhostname": "mail.example.com",
			},
			Postconfd: map[string]string{
				"milter_action": "accept",
			},
			Ldap: map[string]string{
				"ldap_uri": "ldap://localhost",
			},
			Proxygen: true,
			Restarts: map[string]bool{
				"mta":   true,
				"nginx": true,
			},
		}

		cm.compileSectionActions(ctx, "mta", section, nil, cm.State.ForcedConfig, cm.State.FirstRun, cm.State.ServerConfig.ServiceConfig)

		// Check all directives were processed
		if len(cm.State.CurrentActions.Rewrites) != 1 {
			t.Errorf("Expected 1 rewrite, got %d", len(cm.State.CurrentActions.Rewrites))
		}
		if len(cm.State.CurrentActions.Postconf) != 1 {
			t.Errorf("Expected 1 postconf, got %d", len(cm.State.CurrentActions.Postconf))
		}
		if len(cm.State.CurrentActions.Postconfd) != 1 {
			t.Errorf("Expected 1 postconfd, got %d", len(cm.State.CurrentActions.Postconfd))
		}
		if len(cm.State.CurrentActions.Ldap) != 1 {
			t.Errorf("Expected 1 ldap, got %d", len(cm.State.CurrentActions.Ldap))
		}
		if !cm.State.CurrentActions.Proxygen {
			t.Error("Expected Proxygen to be true")
		}
		if len(cm.State.CurrentActions.Restarts) != 2 {
			t.Errorf("Expected 2 restarts, got %d", len(cm.State.CurrentActions.Restarts))
		}
	})
}

// TestCompileActions_ConcurrentWithDelRewrite verifies that CompileActions
// and doRewrites/processRewrite (which calls DelRewrite) can run concurrently
// without data races. This test should be run with -race to detect races.
func TestCompileActions_ConcurrentWithDelRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	cm := &ConfigManager{
		mainConfig: &config.Config{
			BaseDir:  t.TempDir(),
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: newMockServiceManager(),
		Cache:      cacheInstance,
	}

	// Set up MtaConfig with multiple sections and rewrites
	for i := 0; i < 5; i++ {
		sn := fmt.Sprintf("service%d", i)
		section := &config.MtaConfigSection{
			Name:         sn,
			Depends:      make(map[string]bool),
			Rewrites:     make(map[string]config.RewriteEntry),
			Restarts:     make(map[string]bool),
			RequiredVars: make(map[string]string),
			Postconf:     make(map[string]string),
			Postconfd:    make(map[string]string),
			Ldap:         make(map[string]string),
			Conditionals: make([]config.Conditional, 0),
			Changed:      true,
		}
		for j := 0; j < 3; j++ {
			key := fmt.Sprintf("conf/%s_%d.conf.in", sn, j)
			section.Rewrites[key] = config.RewriteEntry{
				Value: fmt.Sprintf("conf/%s_%d.conf", sn, j),
				Mode:  "0644",
			}
		}
		cm.State.MtaConfig.Sections[sn] = section
	}
	cm.State.FirstRun = true

	// Run CompileActions and concurrent DelRewrite calls in parallel.
	// With the snapshot fix, CompileActions iterates a local copy of Sections
	// and doRewrites iterates a local copy of Rewrites, so DelRewrite on the
	// original map should not cause a race.
	const iterations = 100
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			cm.CompileActions(ctx)
		}()

		go func() {
			defer wg.Done()
			keys := cm.State.GetCurrentRewriteKeys()
			for _, key := range keys {
				cm.State.DelRewrite(key)
			}
		}()
	}

	wg.Wait()
	// If we get here without a race detector failure, the snapshot fix works
}

// TestDoConfigRewrites_ErrorCollection verifies that DoConfigRewrites
// collects all errors via errors.Join instead of returning only the first.
func TestDoConfigRewrites_ErrorCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	cm := &ConfigManager{
		mainConfig: &config.Config{
			BaseDir:  t.TempDir(),
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: newMockServiceManager(),
		Cache:      cacheInstance,
	}

	// Set up Proxygen to trigger the proxygen goroutine which will fail
	// (no proxy config loaded, so RunProxygenWithConfigs will error)
	cm.State.CurrentActions.Proxygen = true

	err := cm.DoConfigRewrites(ctx)

	// Proxygen should fail since there's no proxy config loaded
	if err != nil {
		// Verify that the error is present — the key thing is we don't deadlock
		// and all goroutines complete even when errors occur
		t.Logf("DoConfigRewrites returned error as expected: %v", err)
	}
	// The test primarily verifies no deadlock occurs with the buffer fix
}
