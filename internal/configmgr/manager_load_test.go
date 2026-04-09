// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/commands"
	"strings"
	"testing"
)

// TestLoadLocalConfigWithRetry_Success tests successful local config loading
func TestLoadLocalConfigWithRetry_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	// Initialize commands for testing
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Pre-populate cached output to bypass XML file reading
	// (executeLocalConfigCommand reads XML directly, not via commands map)
	mockOutput := "ldap_url = ldap://localhost:389\nzimbra_server_hostname = mail.example.com\nzimbra_home = /opt/zextras"
	cm.cachedLocalConfigOutput = mockOutput

	err := cm.loadLocalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was parsed
	if cm.State.LocalConfig.Data["ldap_url"] != "ldap://localhost:389" {
		t.Errorf("Expected ldap_url to be 'ldap://localhost:389', got: %s",
			cm.State.LocalConfig.Data["ldap_url"])
	}

	// Verify default port was set
	if cm.State.LocalConfig.Data["zmconfigd_listen_port"] != "7171" {
		t.Errorf("Expected default zmconfigd_listen_port '7171', got: %s",
			cm.State.LocalConfig.Data["zmconfigd_listen_port"])
	}

	// Verify OpenDKIM URIs were generated
	if _, ok := cm.State.LocalConfig.Data["opendkim_signingtable_uri"]; !ok {
		t.Error("Expected opendkim_signingtable_uri to be generated")
	}
}

// TestLoadLocalConfigWithRetry_XMLNotAvailable tests behavior when XML file is unavailable
func TestLoadLocalConfigWithRetry_XMLNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Don't set cachedLocalConfigOutput, so it will try to read the XML file
	// which doesn't exist in the test environment

	err := cm.loadLocalConfigWithRetry(context.Background(), 1)
	if err == nil {
		t.Fatal("Expected error when XML file not available")
	}

	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadLocalConfigWithRetry_CommandFails tests retry logic on XML file read failure
func TestLoadLocalConfigWithRetry_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Don't set cachedLocalConfigOutput, so it will try to read XML each time
	// With only 1 retry to keep the test fast
	err := cm.loadLocalConfigWithRetry(context.Background(), 1)
	if err == nil {
		t.Fatal("Expected error when XML file unavailable")
	}

	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadLocalConfigWithRetry_EmptyOutput tests behavior with empty output
func TestLoadLocalConfigWithRetry_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Pre-populate cached output with whitespace-only content
	cm.cachedLocalConfigOutput = "   \n   \n   "

	err := cm.loadLocalConfigWithRetry(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error with empty output, got: %v", err)
	}

	// Verify default port was still set
	if cm.State.LocalConfig.Data["zmconfigd_listen_port"] != "7171" {
		t.Errorf("Expected default zmconfigd_listen_port '7171', got: %s",
			cm.State.LocalConfig.Data["zmconfigd_listen_port"])
	}

	// Verify the data map exists but is mostly empty (except defaults)
	if len(cm.State.LocalConfig.Data) > 1 {
		t.Errorf("Expected only default values, got %d entries", len(cm.State.LocalConfig.Data))
	}
}

// TestLoadLocalConfigWithRetry_CachedOutput tests that cached output is reused
func TestLoadLocalConfigWithRetry_CachedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Pre-populate cached output
	cm.cachedLocalConfigOutput = "key1 = value1\nkey2 = value2"

	// First call - should use cached output
	err := cm.loadLocalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on first call, got: %v", err)
	}

	// Verify data was parsed from cached output
	if cm.State.LocalConfig.Data["key1"] != "value1" {
		t.Errorf("Expected key1=value1, got: %s", cm.State.LocalConfig.Data["key1"])
	}

	// Second call - should still use cached output
	err = cm.loadLocalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on second call, got: %v", err)
	}

	// Verify data is still correct
	if cm.State.LocalConfig.Data["key2"] != "value2" {
		t.Errorf("Expected key2=value2, got: %s", cm.State.LocalConfig.Data["key2"])
	}
}

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
		"",
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
		"",
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
		"",
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
		"",
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

