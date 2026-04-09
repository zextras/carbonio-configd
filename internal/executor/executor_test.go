// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package executor

import (
	"context"
	"errors"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"testing"
)

// mockConfigLookup implements the ConfigLookup interface for testing
type mockConfigLookup struct {
	data map[string]map[string]string
}

func (m *mockConfigLookup) LookUpConfig(ctx context.Context, cfgType, key string) (string, error) {
	if typeData, ok := m.data[cfgType]; ok {
		if val, ok := typeData[key]; ok {
			return val, nil
		}
	}
	// Return error when key is not found
	return "", errors.New("key not found")
}

// mockPostfixManager implements the postfix.Manager interface for testing
type mockPostfixManager struct {
	postconfCalls   map[string]string
	postconfdCalls  []string
	flushConfCalls  int
	flushConfdCalls int
}

func newMockPostfixManager() *mockPostfixManager {
	return &mockPostfixManager{
		postconfCalls:  make(map[string]string),
		postconfdCalls: make([]string, 0),
	}
}

func (m *mockPostfixManager) AddPostconf(_ context.Context, key, value string) error {
	m.postconfCalls[key] = value
	return nil
}

func (m *mockPostfixManager) AddPostconfd(_ context.Context, key string) error {
	m.postconfdCalls = append(m.postconfdCalls, key)
	return nil
}

func (m *mockPostfixManager) FlushPostconf(_ context.Context) error {
	m.flushConfCalls++
	return nil
}

func (m *mockPostfixManager) FlushPostconfd(_ context.Context) error {
	m.flushConfdCalls++
	return nil
}

func (m *mockPostfixManager) GetPendingChanges() (map[string]string, []string) {
	return m.postconfCalls, m.postconfdCalls
}

func (m *mockPostfixManager) ClearPending(_ context.Context) {
	m.postconfCalls = make(map[string]string)
	m.postconfdCalls = make([]string, 0)
}

// mockServiceManager implements the services.Manager interface for testing
type mockServiceManager struct {
	restartQueue map[string]bool
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{
		restartQueue: make(map[string]bool),
	}
}

func (m *mockServiceManager) ControlProcess(_ context.Context, service string, action services.ServiceAction) error {
	return nil
}

func (m *mockServiceManager) IsRunning(_ context.Context, service string) (bool, error) {
	return false, nil
}

func (m *mockServiceManager) AddRestart(_ context.Context, service string) error {
	m.restartQueue[service] = true
	return nil
}

func (m *mockServiceManager) ProcessRestarts(_ context.Context, configLookup func(string) string) error {
	return nil
}

func (m *mockServiceManager) ClearRestarts(_ context.Context) {
	m.restartQueue = make(map[string]bool)
}

func (m *mockServiceManager) GetPendingRestarts() []string {
	svcs := make([]string, 0, len(m.restartQueue))
	for svc := range m.restartQueue {
		svcs = append(svcs, svc)
	}
	return svcs
}

func (m *mockServiceManager) SetDependencies(_ context.Context, deps map[string][]string) {
	// No-op for mock
}

func (m *mockServiceManager) AddDependencyRestarts(_ context.Context, sectionName string, configLookup func(string) string) {
	// No-op for mock
}

func (m *mockServiceManager) HasCommand(service string) bool {
	// For testing, return true for all services
	return true
}

func (m *mockServiceManager) SetUseSystemd(enabled bool) {
	// No-op for mock
}

func newMockLookup() *mockConfigLookup {
	return &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMtaEnableSmtpdPolicyd": "TRUE",
				"zimbraMtaMyNetworks":         "127.0.0.0/8 192.168.1.0/24",
				"zimbraMtaMyOrigin":           "example.com",
				"emptyVar":                    "",
			},
			"SERVICE": {
				"antivirus": "TRUE",
				"antispam":  "TRUE",
				"webmail":   "FALSE",
			},
		},
	}
}

