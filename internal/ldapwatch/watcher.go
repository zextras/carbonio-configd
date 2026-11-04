// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package ldapwatch detects changes of the LDAP entries a configd cycle
// reads and asks the main loop for an immediate cycle instead of waiting for
// the next interval. It is on by default and can be turned off with localconfig
// zmconfigd_ldap_change_trigger=FALSE; it never replaces the interval poll,
// which stays the safety net.
//
// # Mechanism
//
// Two detection modes feed one state machine; in both, the entries' fingerprint
// (entryCSN and modifyTimestamp, see LDAPSource) decides what changed:
//
//   - Sync mode, when Options.Subscriber is set and the directory supports it.
//     A Subscriber (see SyncSubscriber) holds an RFC 4533 refreshAndPersist
//     search open and wakes the watcher for each change. A directory that
//     advertises the content-sync control in its root DSE has the syncprov
//     overlay, which multi-master and replica Carbonio setups load. A wake-up
//     is only a hint: after the debounce window the watcher reads the
//     fingerprint once and reports the components that really changed, so one
//     burst of writes costs one fingerprint read, not one per write.
//   - Poll mode, the fallback. A single-node Carbonio directory does not load
//     syncprov (there is no contextCSN on the root DSE) and OpenLDAP has no
//     persistent search control, so the watcher polls the fingerprint at a
//     short interval and compares it with the previous one.
//
// contextCSN, when present, moves on every write of the database (mailbox
// accounts included), so it cannot tell a relevant change from noise; the
// entryCSN of the few entries a cycle reads can.
//
// # Behaviour
//
//   - The first successful fingerprint only establishes the baseline; it never
//     triggers.
//   - A change is reported once it has been quiet for the debounce window, or
//     when the burst has lasted MaxWait, so a burst of writes produces one
//     trigger and a continuous stream of writes cannot postpone it forever.
//   - A failed poll backs off exponentially (Options.BackoffMin up to
//     Options.BackoffMax) and is logged as a warning, never as an error: a
//     watcher failure is not a failed cycle. After a reconnect the first
//     fingerprint is compared with the last good one, so changes made while
//     the directory was unreachable still trigger.
//   - A lost sync stream is re-opened with the same back-off, which only
//     resets once a stream has lasted BackoffMax, so a flapping server is not
//     hammered. While the stream is live a fingerprint is also read every
//     Options.ResyncInterval as a safety net against a missed notification.
//   - When the directory cannot stream, the watcher warns once, polls, and
//     tries the stream again every Options.ProbeInterval, so a directory that
//     gains syncprov is picked up without a restart.
//   - Run returns promptly when its context is cancelled.
//
// All waiting and time reads go through the injected sys.Clock.
package ldapwatch

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	// defaultInterval is the poll interval used when Options.Interval is unset.
	defaultInterval = 5 * time.Second
	// defaultBackoffMin and defaultBackoffMax bound the wait after a failed
	// poll when Options leaves them unset.
	defaultBackoffMin = time.Second
	defaultBackoffMax = time.Minute

	// maxWaitFactor is how many debounce windows a burst may last before it is
	// reported anyway.
	maxWaitFactor = 10
	// backoffFactor is the growth of the wait after consecutive failures.
	backoffFactor = 2

	// defaultResyncInterval is how often a live sync stream is cross-checked
	// with a fingerprint read when Options.ResyncInterval is unset.
	defaultResyncInterval = 5 * time.Minute
	// defaultProbeInterval is how long the watcher polls after the directory
	// turned out not to support sync before it tries again.
	defaultProbeInterval = 10 * time.Minute
)

// Detection modes, as reported to Options.OnMode.
const (
	// ModeSync: a content-sync stream is live and drives the watcher.
	ModeSync = "sync"
	// ModePoll: the fingerprint is polled (sync unavailable, not configured, or
	// the stream is down and being re-opened).
	ModePoll = "poll"
)

// errStreamClosed is the end of a stream the server closed without an error.
var errStreamClosed = errors.New("change stream closed")

// Fingerprint maps a watched component (for example "global") to a digest of
// the state of its entries. Equal digests mean nothing changed.
type Fingerprint map[string]string

// Source reads the current Fingerprint of the watched entries. An
// implementation must return promptly with ctx.Err() when ctx is cancelled.
type Source interface {
	Fingerprint(ctx context.Context) (Fingerprint, error)
}

