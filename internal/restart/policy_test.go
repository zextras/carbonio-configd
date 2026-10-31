// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package restart_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

const debounce = 30 * time.Second

type queued struct {
	service string
	reasons []string
}

// harness wires a Policy to a recording mock manager and a manual clock.
type harness struct {
	policy   *restart.Policy
	clock    *systest.Clock
	queued   []queued
	process  int
	lookup   func(string) string
	depsSet  int
	addErr   map[string]error
	procErr  error
	callList []string
}

func newHarness() *harness {
	h := &harness{addErr: map[string]error{}}
	h.clock = systest.NewClock(nil)

	mgr := &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, service string, reasons []string) error {
			if err := h.addErr[service]; err != nil {
				return err
			}

			h.queued = append(h.queued, queued{service, append([]string{}, reasons...)})
			h.callList = append(h.callList, "add:"+service)

			return nil
		},
		ProcessRestartsFn: func(_ context.Context, lookup func(string) string) error {
			h.process++
			h.lookup = lookup
			h.callList = append(h.callList, "process")

			return h.procErr
		},
		SetDependenciesFn: func(_ context.Context, _ map[string][]string) {
			h.depsSet++
		},
	}
	h.policy = restart.New(mgr, h.clock)

	return h
}

func req(service string, reasons ...plan.Reason) plan.Restart {
	return plan.Restart{Service: service, Action: plan.ActionRestart, Reasons: reasons}
}

func enabledReason(service string) plan.Reason {
	return plan.Reason{Kind: plan.ReasonServiceEnabled, Service: service}
}

func TestApply_Disabled(t *testing.T) {
	t.Parallel()

	h := newHarness()
	pl := &plan.Plan{
		Restarts: []plan.Restart{req("mta", enabledReason("mta"))},
	}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: false})

	assert.Equal(t, pl.Restarts, carry)
	assert.Empty(t, h.queued)
	assert.Zero(t, h.process)
}

func TestApply_EnabledQueuesWithReasons(t *testing.T) {
	t.Parallel()

	h := newHarness()
	pl := &plan.Plan{
		Restarts: []plan.Restart{
			req("mta", enabledReason("mta")),
			req("proxy", plan.Reason{Kind: plan.ReasonRetry}),
		},
		Enabled: []string{"mta", "proxy"},
	}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Equal(t, []queued{
		{"mta", []string{"service mta was enabled"}},
		{"proxy", []string{"retry: previous attempt did not complete"}},
	}, h.queued)
	assert.Equal(t, []string{"add:mta", "add:proxy", "process"}, h.callList)
	require.Equal(t, 1, h.process)
}

func TestApply_LookupHonorsEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want string
	}{
		{"SERVICE_MTA", "enabled"},
		{"SERVICE_PROXY", "enabled"},
		{"SERVICE_ANTIVIRUS", "disabled"},
		{"SERVICE_", "disabled"},
		{"OTHER_MTA", "disabled"},
		{"mta", "disabled"},
	}

	h := newHarness()
	pl := &plan.Plan{Enabled: []string{"mta", "proxy"}}

	h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})
	require.NotNil(t, h.lookup)

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, h.lookup(tc.key))
		})
	}
}

func TestApply_AddRestartErrorIsCarried(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.addErr["mta"] = errors.New("boom")

	mta, proxy := req("mta", enabledReason("mta")), req("proxy", enabledReason("proxy"))
	pl := &plan.Plan{Restarts: []plan.Restart{mta, proxy}}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})

	assert.Equal(t, []plan.Restart{mta}, carry)
	assert.Equal(t, []queued{{"proxy", []string{"service proxy was enabled"}}}, h.queued)
	assert.Equal(t, 1, h.process)
}

func TestApply_ProcessErrorDoesNotCarry(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.procErr = errors.New("restarts failed")

	carry := h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta")}}, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Equal(t, 1, h.process)
}

