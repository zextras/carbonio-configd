// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// These tests exercise the ConfigManager delegation methods in manager_compare.go.
// They rely only on in-memory state lookups (no retry/sleep paths), so they are
// safe to run under -short.

// TestConfigManager_CheckConditional_Delegates verifies CheckConditional
// delegates to the constructor-initialised comparator.
func TestConfigManager_CheckConditional_Delegates(t *testing.T) {
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	got, err := cm.CheckConditional(context.Background(), "SERVICE", "imapd")
	if err != nil {
		t.Fatalf("CheckConditional() unexpected error: %v", err)
	}
	if !got {
		t.Error("CheckConditional() = false, want true for enabled service")
	}

	// Second call reuses the same comparator.
	got, err = cm.CheckConditional(context.Background(), "SERVICE", "!imapd")
	if err != nil {
		t.Fatalf("CheckConditional() unexpected error on reuse: %v", err)
	}
	if got {
		t.Error("CheckConditional() = true, want false for negated enabled service")
	}
}

// TestConfigManager_CompareKeys_Delegates verifies CompareKeys delegates to the
// constructor-initialised comparator and returns without error for a benign
// single-service state.
func TestConfigManager_CompareKeys_Delegates(t *testing.T) {
	cm := newTestConfigManager(t)
	cm.State.FirstRun = true
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	if err := cm.CompareKeys(context.Background()); err != nil {
		t.Fatalf("CompareKeys() unexpected error: %v", err)
	}
}

// TestConfigManager_ProcessConditionals_ShortSafe covers processConditionals with
// a simple true conditional under -short.
func TestConfigManager_ProcessConditionals_ShortSafe(t *testing.T) {
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	conditionals := []config.Conditional{
		{
			Type:     "SERVICE",
			Key:      "imapd",
			Postconf: map[string]string{"mailbox_transport": "lmtp"},
		},
	}
	cm.processConditionals(context.Background(), conditionals)

	if val, ok := cm.State.CurrentActions.Postconf["mailbox_transport"]; !ok {
		t.Error("expected postconf 'mailbox_transport' to be set")
	} else if val != "lmtp" {
		t.Errorf("expected postconf value 'lmtp', got %q", val)
	}
}
