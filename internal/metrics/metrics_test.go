// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func populated() *Registry {
	r := New()

	// Recorded out of order on purpose: the rendering must not depend on it.
	r.WatchdogRestart("zmstat", false)
	r.RestartRequested("mta", []string{"var-changed", "first-run"})
	r.LoadFailed("sc")
	r.RestartRequested("antivirus", nil)
	r.WatchdogRestart("antivirus", true)
	r.LoadFailed("lc")
	r.PlanSize(map[string]int{"rewrite": 2, "postconf": 0, "restart": 1})
	r.RestartRequested("mta", []string{"var-changed"})
	r.CycleDone(1500*time.Millisecond, []Phase{{"plan", 250 * time.Millisecond}, {"load", time.Second}},
		time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC))

	return r
}

const wantRender = `# HELP configd_cycle_duration_seconds Wall-clock duration of the last completed configd cycle in seconds.
# TYPE configd_cycle_duration_seconds gauge
configd_cycle_duration_seconds 1.5
# HELP configd_cycle_phase_duration_seconds Duration of one phase of the last completed configd cycle in seconds.
# TYPE configd_cycle_phase_duration_seconds gauge
configd_cycle_phase_duration_seconds{phase="load"} 1
configd_cycle_phase_duration_seconds{phase="plan"} 0.25
# HELP configd_cycles_total Number of completed configd cycles since the process started.
# TYPE configd_cycles_total counter
configd_cycles_total 1
# HELP configd_last_cycle_timestamp_seconds Unix time at which the last configd cycle completed.
# TYPE configd_last_cycle_timestamp_seconds gauge
configd_last_cycle_timestamp_seconds 1767225605
# HELP configd_load_failures_total Failed configuration loads, by source (lc, gc, mc, sc loaders, timeout, mta_config).
# TYPE configd_load_failures_total counter
configd_load_failures_total{source="lc"} 1
configd_load_failures_total{source="sc"} 1
# HELP configd_plan_actions Actions in the plan of the last completed cycle, by kind.
# TYPE configd_plan_actions gauge
configd_plan_actions{kind="postconf"} 0
configd_plan_actions{kind="restart"} 1
configd_plan_actions{kind="rewrite"} 2
# HELP configd_plan_actions_total Actions planned since the process started, by kind.
# TYPE configd_plan_actions_total counter
configd_plan_actions_total{kind="postconf"} 0
configd_plan_actions_total{kind="restart"} 1
configd_plan_actions_total{kind="rewrite"} 2
# HELP configd_restart_requests_total Service restarts queued by the restart policy, by service and reason kind; a restart with several reasons counts once under each kind.
# TYPE configd_restart_requests_total counter
configd_restart_requests_total{reason="first-run",service="mta"} 1
configd_restart_requests_total{reason="unknown",service="antivirus"} 1
configd_restart_requests_total{reason="var-changed",service="mta"} 2
# HELP configd_watchdog_restarts_total Restarts attempted by the service watchdog, by service and result.
# TYPE configd_watchdog_restarts_total counter
configd_watchdog_restarts_total{result="failure",service="zmstat"} 1
configd_watchdog_restarts_total{result="success",service="antivirus"} 1
`

func TestRender_ExactFormat(t *testing.T) {
	t.Parallel()

	assert.Equal(t, wantRender, string(populated().Render()))
}

func TestRender_Deterministic(t *testing.T) {
	t.Parallel()

	first := populated().Render()
	for range 20 {
		assert.Equal(t, string(first), string(populated().Render()))
	}
}