func TestEvaluateConditional_Service(t *testing.T) {
	tests := []struct {
		name     string
		cond     config.Conditional
		expected bool
	}{
		{
			name: "Service enabled",
			cond: config.Conditional{
				Type:    "SERVICE",
				Key:     "antivirus",
				Negated: false,
			},
			expected: true,
		},
		{
			name: "Service disabled",
			cond: config.Conditional{
				Type:    "SERVICE",
				Key:     "webmail",
				Negated: false,
			},
			expected: false,
		},
		{
			name: "Service enabled with negation",
			cond: config.Conditional{
				Type:    "SERVICE",
				Key:     "antivirus",
				Negated: true,
			},
			expected: false,
		},
		{
			name: "Service disabled with negation",
			cond: config.Conditional{
				Type:    "SERVICE",
				Key:     "webmail",
				Negated: true,
			},
			expected: true,
		},
		{
			name: "Service not found",
			cond: config.Conditional{
				Type:    "SERVICE",
				Key:     "nonexistent",
				Negated: false,
			},
			expected: false,
		},
	}

	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.EvaluateConditional(context.Background(), &tt.cond)
			if result != tt.expected {
				t.Errorf("EvaluateConditional() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestEvaluateConditional_Var(t *testing.T) {
	tests := []struct {
		name     string
		cond     config.Conditional
		expected bool
	}{
		{
			name: "VAR enabled",
			cond: config.Conditional{
				Type:    "VAR",
				Key:     "zimbraMtaEnableSmtpdPolicyd",
				Negated: false,
			},
			expected: true,
		},
		{
			name: "VAR empty string",
			cond: config.Conditional{
				Type:    "VAR",
				Key:     "emptyVar",
				Negated: false,
			},
			expected: false,
		},
		{
			name: "VAR enabled with negation",
			cond: config.Conditional{
				Type:    "VAR",
				Key:     "zimbraMtaEnableSmtpdPolicyd",
				Negated: true,
			},
			expected: false,
		},
		{
			name: "VAR not found",
			cond: config.Conditional{
				Type:    "VAR",
				Key:     "nonexistentVar",
				Negated: false,
			},
			expected: false,
		},
		{
			name: "VAR not found with negation",
			cond: config.Conditional{
				Type:    "VAR",
				Key:     "nonexistentVar",
				Negated: true,
			},
			expected: true,
		},
	}

	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.EvaluateConditional(context.Background(), &tt.cond)
			if result != tt.expected {
				t.Errorf("EvaluateConditional() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExecuteSection(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	mockSvc := newMockServiceManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, mockSvc)

	section := &config.MtaConfigSection{
		Name: "mta",
		Postconf: map[string]string{
			"myhostname": "mail.example.com",
		},
		Postconfd: map[string]string{},
		Ldap:      map[string]string{},
		Conditionals: []config.Conditional{
			{
				Type:    "VAR",
				Key:     "zimbraMtaEnableSmtpdPolicyd",
				Negated: false,
				Postconf: map[string]string{
					"policy_time_limit": "3600",
				},
			},
			{
				Type:    "VAR",
				Key:     "zimbraMtaMyNetworks",
				Negated: false,
				Postconf: map[string]string{
					"mynetworks": "127.0.0.0/8 192.168.1.0/24",
				},
			},
			{
				Type:    "SERVICE",
				Key:     "webmail",
				Negated: false,
				Postconf: map[string]string{
					"webmail_enabled": "yes",
				},
			},
		},
	}

	postconf, postconfd, ldap, _ := executor.ExecuteSection(context.Background(), section)

	// Check base postconf is present
	if val, ok := postconf["myhostname"]; !ok || val != "mail.example.com" {
		t.Errorf("Expected base postconf myhostname=mail.example.com, got %s", val)
	}

	// Check first conditional (zimbraMtaEnableSmtpdPolicyd is TRUE)
	if val, ok := postconf["policy_time_limit"]; !ok || val != "3600" {
		t.Errorf("Expected conditional postconf policy_time_limit=3600, got %s", val)
	}

	// Check second conditional (zimbraMtaMyNetworks is present)
	if val, ok := postconf["mynetworks"]; !ok || val != "127.0.0.0/8 192.168.1.0/24" {
		t.Errorf("Expected conditional postconf mynetworks, got %s", val)
	}

	// Check third conditional (webmail service is FALSE, should not be present)
	if _, ok := postconf["webmail_enabled"]; ok {
		t.Errorf("Expected webmail_enabled to not be present (service disabled)")
	}

	// Check empty postconfd and ldap
	if len(postconfd) != 0 {
		t.Errorf("Expected empty postconfd, got %d entries", len(postconfd))
	}
	if len(ldap) != 0 {
		t.Errorf("Expected empty ldap, got %d entries", len(ldap))
	}
}

func TestExecuteSection_WithNegation(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	section := &config.MtaConfigSection{
		Name:      "mta",
		Postconf:  map[string]string{},
		Postconfd: map[string]string{},
		Ldap:      map[string]string{},
		Conditionals: []config.Conditional{
			{
				Type:    "VAR",
				Key:     "zimbraMtaEnableSmtpdPolicyd",
				Negated: true, // Negated condition
				Postconfd: map[string]string{
					"policy_time_limit": "DELETE",
				},
			},
		},
	}

	_, postconfd, _, _ := executor.ExecuteSection(context.Background(), section)

	// Check negated conditional (zimbraMtaEnableSmtpdPolicyd is TRUE, but negated, so should not execute)
	if _, ok := postconfd["policy_time_limit"]; ok {
		t.Errorf("Expected postconfd policy_time_limit to not be present (negated condition not met)")
	}

	// Now test with a false variable
	section.Conditionals[0].Key = "emptyVar" // emptyVar is ""
	_, postconfd, _, _ = executor.ExecuteSection(context.Background(), section)

	// This time, the negated condition should execute
	if val, ok := postconfd["policy_time_limit"]; !ok || val != "DELETE" {
		t.Errorf("Expected postconfd policy_time_limit=DELETE, got %s", val)
	}
}

func TestExecuteSection_NestedConditionals(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	section := &config.MtaConfigSection{
		Name:      "mta",
		Postconf:  map[string]string{},
		Postconfd: map[string]string{},
		Ldap:      map[string]string{},
		Conditionals: []config.Conditional{
			{
				Type:    "SERVICE",
				Key:     "antivirus",
				Negated: false,
				Postconf: map[string]string{
					"content_filter": "amavis",
				},
				Nested: []config.Conditional{
					{
						Type:    "VAR",
						Key:     "zimbraMtaEnableSmtpdPolicyd",
						Negated: false,
						Postconf: map[string]string{
							"nested_policy": "enabled",
						},
					},
				},
			},
		},
	}

	postconf, _, _, _ := executor.ExecuteSection(context.Background(), section)

	// Check parent conditional is executed (antivirus is TRUE)
	if val, ok := postconf["content_filter"]; !ok || val != "amavis" {
		t.Errorf("Expected content_filter=amavis, got %s", val)
	}

	// Check nested conditional is executed (zimbraMtaEnableSmtpdPolicyd is TRUE)
	if val, ok := postconf["nested_policy"]; !ok || val != "enabled" {
		t.Errorf("Expected nested_policy=enabled, got %s", val)
	}
}

func TestCheckRequiredVars(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	section := &config.MtaConfigSection{
		Name: "mta",
		RequiredVars: map[string]string{
			"zimbraMtaEnableSmtpdPolicyd": "VAR",
			"antivirus":                   "SERVICE",
			"nonexistent":                 "VAR",
		},
	}

	result := executor.CheckRequiredVars(context.Background(), section)

	// Should return false because "nonexistent" is missing
	if result {
		t.Errorf("Expected CheckRequiredVars to return false (missing var), got true")
	}

	// Now test with all required vars present
	section.RequiredVars = map[string]string{
		"zimbraMtaEnableSmtpdPolicyd": "VAR",
		"antivirus":                   "SERVICE",
	}

	result = executor.CheckRequiredVars(context.Background(), section)

	if !result {
		t.Errorf("Expected CheckRequiredVars to return true (all vars present), got false")
	}
}

func TestGetSectionDependencies(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	section := &config.MtaConfigSection{
		Name: "antispam",
		Depends: map[string]bool{
			"amavis": true,
			"mta":    true,
		},
	}

	deps := executor.GetSectionDependencies(section)

	if len(deps) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(deps))
	}

	// Check both dependencies are present (order doesn't matter)
	foundAmavis := false
	foundMta := false
	for _, dep := range deps {
		if dep == "amavis" {
			foundAmavis = true
		}
		if dep == "mta" {
			foundMta = true
		}
	}

	if !foundAmavis || !foundMta {
		t.Errorf("Expected dependencies 'amavis' and 'mta', got %v", deps)
	}
}

// TestApplyPostfixDirectives tests the ApplyPostfixDirectives method.
func TestApplyPostfixDirectives(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	postconf := map[string]string{
		"myhostname":   "mail.example.com",
		"mynetworks":   "127.0.0.0/8",
		"smtpd_banner": "$myhostname ESMTP",
	}

	postconfd := map[string]string{
		"content_filter":     "",
		"virtual_alias_maps": "",
	}

	err := executor.ApplyPostfixDirectives(context.Background(), postconf, postconfd)
	if err != nil {
		t.Errorf("ApplyPostfixDirectives failed: %v", err)
	}

	// Verify postconf calls
	if len(mockPfx.postconfCalls) != 3 {
		t.Errorf("Expected 3 postconf calls, got %d", len(mockPfx.postconfCalls))
	}
	if mockPfx.postconfCalls["myhostname"] != "mail.example.com" {
		t.Errorf("Expected myhostname=mail.example.com, got %s", mockPfx.postconfCalls["myhostname"])
	}

	// Verify postconfd calls
	if len(mockPfx.postconfdCalls) != 2 {
		t.Errorf("Expected 2 postconfd calls, got %d", len(mockPfx.postconfdCalls))
	}
}

// TestFlushPostfixChanges tests the FlushPostfixChanges method.
func TestFlushPostfixChanges(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	// Add some changes
	postconf := map[string]string{
		"myhostname": "mail.example.com",
	}
	postconfd := map[string]string{
		"content_filter": "",
	}
	executor.ApplyPostfixDirectives(context.Background(), postconf, postconfd)

	// Flush changes
	err := executor.FlushPostfixChanges(context.Background())
	if err != nil {
		t.Errorf("FlushPostfixChanges failed: %v", err)
	}

	// Verify flush was called
	if mockPfx.flushConfCalls != 1 {
		t.Errorf("Expected 1 FlushPostconf call, got %d", mockPfx.flushConfCalls)
	}
	if mockPfx.flushConfdCalls != 1 {
		t.Errorf("Expected 1 FlushPostconfd call, got %d", mockPfx.flushConfdCalls)
	}
}

// TestProcessAllSections tests processing multiple sections and batching postfix changes.
func TestProcessAllSections(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	// Create MtaConfig with multiple sections
	mtaConfig := &config.MtaConfig{
		Sections: map[string]*config.MtaConfigSection{
			"mta": {
				Name:    "mta",
				Changed: false,
				Postconf: map[string]string{
					"myhostname": "mail.example.com",
					"mynetworks": "127.0.0.0/8",
				},
				Postconfd: map[string]string{
					"content_filter": "",
				},
				RequiredVars: map[string]string{},
				Depends:      map[string]bool{},
				Rewrites:     map[string]config.RewriteEntry{},
				Restarts:     map[string]bool{},
			},
			"proxy": {
				Name:    "proxy",
				Changed: false,
				Postconf: map[string]string{
					"smtpd_banner": "$myhostname ESMTP",
				},
				Postconfd:    map[string]string{},
				RequiredVars: map[string]string{},
				Depends:      map[string]bool{},
				Rewrites:     map[string]config.RewriteEntry{},
				Restarts:     map[string]bool{},
			},
			"antispam": {
				Name:    "antispam",
				Changed: false,
				Conditionals: []config.Conditional{
					{
						Type:    "SERVICE",
						Key:     "antispam",
						Negated: false,
						Postconf: map[string]string{
							"content_filter": "smtp-amavis:[127.0.0.1]:10024",
						},
						Postconfd: map[string]string{},
					},
				},
				RequiredVars: map[string]string{},
				Depends:      map[string]bool{},
				Rewrites:     map[string]config.RewriteEntry{},
				Restarts:     map[string]bool{},
			},
		},
	}

	// Process all sections
	err := executor.ProcessAllSections(context.Background(), mtaConfig)
	if err != nil {
		t.Fatalf("ProcessAllSections failed: %v", err)
	}

	// Verify that all postconf changes were queued
	expectedPostconfCount := 4 // myhostname, mynetworks, smtpd_banner, content_filter
	if len(mockPfx.postconfCalls) != expectedPostconfCount {
		t.Errorf("Expected %d postconf calls, got %d", expectedPostconfCount, len(mockPfx.postconfCalls))
	}

	// Verify specific postconf values
	if mockPfx.postconfCalls["myhostname"] != "mail.example.com" {
		t.Errorf("Expected myhostname=mail.example.com, got %s", mockPfx.postconfCalls["myhostname"])
	}
	if mockPfx.postconfCalls["content_filter"] != "smtp-amavis:[127.0.0.1]:10024" {
		t.Errorf("Expected content_filter from conditional, got %s", mockPfx.postconfCalls["content_filter"])
	}

	// Verify postconfd deletions
	if len(mockPfx.postconfdCalls) != 1 {
		t.Errorf("Expected 1 postconfd call, got %d", len(mockPfx.postconfdCalls))
	}

	// Now flush changes
	err = executor.FlushPostfixChanges(context.Background())
	if err != nil {
		t.Fatalf("FlushPostfixChanges failed: %v", err)
	}

	// Verify flush was called
	if mockPfx.flushConfCalls != 1 {
		t.Errorf("Expected 1 FlushPostconf call, got %d", mockPfx.flushConfCalls)
	}
	if mockPfx.flushConfdCalls != 1 {
		t.Errorf("Expected 1 FlushPostconfd call, got %d", mockPfx.flushConfdCalls)
	}
}

// TestProcessAllSectionsWithMissingRequiredVars tests that sections with missing required vars are skipped.
func TestProcessAllSectionsWithMissingRequiredVars(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	mtaConfig := &config.MtaConfig{
		Sections: map[string]*config.MtaConfigSection{
			"valid": {
				Name: "valid",
				Postconf: map[string]string{
					"myhostname": "mail.example.com",
				},
				RequiredVars: map[string]string{},
			},
			"invalid": {
				Name: "invalid",
				Postconf: map[string]string{
					"mynetworks": "127.0.0.0/8",
				},
				RequiredVars: map[string]string{
					"nonexistent": "VAR", // This var doesn't exist in mockLookup
				},
			},
		},
	}

	// Process all sections
	err := executor.ProcessAllSections(context.Background(), mtaConfig)
	if err != nil {
		t.Fatalf("ProcessAllSections failed: %v", err)
	}

	// Should only have processed the valid section
	if len(mockPfx.postconfCalls) != 1 {
		t.Errorf("Expected 1 postconf call (only valid section), got %d", len(mockPfx.postconfCalls))
	}
	if mockPfx.postconfCalls["myhostname"] != "mail.example.com" {
		t.Error("Expected myhostname from valid section")
	}
	if _, exists := mockPfx.postconfCalls["mynetworks"]; exists {
		t.Error("Should not have processed invalid section")
	}
}

// TestExpandValue tests the ExpandValue method for FILE, VAR, LOCAL, and MAPLOCAL directives.
func TestExpandValue(t *testing.T) {
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"FILE": {
				"zmconfigd/test.cf": "permit_sasl_authenticated, reject",
			},
			"VAR": {
				"zimbraMtaMyNetworks": "127.0.0.0/8 192.168.1.0/24",
			},
			"LOCAL": {
				"zimbra_server_hostname": "mail.example.com",
			},
			"MAPLOCAL": {
				"zimbraSSLDHParam": "/opt/zextras/conf/dhparam.pem",
			},
		},
	}
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:        "FILE directive",
			input:       "FILE zmconfigd/test.cf",
			expected:    "permit_sasl_authenticated, reject",
			expectError: false,
		},
		{
			name:        "VAR directive",
			input:       "VAR:zimbraMtaMyNetworks",
			expected:    "127.0.0.0/8 192.168.1.0/24",
			expectError: false,
		},
		{
			name:        "LOCAL directive",
			input:       "LOCAL:zimbra_server_hostname",
			expected:    "mail.example.com",
			expectError: false,
		},
		{
			name:        "MAPLOCAL directive",
			input:       "MAPLOCAL:zimbraSSLDHParam",
			expected:    "/opt/zextras/conf/dhparam.pem",
			expectError: false,
		},
		{
			name:        "Literal value",
			input:       "smtp-amavis:[127.0.0.1]:10024",
			expected:    "smtp-amavis:[127.0.0.1]:10024",
			expectError: false,
		},
		{
			name:        "Empty value",
			input:       "",
			expected:    "",
			expectError: false,
		},
		{
			name:        "FILE not found",
			input:       "FILE zmconfigd/nonexistent.cf",
			expected:    "",
			expectError: true,
		},
		{
			name:        "VAR not found",
			input:       "VAR:nonexistent",
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.ExpandValue(context.Background(), tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for input %q, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("Expected %q, got %q", tt.expected, result)
				}
			}
		})
	}
}

