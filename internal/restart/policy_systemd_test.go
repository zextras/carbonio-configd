// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package restart_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
)

type applied struct {
	service string
	action  plan.Action
}

// fakeSystemd records the actions the policy carries out on a systemd host.
type fakeSystemd struct {
	systemd bool
	calls   []applied
	err     map[string]error
}

func (f *fakeSystemd) IsSystemdMode() bool { return f.systemd }

func (f *fakeSystemd) ApplySystemd(_ context.Context, service string, action plan.Action) error {
	f.calls = append(f.calls, applied{service, action})

	return f.err[service]
}

func newSystemdHarness(systemd bool) (*harness, *fakeSystemd) {
	h := newHarness()
	sd := &fakeSystemd{systemd: systemd, err: map[string]error{}}
	h.policy.WithSystemd(sd)

	return h, sd
}

func logCtx(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer

	ctx := logger.ContextWithLogger(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)))

	return ctx, &logs
}

func TestApply_Systemd_HeavyServicesAreOnlyFlagged(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	ctx, logs := logCtx(t)

	reason := plan.Reason{Kind: plan.ReasonVarChanged, Section: "mailbox", Type: "VAR", Key: "zimbraX", Old: "a", New: "b"}
	pl := &plan.Plan{Restarts: []plan.Restart{
		{Service: "ldap", Action: plan.ActionRestart, Reasons: []plan.Reason{reason}},
		{Service: "mailboxd", Action: plan.ActionRestart, Reasons: []plan.Reason{reason}},
		{Service: "appserver-db", Action: plan.ActionPendingRestart, Reasons: []plan.Reason{reason}},
		{Service: "store", Action: plan.ActionRestart, Reasons: []plan.Reason{reason}},
	}}

	carry := h.policy.Apply(ctx, pl, restart.Settings{Enabled: true})

	assert.Empty(t, carry, "a flagged restart is consumed, not carried")
	assert.Empty(t, sd.calls, "heavy services are never acted on")
	assert.Empty(t, h.queued, "heavy services never reach the restart queue")

	out := logs.String()
	for _, svc := range []string{"ldap", "mailboxd", "appserver-db", "store"} {
		assert.Equal(t, 1, strings.Count(out, "service="+svc+" "), "one INFO line for %s:\n%s", svc, out)
	}

	assert.Equal(t, 4, strings.Count(out, "level=INFO msg=\"Restart pending"))
	assert.Contains(t, out, "section mailbox changed because VAR zimbraX changed 'a'->'b'", "reasons are logged")
}

func TestApply_Systemd_LightServicesTryReloadOrRestart(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	pl := &plan.Plan{Restarts: []plan.Restart{
		req("amavis", enabledReason("amavis")),
		{Service: "memcached", Action: plan.ActionStart},
		{Service: "stats", Action: plan.ActionStop},
		req("ldap"),
	}}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Equal(t, []applied{
		{"amavis", plan.ActionRestart},
		{"memcached", plan.ActionStart},
		{"stats", plan.ActionStop},
	}, sd.calls)
	assert.Empty(t, h.queued, "systemd mode does not use the restart queue")
	assert.Equal(t, 1, h.process, "the manager is still drained once per cycle")
}

func TestApply_Systemd_DisabledCarriesEverythingIncludingPending(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	pl := &plan.Plan{Restarts: []plan.Restart{
		req("mta"),
		{Service: "ldap", Action: plan.ActionPendingRestart},
	}}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: false})

	assert.Equal(t, pl.Restarts, carry)
	assert.Empty(t, sd.calls)
	assert.Zero(t, h.process)
}

func TestApply_NotSystemdHostKeepsLegacyRestartsForEverything(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(false)
	pl := &plan.Plan{Restarts: []plan.Restart{
		req("ldap", enabledReason("ldap")),
		req("mailbox", enabledReason("mailbox")),
		req("mta", enabledReason("mta")),
	}}

	carry := h.policy.Apply(t.Context(), pl, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Empty(t, sd.calls)
	assert.Equal(t, []string{"add:ldap", "add:mailbox", "add:mta", "process"}, h.callList,
		"legacy hosts restart heavy services too (mta restart=reload happens in the manager)")
}

func TestApply_NoSystemdWiringIsLegacy(t *testing.T) {
	t.Parallel()

	h := newHarness()

	carry := h.policy.Apply(t.Context(), &plan.Plan{Restarts: []plan.Restart{req("ldap")}}, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
	assert.Equal(t, []string{"add:ldap", "process"}, h.callList)
}

func TestApply_Systemd_FailureIsRetriedThenDropped(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	sd.err["proxy"] = errors.New("unit failed")
	ctx, logs := logCtx(t)

	proxy, mta := req("proxy", enabledReason("proxy")), req("mta", enabledReason("mta"))
	pl := &plan.Plan{Restarts: []plan.Restart{mta, proxy}}

	// Attempts 1 and 2 are carried; the healthy service is not affected.
	carry := h.policy.Apply(ctx, pl, restart.Settings{Enabled: true})
	assert.Equal(t, []plan.Restart{proxy}, carry)

	carry = h.policy.Apply(ctx, &plan.Plan{Restarts: carry}, restart.Settings{Enabled: true})
	assert.Equal(t, []plan.Restart{proxy}, carry)
	assert.NotContains(t, logs.String(), "level=ERROR")

	// The third failure gives up.
	carry = h.policy.Apply(ctx, &plan.Plan{Restarts: carry}, restart.Settings{Enabled: true})
	assert.Empty(t, carry)
	assert.Contains(t, logs.String(), "level=ERROR")
	assert.Contains(t, logs.String(), "Giving up")

	// A later success starts from a clean slate.
	sd.err["proxy"] = nil
	carry = h.policy.Apply(ctx, &plan.Plan{Restarts: []plan.Restart{proxy}}, restart.Settings{Enabled: true})
	assert.Empty(t, carry)
}

func TestApply_Systemd_ShutdownIsNotAFailure(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	sd.err["proxy"] = context.Canceled

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	carry := h.policy.Apply(ctx, &plan.Plan{Restarts: []plan.Restart{req("proxy")}}, restart.Settings{Enabled: true})

	assert.Empty(t, carry)
}

func TestApply_Systemd_DebouncedRestartsFollowTheSameRules(t *testing.T) {
	t.Parallel()

	h, sd := newSystemdHarness(true)
	ctx, logs := logCtx(t)
	pl := &plan.Plan{Restarts: []plan.Restart{req("proxy"), req("ldap")}}
	s := restart.Settings{Enabled: true, Debounce: debounce}

	h.policy.Apply(ctx, pl, s)
	assert.Empty(t, sd.calls, "nothing before the quiet period")
	assert.NotContains(t, logs.String(), "Restart pending")

	h.clock.Advance(debounce)
	h.policy.Apply(ctx, &plan.Plan{}, s)

	require.Equal(t, []applied{{"proxy", plan.ActionRestart}}, sd.calls)
	assert.Equal(t, 1, strings.Count(logs.String(), "Restart pending"))

	// Both were consumed: nothing fires again.
	h.clock.Advance(debounce)
	h.policy.Apply(ctx, &plan.Plan{}, s)
	assert.Len(t, sd.calls, 1)
	assert.Equal(t, 1, strings.Count(logs.String(), "Restart pending"))
}
