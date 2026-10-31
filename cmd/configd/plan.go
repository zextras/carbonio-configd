// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/cli/plancmd"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// planOnHost returns the planner of "configd plan": it loads the configuration
// like a forced rewrite does and plans it against the files and state on disk,
// without writing anything.
func planOnHost(env sys.System) plancmd.Planner {
	return func(ctx context.Context, sections []string) (*plan.Plan, error) {
		mainCfg, appState, ldapClient, err := newRuntime(ctx)
		if err != nil {
			return nil, err
		}

		// A plan must not leave a cache behind, so caching is off.
		cm := configmgr.NewConfigManager(ctx, mainCfg, appState, ldapClient, cache.New(ctx, true), env)

		if err := cm.LoadAllConfigs(ctx); err != nil {
			return nil, fmt.Errorf("load configs: %w", err)
		}

		if err := cm.ParseMtaConfig(ctx, mainCfg.ConfigFile); err != nil {
			return nil, fmt.Errorf("parse %s: %w", mainCfg.ConfigFile, err)
		}

		return cm.PlanHost(ctx, sections)
	}
}