// TestApplyPostfixDirectivesWithExpansion tests that ApplyPostfixDirectives expands values.
func TestApplyPostfixDirectivesWithExpansion(t *testing.T) {
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"FILE": {
				"zmconfigd/smtpd_recipient_restrictions.cf": "permit_sasl_authenticated, permit_mynetworks, reject_unauth_destination",
			},
			"VAR": {
				"zimbraMtaMyNetworks": "127.0.0.0/8",
			},
		},
	}
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	postconf := map[string]string{
		"smtpd_recipient_restrictions": "FILE zmconfigd/smtpd_recipient_restrictions.cf",
		"mynetworks":                   "VAR:zimbraMtaMyNetworks",
		"myhostname":                   "mail.example.com", // literal
	}
	postconfd := map[string]string{}

	err := executor.ApplyPostfixDirectives(context.Background(), postconf, postconfd)
	if err != nil {
		t.Fatalf("ApplyPostfixDirectives failed: %v", err)
	}

	// Verify expanded values
	expected := map[string]string{
		"smtpd_recipient_restrictions": "permit_sasl_authenticated, permit_mynetworks, reject_unauth_destination",
		"mynetworks":                   "127.0.0.0/8",
		"myhostname":                   "mail.example.com",
	}

	for key, expectedValue := range expected {
		if mockPfx.postconfCalls[key] != expectedValue {
			t.Errorf("Key %s: expected %q, got %q", key, expectedValue, mockPfx.postconfCalls[key])
		}
	}
}

