// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package mtaops provides operations for MTA configuration management including
// Postfix postconf operations, LDAP write-back, and MAPFILE handling.
package mtaops

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/state"
)

// PostconfOperation represents a Postfix postconf command to execute.
type PostconfOperation struct {
	Key   string // Postfix parameter name
	Value string // Value to set
}

// PostconfdOperation represents a Postfix postconf -X (delete) command.
type PostconfdOperation struct {
	Key string // Postfix parameter name to delete
}

// LdapOperation represents an LDAP cn=config modification operation.
type LdapOperation struct {
	Key   string // Internal key name (e.g., "ldap_db_maxsize")
	Value string // Value to set in LDAP
}

// MapfileOperation represents a MAPFILE/MAPLOCAL operation.
type MapfileOperation struct {
	Key        string // LDAP attribute name (e.g., "zimbraSSLDHParam")
	IsLocal    bool   // true for MAPLOCAL (just check existence), false for MAPFILE (base64 decode)
	FilePath   string // Destination file path
	Base64Data string // Base64-encoded data from LDAP (only for MAPFILE)
}

// Executor defines the interface for executing MTA operations.
type Executor interface {
	// ExecutePostconf executes a postconf -e operation
	ExecutePostconf(ctx context.Context, op PostconfOperation) error

	// ExecutePostconfBatch executes multiple postconf -e operations in a single call
	ExecutePostconfBatch(ctx context.Context, ops []PostconfOperation) error

	// ExecutePostconfd executes a postconf -X operation
	ExecutePostconfd(ctx context.Context, op PostconfdOperation) error

	// ExecutePostconfdBatch executes multiple postconf -X operations in a single call
	ExecutePostconfdBatch(ctx context.Context, ops []PostconfdOperation) error

	// ExecuteMapfile executes a MAPFILE/MAPLOCAL operation
	ExecuteMapfile(ctx context.Context, op MapfileOperation) error

	// ExecuteLdapWrite executes an LDAP cn=config write operation
	ExecuteLdapWrite(ctx context.Context, op LdapOperation) error
}

// OperationResolver resolves directive values from state.
type OperationResolver interface {
	// ResolveValue resolves a value based on type (VAR, LOCAL, FILE, MAPLOCAL, or literal)
	ResolveValue(ctx context.Context, valueType, key string, state *state.State) (string, error)
}