// TestPostProcessLocalConfig tests OpenDKIM URI generation
func TestPostProcessLocalConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Set up test data with ldap_url
	cm.State.LocalConfig.Data = map[string]string{
		"ldap_url": "ldap://server1:389 ldap://server2:389",
	}

	cm.postProcessLocalConfig()

	// Verify OpenDKIM URIs were generated
	signingURI, ok := cm.State.LocalConfig.Data["opendkim_signingtable_uri"]
	if !ok {
		t.Fatal("Expected opendkim_signingtable_uri to be generated")
	}

	if signingURI == "" {
		t.Error("Expected non-empty opendkim_signingtable_uri")
	}

	// Check that both servers are in the URI
	if !strings.Contains(signingURI, "ldap://server1:389") || !strings.Contains(signingURI, "ldap://server2:389") {
		t.Errorf("Expected both servers in URI, got: %s", signingURI)
	}

	keyURI, ok := cm.State.LocalConfig.Data["opendkim_keytable_uri"]
	if !ok {
		t.Fatal("Expected opendkim_keytable_uri to be generated")
	}

	if keyURI == "" {
		t.Error("Expected non-empty opendkim_keytable_uri")
	}
}

// TestPostProcessLocalConfig_DefaultPort tests default port setting
func TestPostProcessLocalConfig_DefaultPort(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	cm.State.LocalConfig.Data = map[string]string{
		"some_key": "some_value",
	}

	cm.postProcessLocalConfig()

	// Verify default port was set
	if cm.State.LocalConfig.Data["zmconfigd_listen_port"] != "7171" {
		t.Errorf("Expected default port '7171', got: %s",
			cm.State.LocalConfig.Data["zmconfigd_listen_port"])
	}
}

// TestPostProcessLocalConfig_NoLDAPUrl tests behavior without ldap_url
func TestPostProcessLocalConfig_NoLDAPUrl(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	cm.State.LocalConfig.Data = map[string]string{
		"key1": "value1",
	}

	cm.postProcessLocalConfig()

	// Verify OpenDKIM URIs were NOT generated
	if _, ok := cm.State.LocalConfig.Data["opendkim_signingtable_uri"]; ok {
		t.Error("Expected opendkim_signingtable_uri to NOT be generated without ldap_url")
	}
}

// TestLoadServerConfigWithRetry_Success tests successful server config loading
func TestLoadServerConfigWithRetry_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Mock gs command output (LDAP attribute format)
	mockOutput := `zimbraServiceEnabled: mta mailbox
zimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3
zimbraMtaMyNetworks: 127.0.0.0/8 10.0.0.0/8
zimbraSSLExcludeCipherSuites: ECDHE-RSA-DES-CBC3-SHA ECDHE-ECDSA-DES-CBC3-SHA
zimbraMtaHeaderChecks: pcre:/opt/zextras/conf/postfix_header_checks`

	commands.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		"",
		func(_ context.Context, args ...string) (string, error) {
			if len(args) == 0 || args[0] != "testhost" {
				return "", fmt.Errorf("expected hostname argument")
			}
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was parsed
	if cm.State.ServerConfig.Data["zimbraServiceEnabled"] != "mta mailbox" {
		t.Errorf("Expected zimbraServiceEnabled to be 'mta mailbox', got: %s",
			cm.State.ServerConfig.Data["zimbraServiceEnabled"])
	}

	// Verify ServiceConfig was populated
	if cm.State.ServerConfig.ServiceConfig["mta"] != "zimbraServiceEnabled" {
		t.Error("Expected mta service to be enabled")
	}

	// Verify SSL protocols were processed (should be sorted)
	if _, ok := cm.State.ServerConfig.Data["zimbraMailboxdSSLProtocols"]; !ok {
		t.Error("Expected zimbraMailboxdSSLProtocols to be processed")
	}

	// Verify zimbraMtaMyNetworksPerLine was generated
	if networks, ok := cm.State.ServerConfig.Data["zimbraMtaMyNetworksPerLine"]; !ok {
		t.Error("Expected zimbraMtaMyNetworksPerLine to be generated")
	} else if !strings.Contains(networks, "\n") {
		t.Error("Expected zimbraMtaMyNetworksPerLine to contain newlines")
	}

	// Verify comma-separated conversion
	if headers, ok := cm.State.ServerConfig.Data["zimbraMtaHeaderChecks"]; !ok {
		t.Error("Expected zimbraMtaHeaderChecks to be processed")
	} else if headers != "pcre:/opt/zextras/conf/postfix_header_checks" {
		t.Errorf("Expected single header check, got: %s", headers)
	}
}