// mockFailingPostfixManager is a mockPostfixManager that can return errors on demand.
type mockFailingPostfixManager struct {
	mockPostfixManager
	failAddPostconf  bool
	failAddPostconfd bool
	failFlushConf    bool
	failFlushConfd   bool
}

func newMockFailingPostfixManager() *mockFailingPostfixManager {
	return &mockFailingPostfixManager{
		mockPostfixManager: mockPostfixManager{
			postconfCalls:  make(map[string]string),
			postconfdCalls: make([]string, 0),
		},
	}
}

func (m *mockFailingPostfixManager) AddPostconf(ctx context.Context, key, value string) error {
	if m.failAddPostconf {
		return errors.New("AddPostconf error")
	}
	return m.mockPostfixManager.AddPostconf(ctx, key, value)
}

func (m *mockFailingPostfixManager) AddPostconfd(ctx context.Context, key string) error {
	if m.failAddPostconfd {
		return errors.New("AddPostconfd error")
	}
	return m.mockPostfixManager.AddPostconfd(ctx, key)
}

func (m *mockFailingPostfixManager) FlushPostconf(ctx context.Context) error {
	if m.failFlushConf {
		return errors.New("FlushPostconf error")
	}
	return m.mockPostfixManager.FlushPostconf(ctx)
}

