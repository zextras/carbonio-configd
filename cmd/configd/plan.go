// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/cli/plancmd"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// planOnHost returns the planner of "configd plan": it loads the configuration
// like a forced rewrite does and plans it against the files and state on disk,
// without writing anything.
func planOnHost(env sys.System) plancmd.Planner {
	return func(ctx context.Context, sections []string) (*plan.Plan, error) {
		// A plan must not leave a cache behind, so caching is off.
		rt, err := buildRuntime(ctx, env, &runtimeOpts{skipCache: true})
		if err != nil {
			return nil, err
		}

		if _, _, err := runLoadAndParse(ctx, rt.cm, rt.cfg); err != nil {
			return nil, fmt.Errorf("load configuration: %w", err)
		}

		return rt.cm.PlanHost(ctx, sections)
	}
}