// TestLoadServerConfigWithRetry_NoHostname tests error when hostname is missing
func TestLoadServerConfigWithRetry_NoHostname(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)
	cm.mainConfig.Hostname = "" // Clear hostname

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when hostname is missing")
	}

	if !strings.Contains(err.Error(), "hostname required") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadServerConfigWithRetry_CommandNotAvailable tests error when gs command unavailable
func TestLoadServerConfigWithRetry_CommandNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands = make(map[string]*commands.Command)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command not available")
	}

	if !strings.Contains(err.Error(), "gs command not available") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadServerConfigWithRetry_CommandFails tests retry logic
func TestLoadServerConfigWithRetry_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	attempts := 0
	commands.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		"",
		func(_ context.Context, args ...string) (string, error) {
			attempts++
			return "", fmt.Errorf("command failed")
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command fails")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 retry attempts, got: %d", attempts)
	}
}

// TestLoadServerConfigWithRetry_EmptyOutput tests behavior with empty output
func TestLoadServerConfigWithRetry_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "   \n   \n   ", nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error with whitespace output, got: %v", err)
	}

	// Verify the data map exists but is empty (or has only post-processed defaults)
	if len(cm.State.ServerConfig.Data) > 5 {
		t.Errorf("Expected minimal or empty config data, got %d entries", len(cm.State.ServerConfig.Data))
	}
}

// TestLoadServerConfigWithRetry_NoCache tests loading without cache
func TestLoadServerConfigWithRetry_NoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)
	cm.Cache = nil // Disable cache

	mockOutput := `zimbraServiceEnabled: mailbox
zimbraMailboxdSSLProtocols: TLSv1.3`

	commands.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was loaded even without cache
	if cm.State.ServerConfig.Data["zimbraServiceEnabled"] != "mailbox" {
		t.Errorf("Expected zimbraServiceEnabled to be 'mailbox', got: %s",
			cm.State.ServerConfig.Data["zimbraServiceEnabled"])
	}
}

// TestLoadServerConfigWithRetry_ServiceMapping tests service config mapping
func TestLoadServerConfigWithRetry_ServiceMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Test mailbox -> mailboxd mapping and mta -> sasl mapping
	mockOutput := `zimbraServiceEnabled: mailbox mta ldap`

	commands.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify service mappings
	if cm.State.ServerConfig.ServiceConfig["mailbox"] != "zimbraServiceEnabled" {
		t.Error("Expected mailbox service to be enabled")
	}

	if cm.State.ServerConfig.ServiceConfig["mailboxd"] != "zimbraServiceEnabled" {
		t.Error("Expected mailboxd service to be mapped from mailbox")
	}

	if cm.State.ServerConfig.ServiceConfig["mta"] != "zimbraServiceEnabled" {
		t.Error("Expected mta service to be enabled")
	}

	if cm.State.ServerConfig.ServiceConfig["sasl"] != "zimbraServiceEnabled" {
		t.Error("Expected sasl service to be mapped from mta")
	}

	if cm.State.ServerConfig.ServiceConfig["ldap"] != "zimbraServiceEnabled" {
		t.Error("Expected ldap service to be enabled")
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
		"",
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
		"",
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
		"",
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

// TestFetchMiscCommand_Success tests successful command execution
func TestFetchMiscCommand_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	mockOutput := "test output data"
	commands.Commands["testcmd"] = commands.NewCommand(
		"Test command",
		"testcmd",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "testcmd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if output != mockOutput {
		t.Errorf("Expected output '%s', got: '%s'", mockOutput, output)
	}
}

// TestFetchMiscCommand_CommandNotAvailable tests behavior when command unavailable
func TestFetchMiscCommand_CommandNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands = make(map[string]*commands.Command)

	output, err := cm.fetchMiscCommand(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("Expected no error when command unavailable, got: %v", err)
	}

	if output != "" {
		t.Errorf("Expected empty output when command unavailable, got: %s", output)
	}
}

// TestFetchMiscCommand_CommandFails tests behavior when command fails
func TestFetchMiscCommand_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["failcmd"] = commands.NewCommand(
		"Failing command",
		"failcmd",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "", fmt.Errorf("command failed")
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "failcmd")
	if err != nil {
		t.Errorf("Expected no error (returns empty on failure), got: %v", err)
	}

	if output != "" {
		t.Errorf("Expected empty output when command fails, got: %s", output)
	}
}

