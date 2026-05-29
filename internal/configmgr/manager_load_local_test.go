// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestLoadLocalConfigWithRetry_Success tests successful local config loading
func TestLoadLocalConfigWithRetry_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

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
	if cm.State.LocalConfig.Data.GetOr("ldap_url", "") != "ldap://localhost:389" {
		t.Errorf("Expected ldap_url to be 'ldap://localhost:389', got: %s",
			cm.State.LocalConfig.Data.GetOr("ldap_url", ""))
	}

	// Verify default port was set
	if cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", "") != "7171" {
		t.Errorf("Expected default zmconfigd_listen_port '7171', got: %s",
			cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", ""))
	}

	// Verify OpenDKIM URIs were generated
	if _, ok := cm.State.LocalConfig.Data.Get("opendkim_signingtable_uri"); !ok {
		t.Error("Expected opendkim_signingtable_uri to be generated")
	}
}

// TestLoadLocalConfigWithRetry_XMLNotAvailable tests behavior when XML file is unavailable
func TestLoadLocalConfigWithRetry_XMLNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

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

	cm := newTestConfigManager(t)

	// Pre-populate cached output with whitespace-only content
	cm.cachedLocalConfigOutput = "   \n   \n   "

	err := cm.loadLocalConfigWithRetry(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error with empty output, got: %v", err)
	}

	// Verify default port was still set
	if cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", "") != "7171" {
		t.Errorf("Expected default zmconfigd_listen_port '7171', got: %s",
			cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", ""))
	}

	// Verify the data map exists but is mostly empty (except defaults)
	if cm.State.LocalConfig.Data.Len() > 1 {
		t.Errorf("Expected only default values, got %d entries", cm.State.LocalConfig.Data.Len())
	}
}

// TestLoadLocalConfigWithRetry_CachedOutput tests that cached output is reused
func TestLoadLocalConfigWithRetry_CachedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	// Pre-populate cached output
	cm.cachedLocalConfigOutput = "key1 = value1\nkey2 = value2"

	// First call - should use cached output
	err := cm.loadLocalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on first call, got: %v", err)
	}

	// Verify data was parsed from cached output
	if cm.State.LocalConfig.Data.GetOr("key1", "") != "value1" {
		t.Errorf("Expected key1=value1, got: %s", cm.State.LocalConfig.Data.GetOr("key1", ""))
	}

	// Second call - should still use cached output
	err = cm.loadLocalConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error on second call, got: %v", err)
	}

	// Verify data is still correct
	if cm.State.LocalConfig.Data.GetOr("key2", "") != "value2" {
		t.Errorf("Expected key2=value2, got: %s", cm.State.LocalConfig.Data.GetOr("key2", ""))
	}
}

// TestPostProcessLocalConfig tests OpenDKIM URI generation
func TestPostProcessLocalConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	cm := newTestConfigManager(t)

	// Set up test data with ldap_url
	cm.State.LocalConfig.Data = config.NewConfigMapFrom(map[string]string{
		"ldap_url": "ldap://server1:389 ldap://server2:389",
	})

	cm.postProcessLocalConfig()

	// Verify OpenDKIM URIs were generated
	signingURI, ok := cm.State.LocalConfig.Data.Get("opendkim_signingtable_uri")
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

	keyURI, ok := cm.State.LocalConfig.Data.Get("opendkim_keytable_uri")
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

	cm := newTestConfigManager(t)

	cm.State.LocalConfig.Data = config.NewConfigMapFrom(map[string]string{
		"some_key": "some_value",
	})

	cm.postProcessLocalConfig()

	// Verify default port was set
	if cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", "") != "7171" {
		t.Errorf("Expected default port '7171', got: %s",
			cm.State.LocalConfig.Data.GetOr("zmconfigd_listen_port", ""))
	}
}

// TestPostProcessLocalConfig_NoLDAPUrl tests behavior without ldap_url
func TestPostProcessLocalConfig_NoLDAPUrl(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	cm := newTestConfigManager(t)

	cm.State.LocalConfig.Data = config.NewConfigMapFrom(map[string]string{
		"key1": "value1",
	})

	cm.postProcessLocalConfig()

	// Verify OpenDKIM URIs were NOT generated
	if _, ok := cm.State.LocalConfig.Data.Get("opendkim_signingtable_uri"); ok {
		t.Error("Expected opendkim_signingtable_uri to NOT be generated without ldap_url")
	}
}