// Subscriber pushes change notifications from the directory.
type Subscriber interface {
	// Subscribe opens a change stream and blocks while it is open. It calls
	// live once the stream is established and its initial content has been
	// delivered, and changed for each change after that. changed may be called
	// often and must be cheap. Subscribe returns when ctx is cancelled (with
	// ctx.Err()), when the stream fails or is closed, or immediately with an
	// error wrapping ErrSyncUnsupported when the directory cannot stream.
	Subscribe(ctx context.Context, live, changed func()) error
}

// Options configure a Watcher.
type Options struct {
	// Source is read for the fingerprint, in poll mode every Interval and in
	// sync mode after a stream notification. Required.
	Source Source
	// Subscriber, when set, makes the watcher event-driven whenever the
	// directory supports it; nil means poll mode only.
	Subscriber Subscriber
	// Clock drives every wait and time read. Required.
	Clock sys.Clock
	// Trigger is called, from the Run goroutine, with the sorted names of the
	// components that changed since the previous trigger. It must not block.
	// Required.
	Trigger func(ctx context.Context, changed []string)
	// Interval is the poll interval of poll mode; values <= 0 mean 5s.
	Interval time.Duration
	// ResyncInterval is how often a live stream is cross-checked with a
	// fingerprint read; values <= 0 mean 5m.
	ResyncInterval time.Duration
	// ProbeInterval is how long the watcher polls after sync turned out to be
	// unsupported before trying the stream again; values <= 0 mean 10m.
	ProbeInterval time.Duration
	// Debounce is the quiet window a change must survive before it is
	// reported; 0 reports a change in the poll that sees it.
	Debounce time.Duration
	// MaxWait bounds how long a burst of changes may postpone the trigger;
	// values <= 0 mean maxWaitFactor * Debounce.
	MaxWait time.Duration
	// BackoffMin and BackoffMax bound the wait after a failed poll; values <= 0
	// mean 1s and 1m.
	BackoffMin, BackoffMax time.Duration
	// OnPollError, when set, is called from the Run goroutine after every
	// failed poll (not for a cancelled context), for metrics. It must not
	// block.
	OnPollError func()
	// OnMode, when set, is called from the Run goroutine with ModePoll when Run
	// starts and with the new mode each time the detection mode changes, for
	// metrics. It must not block.
	OnMode func(mode string)
	// OnStreamLost, when set, is called from the Run goroutine each time an
	// established or attempted sync stream ends with a failure (not for
	// unsupported sync or a cancelled context), for metrics. It must not block.
	OnStreamLost func()
}

// Watcher polls a Source and reports changes through Options.Trigger.
type Watcher struct {
	source       Source
	sub          Subscriber
	clock        sys.Clock
	trigger      func(ctx context.Context, changed []string)
	onPollErr    func()
	onMode       func(mode string)
	onStreamLost func()
	interval     time.Duration
	resync       time.Duration
	probe        time.Duration
	debounce     time.Duration
	maxWait      time.Duration
	backoffMin   time.Duration
	backoffMax   time.Duration

	mode     string // current mode, only touched from Run
	fellBack bool   // the fallback warning was already logged
}

// New returns a Watcher. It panics when a required option is missing: that is
// a programming error, not a runtime condition.
func New(o *Options) *Watcher {
	if o.Source == nil || o.Clock == nil || o.Trigger == nil {
		panic("ldapwatch: Source, Clock and Trigger are required")
	}

	w := &Watcher{
		source:       o.Source,
		sub:          o.Subscriber,
		clock:        o.Clock,
		trigger:      o.Trigger,
		onPollErr:    o.OnPollError,
		onMode:       o.OnMode,
		onStreamLost: o.OnStreamLost,
		interval:     positiveOr(o.Interval, defaultInterval),
		resync:       positiveOr(o.ResyncInterval, defaultResyncInterval),
		probe:        positiveOr(o.ProbeInterval, defaultProbeInterval),
		debounce:     max(o.Debounce, 0),
		backoffMin:   positiveOr(o.BackoffMin, defaultBackoffMin),
		backoffMax:   positiveOr(o.BackoffMax, defaultBackoffMax),
	}

	w.maxWait = positiveOr(o.MaxWait, maxWaitFactor*w.debounce)
	w.backoffMax = max(w.backoffMax, w.backoffMin)

	return w
}

func positiveOr(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}

	return fallback
}

// runState is the mutable state of one Run.
type runState struct {
	base    Fingerprint         // last good fingerprint; nil until the baseline is known
	changed map[string]struct{} // components changed since the last trigger
	bursts  int                 // changes folded into the pending trigger
	first   time.Time           // when the pending burst started
	last    time.Time           // when the latest change of the burst was seen
	backoff time.Duration       // wait after the next failed poll
	failing bool                // the previous poll failed
}