// TestFetchMiscCommand_EmptyOutput tests behavior with empty output
func TestFetchMiscCommand_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["emptycmd"] = commands.NewCommand(
		"Empty command",
		"emptycmd",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "   \n   ", nil
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "emptycmd")
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Whitespace-only output is returned as-is (not filtered)
	if strings.TrimSpace(output) != "" {
		t.Errorf("Expected only whitespace output, got: %s", output)
	}
}

// TestLoadMiscConfig_Success tests successful misc config loading
func TestLoadMiscConfig_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Mock all 4 misc commands
	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "https://proxy1.example.com", nil
		},
	)

	commands.Commands["garpb"] = commands.NewCommand(
		"Get all reverse proxy backends",
		"garpb",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "backend1.example.com", nil
		},
	)

	commands.Commands["gamau"] = commands.NewCommand(
		"Get all MTA auth URLs",
		"gamau",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "ldap://mta-auth.example.com", nil
		},
	)

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify all commands were executed and stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy1.example.com" {
		t.Errorf("Expected garpu output to be stored, got: %s", cm.State.MiscConfig.Data["garpu"])
	}

	if cm.State.MiscConfig.Data["garpb"] != "backend1.example.com" {
		t.Errorf("Expected garpb output to be stored, got: %s", cm.State.MiscConfig.Data["garpb"])
	}

	if cm.State.MiscConfig.Data["gamau"] != "ldap://mta-auth.example.com" {
		t.Errorf("Expected gamau output to be stored, got: %s", cm.State.MiscConfig.Data["gamau"])
	}
}

// TestLoadMiscConfig_NoCache tests misc config loading without cache
func TestLoadMiscConfig_NoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)
	cm.Cache = nil // Disable cache

	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "https://proxy.example.com", nil
		},
	)

	commands.Commands["garpb"] = commands.NewCommand("desc", "garpb", "", func(_ context.Context, args ...string) (string, error) { return "", nil })
	commands.Commands["gamau"] = commands.NewCommand("desc", "gamau", "", func(_ context.Context, args ...string) (string, error) { return "", nil })

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify at least one command output was stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy.example.com" {
		t.Errorf("Expected garpu output to be stored without cache, got: %s", cm.State.MiscConfig.Data["garpu"])
	}
}

// TestLoadMiscConfig_PartialFailure tests misc config when some commands fail
func TestLoadMiscConfig_PartialFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	successCount := 0
	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		"",
		func(_ context.Context, args ...string) (string, error) {
			successCount++
			return "https://proxy.example.com", nil
		},
	)

	// These commands will fail or return empty
	commands.Commands["garpb"] = commands.NewCommand(
		"desc",
		"garpb",
		"",
		func(_ context.Context, args ...string) (string, error) {
			return "", fmt.Errorf("command failed")
		},
	)

	commands.Commands["gamau"] = commands.NewCommand("desc", "gamau", "", func(_ context.Context, args ...string) (string, error) { return "", nil })

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error (partial failures are non-fatal), got: %v", err)
	}

	// Verify successful command output was stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy.example.com" {
		t.Errorf("Expected garpu output to be stored, got: %s", cm.State.MiscConfig.Data["garpu"])
	}

	// Verify failed commands are not in the data map or are empty
	if val, ok := cm.State.MiscConfig.Data["garpb"]; ok && val != "" {
		t.Errorf("Expected garpb to not be stored or be empty, got: %s", val)
	}
}

