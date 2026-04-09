// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/state"
)

// newTestConfigManager creates a ConfigManager suitable for unit tests.
// It uses t.TempDir() for the base directory and includes a cache instance.
// The LDAP client is nil — tests that need LDAP should set it up separately.
func newTestConfigManager(t *testing.T) *ConfigManager {
	t.Helper()

	baseDir := t.TempDir()
	st := state.NewState()
	st.FirstRun = true

	cfg := &config.Config{
		BaseDir:    baseDir,
		ConfigFile: filepath.Join(baseDir, "zmconfigd.cf"),
		Hostname:   "testhost",
	}

	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	return NewConfigManager(ctx, cfg, st, nil, cacheInstance)
}
