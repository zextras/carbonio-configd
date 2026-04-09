// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build manual

package localconfig

import (
	"testing"
)

// TestLoadRealLocalConfig tests parsing the actual localconfig.xml from a Carbonio container.
// Run with: go test -tags=manual -run TestLoadRealLocalConfig
func TestLoadRealLocalConfig(t *testing.T) {
	config, err := LoadLocalConfigFromFile("/tmp/real_localconfig.xml")
	if err != nil {
		t.Fatalf("Failed to parse real localconfig.xml: %v", err)
	}

	t.Logf("Successfully parsed %d keys", len(config))

	// Verify expected keys exist
	expectedKeys := []string{
		"ldap_host",
		"ldap_port",
		"zimbra_server_hostname",
		"ldap_url",
		"zimbra_user",
	}

	for _, key := range expectedKeys {
		if val, ok := config[key]; ok {
			t.Logf("  %s = %s", key, val)
		} else {
			t.Errorf("Expected key %q not found in config", key)
		}
	}

	// Verify reasonable number of keys (Carbonio typically has 50-100)
	if len(config) < 30 {
		t.Errorf("Expected at least 30 keys, got %d", len(config))
	}
}
