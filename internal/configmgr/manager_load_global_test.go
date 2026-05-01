// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/commands"
	"testing"
)

// TestFetchGlobalConfig_Success tests successful global config fetching
func TestFetchGlobalConfig_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Mock gacf command to return valid LDAP output
	mockOutput := `zimbraMtaBlockedExtensionWarnRecipient: TRUE
zimbraAmavisQuarantineAccount: quarantine@example.com
zimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3
zimbraSSLIncludeCipherSuites: ECDHE-RSA-AES256-GCM-SHA384 AES256-GCM-SHA384`

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	result, err := cm.fetchGlobalConfig(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result to be non-nil")
	}

	// Verify post-processing: zimbraQuarantineBannedItems should be TRUE
	if result["zimbraQuarantineBannedItems"] != constTRUE {
		t.Errorf("Expected zimbraQuarantineBannedItems to be TRUE, got: %s",
			result["zimbraQuarantineBannedItems"])
	}

	// Verify SSL protocols were processed
	if _, ok := result["zimbraMailboxdSSLProtocols"]; !ok {
		t.Error("Expected zimbraMailboxdSSLProtocols to be in result")
	}
}

// TestFetchGlobalConfig_CommandNotAvailable tests behavior when gacf command unavailable
func TestFetchGlobalConfig_CommandNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: fetchGlobalConfig has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands = make(map[string]*commands.Command)

	result, err := cm.fetchGlobalConfig(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command not available")
	}

	if result != nil {
		t.Error("Expected nil result on error")
	}
}

// TestFetchGlobalConfig_CommandFails tests retry logic
func TestFetchGlobalConfig_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	attempts := 0
	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			attempts++
			return "", fmt.Errorf("command failed")
		},
	)

	result, err := cm.fetchGlobalConfig(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command fails")
	}

	if result != nil {
		t.Error("Expected nil result on error")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got: %d", attempts)
	}
}

// TestFetchGlobalConfig_EmptyOutput tests behavior with empty output
func TestFetchGlobalConfig_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			return "   \n   \n   ", nil
		},
	)

	result, err := cm.fetchGlobalConfig(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error with empty output, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result map")
	}

	// Verify zimbraQuarantineBannedItems defaults to FALSE when no data
	if result["zimbraQuarantineBannedItems"] != "FALSE" {
		t.Errorf("Expected zimbraQuarantineBannedItems to default to FALSE, got: %s",
			result["zimbraQuarantineBannedItems"])
	}
}

// TestFetchGlobalConfig_QuarantineBannedItemsFalse tests conditional logic
func TestFetchGlobalConfig_QuarantineBannedItemsFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Mock output where conditions are NOT met
	mockOutput := `zimbraMtaBlockedExtensionWarnRecipient: FALSE
zimbraAmavisQuarantineAccount: `

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	result, err := cm.fetchGlobalConfig(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify zimbraQuarantineBannedItems is FALSE
	if result["zimbraQuarantineBannedItems"] != "FALSE" {
		t.Errorf("Expected zimbraQuarantineBannedItems to be FALSE, got: %s",
			result["zimbraQuarantineBannedItems"])
	}
}

// TestLoadGlobalConfigWithRetry_Success tests successful global config loading with cache
func TestLoadGlobalConfigWithRetry_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	mockOutput := `zimbraMtaBlockedExtensionWarnRecipient: TRUE
zimbraAmavisQuarantineAccount: virus-quarantine.account@example.com
zimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3`

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadGlobalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was loaded
	if cm.State.GlobalConfig.Data["zimbraMtaBlockedExtensionWarnRecipient"] != "TRUE" {
		t.Errorf("Expected zimbraMtaBlockedExtensionWarnRecipient to be TRUE, got: %s",
			cm.State.GlobalConfig.Data["zimbraMtaBlockedExtensionWarnRecipient"])
	}

	// Verify zimbraQuarantineBannedItems was set based on conditions
	if cm.State.GlobalConfig.Data["zimbraQuarantineBannedItems"] != "TRUE" {
		t.Errorf("Expected zimbraQuarantineBannedItems to be TRUE, got: %s",
			cm.State.GlobalConfig.Data["zimbraQuarantineBannedItems"])
	}
}

// TestLoadGlobalConfigWithRetry_NoCache tests loading without cache
func TestLoadGlobalConfigWithRetry_NoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)
	cm.Cache = nil // Disable cache

	mockOutput := `zimbraMtaBlockedExtensionWarnRecipient: FALSE`

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadGlobalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was loaded even without cache
	if cm.State.GlobalConfig.Data["zimbraMtaBlockedExtensionWarnRecipient"] != "FALSE" {
		t.Errorf("Expected zimbraMtaBlockedExtensionWarnRecipient to be FALSE, got: %s",
			cm.State.GlobalConfig.Data["zimbraMtaBlockedExtensionWarnRecipient"])
	}
}

// TestLoadGlobalConfigWithRetry_CachedData tests cache hit scenario
func TestLoadGlobalConfigWithRetry_CachedData(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	callCount := 0
	mockOutput := `zimbraMtaBlockedExtensionWarnRecipient: TRUE`

	commands.Commands["gacf"] = commands.NewCommand(
		"Global config test",
		"gacf",
		func(_ context.Context, args ...string) (string, error) {
			callCount++
			return mockOutput, nil
		},
	)

	// First call should fetch from command
	err := cm.loadGlobalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on first call, got: %v", err)
	}

	if callCount != 1 {
		t.Errorf("Expected command to be called once, got: %d", callCount)
	}

	// Second call should use cache
	err = cm.loadGlobalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on second call, got: %v", err)
	}

	if callCount != 1 {
		t.Errorf("Expected command to still be called only once (cached), got: %d", callCount)
	}
}