// TestParseLDAPCommandOutput tests LDAP output parsing
func TestParseLDAPCommandOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name: "regular key:value format",
			input: `zimbraServiceEnabled: mailbox mta
zimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3`,
			expected: map[string]string{
				"zimbraServiceEnabled":       "mailbox mta",
				"zimbraMailboxdSSLProtocols": "TLSv1.2 TLSv1.3",
			},
		},
		{
			name: "base64 encoded values (double colon)",
			input: `zimbraPublicKey:: AQIDBAUG==
zimbraNormalKey: normal value`,
			expected: map[string]string{
				"zimbraPublicKey": "AQIDBAUG==",
				"zimbraNormalKey": "normal value",
			},
		},
		{
			name: "multi-value attributes",
			input: `zimbraServiceEnabled: mailbox
zimbraServiceEnabled: mta
zimbraServiceEnabled: ldap`,
			expected: map[string]string{
				"zimbraServiceEnabled": "mailbox\nmta\nldap",
			},
		},
		{
			name: "empty lines and comments",
			input: `zimbraKey1: value1

# This is a comment
zimbraKey2: value2
# Another comment

zimbraKey3: value3`,
			expected: map[string]string{
				"zimbraKey1": "value1",
				"zimbraKey2": "value2",
				"zimbraKey3": "value3",
			},
		},
		{
			name:     "empty input",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:     "whitespace only",
			input:    "   \n   \n   ",
			expected: map[string]string{},
		},
		{
			name: "mixed formats",
			input: `zimbraNormalKey: normal value
zimbraBase64Key:: QmFzZTY0VmFsdWU=
zimbraMultiValue: value1
zimbraMultiValue: value2`,
			expected: map[string]string{
				"zimbraNormalKey":  "normal value",
				"zimbraBase64Key":  "QmFzZTY0VmFsdWU=",
				"zimbraMultiValue": "value1\nvalue2",
			},
		},
		{
			name: "value with additional colons",
			input: `zimbraURL: https://example.com:8443/path
zimbraLDAPURL: ldap://ldap.example.com:389
zimbraTime: 12:34:56`,
			expected: map[string]string{
				"zimbraURL":     "https://example.com:8443/path",
				"zimbraLDAPURL": "ldap://ldap.example.com:389",
				"zimbraTime":    "12:34:56",
			},
		},
		{
			name: "malformed lines without colon",
			input: `zimbraValidKey: valid value
malformed line without colon
zimbraAnotherKey: another value`,
			expected: map[string]string{
				"zimbraValidKey":   "valid value",
				"zimbraAnotherKey": "another value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLDAPCommandOutput(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d entries, got %d", len(tt.expected), len(result))
			}

			for key, expectedValue := range tt.expected {
				if result[key] != expectedValue {
					t.Errorf("Key %s: expected '%s', got '%s'", key, expectedValue, result[key])
				}
			}
		})
	}
}

// TestProcessIPModeConfig tests IP mode configuration processing
func TestProcessIPModeConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name      string
		ipMode    string
		expected  map[string]string
		checkKeys []string
	}{
		{
			name:   "ipv4 mode",
			ipMode: "ipv4",
			expected: map[string]string{
				"zimbraIPv4BindAddress":     "127.0.0.1",
				"zimbraUnboundBindAddress":  "127.0.0.1",
				"zimbraLocalBindAddress":    "127.0.0.1",
				"zimbraPostconfProtocol":    "ipv4",
				"zimbraAmavisListenSockets": "'10024','10026','10032'",
				"zimbraInetMode":            "inet",
				"zimbraMilterBindAddress":   "127.0.0.1",
			},
			checkKeys: []string{"zimbraIPv4BindAddress", "zimbraUnboundBindAddress", "zimbraPostconfProtocol", "zimbraInetMode"},
		},
		{
			name:   "ipv6 mode",
			ipMode: "ipv6",
			expected: map[string]string{
				"zimbraIPv4BindAddress":     "127.0.0.1",
				"zimbraUnboundBindAddress":  "::1",
				"zimbraLocalBindAddress":    "::1",
				"zimbraPostconfProtocol":    "ipv6",
				"zimbraAmavisListenSockets": "'[::1]:10024','[::1]:10026','[::1]:10032'",
				"zimbraInetMode":            "inet6",
				"zimbraMilterBindAddress":   "[::1]",
			},
			checkKeys: []string{"zimbraUnboundBindAddress", "zimbraPostconfProtocol", "zimbraInetMode", "zimbraAmavisListenSockets"},
		},
		{
			name:   "both mode",
			ipMode: "both",
			expected: map[string]string{
				"zimbraIPv4BindAddress":     "127.0.0.1",
				"zimbraUnboundBindAddress":  "127.0.0.1 ::1",
				"zimbraLocalBindAddress":    "::1",
				"zimbraPostconfProtocol":    "all",
				"zimbraAmavisListenSockets": "'10024','10026','10032','[::1]:10024','[::1]:10026','[::1]:10032'",
				"zimbraInetMode":            "inet6",
			},
			checkKeys: []string{"zimbraUnboundBindAddress", "zimbraPostconfProtocol", "zimbraInetMode"},
		},
		{
			name:   "uppercase mode normalized",
			ipMode: "IPV4",
			expected: map[string]string{
				"zimbraPostconfProtocol": "ipv4",
				"zimbraInetMode":         "inet",
			},
			checkKeys: []string{"zimbraPostconfProtocol", "zimbraInetMode"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newTestConfigManager(t)

			cm.State.ServerConfig.Data = map[string]string{
				"zimbraIPMode": tt.ipMode,
			}

			processIPModeConfigForData(cm.State.ServerConfig.Data)

			for _, key := range tt.checkKeys {
				if cm.State.ServerConfig.Data[key] != tt.expected[key] {
					t.Errorf("Key %s: expected '%s', got '%s'",
						key, tt.expected[key], cm.State.ServerConfig.Data[key])
				}
			}
		})
	}
}

