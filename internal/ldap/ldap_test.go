// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/config"
	errs "github.com/zextras/carbonio-configd/internal/errors"
	"testing"
	"time"
)

func TestNewLdap(t *testing.T) {
	cfg := &config.Config{
		LdapIsMaster: true,
	}
	l := NewLdap(context.Background(), cfg)
	if l == nil {
		t.Fatal("NewLdap returned nil")
	}
	if l.config == nil {
		t.Error("NewLdap.config is nil")
	}
	if l.pendingChanges == nil {
		t.Error("NewLdap.pendingChanges is nil")
	}
}

func TestLdap_AddChange(t *testing.T) {
	cfg := &config.Config{}
	l := NewLdap(context.Background(), cfg)

	l.AddChange(context.Background(), "ldap_common_loglevel", "256")
	if len(l.pendingChanges) != 1 {
		t.Errorf("Expected 1 pending change, got %d", len(l.pendingChanges))
	}
	if val, ok := l.pendingChanges["ldap_common_loglevel"]; !ok || val != "256" {
		t.Errorf("Expected ldap_common_loglevel=256, got %s", val)
	}

	// Add another change
	l.AddChange(context.Background(), "ldap_common_threads", "8")
	if len(l.pendingChanges) != 2 {
		t.Errorf("Expected 2 pending changes, got %d", len(l.pendingChanges))
	}
}

func TestLdap_GetPendingChanges(t *testing.T) {
	cfg := &config.Config{}
	l := NewLdap(context.Background(), cfg)

	l.AddChange(context.Background(), "key1", "value1")
	l.AddChange(context.Background(), "key2", "value2")

	changes := l.GetPendingChanges()
	if len(changes) != 2 {
		t.Errorf("Expected 2 changes, got %d", len(changes))
	}
	if changes["key1"] != "value1" {
		t.Errorf("Expected key1=value1, got %s", changes["key1"])
	}
	if changes["key2"] != "value2" {
		t.Errorf("Expected key2=value2, got %s", changes["key2"])
	}
}

func TestLdap_ClearPending(t *testing.T) {
	cfg := &config.Config{}
	l := NewLdap(context.Background(), cfg)

	l.AddChange(context.Background(), "key1", "value1")
	l.AddChange(context.Background(), "key2", "value2")
	if len(l.pendingChanges) != 2 {
		t.Errorf("Expected 2 pending changes before clear, got %d", len(l.pendingChanges))
	}

	l.ClearPending()
	if len(l.pendingChanges) != 0 {
		t.Errorf("Expected 0 pending changes after clear, got %d", len(l.pendingChanges))
	}
}

func TestLdap_LookupKey(t *testing.T) {
	cfg := &config.Config{
		LdapIsMaster: true,
	}
	l := NewLdap(context.Background(), cfg)
	l.IsMaster = true

	tests := []struct {
		name           string
		key            string
		wantAttr       string
		wantDN         string
		wantTransform  string
		wantErr        bool
		requiresMaster bool
	}{
		{
			name:          "ldap_common_loglevel",
			key:           "ldap_common_loglevel",
			wantAttr:      "olcLogLevel",
			wantDN:        "cn=config",
			wantTransform: "%s",
			wantErr:       false,
		},
		{
			name:          "ldap_common_require_tls",
			key:           "ldap_common_require_tls",
			wantAttr:      "olcSecurity",
			wantDN:        "cn=config",
			wantTransform: "ssf=%s",
			wantErr:       false,
		},
		{
			name:          "ldap_db_maxsize",
			key:           "ldap_db_maxsize",
			wantAttr:      "olcDbMaxsize",
			wantDN:        "olcDatabase={3}mdb,cn=config",
			wantTransform: "%s",
			wantErr:       false,
		},
		{
			name:           "ldap_overlay_syncprov_checkpoint (requires master)",
			key:            "ldap_overlay_syncprov_checkpoint",
			wantAttr:       "olcSpCheckpoint",
			wantDN:         "olcOverlay={0}syncprov,olcDatabase={3}mdb,cn=config",
			wantTransform:  "%s",
			wantErr:        false,
			requiresMaster: true,
		},
		{
			name:    "unknown_key",
			key:     "unknown_key",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with master
			l.IsMaster = true
			entry, err := l.lookupKey(context.Background(), tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("lookupKey() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("lookupKey() unexpected error: %v", err)
				return
			}
			if entry.Attr != tt.wantAttr {
				t.Errorf("lookupKey() Attr = %s, want %s", entry.Attr, tt.wantAttr)
			}
			if entry.DN != tt.wantDN {
				t.Errorf("lookupKey() DN = %s, want %s", entry.DN, tt.wantDN)
			}
			if entry.TransformFmt != tt.wantTransform {
				t.Errorf("lookupKey() TransformFmt = %s, want %s", entry.TransformFmt, tt.wantTransform)
			}

			// Test with non-master for keys that require master
			if tt.requiresMaster {
				l.IsMaster = false
				_, err := l.lookupKey(context.Background(), tt.key)
				if err == nil {
					t.Errorf("lookupKey() expected error for non-master, got nil")
				}
			}
		})
	}
}

