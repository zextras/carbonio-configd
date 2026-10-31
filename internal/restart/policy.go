// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package restart is the restart policy of a configd cycle. It consumes the
// service restarts of a plan.Plan: it hands them to the services.Manager with
// the reasons that caused them, lets the manager run the queue (start order,
// retries, MTA reload conversion, dependency cascade) and returns the plan
// entries it did not consume so the next cycle carries them.
//
// Optionally (localconfig zmconfigd_restart_debounce_seconds, off by default)
// requests are coalesced across cycles: a service is restarted only once it
// has been quiet for the debounce period.
package restart

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// serviceKeyPrefix is the prefix of the lookup keys the manager uses to ask
// whether a dependent service is enabled.
const serviceKeyPrefix = "SERVICE_"

// lookupEnabled is the answer for an enabled service; the manager accepts it
// as a truthy service status.
const (
	lookupEnabled  = "enabled"
	lookupDisabled = "disabled"
)

// Settings are the per-cycle restart settings read from localconfig.
type Settings struct {
	// Enabled is zmconfigd_enable_config_restarts (cfg.RestartConfig). When
	// false the plan restarts are not consumed; they are returned as carry.
	Enabled bool
	// Debounce is zmconfigd_restart_debounce_seconds. 0 disables debouncing:
	// every plan restart is queued in the cycle it appears in.
	Debounce time.Duration
}

// pendingRestart is a debounced restart request.
type pendingRestart struct {
	reasons     []string
	kinds       []string
	lastRequest time.Time
}

// Policy applies Settings to the restarts of a plan.
//
// A Policy is for the sequential main loop only: it holds the debounce state
// in memory (lost when the process restarts) and is not safe for concurrent
// use.
type Policy struct {
	mgr     services.Manager
	clock   sys.Clock
	pending map[string]*pendingRestart
	metrics *metrics.Registry
}

// New returns a Policy that queues restarts on mgr and reads time from clock.
func New(mgr services.Manager, clock sys.Clock) *Policy {
	return &Policy{mgr: mgr, clock: clock, pending: make(map[string]*pendingRestart)}
}

// WithMetrics makes the policy count the restarts it queues in reg (nil: off)
// and returns the policy.
func (p *Policy) WithMetrics(reg *metrics.Registry) *Policy {
	p.metrics = reg

	return p
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

	p.process(ctx, pl.Enabled)

	logger.DebugContext(ctx, "Service restarts complete")

	return carry
}

// queue hands every restart to the manager and returns the ones it refused.
func (p *Policy) queue(ctx context.Context, restarts []plan.Restart) []plan.Restart {
	var carry []plan.Restart

	for _, e := range restarts {
		if err := p.mgr.AddRestartWithReasons(ctx, e.Service, plan.Strings(e.Reasons)); err != nil {
			logger.WarnContext(ctx, "Failed to queue restart",
				"service", e.Service,
				"error", err)

			carry = append(carry, e)

			continue
		}

		p.metrics.RestartRequested(e.Service, reasonKinds(e.Reasons))
	}

	return carry
}

// record merges the restarts into the pending set, restarting each service's
// quiet window at now.
func (p *Policy) record(restarts []plan.Restart, now time.Time) {
	for _, e := range restarts {
		r := p.pending[e.Service]
		if r == nil {
			r = &pendingRestart{}
			p.pending[e.Service] = r
		}

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

// flush queues the pending requests accepted by due, in sorted service order.
// A request the manager refuses stays pending and is retried by the next
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

		if err := p.mgr.AddRestartWithReasons(ctx, name, slices.Clone(r.reasons)); err != nil {
			logger.WarnContext(ctx, "Failed to queue restart",
				"service", name,
				"error", err)

			continue
		}

		p.metrics.RestartRequested(name, r.kinds)
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

// process runs the manager's queue once. The cascade asks "SERVICE_<NAME>"
// and only the services enabled in the plan's snapshot answer "enabled".
func (p *Policy) process(ctx context.Context, enabled []string) {
	lookup := func(key string) string {
		if rest, ok := strings.CutPrefix(key, serviceKeyPrefix); ok && slices.Contains(enabled, strings.ToLower(rest)) {
			return lookupEnabled
		}

		return lookupDisabled
	}

	if err := p.mgr.ProcessRestarts(ctx, lookup); err != nil {
		// A shutdown interrupted the restarts; the manager already logged the
		// pending ones. A normal stop is not an error.
		if ctx.Err() != nil {
			return
		}

		logger.ErrorContext(ctx, "Error during service restarts",
			"error", err)
	}
}