// TestProcessIPModeConfig_NoIPMode tests behavior when zimbraIPMode is not set
func TestProcessIPModeConfig_NoIPMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	cm.State.ServerConfig.Data = map[string]string{
		"someKey": "someValue",
	}

	processIPModeConfigForData(cm.State.ServerConfig.Data)

	// Should not add any IP mode related keys
	if _, ok := cm.State.ServerConfig.Data["zimbraPostconfProtocol"]; ok {
		t.Error("Expected no zimbraPostconfProtocol when IP mode not set")
	}
}

// TestExtractRBLMatches tests the extractRBLMatches function
func TestExtractRBLMatches(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name     string
		text     string
		pattern  string
		expected []string
	}{
		{
			name:     "single match",
			text:     "permit_mynetworks reject_rbl_client zen.spamhaus.org",
			pattern:  "reject_rbl_client",
			expected: []string{"zen.spamhaus.org"},
		},
		{
			name:     "multiple matches",
			text:     "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_rbl_client bl.spamcop.net permit_sasl_authenticated",
			pattern:  "reject_rbl_client",
			expected: []string{"zen.spamhaus.org", "bl.spamcop.net"},
		},
		{
			name:     "no match",
			text:     "permit_mynetworks permit_sasl_authenticated reject_unauth_destination",
			pattern:  "reject_rbl_client",
			expected: nil,
		},
		{
			name:     "pattern at end without domain (last word)",
			text:     "permit_mynetworks reject_rbl_client",
			pattern:  "reject_rbl_client",
			expected: nil,
		},
		{
			name:     "empty text",
			text:     "",
			pattern:  "reject_rbl_client",
			expected: nil,
		},
		{
			name:     "different pattern",
			text:     "reject_rhsbl_client dbl.spamhaus.org reject_rbl_client zen.spamhaus.org",
			pattern:  "reject_rhsbl_client",
			expected: []string{"dbl.spamhaus.org"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRBLMatches(tt.text, tt.pattern)
			if len(result) != len(tt.expected) {
				t.Errorf("extractRBLMatches(%q, %q) = %v, expected %v", tt.text, tt.pattern, result, tt.expected)
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("extractRBLMatches result[%d] = %q, expected %q", i, v, tt.expected[i])
				}
			}
		})
	}
}

// TestRemoveRBLEntries tests the removeRBLEntries function
func TestRemoveRBLEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name     string
		text     string
		pattern  string
		expected string
	}{
		{
			name:     "remove single entry",
			text:     "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_unauth_destination",
			pattern:  "reject_rbl_client",
			expected: "permit_mynetworks reject_unauth_destination",
		},
		{
			name:     "remove multiple entries",
			text:     "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_rbl_client bl.spamcop.net reject_unauth_destination",
			pattern:  "reject_rbl_client",
			expected: "permit_mynetworks reject_unauth_destination",
		},
		{
			name:     "no entry to remove",
			text:     "permit_mynetworks permit_sasl_authenticated reject_unauth_destination",
			pattern:  "reject_rbl_client",
			expected: "permit_mynetworks permit_sasl_authenticated reject_unauth_destination",
		},
		{
			name:     "empty text",
			text:     "",
			pattern:  "reject_rbl_client",
			expected: "",
		},
		{
			name:     "pattern at end (no domain follows)",
			text:     "permit_mynetworks reject_rbl_client",
			pattern:  "reject_rbl_client",
			expected: "permit_mynetworks reject_rbl_client",
		},
		{
			name:     "only pattern and domain",
			text:     "reject_rbl_client zen.spamhaus.org",
			pattern:  "reject_rbl_client",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeRBLEntries(tt.text, tt.pattern)
			if result != tt.expected {
				t.Errorf("removeRBLEntries(%q, %q) = %q, expected %q", tt.text, tt.pattern, result, tt.expected)
			}
		})
	}
}