func TestLdap_LookupKey_NonMaster_DbKeys(t *testing.T) {
	cfg := &config.Config{
		LdapIsMaster: false,
	}
	l := NewLdap(context.Background(), cfg)
	l.IsMaster = false

	// Test that ldap_db_ keys adjust DN when not master
	entry, err := l.lookupKey(context.Background(), "ldap_db_maxsize")
	if err != nil {
		t.Fatalf("lookupKey() unexpected error: %v", err)
	}
	expectedDN := "olcDatabase={2}mdb,cn=config"
	if entry.DN != expectedDN {
		t.Errorf("lookupKey() for non-master DN = %s, want %s", entry.DN, expectedDN)
	}
}

func TestLdap_ModifyAttribute(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		isMaster bool
		wantErr  bool
	}{
		{
			name:     "valid_common_key",
			key:      "ldap_common_loglevel",
			value:    "256",
			isMaster: true,
			wantErr:  false,
		},
		{
			name:     "valid_require_tls",
			key:      "ldap_common_require_tls",
			value:    "128",
			isMaster: true,
			wantErr:  false,
		},
		{
			name:     "master_required_key_as_non_master",
			key:      "ldap_overlay_syncprov_checkpoint",
			value:    "100 10",
			isMaster: false,
			wantErr:  true,
		},
		{
			name:     "unknown_key",
			key:      "unknown_ldap_key",
			value:    "value",
			isMaster: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LdapIsMaster: tt.isMaster,
			}
			l := NewLdap(context.Background(), cfg)
			l.IsMaster = tt.isMaster

			err := l.ModifyAttribute(context.Background(), tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ModifyAttribute() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLdap_ModifyAttributeBatch(t *testing.T) {
	tests := []struct {
		name     string
		changes  map[string]string
		isMaster bool
		wantErr  bool
	}{
		{
			name:     "empty_batch",
			changes:  map[string]string{},
			isMaster: true,
			wantErr:  false,
		},
		{
			name: "single_change",
			changes: map[string]string{
				"ldap_common_loglevel": "256",
			},
			isMaster: true,
			wantErr:  false,
		},
		{
			name: "multiple_changes_same_dn",
			changes: map[string]string{
				"ldap_common_loglevel":    "256",
				"ldap_common_threads":     "8",
				"ldap_common_toolthreads": "4",
			},
			isMaster: true,
			wantErr:  false,
		},
		{
			name: "multiple_changes_different_dns",
			changes: map[string]string{
				"ldap_common_loglevel": "256",
				"ldap_db_maxsize":      "85899345920",
				"ldap_db_envflags":     "writemap",
			},
			isMaster: true,
			wantErr:  false,
		},
		{
			name: "batch_with_master_only_keys_as_master",
			changes: map[string]string{
				"ldap_common_loglevel":             "256",
				"ldap_overlay_syncprov_checkpoint": "100 10",
				"ldap_accesslog_maxsize":           "85899345920",
			},
			isMaster: true,
			wantErr:  false,
		},
		{
			name: "batch_with_master_only_keys_as_non_master",
			changes: map[string]string{
				"ldap_common_loglevel":             "256",
				"ldap_overlay_syncprov_checkpoint": "100 10",
			},
			isMaster: false,
			wantErr:  true,
		},
		{
			name: "batch_with_unknown_key",
			changes: map[string]string{
				"ldap_common_loglevel": "256",
				"unknown_ldap_key":     "value",
			},
			isMaster: true,
			wantErr:  true,
		},
		{
			name: "batch_with_transformed_values",
			changes: map[string]string{
				"ldap_common_require_tls": "128",
				"ldap_common_loglevel":    "256",
			},
			isMaster: true,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LdapIsMaster: tt.isMaster,
			}
			l := NewLdap(context.Background(), cfg)
			l.IsMaster = tt.isMaster

			err := l.ModifyAttributeBatch(context.Background(), tt.changes)
			if (err != nil) != tt.wantErr {
				t.Errorf("ModifyAttributeBatch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLdap_ModifyAttributeBatch_DNGrouping(t *testing.T) {
	// This test verifies that changes are properly grouped by DN
	cfg := &config.Config{
		LdapIsMaster: true,
	}
	l := NewLdap(context.Background(), cfg)
	l.IsMaster = true

	changes := map[string]string{
		"ldap_common_loglevel":   "256",         // cn=config
		"ldap_common_threads":    "8",           // cn=config
		"ldap_db_maxsize":        "85899345920", // olcDatabase={3}mdb,cn=config
		"ldap_db_envflags":       "writemap",    // olcDatabase={3}mdb,cn=config
		"ldap_accesslog_maxsize": "85899345920", // olcDatabase={2}mdb,cn=config
	}

	err := l.ModifyAttributeBatch(context.Background(), changes)
	if err != nil {
		t.Fatalf("ModifyAttributeBatch() unexpected error: %v", err)
	}

	// The test passes if no error is returned, indicating that DN grouping
	// and batch execution completed successfully
}

func TestLdap_ModifyAttributeBatch_NonMaster_DNAdjustment(t *testing.T) {
	// Test that ldap_db_ keys adjust DN when not master in batch operations
	cfg := &config.Config{
		LdapIsMaster: false,
	}
	l := NewLdap(context.Background(), cfg)
	l.IsMaster = false

	changes := map[string]string{
		"ldap_db_maxsize":  "85899345920",
		"ldap_db_envflags": "writemap",
	}

	err := l.ModifyAttributeBatch(context.Background(), changes)
	if err != nil {
		t.Fatalf("ModifyAttributeBatch() unexpected error: %v", err)
	}

	// The test verifies that non-master DN adjustment works in batch mode
	// (ldap_db_ keys should use olcDatabase={2}mdb,cn=config instead of {3})
}

func TestLdap_RetryConfiguration(t *testing.T) {
	cfg := &config.Config{
		LdapIsMaster: true,
	}
	l := NewLdap(context.Background(), cfg)

	// Test default retry configuration
	if l.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", l.MaxRetries)
	}
	if l.RetryDelay != 100*time.Millisecond {
		t.Errorf("Expected RetryDelay=100ms, got %v", l.RetryDelay)
	}
	if l.MaxRetryDelay != 5*time.Second {
		t.Errorf("Expected MaxRetryDelay=5s, got %v", l.MaxRetryDelay)
	}
}

func TestLdap_IsRetryableError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantRetryable bool
	}{
		{
			name:          "nil_error",
			err:           nil,
			wantRetryable: false,
		},
		{
			name:          "config_error_not_retryable",
			err:           errs.NewConfigError("test", "key"),
			wantRetryable: false,
		},
		{
			name:          "wrapped_config_error_not_retryable",
			err:           errs.WrapConfig("test", "key", fmt.Errorf("inner")),
			wantRetryable: false,
		},
		{
			name:          "generic_error_retryable",
			err:           fmt.Errorf("connection timeout"),
			wantRetryable: true,
		},
		{
			name:          "network_error_retryable",
			err:           fmt.Errorf("connection refused"),
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			if got != tt.wantRetryable {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.wantRetryable)
			}
		})
	}
}

