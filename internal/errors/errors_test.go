// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestConfigError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ConfigError
		expected string
	}{
		{
			name: "error with key",
			err: &ConfigError{
				Op:  "load",
				Key: "main.hostname",
				Err: errors.New("file not found"),
			},
			expected: "config load failed for key 'main.hostname': file not found",
		},
		{
			name: "error without key",
			err: &ConfigError{
				Op:  "parse",
				Key: "",
				Err: errors.New("syntax error"),
			},
			expected: "config parse failed: syntax error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("Error() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConfigError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	err := &ConfigError{
		Op:  "load",
		Key: "test",
		Err: innerErr,
	}

	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, innerErr)
	}

	if !errors.Is(err, innerErr) {
		t.Error("errors.Is should find the wrapped error")
	}
}

func TestCacheError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *CacheError
		expected string
	}{
		{
			name: "error with key",
			err: &CacheError{
				Op:  "get",
				Key: "ldap:domains",
				Err: errors.New("not in cache"),
			},
			expected: "cache get failed for key 'ldap:domains': not in cache",
		},
		{
			name: "error without key",
			err: &CacheError{
				Op:  "init",
				Key: "",
				Err: errors.New("failed to initialize"),
			},
			expected: "cache init failed: failed to initialize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("Error() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCacheError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	err := &CacheError{
		Op:  "get",
		Key: "test",
		Err: innerErr,
	}

	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, innerErr)
	}
}

func TestCommandError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *CommandError
		expected string
	}{
		{
			name: "command with exit code",
			err: &CommandError{
				Op:   "execute",
				Cmd:  "/opt/zextras/bin/postconf",
				Exit: 127,
				Err:  errors.New("command not found"),
			},
			expected: "command execute failed for '/opt/zextras/bin/postconf' (exit 127): command not found",
		},
		{
			name: "command with zero exit code",
			err: &CommandError{
				Op:   "run",
				Cmd:  "test",
				Exit: 0,
				Err:  errors.New("unexpected error"),
			},
			expected: "command run failed for 'test' (exit 0): unexpected error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("Error() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCommandError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	err := &CommandError{
		Op:   "execute",
		Cmd:  "test",
		Exit: 1,
		Err:  innerErr,
	}

	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, innerErr)
	}
}

func TestWrapConfig(t *testing.T) {
	tests := []struct {
		name        string
		op          string
		key         string
		err         error
		expectNil   bool
		expectError string
	}{
		{
			name:        "wrap non-nil error",
			op:          "load",
			key:         "main.hostname",
			err:         errors.New("test error"),
			expectNil:   false,
			expectError: "config load failed for key 'main.hostname': test error",
		},
		{
			name:      "wrap nil error returns nil",
			op:        "load",
			key:       "test",
			err:       nil,
			expectNil: true,
		},
		{
			name:        "wrap error without key",
			op:          "parse",
			key:         "",
			err:         errors.New("syntax error"),
			expectNil:   false,
			expectError: "config parse failed: syntax error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := WrapConfig(tt.op, tt.key, tt.err)

			if tt.expectNil {
				if wrapped != nil {
					t.Errorf("WrapConfig() = %v, want nil", wrapped)
				}
				return
			}

			if wrapped == nil {
				t.Fatal("WrapConfig() returned nil, expected error")
			}

			if wrapped.Error() != tt.expectError {
				t.Errorf("Error() = %v, want %v", wrapped.Error(), tt.expectError)
			}

			// Verify it's a ConfigError
			var configErr *ConfigError
			if !errors.As(wrapped, &configErr) {
				t.Error("WrapConfig should return a *ConfigError")
			}
		})
	}
}

