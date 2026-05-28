// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

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

			data := map[string]string{
				"zimbraIPMode": tt.ipMode,
			}
			cm.State.ServerConfig.Data = config.NewConfigMapFrom(data)

			processIPModeConfigForData(data)
			cm.State.ServerConfig.Data.Replace(data)

			for _, key := range tt.checkKeys {
				if v, _ := cm.State.ServerConfig.Data.Get(key); v != tt.expected[key] {
					t.Errorf("Key %s: expected '%s', got '%s'",
						key, tt.expected[key], v)
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

	data := map[string]string{"someKey": "someValue"}
	cm.State.ServerConfig.Data = config.NewConfigMapFrom(data)

	processIPModeConfigForData(data)
	cm.State.ServerConfig.Data.Replace(data)

	// Should not add any IP mode related keys
	if _, ok := cm.State.ServerConfig.Data.Get("zimbraPostconfProtocol"); ok {
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

			data := make(map[string]string, len(tt.input))
			for k, v := range tt.input {
				data[k] = v
			}
			cm.State.ServerConfig.Data = config.NewConfigMapFrom(data)

			processMilterConfigForData(data)
			cm.State.ServerConfig.Data.Replace(data)

			result := cm.State.ServerConfig.Data.GetOr("zimbraMtaSmtpdMilters", "")
			if result != tt.expected {
				t.Errorf("Expected zimbraMtaSmtpdMilters '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