// TestApply_NeverSetsDependencies pins the legacy semantics: a config-driven
// restart does not cascade through DEPENDS (zmconfigd only does so after a
// stop/start), so the policy must leave the manager's dependency map alone.
func TestApply_NeverSetsDependencies(t *testing.T) {
	t.Parallel()

	h := newHarness()

	carry := h.policy.Apply(t.Context(), &plan.Plan{
		Restarts: []plan.Restart{req("amavis", enabledReason("amavis"))},
	}, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Zero(t, h.depsSet)
	assert.Equal(t, 1, h.process, "an empty plan still drains what the manager already holds")
}

func TestApply_Debounce(t *testing.T) {
	t.Parallel()

	settings := restart.Settings{Enabled: true, Debounce: debounce}

	t.Run("not fired before the quiet period, fired at it", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		carry := h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta", enabledReason("mta"))}}, settings)
		assert.Empty(t, carry, "a recorded request is consumed")
		assert.Empty(t, h.queued)

		h.clock.Advance(debounce - time.Second)
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Empty(t, h.queued)

		h.clock.Advance(time.Second)
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Equal(t, []queued{{"mta", []string{"service mta was enabled"}}}, h.queued)

		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Len(t, h.queued, 1, "a flushed request fires once")
	})

	t.Run("repeated requests restart the window and merge reasons", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta", enabledReason("mta"))}}, settings)
		h.clock.Advance(debounce - time.Second)

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{
			req("mta", enabledReason("mta"), plan.Reason{Kind: plan.ReasonRetry}),
		}}, settings)

		h.clock.Advance(debounce - time.Second)
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Empty(t, h.queued, "the second request restarted the quiet window")

		h.clock.Advance(time.Second)
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Equal(t, []queued{{"mta", []string{
			"service mta was enabled",
			"retry: previous attempt did not complete",
		}}}, h.queued)
	})

	t.Run("services flush independently in sorted order", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("proxy")}}, settings)
		h.clock.Advance(debounce / 2)
		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta")}}, settings)
		h.clock.Advance(debounce / 2)

		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Equal(t, []queued{{"proxy", []string{}}}, h.queued)

		h.clock.Advance(debounce / 2)
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Equal(t, []queued{{"proxy", []string{}}, {"mta", []string{}}}, h.queued)
	})

	t.Run("flush batch is processed once", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("proxy"), req("mta")}}, settings)
		h.clock.Advance(debounce)

		before := h.process
		h.policy.Apply(t.Context(), &plan.Plan{Enabled: []string{"mta"}}, settings)

		assert.Equal(t, []string{"mta", "proxy"}, []string{h.queued[0].service, h.queued[1].service})
		assert.Equal(t, before+1, h.process)
	})

	t.Run("a refused flush stays pending and retries", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		h.addErr["mta"] = errors.New("boom")

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta")}}, settings)
		h.clock.Advance(debounce)

		carry := h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Empty(t, carry)
		assert.Empty(t, h.queued)

		delete(h.addErr, "mta")
		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Equal(t, []queued{{"mta", []string{}}}, h.queued)
	})

	t.Run("switching debounce to zero flushes pending", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta", enabledReason("mta"))}}, settings)
		assert.Empty(t, h.queued)

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("proxy")}}, restart.Settings{Enabled: true})

		assert.Equal(t, []queued{
			{"mta", []string{"service mta was enabled"}},
			{"proxy", []string{}},
		}, h.queued)
	})

	t.Run("disabled neither flushes nor drops pending", func(t *testing.T) {
		t.Parallel()

		h := newHarness()

		h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta")}}, settings)
		h.clock.Advance(2 * debounce)

		before := h.process
		carry := h.policy.Apply(t.Context(), &plan.Plan{}, restart.Settings{Enabled: false, Debounce: debounce})
		assert.Empty(t, carry)
		assert.Empty(t, h.queued)
		assert.Equal(t, before, h.process)

		h.policy.Apply(t.Context(), &plan.Plan{}, settings)
		assert.Len(t, h.queued, 1, "still pending, fires once restarts are enabled again")
	})
}

func TestApply_MetricsCountQueuedRestartsByReasonKind(t *testing.T) {
	t.Parallel()

	h := newHarness()
	reg := metrics.New()
	h.policy.WithMetrics(reg)
	h.addErr["proxy"] = errors.New("refused")

	pl := &plan.Plan{Restarts: []plan.Restart{
		req("mta", enabledReason("mta"), plan.Reason{Kind: plan.ReasonVarChanged}),
		req("proxy", plan.Reason{Kind: plan.ReasonRetry}),
	}}

	h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})

	out := string(reg.Render())
	assert.Contains(t, out, `configd_restart_requests_total{reason="service-enabled",service="mta"} 1`)
	assert.Contains(t, out, `configd_restart_requests_total{reason="var-changed",service="mta"} 1`)
	assert.NotContains(t, out, `service="proxy"`, "a restart the manager refused is not counted")
}

func TestApply_MetricsDisabledRestartsNotCounted(t *testing.T) {
	t.Parallel()

	h := newHarness()
	reg := metrics.New()
	h.policy.WithMetrics(reg)

	h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("mta", enabledReason("mta"))}},
		restart.Settings{Enabled: false})

	assert.Empty(t, reg.Render())
}

func TestApply_MetricsDebouncedCountedWhenQueued(t *testing.T) {
	t.Parallel()

	h := newHarness()
	reg := metrics.New()
	h.policy.WithMetrics(reg)

	settings := restart.Settings{Enabled: true, Debounce: debounce}
	pl := &plan.Plan{Restarts: []plan.Restart{req("mta", enabledReason("mta"))}}

	h.policy.Apply(t.Context(), pl, settings)
	assert.Empty(t, reg.Render(), "still inside the quiet window")

	h.clock.Advance(debounce)
	h.policy.Apply(t.Context(), &plan.Plan{}, settings)

	assert.Contains(t, string(reg.Render()), `configd_restart_requests_total{reason="service-enabled",service="mta"} 1`)
}

// TestApply_ShutdownDuringRestartsIsNotAnError: when the context is cancelled
// the manager reports the interrupted processing; the policy must not turn
// that normal stop into an ERROR log.
func TestApply_ShutdownDuringRestartsIsNotAnError(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.procErr = context.Canceled

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var logs bytes.Buffer

	ctx = logger.ContextWithLogger(ctx, slog.New(slog.NewTextHandler(&logs, nil)))

	h.policy.Apply(ctx, &plan.Plan{Restarts: []plan.Restart{req("mta")}}, restart.Settings{Enabled: true})

	assert.NotContains(t, logs.String(), "level=ERROR")
}
