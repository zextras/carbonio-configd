// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/plan"
)

// planCounts returns the number of actions of pl by kind. Every kind is
// present, zero included. A nil plan counts as empty.
func planCounts(pl *plan.Plan) map[string]int {
	counts := map[string]int{
		"mapfile": 0, "rewrite": 0, "postconf": 0, "postconfd": 0, "ldap": 0, "proxygen": 0, "restart": 0,
	}

	if pl == nil {
		return counts
	}

	counts["mapfile"] = len(pl.Mapfiles)
	counts["rewrite"] = len(pl.Rewrites)
	counts["postconf"] = len(pl.Postconf)
	counts["postconfd"] = len(pl.Postconfd)
	counts["ldap"] = len(pl.Ldap)
	counts["restart"] = len(pl.Restarts)

	if pl.Proxygen != nil {
		counts["proxygen"] = 1
	}

	return counts
}

// observeCycle records the metrics of a completed cycle, logs the optional
// "Cycle summary" line (zmconfigd_cycle_summary=TRUE) and exports the
// metrics textfile when one is configured.
func observeCycle(ctx context.Context, deps *daemonLoopDeps, cycle int, dur *loopPhaseDurations, total time.Duration) {
	counts := planCounts(dur.plan)

	reg := deps.configManager.Metrics()
	reg.CycleDone(total, []metrics.Phase{
		{Name: "load", Duration: dur.load},
		{Name: "parse", Duration: dur.parse},
		{Name: "build_deps", Duration: dur.config.buildDeps},
		{Name: "plan", Duration: dur.config.plan},
		{Name: "execute", Duration: dur.config.rewrites},
		{Name: "restarts", Duration: dur.restarts},
	}, deps.env.Clock.Now())
	reg.PlanSize(counts)

	if deps.cfg.CycleSummary {
		logger.InfoContext(ctx, "Cycle summary",
			"cycle", cycle,
			"first_run", dur.plan != nil && dur.plan.FirstRun,
			"duration_seconds", total.Seconds(),
			"load_seconds", dur.load.Seconds(),
			"parse_seconds", dur.parse.Seconds(),
			"plan_seconds", dur.config.plan.Seconds(),
			"execute_seconds", dur.config.rewrites.Seconds(),
			"restarts_seconds", dur.restarts.Seconds(),
			"mapfiles", counts["mapfile"],
			"rewrites", counts["rewrite"],
			"postconf", counts["postconf"],
			"postconfd", counts["postconfd"],
			"ldap", counts["ldap"],
			"proxygen", counts["proxygen"],
			"restarts", counts["restart"])
	}

	exportMetrics(ctx, deps)
}

// exportMetrics writes the metrics textfile when zmconfigd_metrics_textfile is
// set; with the default (unset) it does nothing.
func exportMetrics(ctx context.Context, deps *daemonLoopDeps) {
	path := deps.cfg.MetricsTextfile
	if path == "" {
		return
	}

	if err := metrics.WriteTextfile(deps.env.FS, path, deps.configManager.Metrics()); err != nil {
		logger.WarnContext(ctx, "Failed to write metrics textfile", "path", path, "error", err)
	}
}
