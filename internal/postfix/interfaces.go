// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package postfix provides interfaces for managing Postfix MTA configuration.
// It defines the Manager interface for postconf operations and holds pending
// configuration changes before they are flushed to postfix.
package postfix

import "context"

// Manager interface defines methods for Postfix configuration management.
type Manager interface {
	// AddPostconf adds a postconf directive (postconf -e key=value)
	AddPostconf(ctx context.Context, key, value string) error

	// AddPostconfd adds a postconfd directive (postconf -X key for deletion)
	AddPostconfd(ctx context.Context, key string) error

	// FlushPostconf executes accumulated postconf commands
	FlushPostconf(ctx context.Context) error

	// FlushPostconfd executes accumulated postconfd deletions
	FlushPostconfd(ctx context.Context) error

	// GetPendingChanges returns the current pending changes
	GetPendingChanges() (postconf map[string]string, postconfd []string)

	// ClearPending clears all pending changes
	ClearPending(ctx context.Context)
}

// PostfixConfig holds postfix configuration state.
//
//nolint:revive // PostfixConfig name is intentional for clarity
type PostfixConfig struct {
	PostconfChanges    map[string]string // key -> value
	PostconfdDeletions []string          // keys to delete
}