func (m *mockFailingPostfixManager) FlushPostconfd(ctx context.Context) error {
	if m.failFlushConfd {
		return errors.New("FlushPostconfd error")
	}
	return m.mockPostfixManager.FlushPostconfd(ctx)
}

// mockFailingServiceManager is a mockServiceManager that returns an error on AddRestart.
type mockFailingServiceManager struct {
	mockServiceManager
	failAddRestart bool
}

func newMockFailingServiceManager() *mockFailingServiceManager {
	return &mockFailingServiceManager{
		mockServiceManager: mockServiceManager{
			restartQueue: make(map[string]bool),
		},
	}
}

func (m *mockFailingServiceManager) AddRestart(_ context.Context, service string) error {
	if m.failAddRestart {
		return errors.New("AddRestart error")
	}
	m.restartQueue[service] = true
	return nil
}

// TestExpandValue_LocalError tests that ExpandValue propagates errors from LOCAL lookup.
func TestExpandValue_LocalError(t *testing.T) {
	// mockConfigLookup returns error when key is not found; LOCAL:badkey is not in data.
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{},
	}
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	_, err := exec.ExpandValue(context.Background(), "LOCAL:badkey")
	if err == nil {
		t.Error("Expected error for LOCAL:badkey, got nil")
	}
}