func TestWrapCache(t *testing.T) {
	tests := []struct {
		name        string
		op          string
		key         string
		err         error
		expectNil   bool
		expectError string
	}{
		{
			name:        "wrap cache error",
			op:          "get",
			key:         "ldap:domains",
			err:         errors.New("not found"),
			expectNil:   false,
			expectError: "cache get failed for key 'ldap:domains': not found",
		},
		{
			name:      "nil error returns nil",
			op:        "set",
			key:       "test",
			err:       nil,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := WrapCache(tt.op, tt.key, tt.err)

			if tt.expectNil {
				if wrapped != nil {
					t.Errorf("WrapCache() = %v, want nil", wrapped)
				}
				return
			}

			if wrapped == nil {
				t.Fatal("WrapCache() returned nil, expected error")
			}

			if wrapped.Error() != tt.expectError {
				t.Errorf("Error() = %v, want %v", wrapped.Error(), tt.expectError)
			}

			var cacheErr *CacheError
			if !errors.As(wrapped, &cacheErr) {
				t.Error("WrapCache should return a *CacheError")
			}
		})
	}
}

func TestWrapCommand(t *testing.T) {
	tests := []struct {
		name        string
		op          string
		cmd         string
		exit        int
		err         error
		expectNil   bool
		expectError string
	}{
		{
			name:        "wrap command error",
			op:          "execute",
			cmd:         "/opt/zextras/bin/postconf",
			exit:        127,
			err:         errors.New("not found"),
			expectNil:   false,
			expectError: "command execute failed for '/opt/zextras/bin/postconf' (exit 127): not found",
		},
		{
			name:      "nil error returns nil",
			op:        "run",
			cmd:       "test",
			exit:      0,
			err:       nil,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := WrapCommand(tt.op, tt.cmd, tt.exit, tt.err)

			if tt.expectNil {
				if wrapped != nil {
					t.Errorf("WrapCommand() = %v, want nil", wrapped)
				}
				return
			}

			if wrapped == nil {
				t.Fatal("WrapCommand() returned nil, expected error")
			}

			if wrapped.Error() != tt.expectError {
				t.Errorf("Error() = %v, want %v", wrapped.Error(), tt.expectError)
			}

			var cmdErr *CommandError
			if !errors.As(wrapped, &cmdErr) {
				t.Error("WrapCommand should return a *CommandError")
			}

			if cmdErr.Exit != tt.exit {
				t.Errorf("Exit = %v, want %v", cmdErr.Exit, tt.exit)
			}
		})
	}
}

func TestErrorConstants(t *testing.T) {
	constants := map[string]string{
		ErrNotFound:       "not found",
		ErrInvalidInput:   "invalid input",
		ErrPermission:     "permission denied",
		ErrTimeout:        "operation timed out",
		ErrUnavailable:    "service unavailable",
		ErrInvalidConfig:  "invalid configuration",
		ErrUnknownKey:     "unknown key",
		ErrNotMaster:      "not a master",
		ErrEmptyCommand:   "empty command string",
		ErrCacheEntry:     "cache entry does not exist",
		ErrFailedToFetch:  "failed to fetch fresh data",
		ErrAllDisabled:    "all services detected disabled",
		ErrLoadingTimeout: "configuration loading timed out",
	}

	for constant, expected := range constants {
		if constant != expected {
			t.Errorf("Constant = %v, want %v", constant, expected)
		}
	}
}

func TestErrConfigInvalid(t *testing.T) {
	if ErrConfigInvalid == nil {
		t.Fatal("ErrConfigInvalid should not be nil")
	}

	if ErrConfigInvalid.Error() != ErrInvalidConfig {
		t.Errorf("ErrConfigInvalid.Error() = %v, want %v", ErrConfigInvalid.Error(), ErrInvalidConfig)
	}
}

func TestNewConfigError(t *testing.T) {
	err := NewConfigError("validate", "test.key")

	if err == nil {
		t.Fatal("NewConfigError should not return nil")
	}

	expected := "config validate failed for key 'test.key': invalid configuration"
	if err.Error() != expected {
		t.Errorf("Error() = %v, want %v", err.Error(), expected)
	}

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Error("NewConfigError should return a *ConfigError")
	}

	if configErr.Op != "validate" {
		t.Errorf("Op = %v, want validate", configErr.Op)
	}

	if configErr.Key != "test.key" {
		t.Errorf("Key = %v, want test.key", configErr.Key)
	}

	if !errors.Is(err, ErrConfigInvalid) {
		t.Error("NewConfigError should wrap ErrConfigInvalid")
	}
}