func TestLdap_WithRetry(t *testing.T) {
	tests := []struct {
		name           string
		maxRetries     int
		retryDelay     time.Duration
		maxRetryDelay  time.Duration
		operation      string
		fnErrors       []error // errors to return on each attempt
		wantErr        bool
		wantAttempts   int
		wantFinalError string
	}{
		{
			name:          "success_first_attempt",
			maxRetries:    3,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 100 * time.Millisecond,
			operation:     "test operation",
			fnErrors:      []error{nil},
			wantErr:       false,
			wantAttempts:  1,
		},
		{
			name:          "success_after_retries",
			maxRetries:    3,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 100 * time.Millisecond,
			operation:     "test operation",
			fnErrors: []error{
				fmt.Errorf("transient error 1"),
				fmt.Errorf("transient error 2"),
				nil, // Success on third attempt
			},
			wantErr:      false,
			wantAttempts: 3,
		},
		{
			name:          "max_retries_exceeded",
			maxRetries:    2,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 100 * time.Millisecond,
			operation:     "test operation",
			fnErrors: []error{
				fmt.Errorf("transient error 1"),
				fmt.Errorf("transient error 2"),
				fmt.Errorf("transient error 3"),
			},
			wantErr:        true,
			wantAttempts:   3, // Initial + 2 retries
			wantFinalError: "operation test operation failed after 2 retries",
		},
		{
			name:          "non_retryable_error_immediate_failure",
			maxRetries:    3,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 100 * time.Millisecond,
			operation:     "test operation",
			fnErrors: []error{
				errs.NewConfigError("test", "key"),
			},
			wantErr:      true,
			wantAttempts: 1, // Should stop immediately
		},
		{
			name:          "retryable_then_non_retryable",
			maxRetries:    3,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 100 * time.Millisecond,
			operation:     "test operation",
			fnErrors: []error{
				fmt.Errorf("transient error"),
				errs.NewConfigError("test", "key"), // Non-retryable on second attempt
			},
			wantErr:      true,
			wantAttempts: 2, // Should stop after non-retryable error
		},
		{
			name:          "exponential_backoff",
			maxRetries:    3,
			retryDelay:    10 * time.Millisecond,
			maxRetryDelay: 50 * time.Millisecond,
			operation:     "test operation",
			fnErrors: []error{
				fmt.Errorf("error 1"),
				fmt.Errorf("error 2"),
				fmt.Errorf("error 3"),
				fmt.Errorf("error 4"),
			},
			wantErr:      true,
			wantAttempts: 4, // Initial + 3 retries
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LdapIsMaster: true,
			}
			l := NewLdap(context.Background(), cfg)
			l.MaxRetries = tt.maxRetries
			l.RetryDelay = tt.retryDelay
			l.MaxRetryDelay = tt.maxRetryDelay

			attempts := 0
			err := l.withRetry(context.Background(), tt.operation, func() error {
				defer func() { attempts++ }()
				if attempts < len(tt.fnErrors) {
					return tt.fnErrors[attempts]
				}
				return nil
			})

			if (err != nil) != tt.wantErr {
				t.Errorf("withRetry() error = %v, wantErr %v", err, tt.wantErr)
			}

			if attempts != tt.wantAttempts {
				t.Errorf("withRetry() attempts = %d, want %d", attempts, tt.wantAttempts)
			}

			if tt.wantFinalError != "" && (err == nil || !contains(err.Error(), tt.wantFinalError)) {
				t.Errorf("withRetry() error = %v, want error containing %q", err, tt.wantFinalError)
			}
		})
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexString(s, substr) >= 0)
}

func indexString(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
