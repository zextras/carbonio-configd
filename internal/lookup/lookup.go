// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package lookup provides interfaces for configuration value lookups.
// It defines the ConfigLookup interface used throughout configd for
// retrieving configuration values from various sources (local, global,
// server, misc configurations).
package lookup

import "context"

// ConfigLookup defines the interface for looking up configuration values.
type ConfigLookup interface {
	LookUpConfig(ctx context.Context, cfgType, key string) (string, error)
}