// TestExpandValue_MapLocalError tests that ExpandValue propagates errors from MAPLOCAL lookup.
func TestExpandValue_MapLocalError(t *testing.T) {
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{},
	}
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	_, err := exec.ExpandValue(context.Background(), "MAPLOCAL:badkey")
	if err == nil {
		t.Error("Expected error for MAPLOCAL:badkey, got nil")
	}
}

// TestApplyPostfixDirectives_AddPostconfError tests that AddPostconf errors are propagated.
func TestApplyPostfixDirectives_AddPostconfError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockFailingPostfixManager()
	mockPfx.failAddPostconf = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	postconf := map[string]string{"myhostname": "mail.example.com"}
	postconfd := map[string]string{}

	err := exec.ApplyPostfixDirectives(context.Background(), postconf, postconfd)
	if err == nil {
		t.Error("Expected error from AddPostconf, got nil")
	}
}

// TestApplyPostfixDirectives_AddPostconfdError tests that AddPostconfd errors are propagated.
func TestApplyPostfixDirectives_AddPostconfdError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockFailingPostfixManager()
	mockPfx.failAddPostconfd = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	postconf := map[string]string{}
	postconfd := map[string]string{"content_filter": ""}

	err := exec.ApplyPostfixDirectives(context.Background(), postconf, postconfd)
	if err == nil {
		t.Error("Expected error from AddPostconfd, got nil")
	}
}