// TestProcessRBLPatterns tests the processRBLPatterns function
func TestProcessRBLPatterns(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name              string
		restriction       string
		types             []rblType
		expectedExtracted map[string][]string
		expectedCleaned   string
	}{
		{
			name:        "single type extraction",
			restriction: "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_unauth_destination",
			types: []rblType{
				{pattern: "reject_rbl_client", dataKey: "zimbraMtaRestrictionRBLs"},
			},
			expectedExtracted: map[string][]string{
				"zimbraMtaRestrictionRBLs": {"zen.spamhaus.org"},
			},
			expectedCleaned: "permit_mynetworks reject_unauth_destination",
		},
		{
			name:        "multiple types extraction",
			restriction: "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_rhsbl_client dbl.spamhaus.org reject_unauth_destination",
			types: []rblType{
				{pattern: "reject_rbl_client", dataKey: "zimbraMtaRestrictionRBLs"},
				{pattern: "reject_rhsbl_client", dataKey: "zimbraMtaRestrictionRHSBLCs"},
			},
			expectedExtracted: map[string][]string{
				"zimbraMtaRestrictionRBLs":    {"zen.spamhaus.org"},
				"zimbraMtaRestrictionRHSBLCs": {"dbl.spamhaus.org"},
			},
			expectedCleaned: "permit_mynetworks reject_unauth_destination",
		},
		{
			name:        "no matches",
			restriction: "permit_mynetworks reject_unauth_destination",
			types: []rblType{
				{pattern: "reject_rbl_client", dataKey: "zimbraMtaRestrictionRBLs"},
			},
			expectedExtracted: map[string][]string{
				"zimbraMtaRestrictionRBLs": nil,
			},
			expectedCleaned: "permit_mynetworks reject_unauth_destination",
		},
		{
			name:        "empty restriction",
			restriction: "",
			types: []rblType{
				{pattern: "reject_rbl_client", dataKey: "zimbraMtaRestrictionRBLs"},
			},
			expectedExtracted: map[string][]string{
				"zimbraMtaRestrictionRBLs": nil,
			},
			expectedCleaned: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted, cleaned := processRBLPatterns(tt.restriction, tt.types)

			if cleaned != tt.expectedCleaned {
				t.Errorf("processRBLPatterns cleaned = %q, expected %q", cleaned, tt.expectedCleaned)
			}

			for key, expectedMatches := range tt.expectedExtracted {
				matches := extracted[key]
				if len(matches) != len(expectedMatches) {
					t.Errorf("extracted[%q] = %v, expected %v", key, matches, expectedMatches)
					continue
				}
				for i, v := range matches {
					if v != expectedMatches[i] {
						t.Errorf("extracted[%q][%d] = %q, expected %q", key, i, v, expectedMatches[i])
					}
				}
			}
		})
	}
}