func TestIsConfigError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name:   "direct ConfigError",
			err:    &ConfigError{Op: "test", Key: "key", Err: errors.New("test")},
			expect: true,
		},
		{
			name:   "wrapped ConfigError",
			err:    fmt.Errorf("outer: %w", &ConfigError{Op: "test", Key: "key", Err: errors.New("test")}),
			expect: true,
		},
		{
			name:   "not a ConfigError",
			err:    errors.New("plain error"),
			expect: false,
		},
		{
			name:   "nil error",
			err:    nil,
			expect: false,
		},
		{
			name:   "CacheError is not ConfigError",
			err:    &CacheError{Op: "test", Key: "key", Err: errors.New("test")},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsConfigError(tt.err)
			if result != tt.expect {
				t.Errorf("IsConfigError() = %v, want %v", result, tt.expect)
			}
		})
	}
}

func TestErrorWrappingChain(t *testing.T) {
	// Create a chain: base error -> ConfigError -> fmt.Errorf wrapper
	baseErr := errors.New("base error")
	configErr := WrapConfig("load", "test.key", baseErr)
	wrappedErr := fmt.Errorf("additional context: %w", configErr)

	// Should be able to unwrap all the way to base error
	if !errors.Is(wrappedErr, baseErr) {
		t.Error("Should be able to find base error in wrapped chain")
	}

	// Should be able to identify ConfigError
	var ce *ConfigError
	if !errors.As(wrappedErr, &ce) {
		t.Error("Should be able to extract ConfigError from chain")
	}

	if ce.Op != "load" {
		t.Errorf("ConfigError.Op = %v, want load", ce.Op)
	}

	if ce.Key != "test.key" {
		t.Errorf("ConfigError.Key = %v, want test.key", ce.Key)
	}
}

func TestErrorTypes(t *testing.T) {
	t.Run("ConfigError implements error", func(t *testing.T) {
		var _ error = &ConfigError{}
	})

	t.Run("CacheError implements error", func(t *testing.T) {
		var _ error = &CacheError{}
	})

	t.Run("CommandError implements error", func(t *testing.T) {
		var _ error = &CommandError{}
	})
}

// TestLDAPSentinels verifies LDAP sentinel errors can be detected via errors.Is.
func TestLDAPSentinels(t *testing.T) {
	underlying := errors.New("connection reset by peer")
	wrapped := fmt.Errorf("%w: %w", ErrLDAPUnhealthyConnection, underlying)

	if !errors.Is(wrapped, ErrLDAPUnhealthyConnection) {
		t.Error("expected wrapped error to satisfy errors.Is(ErrLDAPUnhealthyConnection)")
	}

	if !errors.Is(wrapped, underlying) {
		t.Error("expected wrapped error to preserve the underlying cause")
	}
}

// TestParseError verifies ParseError wraps the sentinel and exposes context.
func TestParseError(t *testing.T) {
	err := NewLDAPParseError("ldap command output", 42, "missing colon")

	if !errors.Is(err, ErrLDAPParse) {
		t.Error("expected ParseError to satisfy errors.Is(ErrLDAPParse)")
	}

	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected errors.As to bind ParseError, got %v", err)
	}

	if pe.Line != 42 || pe.Op == "" || pe.Msg == "" {
		t.Errorf("ParseError context missing: %+v", pe)
	}

	// Message should contain both op and line.
	msg := pe.Error()
	if !containsAll(msg, "ldap command output", "line 42", "missing colon") {
		t.Errorf("ParseError message missing context: %q", msg)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}

	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}

	return -1
}