// TestFlushPostfixChanges_FlushPostconfError tests that FlushPostconf errors are propagated.
func TestFlushPostfixChanges_FlushPostconfError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockFailingPostfixManager()
	mockPfx.failFlushConf = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	err := exec.FlushPostfixChanges(context.Background())
	if err == nil {
		t.Error("Expected error from FlushPostconf, got nil")
	}
}

// TestFlushPostfixChanges_FlushPostconfdError tests that FlushPostconfd errors are propagated.
func TestFlushPostfixChanges_FlushPostconfdError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockFailingPostfixManager()
	mockPfx.failFlushConfd = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	err := exec.FlushPostfixChanges(context.Background())
	if err == nil {
		t.Error("Expected error from FlushPostconfd, got nil")
	}
}

// TestProcessAllSections_ApplyError tests that ApplyPostfixDirectives errors are propagated.
func TestProcessAllSections_ApplyError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockFailingPostfixManager()
	mockPfx.failAddPostconf = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, newMockServiceManager())

	mtaConfig := &config.MtaConfig{
		Sections: map[string]*config.MtaConfigSection{
			"mta": {
				Name: "mta",
				Postconf: map[string]string{
					"myhostname": "mail.example.com",
				},
				Postconfd:    map[string]string{},
				RequiredVars: map[string]string{},
				Depends:      map[string]bool{},
				Rewrites:     map[string]config.RewriteEntry{},
				Restarts:     map[string]bool{},
			},
		},
	}

	err := exec.ProcessAllSections(context.Background(), mtaConfig)
	if err == nil {
		t.Error("Expected error from ProcessAllSections when ApplyPostfixDirectives fails, got nil")
	}
}

