// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.InitStructuredLogging(&logger.Config{
		Level:  slog.LevelError, // Reduce noise in tests
		Format: "text",
	})
	os.Exit(m.Run())
}

// TestCheckConditional_BasicTrue tests CheckConditional with a true value
func TestCheckConditional_BasicTrue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	result, err := cm.CheckConditional(context.Background(), "SERVICE", "imapd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !result {
		t.Error("Expected true for enabled service")
	}
}

// TestCheckConditional_BasicFalse tests CheckConditional with a false value
func TestCheckConditional_BasicFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// Don't add imapd to ServiceConfig - absence means disabled

	result, err := cm.CheckConditional(context.Background(), "SERVICE", "imapd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result {
		t.Error("Expected false for disabled service")
	}
}

// TestCheckConditional_Negated tests CheckConditional with negation
func TestCheckConditional_Negated(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// Negated conditional - should return false when service is enabled
	result, err := cm.CheckConditional(context.Background(), "SERVICE", "!imapd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result {
		t.Error("Expected false for negated enabled service")
	}
}

// TestCheckConditional_NegatedFalse tests CheckConditional with negation on false value
func TestCheckConditional_NegatedFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// Don't add imapd to ServiceConfig - absence means disabled

	// Negated conditional - should return true when service is disabled
	result, err := cm.CheckConditional(context.Background(), "SERVICE", "!imapd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !result {
		t.Error("Expected true for negated disabled service")
	}
}

// TestCheckConditional_MissingKey tests CheckConditional with missing key
func TestCheckConditional_MissingKey(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	// Missing key should be treated as false
	result, err := cm.CheckConditional(context.Background(), "SERVICE", "nonexistent")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result {
		t.Error("Expected false for missing service")
	}
}

// TestCheckConditional_MissingKeyNegated tests CheckConditional with missing key negated
func TestCheckConditional_MissingKeyNegated(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	// Missing key with negation should be treated as true
	result, err := cm.CheckConditional(context.Background(), "SERVICE", "!nonexistent")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !result {
		t.Error("Expected true for negated missing service")
	}
}

// TestCheckConditional_VarType tests CheckConditional with VAR type
func TestCheckConditional_VarType(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// VAR type looks in GlobalConfig, MiscConfig, or ServerConfig
	cm.State.GlobalConfig.Data.Set("zimbraIPMode", "ipv4")

	result, err := cm.CheckConditional(context.Background(), "VAR", "zimbraIPMode")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !result {
		t.Error("Expected true for existing VAR")
	}
}

// TestCheckConditional_ZeroValue tests CheckConditional with zero value (should be false)
func TestCheckConditional_ZeroValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// VAR type looks in GlobalConfig first
	cm.State.GlobalConfig.Data.Set("some_counter", "0")

	result, err := cm.CheckConditional(context.Background(), "VAR", "some_counter")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result {
		t.Error("Expected false for zero value")
	}
}

// TestCompareKeys_FirstRun tests CompareKeys on first run
func TestCompareKeys_FirstRun(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = true

	// Add a service to ServerConfig
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// Create a section with required vars
	cm.State.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}
	cm.State.LocalConfig.Data.Set("zimbraImapBindPort", "143")

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// On first run, service should be added to current services
	if status, exists := cm.State.CurrentActions.Services["imapd"]; !exists {
		t.Error("Expected imapd to be in CurrentActions.Services")
	} else if status != "running" {
		t.Errorf("Expected imapd status to be 'running', got: %s", status)
	}
}

// TestCompareKeys_ConfigChange tests CompareKeys detects config changes
func TestCompareKeys_ConfigChange(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = false

	// Create a section with required vars
	cm.State.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}

	// Set initial value
	cm.State.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "143")
	// Set new value in GlobalConfig (VAR type checks GlobalConfig first)
	cm.State.GlobalConfig.Data.Set("zimbraImapBindPort", "7143")

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Section should be marked as changed
	if !cm.State.MtaConfig.Sections["imap"].Changed {
		t.Error("Expected section 'imap' to be marked as changed")
	}

	// Check that changed keys were recorded
	changedKeys := cm.State.ChangedKeys["imap"]
	found := false
	for _, key := range changedKeys {
		if key == "zimbraImapBindPort" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'zimbraImapBindPort' to be in changed keys")
	}
}

// TestCompareKeys_ServiceDisabled tests CompareKeys detects disabled service
func TestCompareKeys_ServiceDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = false

	// Add service as currently running
	cm.State.CurrentActions.Services["imapd"] = "running"
	// Don't add to ServiceConfig - absence means disabled

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Service should be queued for stop (0)
	if action, exists := cm.State.CurrentActions.Restarts["imapd"]; !exists {
		t.Error("Expected imapd to be in restarts queue")
	} else if action != 0 {
		t.Errorf("Expected imapd restart action to be 0 (stop), got: %d", action)
	}
}