func (s *runState) pending() bool { return len(s.changed) > 0 }

// Run watches until ctx is cancelled and then returns.
func (w *Watcher) Run(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "ldapwatch")
	st := &runState{backoff: w.backoffMin, changed: make(map[string]struct{})}

	w.setMode(ModePoll)

	for ctx.Err() == nil {
		if w.sub == nil {
			w.poll(ctx, st, time.Time{})

			continue
		}

		if !w.stream(ctx, st) {
			return
		}

		// The directory cannot stream: poll for a while, then look again.
		w.poll(ctx, st, w.clock.Now().Add(w.probe))
	}
}

// setMode reports a change of the detection mode.
func (w *Watcher) setMode(mode string) {
	if mode == w.mode {
		return
	}

	w.mode = mode

	if w.onMode != nil {
		w.onMode(mode)
	}
}

// poll polls the fingerprint until ctx is cancelled or, when until is not the
// zero time, until that instant has passed.
func (w *Watcher) poll(ctx context.Context, st *runState, until time.Time) {
	for ctx.Err() == nil && (until.IsZero() || w.clock.Now().Before(until)) {
		if err := w.clock.Sleep(ctx, w.step(ctx, st)); err != nil {
			return
		}
	}
}

// step performs one poll and returns how long to wait before the next one.
func (w *Watcher) step(ctx context.Context, st *runState) time.Duration {
	fp, err := w.source.Fingerprint(ctx)
	if err != nil {
		return w.pollFailed(ctx, st, err)
	}

	w.pollRecovered(ctx, st)

	now := w.clock.Now()

	if st.base == nil {
		st.base = fp

		logger.DebugContext(ctx, "LDAP change baseline established", "components", len(fp))
	} else {
		w.observe(ctx, st, fp, now)
	}

	if !st.pending() {
		return w.interval
	}

	if w.due(st, now) {
		w.fire(ctx, st, now)

		return w.interval
	}

	return min(w.debounce, w.interval)
}

// observe folds the differences between the baseline and fp into the pending
// burst and makes fp the new baseline.
func (w *Watcher) observe(ctx context.Context, st *runState, fp Fingerprint, now time.Time) {
	diff := changedComponents(st.base, fp)
	if len(diff) == 0 {
		return
	}

	st.base = fp

	if !st.pending() {
		st.first = now
	}

	st.last = now
	st.bursts++

	for _, c := range diff {
		st.changed[c] = struct{}{}
	}

	logger.DebugContext(ctx, "LDAP change observed", "components", diff)
}

// due reports whether the pending burst has to be reported now.
func (w *Watcher) due(st *runState, now time.Time) bool {
	return w.debounce == 0 || now.Sub(st.last) >= w.debounce || now.Sub(st.first) >= w.maxWait
}

// fire reports the pending burst and clears it.
func (w *Watcher) fire(ctx context.Context, st *runState, now time.Time) {
	components := slices.Sorted(maps.Keys(st.changed))

	logger.InfoContext(ctx, "LDAP change detected, requesting configuration reload",
		"components", components,
		"changes_coalesced", st.bursts,
		"burst_ms", now.Sub(st.first).Milliseconds())

	clear(st.changed)

	st.bursts = 0

	w.trigger(ctx, components)
}

// pollFailed logs a failed poll and returns the back-off wait.
func (w *Watcher) pollFailed(ctx context.Context, st *runState, err error) time.Duration {
	// A cancelled context is a shutdown, not a failed poll.
	if ctx.Err() != nil {
		return 0
	}

	if w.onPollErr != nil {
		w.onPollErr()
	}

	wait := st.backoff
	st.backoff = min(st.backoff*backoffFactor, w.backoffMax)
	st.failing = true

	logger.WarnContext(ctx, "LDAP change poll failed, backing off",
		"error", err,
		"retry_in_seconds", wait.Seconds())

	return wait
}

// pollRecovered resets the back-off after a successful poll.
func (w *Watcher) pollRecovered(ctx context.Context, st *runState) {
	st.backoff = w.backoffMin

	if st.failing {
		st.failing = false

		logger.InfoContext(ctx, "LDAP change poll recovered")
	}
}

// changedComponents returns the sorted names of the components whose digest
// differs between prev and cur, including added and removed ones.
func changedComponents(prev, cur Fingerprint) []string {
	var diff []string

	for name, digest := range cur {
		if old, ok := prev[name]; !ok || old != digest {
			diff = append(diff, name)
		}
	}

	for name := range prev {
		if _, ok := cur[name]; !ok {
			diff = append(diff, name)
		}
	}

	slices.Sort(diff)

	return diff
}