// TestProcessAllSections_AddRestartError tests that AddRestart errors are logged but do not stop processing.
func TestProcessAllSections_AddRestartError(t *testing.T) {
	mockLookup := newMockLookup()
	st := state.NewState()
	mockPfx := newMockPostfixManager()
	failSvc := newMockFailingServiceManager()
	failSvc.failAddRestart = true
	exec := NewSectionExecutor(mockLookup, st, mockPfx, failSvc)

	mtaConfig := &config.MtaConfig{
		Sections: map[string]*config.MtaConfigSection{
			"mta": {
				Name:         "mta",
				Postconf:     map[string]string{"myhostname": "mail.example.com"},
				Postconfd:    map[string]string{},
				RequiredVars: map[string]string{},
				Depends:      map[string]bool{},
				Rewrites:     map[string]config.RewriteEntry{},
				Restarts:     map[string]bool{"mta": true},
			},
		},
	}

	// AddRestart error is only logged (WarnContext), processing continues.
	err := exec.ProcessAllSections(context.Background(), mtaConfig)
	if err != nil {
		t.Errorf("Expected ProcessAllSections to succeed despite AddRestart error, got: %v", err)
	}
}

// TestExecuteSection_WithRestarts tests that RESTART directives are properly collected.
func TestExecuteSection_WithRestarts(t *testing.T) {
	st := &state.State{}
	mockLookup := newMockLookup()
	mockPfx := newMockPostfixManager()
	mockSvc := newMockServiceManager()
	executor := NewSectionExecutor(mockLookup, st, mockPfx, mockSvc)

	section := &config.MtaConfigSection{
		Name:      "mta",
		Postconf:  map[string]string{},
		Postconfd: map[string]string{},
		Ldap:      map[string]string{},
		Restarts: map[string]bool{
			"mta": true,
		},
		Conditionals: []config.Conditional{
			{
				Type:    "SERVICE",
				Key:     "antivirus",
				Negated: false,
				Restarts: map[string]bool{
					"amavis": true,
				},
			},
			{
				Type:    "SERVICE",
				Key:     "webmail",
				Negated: false,
				Restarts: map[string]bool{
					"proxy": true, // Should NOT be included (webmail is FALSE)
				},
			},
		},
	}

	_, _, _, restarts := executor.ExecuteSection(context.Background(), section)

	// Check base restart is present
	if !restarts["mta"] {
		t.Error("Expected base restart 'mta' to be present")
	}

	// Check conditional restart is present (antivirus service is TRUE)
	if !restarts["amavis"] {
		t.Error("Expected conditional restart 'amavis' to be present")
	}

	// Check conditional restart is NOT present (webmail service is FALSE)
	if restarts["proxy"] {
		t.Error("Expected conditional restart 'proxy' to not be present (service disabled)")
	}

	// Should have exactly 2 restarts
	if len(restarts) != 2 {
		t.Errorf("Expected 2 restarts, got %d", len(restarts))
	}
}
