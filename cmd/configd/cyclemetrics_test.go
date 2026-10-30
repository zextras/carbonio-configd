// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const metricsPath = "/var/lib/node_exporter/configd.prom"

func sampleDurations() *loopPhaseDurations {
	return &loopPhaseDurations{
		load:     time.Second,
		parse:    100 * time.Millisecond,
		config:   phaseTimings{plan: 200 * time.Millisecond, rewrites: 300 * time.Millisecond},
		restarts: 50 * time.Millisecond,
		plan: &plan.Plan{
			FirstRun: true,
			Rewrites: []plan.Rewrite{{Target: "a"}, {Target: "b"}},
			Postconf: []plan.Postconf{{Key: "k"}},
			Restarts: []plan.Restart{{Service: "mta"}},
			Proxygen: &plan.Proxygen{},
		},
	}
}

func captureLogs(buf *bytes.Buffer) context.Context {
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger.ContextWithLogger(context.Background(), l)
}

func TestPlanCounts(t *testing.T) {
	assert.Equal(t, map[string]int{
		"mapfile": 0, "rewrite": 2, "postconf": 1, "postconfd": 0, "ldap": 0, "proxygen": 1, "restart": 1,
	}, planCounts(sampleDurations().plan))

	assert.Equal(t, map[string]int{
		"mapfile": 0, "rewrite": 0, "postconf": 0, "postconfd": 0, "ldap": 0, "proxygen": 0, "restart": 0,
	}, planCounts(nil))
}

// TestObserveCycle_OffByDefault pins the default behavior: with neither
// zmconfigd_metrics_textfile nor zmconfigd_cycle_summary set, a cycle writes
// no file and logs no extra line.
func TestObserveCycle_OffByDefault(t *testing.T) {
	deps := newLoopFixture(t)
	fakes := systest.New()
	deps.env = fakes.System()

	var buf bytes.Buffer

	observeCycle(captureLogs(&buf), deps, 1, sampleDurations(), 2*time.Second)

	assert.Empty(t, fakes.Rec.Ops(), "no filesystem effect without a configured textfile")
	assert.Empty(t, buf.String(), "no log line without zmconfigd_cycle_summary")
}

func TestObserveCycle_TextfileWrittenWhenConfigured(t *testing.T) {
	deps := newLoopFixture(t)
	fakes := systest.New()
	deps.env = fakes.System()
	deps.cfg.MetricsTextfile = metricsPath

	var buf bytes.Buffer

	observeCycle(captureLogs(&buf), deps, 1, sampleDurations(), 2*time.Second)

	assert.Equal(t, []string{"fs.write-atomic " + metricsPath + " mode=0644"}, fakes.Rec.Strings())
	assert.Empty(t, buf.String(), "the textfile alone adds no log line")

	data, ok := fakes.FS.Data(metricsPath)
	require.True(t, ok)

	out := string(data)
	assert.Contains(t, out, "configd_cycle_duration_seconds 2\n")
	assert.Contains(t, out, `configd_cycle_phase_duration_seconds{phase="load"} 1`)
	assert.Contains(t, out, `configd_cycle_phase_duration_seconds{phase="restarts"} 0.05`)
	assert.Contains(t, out, `configd_plan_actions{kind="rewrite"} 2`)
	assert.Contains(t, out, `configd_plan_actions{kind="proxygen"} 1`)
	assert.Contains(t, out, "configd_cycles_total 1\n")
	assert.Contains(t, out, "configd_last_cycle_timestamp_seconds 1767225600\n")
}

func TestObserveCycle_TextfileRewrittenEachCycle(t *testing.T) {
	deps := newLoopFixture(t)
	fakes := systest.New()
	deps.env = fakes.System()
	deps.cfg.MetricsTextfile = metricsPath

	ctx := context.Background()

	observeCycle(ctx, deps, 1, sampleDurations(), time.Second)
	observeCycle(ctx, deps, 2, sampleDurations(), time.Second)

	data, _ := fakes.FS.Data(metricsPath)
	assert.Contains(t, string(data), "configd_cycles_total 2\n")
	assert.Contains(t, string(data), `configd_plan_actions_total{kind="rewrite"} 4`)
}

func TestObserveCycle_WriteFailureWarnsOnly(t *testing.T) {
	deps := newLoopFixture(t)
	fakes := systest.New()
	fakes.FS.Fail = func(op, _ string) error {
		if op == "WriteFileAtomic" {
			return assert.AnError
		}

		return nil
	}
	deps.env = fakes.System()
	deps.cfg.MetricsTextfile = metricsPath

	var buf bytes.Buffer

	observeCycle(captureLogs(&buf), deps, 1, sampleDurations(), time.Second)

	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "Failed to write metrics textfile")
}

func TestObserveCycle_CycleSummaryOptIn(t *testing.T) {
	deps := newLoopFixture(t)
	deps.env = systest.New().System()
	deps.cfg.CycleSummary = true

	var buf bytes.Buffer

	observeCycle(captureLogs(&buf), deps, 7, sampleDurations(), 2*time.Second)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "level=INFO")
	assert.Contains(t, lines[0], `msg="Cycle summary"`)

	for _, kv := range []string{
		"cycle=7", "first_run=true", "duration_seconds=2", "load_seconds=1", "plan_seconds=0.2",
		"execute_seconds=0.3", "rewrites=2", "postconf=1", "postconfd=0", "ldap=0", "proxygen=1", "restarts=1",
	} {
		assert.Contains(t, lines[0], " "+kv, kv)
	}
}

func TestFinalizeIteration_NoDefaultOutputChange(t *testing.T) {
	deps := newLoopFixture(t)
	fakes := systest.New()
	deps.env = fakes.System()
	deps.args.Once = true

	var buf bytes.Buffer

	finalizeIteration(captureLogs(&buf), deps, &loopIterState{}, &loopPhaseDurations{t1: time.Now()})

	assert.NotContains(t, buf.String(), "Cycle summary")
	assert.Empty(t, fakes.Rec.Ops())
	assert.Contains(t, string(deps.configManager.Metrics().Render()), "configd_cycles_total 1")
}
