// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package errs provides custom error types for configd components.
// It defines structured error types for configuration, service management,
// parsing, and template processing operations, supporting error wrapping
// and contextual information.
package errs

import (
	"errors"
	"fmt"
)

// Error types for consistent error handling across configd packages

// ConfigError represents configuration-related errors
type ConfigError struct {
	Op  string // operation that failed
	Key string // configuration key involved
	Err error  // underlying error
}

func (e *ConfigError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("config %s failed for key '%s': %v", e.Op, e.Key, e.Err)
	}

	return fmt.Sprintf("config %s failed: %v", e.Op, e.Err)
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// CacheError represents cache-related errors
type CacheError struct {
	Op  string // operation that failed
	Key string // cache key
	Err error  // underlying error
}

func (e *CacheError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("cache %s failed for key '%s': %v", e.Op, e.Key, e.Err)
	}

	return fmt.Sprintf("cache %s failed: %v", e.Op, e.Err)
}

func (e *CacheError) Unwrap() error {
	return e.Err
}

// Helper functions for creating wrapped errors

// WrapConfig creates a new ConfigError with proper error wrapping
func WrapConfig(op, key string, err error) error {
	if err == nil {
		return nil
	}

	return &ConfigError{Op: op, Key: key, Err: err}
}

// WrapCache creates a new CacheError with proper error wrapping
func WrapCache(op, key string, err error) error {
	if err == nil {
		return nil
	}

	return &CacheError{Op: op, Key: key, Err: err}
}

// Common error messages
const (
	ErrInvalidConfig = "invalid configuration"
	ErrNotMaster     = "not a master"
)

// Common error types
var (
	// ErrConfigInvalid is a static error for invalid configuration
	ErrConfigInvalid = errors.New(ErrInvalidConfig)

	// ErrLDAPUnhealthyConnection is returned when an LDAP pool connection
	// has failed with a transport or protocol error and must not be reused.
	// Callers can detect this class via errors.Is.
	ErrLDAPUnhealthyConnection = errors.New("ldap: unhealthy connection")
)

// Common error constructors

// NewConfigError creates a ConfigError with an invalid configuration error.
func NewConfigError(op, key string) error {
	return &ConfigError{Op: op, Key: key, Err: ErrConfigInvalid}
}

// Type checking helpers

// IsConfigError checks if an error is or contains a ConfigError
func IsConfigError(err error) bool {
	var configErr *ConfigError
	return errors.As(err, &configErr)
}
