// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package configmgr

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/state"
)

func TestConfigManager_BasicIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	// Skip if Carbonio environment is not available
	if _, err := os.Stat("/opt/zextras/conf/localconfig.xml"); os.IsNotExist(err) {
		t.Skip("Skipping: Carbonio environment not available (/opt/zextras/conf/localconfig.xml missing)")
	}

	ctx := context.Background()
	// Initialize logger for testing

	// Create test configuration
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-test"

	// Create application state
	appState := state.NewState()

	// Create LDAP client
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Create ConfigManager
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Test loading configurations
	t.Run("LoadAllConfigs", func(t *testing.T) {
		// Use a timeout to prevent hanging
		done := make(chan error, 1)

		go func() {
			done <- cm.LoadAllConfigs(context.Background())
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("LoadAllConfigs failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("LoadAllConfigs timed out")
		}
	})

	// Test configuration lookup
	t.Run("LookUpConfig", func(t *testing.T) {
		// Test looking up a known configuration
		value, err := cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value == "" {
			t.Error("Expected non-empty configuration value")
		}
	})

	// Test configuration changes
	t.Run("ConfigChanges", func(t *testing.T) {
		// Simulate a configuration change
		err := cm.CompareKeys(context.Background())
		if err != nil {
			t.Errorf("CompareKeys failed: %v", err)
		}

		// Check if changes were detected
		if len(cm.State.ChangedKeys) > 0 {
			t.Logf("Detected configuration changes in %d sections", len(cm.State.ChangedKeys))
		}
	})

	// Test error handling for invalid config type
	t.Run("InvalidConfigType", func(t *testing.T) {
		_, err := cm.LookUpConfig(ctx, "invalid", "test_key")
		if err == nil {
			t.Error("Expected error for invalid config type")
		}
	})
}

func TestConfigManager_ConcurrentAccess_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	// Skip if Carbonio environment is not available
	if _, err := os.Stat("/opt/zextras/conf/localconfig.xml"); os.IsNotExist(err) {
		t.Skip("Skipping: Carbonio environment not available (/opt/zextras/conf/localconfig.xml missing)")
	}

	ctx := context.Background()

	// Create test components
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configuration
	done := make(chan error, 1)
	go func() {
		done <- cm.LoadAllConfigs(context.Background())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadAllConfigs failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadAllConfigs timed out")
	}

	// Test concurrent lookups
	const numGoroutines = 10
	const numLookups = 5

	completed := make(chan bool, numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer func() { completed <- true }()

			for j := range numLookups {
				_, err := cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
				if err != nil {
					t.Errorf("Concurrent lookup %d-%d failed: %v", id, j, err)
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for range numGoroutines {
		select {
		case <-completed:
			// Goroutine completed successfully
		case <-time.After(10 * time.Second):
			t.Error("Concurrent operations timed out")
		}
	}
}

func TestConfigManager_ErrorHandling_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	// Create test components
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	t.Run("InvalidKey", func(t *testing.T) {
		_, err := cm.LookUpConfig(ctx, "LOCAL", "invalid_key_that_does_not_exist")
		if err != nil {
			t.Logf("Expected error for invalid key: %v", err)
		}
	})

	t.Run("EmptyConfigType", func(t *testing.T) {
		_, err := cm.LookUpConfig(ctx, "", "some_key")
		if err == nil {
			t.Error("Expected error for empty config type")
		}
	})

	t.Run("EmptyKey", func(t *testing.T) {
		_, err := cm.LookUpConfig(ctx, "LOCAL", "")
		if err == nil {
			t.Error("Expected error for empty key")
		}
	})
}
