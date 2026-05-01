// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"testing"
)

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
