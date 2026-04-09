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

// CommandError represents command execution errors
type CommandError struct {
	Op   string // operation that failed
	Cmd  string // command that failed
	Err  error  // underlying error
	Exit int    // exit code
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("command %s failed for '%s' (exit %d): %v", e.Op, e.Cmd, e.Exit, e.Err)
}

func (e *CommandError) Unwrap() error {
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

// WrapCommand creates a new CommandError with proper error wrapping
func WrapCommand(op, cmd string, exit int, err error) error {
	if err == nil {
		return nil
	}

	return &CommandError{Op: op, Cmd: cmd, Exit: exit, Err: err}
}

// Common error messages
const (
	ErrNotFound       = "not found"
	ErrInvalidInput   = "invalid input"
	ErrPermission     = "permission denied"
	ErrTimeout        = "operation timed out"
	ErrUnavailable    = "service unavailable"
	ErrInvalidConfig  = "invalid configuration"
	ErrUnknownKey     = "unknown key"
	ErrNotMaster      = "not a master"
	ErrEmptyCommand   = "empty command string"
	ErrCacheEntry     = "cache entry does not exist"
	ErrFailedToFetch  = "failed to fetch fresh data"
	ErrAllDisabled    = "all services detected disabled"
	ErrLoadingTimeout = "configuration loading timed out"
)

// Common error types
var (
	// ErrConfigInvalid is a static error for invalid configuration
	ErrConfigInvalid = errors.New(ErrInvalidConfig)

	// ErrLDAPUnhealthyConnection is returned when an LDAP pool connection
	// has failed with a transport or protocol error and must not be reused.
	// Callers can detect this class via errors.Is.
	ErrLDAPUnhealthyConnection = errors.New("ldap: unhealthy connection")

	// ErrLDAPProtocol indicates an LDAP protocol-level failure that should
	// invalidate the associated connection.
	ErrLDAPProtocol = errors.New("ldap: protocol error")

	// ErrLDAPTransport indicates an LDAP transport-layer failure that should
	// invalidate the associated connection.
	ErrLDAPTransport = errors.New("ldap: transport error")

	// ErrLDAPParse is the sentinel for malformed LDAP command output lines.
	// Wrap it with additional context (operation, line) for callers to
	// classify via errors.Is.
	ErrLDAPParse = errors.New("ldap: parse error")
)

// ParseError describes a malformed input line encountered by a loader or
// parser. It wraps a sentinel via Unwrap so callers can use errors.Is to
// detect parse-category failures while still accessing structured details.
type ParseError struct {
	Op   string // operation / source (e.g. "ldap command output")
	Line int    // 1-based input line number if available, 0 otherwise
	Msg  string // human-readable description of the malformed input
	Err  error  // underlying sentinel, typically ErrLDAPParse
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s: %s at line %d: %v", e.Op, e.Msg, e.Line, e.Err)
	}

	return fmt.Sprintf("%s: %s: %v", e.Op, e.Msg, e.Err)
}

// Unwrap exposes the underlying sentinel so callers can use errors.Is.
func (e *ParseError) Unwrap() error {
	return e.Err
}

// NewLDAPParseError creates a ParseError wrapping ErrLDAPParse.
func NewLDAPParseError(op string, line int, msg string) error {
	return &ParseError{Op: op, Line: line, Msg: msg, Err: ErrLDAPParse}
}

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
