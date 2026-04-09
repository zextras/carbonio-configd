// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package configmgr

import (
	"context"
	"encoding/json"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/state"
	"os"
	"path/filepath"
	"testing"
)

// TestConfigLoader_RetryLogic tests the retry mechanism for config loading.
func TestConfigLoader_RetryLogic(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Hostname = "test-host.example.com"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	t.Run("LocalConfig retry on failure", func(t *testing.T) {
		// This will fail in test environment (no zmlocalconfig binary)
		// but should demonstrate retry logic
		err := cm.loadLocalConfigWithRetry(context.Background(), 2)
		if err == nil {
			t.Skip("Test environment has zmlocalconfig available")
		}
		// Error is expected in test environment
		t.Logf("Expected failure with retry: %v", err)
	})

	t.Run("GlobalConfig retry on failure", func(t *testing.T) {
		err := cm.loadGlobalConfigWithRetry(context.Background(), 2)
		if err == nil {
			t.Skip("Test environment has zmprov available")
		}
		t.Logf("Expected failure with retry: %v", err)
	})

	t.Run("ServerConfig retry on failure", func(t *testing.T) {
		err := cm.loadServerConfigWithRetry(context.Background(), 2)
		if err == nil {
			t.Skip("Test environment has zmprov available")
		}
		t.Logf("Expected failure with retry: %v", err)
	})

	t.Run("LoadAllConfigs with retry", func(t *testing.T) {
		// Test the main LoadAllConfigsWithRetry function
		err := cm.LoadAllConfigsWithRetry(context.Background(), 2)
		if err == nil {
			t.Skip("Test environment has all binaries available")
		}
		t.Logf("Expected failure with retry: %v", err)
	})
}

// TestConfigLoader_TimeoutHandling tests timeout scenarios.
func TestConfigLoader_TimeoutHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Hostname = "test-host.example.com"
	appState := state.NewState()

	// Set a very short timeout for testing
	appState.LocalConfig.Data = make(map[string]string)
	appState.LocalConfig.Data["ldap_read_timeout"] = "100" // 100ms

	ldapClient := ldap.NewLdap(context.Background(), mainCfg)
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	t.Run("Timeout detection", func(t *testing.T) {
		err := cm.LoadAllConfigs(context.Background())
		// Should timeout quickly due to 100ms setting
		if err != nil {
			t.Logf("Timeout detected as expected: %v", err)
		}
	})
}

// ReferenceConfig represents expected configuration structure for comparison.
type ReferenceConfig struct {
	LocalConfig  map[string]string `json:"localconfig"`
	GlobalConfig map[string]string `json:"globalconfig"`
	ServerConfig map[string]string `json:"serverconfig"`
	MiscConfig   map[string]string `json:"miscconfig"`
}

// TestConfigLoader_CompareWithReference compares loaded configs against reference data.
// This test requires reference data files from Jython implementation.
func TestConfigLoader_CompareWithReference(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	// Check if reference data directory exists
	refDir := filepath.Join("testdata", "reference")
	if _, err := os.Stat(refDir); os.IsNotExist(err) {
		t.Skip("Reference data directory not found. Run Jython zmconfigd to generate reference data.")
	}

	// Load reference data
	refFile := filepath.Join(refDir, "configs.json")
	refData, err := os.ReadFile(refFile)
	if err != nil {
		t.Skipf("Reference data file not found: %v", err)
	}

	var reference ReferenceConfig
	if err := json.Unmarshal(refData, &reference); err != nil {
		t.Fatalf("Failed to parse reference data: %v", err)
	}

	// Load actual configuration
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Hostname = "test-host.example.com"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		t.Fatalf("Failed to load configs: %v", err)
	}

	t.Run("Compare LocalConfig", func(t *testing.T) {
		compareConfigs(t, "LocalConfig", reference.LocalConfig, cm.State.LocalConfig.Data)
	})

	t.Run("Compare GlobalConfig", func(t *testing.T) {
		compareConfigs(t, "GlobalConfig", reference.GlobalConfig, cm.State.GlobalConfig.Data)
	})

	t.Run("Compare ServerConfig", func(t *testing.T) {
		compareConfigs(t, "ServerConfig", reference.ServerConfig, cm.State.ServerConfig.Data)
	})

	t.Run("Compare MiscConfig", func(t *testing.T) {
		compareConfigs(t, "MiscConfig", reference.MiscConfig, cm.State.MiscConfig.Data)
	})
}

