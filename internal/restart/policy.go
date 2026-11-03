// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package restart is the restart policy of a configd cycle. It consumes the
// service restarts of a plan.Plan and returns the plan entries it did not
// consume so the next cycle carries them.
//
// What "restart" means depends on how the host runs its services:
//
//   - Legacy hosts (no Carbonio systemd target enabled): every entry is handed
//     to the services.Manager with the reasons that caused it, and the manager
//     runs the queue (start order, retries, MTA reload conversion).
//   - Systemd hosts (WithSystemd): a restart of a heavy service (plan.HeavyService:
//     mailbox, appserver-db, store, ldap) is never executed, only flagged
//     "restart pending" with one INFO line carrying the reasons; any other
//     service gets "systemctl try-reload-or-restart" on its units. Start and
//     stop entries are carried out as such.
//
// Config-driven restarts never cascade through DEPENDS (legacy zmconfigd
// queues dependents only after a stop or start); only the watchdog does.
//
// Optionally (localconfig zmconfigd_restart_debounce_seconds, off by default)
// requests are coalesced across cycles: a service is acted on only once it has
// been quiet for the debounce period.
package restart

import (
	"context"
	"slices"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// maxSystemdAttempts is how many cycles a failing systemd action is tried
// before it is dropped (the units' own Restart= handles crashes); it mirrors
// the service manager's MaxFailedRestarts.
const maxSystemdAttempts = 3

// Systemd is the host side of the systemd mode: whether services are managed
// through units, and how a plan entry is carried out on them. It is satisfied
// by *services.ServiceManager.
type Systemd interface {
	// IsSystemdMode reports whether services are managed through systemd units.
	IsSystemdMode() bool
	// ApplySystemd carries out a restart (try-reload-or-restart), start or
	// stop of service.
	ApplySystemd(ctx context.Context, service string, action plan.Action) error
}

// Settings are the per-cycle restart settings read from localconfig.
type Settings struct {
	// Enabled is zmconfigd_enable_config_restarts (cfg.RestartConfig). When
	// false the plan restarts are not consumed; they are returned as carry.
	Enabled bool
	// Debounce is zmconfigd_restart_debounce_seconds. 0 disables debouncing:
	// every plan restart is acted on in the cycle it appears in.
	Debounce time.Duration
}

// pendingRestart is a debounced restart request.
type pendingRestart struct {
	action      plan.Action
	reasons     []string
	kinds       []string
	lastRequest time.Time
}

// Policy applies Settings to the restarts of a plan.
//
// A Policy is for the sequential main loop only: it holds the debounce and
// failure state in memory (lost when the process restarts) and is not safe for
// concurrent use.
type Policy struct {
	mgr      services.Manager
	systemd  Systemd
	clock    sys.Clock
	pending  map[string]*pendingRestart
	failures map[string]int
	metrics  *metrics.Registry
}

// New returns a Policy that queues restarts on mgr and reads time from clock.
func New(mgr services.Manager, clock sys.Clock) *Policy {
	return &Policy{
		mgr:      mgr,
		clock:    clock,
		pending:  make(map[string]*pendingRestart),
		failures: make(map[string]int),
	}
}

// WithMetrics makes the policy count the restarts it queues in reg (nil: off)
// and returns the policy.
func (p *Policy) WithMetrics(reg *metrics.Registry) *Policy {
	p.metrics = reg

	return p
}

// WithSystemd lets the policy follow the systemd rules when sd reports a
// systemd host (nil: always the legacy path) and returns the policy.
func (p *Policy) WithSystemd(sd Systemd) *Policy {
	p.systemd = sd

	return p
}

// systemdMode reports whether this cycle follows the systemd rules.
func (p *Policy) systemdMode() bool {
	return p.systemd != nil && p.systemd.IsSystemdMode()
}

// Apply runs the restart policy for one cycle and returns the plan restarts
// that were NOT consumed (to be carried into the next cycle's
// plan.Input.Carry.Restarts). Call it every cycle, even when the plan has no
// restarts, so debounced requests fire when they come due.
func (p *Policy) Apply(ctx context.Context, pl *plan.Plan, s Settings) []plan.Restart {
	ctx = logger.ContextWithComponentOnce(ctx, "restart")

	if !s.Enabled {
		return pl.Restarts
	}

	logger.DebugContext(ctx, "Executing service restarts")

	var carry []plan.Restart

	if s.Debounce <= 0 {
		// Anything held back by an earlier debounce setting goes out now.
		p.flush(ctx, func(*pendingRestart) bool { return true })
		carry = p.queue(ctx, pl.Restarts)
	} else {
		now := p.clock.Now()
		p.record(pl.Restarts, now)
		p.flush(ctx, func(r *pendingRestart) bool { return now.Sub(r.lastRequest) >= s.Debounce })
	}

	p.process(ctx)

	logger.DebugContext(ctx, "Service restarts complete")

	return carry
}

// queue acts on every restart and returns the ones that must be tried again.
func (p *Policy) queue(ctx context.Context, restarts []plan.Restart) []plan.Restart {
	var carry []plan.Restart

	for _, e := range restarts {
		if retry := p.dispatch(ctx, e.Service, e.Action, plan.Strings(e.Reasons), reasonKinds(e.Reasons)); retry {
			carry = append(carry, e)
		}
	}

	return carry
}

// dispatch acts on one restart request and reports whether it must be tried
// again (the manager refused it, or the systemd action failed and has attempts
// left).
func (p *Policy) dispatch(
	ctx context.Context, service string, action plan.Action, reasons, kinds []string,
) (retry bool) {
	if !p.systemdMode() {
		return p.queueLegacy(ctx, service, reasons, kinds)
	}

	switch plan.Classify(action, service, true) {
	case plan.ActionPendingRestart:
		logger.InfoContext(ctx, "Restart pending: service is not restarted automatically, restart it manually",
			"service", service, "reasons", reasons)

		return false
	default:
		return p.applySystemd(ctx, service, action, kinds)
	}
}

// queueLegacy hands the restart to the manager; it returns true when the
// manager refused it.
func (p *Policy) queueLegacy(ctx context.Context, service string, reasons, kinds []string) (retry bool) {
	if err := p.mgr.AddRestartWithReasons(ctx, service, slices.Clone(reasons)); err != nil {
		logger.WarnContext(ctx, "Failed to queue restart",
			"service", service,
			"error", err)

		return true
	}

	p.metrics.RestartRequested(service, kinds)

	return false
}

// applySystemd carries out the action on a systemd host; it returns true when
// the action failed and has attempts left.
func (p *Policy) applySystemd(
	ctx context.Context, service string, action plan.Action, kinds []string,
) (retry bool) {
	err := p.systemd.ApplySystemd(ctx, service, action)
	if err == nil {
		delete(p.failures, service)
		p.metrics.RestartRequested(service, kinds)

		return false
	}

	// A shutdown cut the action short: not a failed attempt.
	if ctx.Err() != nil {
		return false
	}

	p.failures[service]++

	if p.failures[service] >= maxSystemdAttempts {
		delete(p.failures, service)
		logger.ErrorContext(ctx, "Giving up on service action after repeated failures",
			"service", service, "action", string(action), "attempts", maxSystemdAttempts, "error", err)

		return false
	}

	logger.WarnContext(ctx, "Service action failed, will retry next cycle",
		"service", service, "action", string(action), "error", err)

	return true
}

// record merges the restarts into the pending set, restarting each service's
// quiet window at now. The latest action of a service wins.
func (p *Policy) record(restarts []plan.Restart, now time.Time) {
	for _, e := range restarts {
		r := p.pending[e.Service]
		if r == nil {
			r = &pendingRestart{}
			p.pending[e.Service] = r
		}

		r.action = e.Action

		for _, reason := range plan.Strings(e.Reasons) {
			if !slices.Contains(r.reasons, reason) {
				r.reasons = append(r.reasons, reason)
			}
		}

		for _, kind := range reasonKinds(e.Reasons) {
			if !slices.Contains(r.kinds, kind) {
				r.kinds = append(r.kinds, kind)
			}
		}

		r.lastRequest = now
	}
}

// flush acts on the pending requests accepted by due, in sorted service order.
// A request that must be tried again stays pending and is retried by the next
// Apply.
func (p *Policy) flush(ctx context.Context, due func(*pendingRestart) bool) {
	names := make([]string, 0, len(p.pending))
	for name := range p.pending {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		r := p.pending[name]
		if !due(r) {
			continue
		}

		if p.dispatch(ctx, name, r.action, slices.Clone(r.reasons), r.kinds) {
			continue
		}

		delete(p.pending, name)
	}
}

// reasonKinds returns the distinct kinds of reasons, sorted.
func reasonKinds(reasons []plan.Reason) []string {
	kinds := make([]string, 0, len(reasons))

	for i := range reasons {
		kind := string(reasons[i].Kind)
		if !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}

	slices.Sort(kinds)

	return kinds
}

// process runs the manager's queue once. Config-driven restarts never cascade
// through DEPENDS, so no enabled-service lookup is given (the manager is shared
// with the watchdog, which does cascade).
func (p *Policy) process(ctx context.Context) {
	if err := p.mgr.ProcessRestarts(ctx, nil); err != nil {
		// A shutdown interrupted the restarts; the manager already logged the
		// pending ones. A normal stop is not an error.
		if ctx.Err() != nil {
			return
		}

		logger.ErrorContext(ctx, "Error during service restarts",
			"error", err)
	}
}