// TestRender_PrometheusTextFormat checks the structural rules promtool
// enforces: HELP then TYPE directly before the first sample of every metric,
// valid names, counters ending in _total, sorted metrics, no duplicate series.
func TestRender_PrometheusTextFormat(t *testing.T) {
	t.Parallel()

	var (
		sample   = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[a-zA-Z_][a-zA-Z0-9_]*="[^"\\]*"(,[a-zA-Z_][a-zA-Z0-9_]*="[^"\\]*")*\})? -?[0-9][0-9.e+-]*$`)
		lines    = strings.Split(strings.TrimSuffix(string(populated().Render()), "\n"), "\n")
		seen     = map[string]bool{}
		prevName string
		current  string
	)

	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name := strings.Fields(line)[2]
			assert.Greater(t, name, prevName, "metrics must be sorted")
			assert.True(t, strings.HasPrefix(lines[i+1], "# TYPE "+name+" "), "TYPE must follow HELP")

			prevName, current = name, name
		case strings.HasPrefix(line, "# TYPE "):
			f := strings.Fields(line)
			if f[3] == "counter" {
				assert.True(t, strings.HasSuffix(f[2], "_total"), "counter %s must end in _total", f[2])
			}
		default:
			m := sample.FindStringSubmatch(line)
			require.NotNil(t, m, "malformed sample line %q", line)
			assert.Equal(t, current, m[1], "sample outside its metric block")
			assert.False(t, seen[line[:strings.LastIndex(line, " ")]], "duplicate series %q", line)

			seen[line[:strings.LastIndex(line, " ")]] = true
		}
	}
}

func TestLabelValueEscaping(t *testing.T) {
	t.Parallel()

	r := New()
	r.LoadFailed("a\"b\\c\nd")

	assert.Contains(t, string(r.Render()), `configd_load_failures_total{source="a\"b\\c\nd"} 1`)
}

func TestCounterAccumulatesGaugeReplaces(t *testing.T) {
	t.Parallel()

	r := New()
	r.PlanSize(map[string]int{"rewrite": 3})
	r.PlanSize(map[string]int{"rewrite": 1})

	out := string(r.Render())
	assert.Contains(t, out, `configd_plan_actions{kind="rewrite"} 1`)
	assert.Contains(t, out, `configd_plan_actions_total{kind="rewrite"} 4`)
}

func TestNilRegistryIsNoop(t *testing.T) {
	t.Parallel()

	var r *Registry

	assert.NotPanics(t, func() {
		r.CycleDone(time.Second, nil, time.Time{})
		r.PlanSize(map[string]int{"x": 1})
		r.RestartRequested("s", nil)
		r.WatchdogRestart("s", true)
		r.LoadFailed("lc")
	})
	assert.Empty(t, r.Render())
	assert.NoError(t, WriteTextfile(systest.New().FS, "/x.prom", r))
}

func TestEmptyRegistryRendersNothing(t *testing.T) {
	t.Parallel()

	assert.Empty(t, New().Render())
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	r := New()
	done := make(chan struct{})

	for range 8 {
		go func() {
			for range 200 {
				r.WatchdogRestart("svc", true)
				_ = r.Render()
			}

			done <- struct{}{}
		}()
	}

	for range 8 {
		<-done
	}

	assert.Contains(t, string(r.Render()), `configd_watchdog_restarts_total{result="success",service="svc"} 1600`)
}

func TestWriteTextfile(t *testing.T) {
	t.Parallel()

	f := systest.New()

	require.NoError(t, WriteTextfile(f.FS, "/var/lib/node_exporter/configd.prom", populated()))

	data, ok := f.FS.Data("/var/lib/node_exporter/configd.prom")
	require.True(t, ok)
	assert.Equal(t, wantRender, string(data))

	assert.Equal(t, []string{"fs.write-atomic /var/lib/node_exporter/configd.prom mode=0644"}, f.Rec.Strings(),
		"one atomic write with node_exporter-readable permissions, nothing else")
}

func TestWriteTextfile_ErrorIsReported(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Fail = func(op, _ string) error {
		if op == "WriteFileAtomic" {
			return assert.AnError
		}

		return nil
	}

	err := WriteTextfile(f.FS, "/x.prom", populated())
	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "/x.prom")
}