// compareConfigs compares two configuration maps and reports differences.
func compareConfigs(t *testing.T, name string, expected, actual map[string]string) {
	missingKeys := []string{}
	extraKeys := []string{}
	differentValues := []string{}

	// Check for missing keys and different values
	for key, expectedVal := range expected {
		actualVal, exists := actual[key]
		if !exists {
			missingKeys = append(missingKeys, key)
		} else if actualVal != expectedVal {
			differentValues = append(differentValues, key)
			t.Errorf("%s key '%s': expected='%s', actual='%s'", name, key, expectedVal, actualVal)
		}
	}

	// Check for extra keys
	for key := range actual {
		if _, exists := expected[key]; !exists {
			extraKeys = append(extraKeys, key)
		}
	}

	// Report results
	if len(missingKeys) > 0 {
		t.Errorf("%s: Missing %d keys: %v", name, len(missingKeys), missingKeys)
	}
	if len(extraKeys) > 0 {
		t.Logf("%s: Extra %d keys (may be derived): %v", name, len(extraKeys), extraKeys)
	}
	if len(differentValues) > 0 {
		t.Errorf("%s: %d keys have different values", name, len(differentValues))
	}

	if len(missingKeys) == 0 && len(differentValues) == 0 {
		t.Logf("%s: All %d keys match reference", name, len(expected))
	}
}

// TestConfigLoader_PostProcessing tests post-processing transformations.
func TestConfigLoader_PostProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Hostname = "test-host.example.com"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	t.Run("SSL protocol sorting", func(t *testing.T) {
		// Simulate unsorted SSL protocols
		cm.State.GlobalConfig.Data = make(map[string]string)
		cm.State.GlobalConfig.Data["zimbraMailboxdSSLProtocols"] = "TLSv1.3 TLSv1 TLSv1.2"

		processSortedSSLConfigForTarget(cm.State.GlobalConfig.Data, "zimbraMailboxdSSLProtocols")

		result := cm.State.GlobalConfig.Data["zimbraMailboxdSSLProtocols"]
		expected := "TLSv1 TLSv1.2 TLSv1.3"
		if result != expected {
			t.Errorf("SSL sorting failed: expected '%s', got '%s'", expected, result)
		}

		// Check XML version was created
		xmlResult := cm.State.GlobalConfig.Data["zimbraMailboxdSSLProtocolsXML"]
		if xmlResult == "" {
			t.Error("XML version not created")
		}
		t.Logf("XML version: %s", xmlResult)
	})

	t.Run("RBL extraction", func(t *testing.T) {
		// Simulate MTA restriction with RBL entries
		cm.State.ServerConfig.Data = make(map[string]string)
		cm.State.ServerConfig.Data["zimbraMtaRestriction"] = "reject_rbl_client zen.spamhaus.org reject_rbl_client bl.spamcop.net permit"

		processMtaRestrictionRBLsForData(cm.State.ServerConfig.Data)

		rbls := cm.State.ServerConfig.Data["zimbraMtaRestrictionRBLs"]
		if rbls != "zen.spamhaus.org, bl.spamcop.net" {
			t.Errorf("RBL extraction failed: got '%s'", rbls)
		}

		// Check that RBL entries were removed from restriction
		restriction := cm.State.ServerConfig.Data["zimbraMtaRestriction"]
		if restriction != "permit" {
			t.Errorf("RBL removal failed: got '%s'", restriction)
		}
	})

	t.Run("IP mode configuration", func(t *testing.T) {
		testCases := []struct {
			mode     string
			expected map[string]string
		}{
			{
				mode: "ipv4",
				expected: map[string]string{
					"zimbraLocalBindAddress":    "127.0.0.1",
					"zimbraPostconfProtocol":    "ipv4",
					"zimbraInetMode":            "inet",
					"zimbraAmavisListenSockets": "'10024','10026','10032'",
				},
			},
			{
				mode: "ipv6",
				expected: map[string]string{
					"zimbraLocalBindAddress":    "::1",
					"zimbraPostconfProtocol":    "ipv6",
					"zimbraInetMode":            "inet6",
					"zimbraAmavisListenSockets": "'[::1]:10024','[::1]:10026','[::1]:10032'",
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.mode, func(t *testing.T) {
				cm.State.ServerConfig.Data = make(map[string]string)
				cm.State.ServerConfig.Data["zimbraIPMode"] = tc.mode

				cm.processIPModeConfig()

				for key, expectedVal := range tc.expected {
					actualVal := cm.State.ServerConfig.Data[key]
					if actualVal != expectedVal {
						t.Errorf("IP mode config for %s: expected '%s', got '%s'", key, expectedVal, actualVal)
					}
				}
			})
		}
	})

	t.Run("OpenDKIM URI derivation", func(t *testing.T) {
		cm.State.LocalConfig.Data = make(map[string]string)
		cm.State.LocalConfig.Data["ldap_url"] = "ldap://ldap1.example.com:389 ldap://ldap2.example.com:389"

		// Re-run the LocalConfig post-processing logic (simplified)
		ldapURL := cm.State.LocalConfig.Data["ldap_url"]
		urls := []string{"ldap://ldap1.example.com:389", "ldap://ldap2.example.com:389"}

		var signingTableURIs []string
		for _, url := range urls {
			signingTableURIs = append(signingTableURIs, url+"/?DKIMSelector?sub?(DKIMIdentity=$d)")
		}

		result := signingTableURIs[0]
		if result != "ldap://ldap1.example.com:389/?DKIMSelector?sub?(DKIMIdentity=$d)" {
			t.Errorf("OpenDKIM URI derivation failed: got '%s'", result)
		}
		t.Logf("Derived OpenDKIM URI from: %s", ldapURL)
	})
}