// TestCompareKeys_ServiceEnabled tests CompareKeys detects newly enabled service
func TestCompareKeys_ServiceEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = false

	// Service not in current services but enabled in ServerConfig
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// Ensure service manager has command for this service
	mockServiceMgr := &testutil.MockServiceManager{
		HasCommandFn: func(service string) bool { return service == "imapd" },
	}
	cm.ServiceMgr = mockServiceMgr

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Service should be queued for start (1)
	if action, exists := cm.State.CurrentActions.Restarts["imapd"]; !exists {
		t.Error("Expected imapd to be in restarts queue")
	} else if action != 1 {
		t.Errorf("Expected imapd restart action to be 1 (start), got: %d", action)
	}
}

// TestCompareKeys_AllServicesDisabled tests CompareKeys fails when all services disabled
func TestCompareKeys_AllServicesDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	// Add multiple services, all disabled (not in ServiceConfig)
	cm.State.CurrentActions.Services["imapd"] = "running"
	cm.State.CurrentActions.Services["mta"] = "running"
	// Don't add to ServiceConfig - absence means all are disabled

	err := cm.CompareKeys(context.Background())
	if err == nil {
		t.Fatal("Expected error when all services disabled")
	}
	if err.Error() != "all services detected disabled" {
		t.Errorf("Expected 'all services detected disabled' error, got: %v", err)
	}
}

// TestCompareKeys_ForcedConfig tests CompareKeys with forced configuration
func TestCompareKeys_ForcedConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = false

	// Create two sections
	cm.State.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}
	cm.State.MtaConfig.Sections["smtp"] = &config.MtaConfigSection{
		Name:         "smtp",
		RequiredVars: map[string]string{"zimbraSmtpPort": "VAR"},
		Changed:      false,
	}

	// Force only imap section
	cm.State.ForcedConfig["imap"] = "true"

	// Set different values in GlobalConfig (VAR type)
	cm.State.GlobalConfig.Data.Set("zimbraImapBindPort", "143")
	cm.State.GlobalConfig.Data.Set("zimbraSmtpPort", "25")
	cm.State.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "7143")
	cm.State.LastVal(context.Background(), "smtp", "VAR", "zimbraSmtpPort", "587")

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Only imap section should be processed (and marked changed)
	if !cm.State.MtaConfig.Sections["imap"].Changed {
		t.Error("Expected forced section 'imap' to be marked as changed")
	}
	// smtp should not be changed since it's not in forced config
	if cm.State.MtaConfig.Sections["smtp"].Changed {
		t.Error("Expected non-forced section 'smtp' to NOT be marked as changed")
	}
}

// TestCompareKeys_ValueBecomeUndefined tests CompareKeys when value becomes undefined
func TestCompareKeys_ValueBecomeUndefined(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = false

	cm.State.MtaConfig.Sections["imap"] = &config.MtaConfigSection{
		Name:         "imap",
		RequiredVars: map[string]string{"zimbraImapBindPort": "VAR"},
		Changed:      false,
	}

	// Set initial value
	cm.State.LastVal(context.Background(), "imap", "VAR", "zimbraImapBindPort", "143")
	// Don't set it in LocalConfig (it's now undefined)

	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Section should be marked as changed
	if !cm.State.MtaConfig.Sections["imap"].Changed {
		t.Error("Expected section 'imap' to be marked as changed when value becomes undefined")
	}
}

// TestProcessConditionals_Simple tests processConditionals with a simple conditional
func TestProcessConditionals_Simple(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	conditionals := []config.Conditional{
		{
			Type: "SERVICE",
			Key:  "imapd",
			Postconf: map[string]string{
				"mailbox_transport": "lmtp:unix:/opt/zextras/data/mailboxd/imap",
			},
		},
	}

	cm.processConditionals(context.Background(), conditionals)

	// Check that postconf was added
	if val, exists := cm.State.CurrentActions.Postconf["mailbox_transport"]; !exists {
		t.Error("Expected postconf 'mailbox_transport' to be set")
	} else if val != "lmtp:unix:/opt/zextras/data/mailboxd/imap" {
		t.Errorf("Expected postconf value to be 'lmtp:unix:/opt/zextras/data/mailboxd/imap', got: %s", val)
	}
}

// TestProcessConditionals_Negated tests processConditionals with negated conditional
func TestProcessConditionals_Negated(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// Don't add imapd to ServiceConfig - absence means disabled

	conditionals := []config.Conditional{
		{
			Type:    "SERVICE",
			Key:     "imapd",
			Negated: true, // Should process when imapd is disabled
			Postconf: map[string]string{
				"mailbox_transport": "error:service disabled",
			},
		},
	}

	cm.processConditionals(context.Background(), conditionals)

	// Check that postconf was added (because condition is negated and service is false)
	if val, exists := cm.State.CurrentActions.Postconf["mailbox_transport"]; !exists {
		t.Error("Expected postconf 'mailbox_transport' to be set")
	} else if val != "error:service disabled" {
		t.Errorf("Expected postconf value to be 'error:service disabled', got: %s", val)
	}
}

