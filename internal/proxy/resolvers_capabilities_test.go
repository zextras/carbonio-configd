// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - capabilities resolver tests
package proxy

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestResolvePOP3Capabilities tests all branches of resolvePOP3Capabilities
func TestResolvePOP3Capabilities(t *testing.T) {
	tests := []struct {
		name       string
		globalData map[string]string
		expected   []string
	}{
		{
			name:       "no attribute returns defaults",
			globalData: map[string]string{},
			expected:   defaultPOP3Capabilities,
		},
		{
			name:       "custom single capability",
			globalData: map[string]string{"zimbraReverseProxyPop3EnabledCapability": "TOP"},
			expected:   []string{"TOP"},
		},
		{
			name: "custom multi-line capabilities",
			globalData: map[string]string{
				"zimbraReverseProxyPop3EnabledCapability": "TOP\nUIDL\nUSER",
			},
			expected: []string{"TOP", "UIDL", "USER"},
		},
		{
			name: "capability with spaces (like pop3ExpireCapability)",
			globalData: map[string]string{
				"zimbraReverseProxyPop3EnabledCapability": "EXPIRE 31 USER\nTOP",
			},
			expected: []string{"EXPIRE 31 USER", "TOP"},
		},
		{
			name: "empty lines are filtered",
			globalData: map[string]string{
				"zimbraReverseProxyPop3EnabledCapability": "TOP\n\nUIDL\n",
			},
			expected: []string{"TOP", "UIDL"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			result, err := g.resolvePOP3Capabilities(context.Background())
			if err != nil {
				t.Fatalf("resolvePOP3Capabilities failed: %v", err)
			}
			caps, ok := result.([]string)
			if !ok {
				t.Fatalf("expected []string, got %T", result)
			}
			if len(caps) != len(tt.expected) {
				t.Fatalf("expected %v caps, got %v: %v", len(tt.expected), len(caps), caps)
			}
			for i, c := range caps {
				if c != tt.expected[i] {
					t.Errorf("caps[%d]: expected %q, got %q", i, tt.expected[i], c)
				}
			}
		})
	}
}

// TestResolveIMAPCapabilities tests all branches of resolveIMAPCapabilities
func TestResolveIMAPCapabilities(t *testing.T) {
	defaultCaps := []string{
		"IMAP4rev1", "ID", "LITERAL+", "SASL-IR", "IDLE", "NAMESPACE",
		"ACL", "BINARY", "CATENATE", "CHILDREN", "CONDSTORE", "ENABLE",
		"ESEARCH", "ESORT", "I18NLEVEL=1", "LIST-EXTENDED", "LIST-STATUS",
		"MULTIAPPEND", "QRESYNC", "QUOTA", "RIGHTS=ektx", "SEARCHRES",
		"SORT", "THREAD=ORDEREDSUBJECT", "UIDPLUS", "UNSELECT", "WITHIN", "XLIST",
	}

	tests := []struct {
		name           string
		globalData     map[string]string
		expectDefaults bool
		expectContains string
	}{
		{
			name:           "no attribute returns defaults",
			globalData:     map[string]string{},
			expectDefaults: true,
		},
		{
			name: "custom capabilities from config",
			globalData: map[string]string{
				"zimbraReverseProxyImapEnabledCapability": "IMAP4rev1 IDLE",
			},
			expectContains: "IMAP4rev1",
		},
		{
			name: "comma-separated capabilities",
			globalData: map[string]string{
				"zimbraReverseProxyImapEnabledCapability": "IMAP4rev1,IDLE,NAMESPACE",
			},
			expectContains: "IMAP4rev1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{
				GlobalConfig: &config.GlobalConfig{Data: tt.globalData},
			}
			result, err := g.resolveIMAPCapabilities(context.Background())
			if err != nil {
				t.Fatalf("resolveIMAPCapabilities failed: %v", err)
			}
			caps, ok := result.([]string)
			if !ok {
				t.Fatalf("expected []string, got %T", result)
			}
			if tt.expectDefaults {
				if len(caps) != len(defaultCaps) {
					t.Fatalf("expected %d default caps, got %d: %v", len(defaultCaps), len(caps), caps)
				}
				return
			}
			if tt.expectContains != "" {
				found := false
				for _, c := range caps {
					if c == tt.expectContains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected capabilities to contain %q, got %v", tt.expectContains, caps)
				}
			}
		})
	}
}

// TestFormatCapabilities tests formatCapabilities
func TestFormatCapabilities(t *testing.T) {
	t.Run("formats string slice into quoted space-separated string", func(t *testing.T) {
		result, err := formatCapabilities([]string{"IMAP4rev1", "ID", "LITERAL+"}, "IMAP")
		if err != nil {
			t.Fatalf("formatCapabilities failed: %v", err)
		}
		expected := ` "IMAP4rev1" "ID" "LITERAL+"`
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("returns error for non-string-slice input", func(t *testing.T) {
		_, err := formatCapabilities(42, "IMAP")
		if err == nil {
			t.Error("expected error for non-[]string input")
		}
	})
}

// TestFormatIMAPCapabilities tests formatIMAPCapabilities
func TestFormatIMAPCapabilities(t *testing.T) {
	t.Run("formats IMAP capabilities correctly", func(t *testing.T) {
		result, err := formatIMAPCapabilities([]string{"IMAP4rev1", "IDLE"})
		if err != nil {
			t.Fatalf("formatIMAPCapabilities failed: %v", err)
		}
		expected := ` "IMAP4rev1" "IDLE"`
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("returns error for non-string-slice input", func(t *testing.T) {
		_, err := formatIMAPCapabilities("not-a-slice")
		if err == nil {
			t.Error("expected error for non-[]string input")
		}
	})
}

// TestFormatPOP3Capabilities tests formatPOP3Capabilities
func TestFormatPOP3Capabilities(t *testing.T) {
	t.Run("formats POP3 capabilities correctly", func(t *testing.T) {
		result, err := formatPOP3Capabilities([]string{"TOP", "UIDL", "USER"})
		if err != nil {
			t.Fatalf("formatPOP3Capabilities failed: %v", err)
		}
		expected := ` "TOP" "UIDL" "USER"`
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("returns error for non-string-slice input", func(t *testing.T) {
		_, err := formatPOP3Capabilities(42)
		if err == nil {
			t.Error("expected error for non-[]string input")
		}
	})
}

// TestResolvePOP3Capabilities_WhitespaceOnly tests resolvePOP3Capabilities with whitespace-only input
func TestResolvePOP3Capabilities_WhitespaceOnly(t *testing.T) {
	g := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: map[string]string{
			"zimbraReverseProxyPop3EnabledCapability": "\n\n\n",
		}},
	}

	result, err := g.resolvePOP3Capabilities(context.Background())
	if err != nil {
		t.Fatalf("resolvePOP3Capabilities failed: %v", err)
	}

	caps, ok := result.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", result)
	}

	if len(caps) != len(defaultPOP3Capabilities) {
		t.Errorf("expected %d default capabilities for whitespace-only input, got %d: %v",
			len(defaultPOP3Capabilities), len(caps), caps)
	}
}