// TestProcessMtaRestrictionRBLsForData tests processMtaRestrictionRBLsForData
func TestProcessMtaRestrictionRBLsForData(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	t.Run("extracts all rbl types and cleans restriction", func(t *testing.T) {
		data := map[string]string{
			"zimbraMtaRestriction": "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_rhsbl_client dbl.spamhaus.org reject_rhsbl_sender rhsbl.example.com reject_rhsbl_reverse_client rcbl.example.com reject_unauth_destination",
		}

		processMtaRestrictionRBLsForData(data)

		if data["zimbraMtaRestrictionRBLs"] != "zen.spamhaus.org" {
			t.Errorf("zimbraMtaRestrictionRBLs = %q, expected %q", data["zimbraMtaRestrictionRBLs"], "zen.spamhaus.org")
		}
		if data["zimbraMtaRestrictionRHSBLCs"] != "dbl.spamhaus.org" {
			t.Errorf("zimbraMtaRestrictionRHSBLCs = %q, expected %q", data["zimbraMtaRestrictionRHSBLCs"], "dbl.spamhaus.org")
		}
		if data["zimbraMtaRestrictionRHSBLSs"] != "rhsbl.example.com" {
			t.Errorf("zimbraMtaRestrictionRHSBLSs = %q, expected %q", data["zimbraMtaRestrictionRHSBLSs"], "rhsbl.example.com")
		}
		if data["zimbraMtaRestrictionRHSBLRCs"] != "rcbl.example.com" {
			t.Errorf("zimbraMtaRestrictionRHSBLRCs = %q, expected %q", data["zimbraMtaRestrictionRHSBLRCs"], "rcbl.example.com")
		}
		// Cleaned restriction should have the entries removed
		if strings.Contains(data["zimbraMtaRestriction"], "reject_rbl_client") {
			t.Error("cleaned restriction still contains reject_rbl_client")
		}
		if !strings.Contains(data["zimbraMtaRestriction"], "reject_unauth_destination") {
			t.Error("cleaned restriction lost reject_unauth_destination")
		}
	})

	t.Run("no zimbraMtaRestriction key", func(t *testing.T) {
		data := map[string]string{
			"someOtherKey": "value",
		}
		processMtaRestrictionRBLsForData(data)
		// Should be a no-op
		if len(data) != 1 {
			t.Errorf("expected 1 key, got %d", len(data))
		}
	})

	t.Run("empty zimbraMtaRestriction", func(t *testing.T) {
		data := map[string]string{
			"zimbraMtaRestriction": "",
		}
		processMtaRestrictionRBLsForData(data)
		// Should be a no-op
		if len(data) != 1 {
			t.Errorf("expected 1 key, got %d", len(data))
		}
	})

	t.Run("multiple RBL entries of same type joined with comma", func(t *testing.T) {
		data := map[string]string{
			"zimbraMtaRestriction": "permit_mynetworks reject_rbl_client zen.spamhaus.org reject_rbl_client bl.spamcop.net",
		}
		processMtaRestrictionRBLsForData(data)

		expected := "zen.spamhaus.org, bl.spamcop.net"
		if data["zimbraMtaRestrictionRBLs"] != expected {
			t.Errorf("zimbraMtaRestrictionRBLs = %q, expected %q", data["zimbraMtaRestrictionRBLs"], expected)
		}
	})
}

// TestProcessMilterConfig tests milter configuration processing
func TestProcessMilterConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name     string
		input    map[string]string
		expected string
	}{
		{
			name: "milter enabled with bind address and port",
			input: map[string]string{
				"zimbraMilterServerEnabled": "TRUE",
				"zimbraMilterBindAddress":   "127.0.0.1",
				"zimbraMilterBindPort":      "7026",
			},
			expected: "inet:127.0.0.1:7026",
		},
		{
			name: "milter enabled but missing bind address",
			input: map[string]string{
				"zimbraMilterServerEnabled": "TRUE",
				"zimbraMilterBindPort":      "7026",
			},
			expected: "",
		},
		{
			name: "milter enabled but missing bind port",
			input: map[string]string{
				"zimbraMilterServerEnabled": "TRUE",
				"zimbraMilterBindAddress":   "127.0.0.1",
			},
			expected: "",
		},
		{
			name: "milter disabled",
			input: map[string]string{
				"zimbraMilterServerEnabled": "FALSE",
				"zimbraMilterBindAddress":   "127.0.0.1",
				"zimbraMilterBindPort":      "7026",
			},
			expected: "",
		},
		{
			name: "milter not configured",
			input: map[string]string{
				"someOtherKey": "value",
			},
			expected: "",
		},
		{
			name: "milter enabled with existing milters",
			input: map[string]string{
				"zimbraMilterServerEnabled": "TRUE",
				"zimbraMilterBindAddress":   "127.0.0.1",
				"zimbraMilterBindPort":      "7026",
				"zimbraMtaSmtpdMilters":     "inet:existing.milter:8026",
			},
			expected: "inet:existing.milter:8026, inet:127.0.0.1:7026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newTestConfigManager(t)

			cm.State.ServerConfig.Data = make(map[string]string)
			for k, v := range tt.input {
				cm.State.ServerConfig.Data[k] = v
			}

			processMilterConfigForData(cm.State.ServerConfig.Data)

			result := cm.State.ServerConfig.Data["zimbraMtaSmtpdMilters"]
			if result != tt.expected {
				t.Errorf("Expected zimbraMtaSmtpdMilters '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