// TestProcessConditionals_Skipped tests processConditionals with false condition
func TestProcessConditionals_Skipped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// Don't add imapd to ServiceConfig - absence means disabled

	conditionals := []config.Conditional{
		{
			Type: "SERVICE",
			Key:  "imapd",
			Postconf: map[string]string{
				"mailbox_transport": "lmtp:unix:/opt/zextras/data/mailboxd/imap",
			},
		},
	}

	cm.processConditionals(context.Background(), conditionals)

	// Check that postconf was NOT added (condition is false)
	if _, exists := cm.State.CurrentActions.Postconf["mailbox_transport"]; exists {
		t.Error("Expected postconf 'mailbox_transport' NOT to be set when condition is false")
	}
}

// TestProcessConditionals_Nested tests processConditionals with nested conditionals
func TestProcessConditionals_Nested(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")
	// VAR type looks in GlobalConfig
	cm.State.GlobalConfig.Data.Set("zimbraIPMode", "ipv6")

	conditionals := []config.Conditional{
		{
			Type: "SERVICE",
			Key:  "imapd",
			Nested: []config.Conditional{
				{
					Type: "VAR",
					Key:  "zimbraIPMode",
					Postconf: map[string]string{
						"inet_protocols": "ipv6",
					},
				},
			},
		},
	}

	cm.processConditionals(context.Background(), conditionals)

	// Check that nested postconf was added (both conditions true)
	if val, exists := cm.State.CurrentActions.Postconf["inet_protocols"]; !exists {
		t.Error("Expected nested postconf 'inet_protocols' to be set")
	} else if val != "ipv6" {
		t.Errorf("Expected nested postconf value to be 'ipv6', got: %s", val)
	}
}

// TestProcessConditionals_MultipleDirectives tests processConditionals with multiple directive types
func TestProcessConditionals_MultipleDirectives(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("cbpolicyd", "TRUE")

	conditionals := []config.Conditional{
		{
			Type: "SERVICE",
			Key:  "cbpolicyd",
			Postconf: map[string]string{
				"smtpd_end_of_data_restrictions": "check_policy_service inet:127.0.0.1:10031",
			},
			Postconfd: map[string]string{
				"policy.cf": "some config content",
			},
			Ldap: map[string]string{
				"ldap-alias.cf": "ldap://localhost",
			},
		},
	}

	cm.processConditionals(context.Background(), conditionals)

	// Check all directive types were added
	if _, exists := cm.State.CurrentActions.Postconf["smtpd_end_of_data_restrictions"]; !exists {
		t.Error("Expected postconf to be set")
	}
	if _, exists := cm.State.CurrentActions.Postconfd["policy.cf"]; !exists {
		t.Error("Expected postconfd to be set")
	}
	if _, exists := cm.State.CurrentActions.Ldap["ldap-alias.cf"]; !exists {
		t.Error("Expected ldap to be set")
	}
}

// TestCheckConditional_ComparatorAvailable verifies CheckConditional works
// through the constructor-initialised comparator.
func TestCheckConditional_ComparatorAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// NewConfigManager initialises the comparator eagerly.
	if cm.comparator == nil {
		t.Fatal("Expected comparator to be initialized by the constructor")
	}

	result, err := cm.CheckConditional(context.Background(), "SERVICE", "imapd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if !result {
		t.Error("Expected true for enabled service")
	}

	if cm.comparator == nil {
		t.Error("Expected comparator to remain initialized after CheckConditional")
	}
}

// TestCompareKeys_ComparatorAvailable verifies the comparator is wired up by
// the constructor and remains usable across CompareKeys calls.
func TestCompareKeys_ComparatorAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = true
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// NewConfigManager initialises the comparator eagerly.
	if cm.comparator == nil {
		t.Fatal("Expected comparator to be initialized by the constructor")
	}

	// CompareKeys uses the existing comparator without error.
	if err := cm.CompareKeys(context.Background()); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cm.comparator == nil {
		t.Error("Expected comparator to remain initialized after CompareKeys")
	}
}

// TestCompareKeys_ReuseComparator tests that CompareKeys reuses the comparator on subsequent calls
func TestCompareKeys_ReuseComparator(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.State.FirstRun = true
	cm.State.ServerConfig.ServiceConfig.Set("imapd", "TRUE")

	// First call initializes the comparator
	err := cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error on first call, got: %v", err)
	}
	firstComparator := cm.comparator

	// Second call should reuse the same comparator
	err = cm.CompareKeys(context.Background())
	if err != nil {
		t.Fatalf("Expected no error on second call, got: %v", err)
	}
	secondComparator := cm.comparator

	if firstComparator != secondComparator {
		t.Error("Expected comparator to be reused on subsequent calls")
	}
}

// TestConfigManager_Comparator verifies the Comparator() accessor returns the
// embedded comparator that was initialised by the constructor.
func TestConfigManager_Comparator(t *testing.T) {
	cm := newTestConfigManager(t)

	comp := cm.Comparator()
	if comp == nil {
		t.Fatal("Comparator() returned nil, expected a valid Comparator instance")
	}
}
