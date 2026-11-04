// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldapwatch"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// subStep is what one Subscribe call does: it may report the stream live, play
// notifications, and return an error; a nil return value after the step means
// "stay open until ctx is done".
type subStep func(ctx context.Context, live, changed func()) (err error, stayOpen bool)

// scriptedSubscriber runs one step per Subscribe call; past the end of the
// script every call reports live and stays open.
type scriptedSubscriber struct {
	mu      sync.Mutex
	steps   []subStep
	calls   int
	changed func()
}

func (s *scriptedSubscriber) Subscribe(ctx context.Context, live, changed func()) error {
	s.mu.Lock()
	i := s.calls
	s.calls++
	s.changed = changed
	s.mu.Unlock()

	step := liveAndStay
	if i < len(s.steps) {
		step = s.steps[i]
	}

	err, stay := step(ctx, live, changed)
	if stay {
		<-ctx.Done()

		return ctx.Err() //nolint:wrapcheck // the watcher wants the bare context error
	}

	return err
}

func (s *scriptedSubscriber) subscribed() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

func (s *scriptedSubscriber) notify() {
	s.mu.Lock()
	changed := s.changed
	s.mu.Unlock()

	changed()
}

func liveAndStay(_ context.Context, live, _ func()) (error, bool) {
	live()

	return nil, true
}

func liveThenFail(err error) subStep {
	return func(_ context.Context, live, _ func()) (error, bool) {
		live()

		return err, false
	}
}

// liveUntil reports the stream live and keeps it open until err is sent on drop,
// which ends it with that error.
func liveUntil(drop <-chan error) subStep {
	return func(ctx context.Context, live, _ func()) (error, bool) {
		live()

		select {
		case err := <-drop:
			return err, false
		case <-ctx.Done():
			return ctx.Err(), false
		}
	}
}

func unsupported(context.Context, func(), func()) (error, bool) {
	return ldapwatch.ErrSyncUnsupported, false
}

// timerCountingClock counts the timers the watcher creates: every stream
// notification it handles arms exactly one quiet-window timer, which is how a
// test knows the notification was processed before it moves the clock.
type timerCountingClock struct {
	*systest.Clock
	timers atomic.Int64
}

func (c *timerCountingClock) NewTimer(d time.Duration) sys.Timer {
	c.timers.Add(1)

	return c.Clock.NewTimer(d)
}

// logRecorder is a slog.Handler that keeps "level message" for every record.
type logRecorder struct {
	mu   sync.Mutex
	logs []string
}

func (*logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.logs = append(l.logs, r.Level.String()+" "+r.Message)

	return nil
}

func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *logRecorder) WithGroup(string) slog.Handler { return l }

// with returns the records whose message contains substr.
func (l *logRecorder) with(substr string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []string

	for _, e := range l.logs {
		if strings.Contains(e, substr) {
			out = append(out, e)
		}
	}

	return out
}

type streamHarness struct {
	t      *testing.T
	cancel context.CancelFunc
	clock  *systest.Clock
	timers *timerCountingClock
	src    *scriptedSource
	sub    *scriptedSubscriber
	done   chan struct{}

	logs *logRecorder

	mu    sync.Mutex
	fired [][]string
	modes []string
	lost  int
	perrs int
}

func startStream(t *testing.T, opts ldapwatch.Options, steps ...subStep) *streamHarness {
	t.Helper()

	clock := systest.New().Clock
	h := &streamHarness{
		t:      t,
		clock:  clock,
		timers: &timerCountingClock{Clock: clock},
		src:    &scriptedSource{fp: fp("g0", "s0", "d0")},
		sub:    &scriptedSubscriber{steps: steps},
		done:   make(chan struct{}),
		logs:   &logRecorder{},
	}

	opts.Source = h.src
	opts.Subscriber = h.sub
	opts.Clock = h.timers
	opts.Trigger = func(_ context.Context, changed []string) {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.fired = append(h.fired, changed)
	}
	opts.OnMode = func(m string) {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.modes = append(h.modes, m)
	}
	opts.OnStreamLost = func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.lost++
	}
	opts.OnPollError = func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.perrs++
	}

	w := ldapwatch.New(&opts)

	ctx, cancel := context.WithCancel(logger.ContextWithLogger(context.Background(), slog.New(h.logs)))
	h.cancel = cancel

	go func() {
		defer close(h.done)

		w.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-h.done
	})

	return h
}

// notify plays a change notification and waits until the watcher handled it.
func (h *streamHarness) notify() {
	h.t.Helper()

	before := h.timers.timers.Load()

	h.sub.notify()

	require.Eventually(h.t, func() bool { return h.timers.timers.Load() > before }, 5*time.Second, time.Millisecond)
}

func (h *streamHarness) triggers() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([][]string(nil), h.fired...)
}

func (h *streamHarness) modeLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]string(nil), h.modes...)
}

func (h *streamHarness) streamLosses() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.lost
}

// settled waits until the baseline read happened, the stream is live and the
// watcher is parked on its resync timer alone.
func (h *streamHarness) settled(polls, subscribes int) {
	h.t.Helper()

	require.Eventually(h.t, func() bool {
		return h.src.polls() == polls && h.sub.subscribed() == subscribes && h.clock.Waiters() == 1 &&
			len(h.modeLog()) > 0 && h.modeLog()[len(h.modeLog())-1] == ldapwatch.ModeSync
	}, 5*time.Second, time.Millisecond)
}

// waiters waits until n timers are armed.
func (h *streamHarness) waiters(n int) {
	h.t.Helper()

	require.Eventually(h.t, func() bool { return h.clock.Waiters() == n }, 5*time.Second, time.Millisecond)
}

func (h *streamHarness) eventuallyTriggers(n int) {
	h.t.Helper()

	require.Eventually(h.t, func() bool { return len(h.triggers()) == n }, 5*time.Second, time.Millisecond)
}

// Notifications drive the watcher; the interval poll does not run while the
// stream is live. A burst of notifications is one fingerprint read and one
// trigger.
func TestStream_BurstOfNotificationsIsOneReadAndOneTrigger(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce})
	h.settled(1, 1)

	// Poll intervals pass: nothing reads the directory.
	h.clock.Advance(interval)
	h.clock.Advance(interval)
	h.settled(1, 1)

	h.src.set(fp("g1", "s0", "d0"))

	for range 5 {
		h.notify()
		h.clock.Advance(500 * time.Millisecond) // inside the quiet window: it restarts
	}

	h.waiters(2) // the resync timer and the quiet window
	assert.Equal(t, 1, h.src.polls(), "no read before the window is quiet")
	assert.Empty(t, h.triggers())

	h.clock.Advance(debounce)
	h.eventuallyTriggers(1)

	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.triggers())
	assert.Equal(t, 2, h.src.polls(), "one fingerprint read for the whole burst")

	h.settled(2, 1)
	h.clock.Advance(interval)
	h.settled(2, 1)
	assert.Len(t, h.triggers(), 1)
}

// A steady stream of writes cannot postpone the read forever.
func TestStream_ContinuousNotificationsAreReadAtMaxWait(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce, MaxWait: 3 * debounce})
	h.settled(1, 1)

	h.src.set(fp("g1", "s0", "d0"))

	// Notifications every 1.5s never leave a quiet window of 2s.
	for range 3 {
		h.notify()
		h.clock.Advance(1500 * time.Millisecond)
	}

	assert.Empty(t, h.triggers(), "4.5s after the first notification, max wait is 6s")

	h.notify()
	h.clock.Advance(1500 * time.Millisecond)
	h.eventuallyTriggers(1)
}

// A notification for a change the fingerprint does not show (a write to an
// attribute that does not move entryCSN of a watched entry, or an event for an
// entry that was changed back) costs one read and triggers nothing.
func TestStream_NotificationWithoutFingerprintChangeTriggersNothing(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce})
	h.settled(1, 1)

	h.notify()
	h.waiters(2)
	h.clock.Advance(debounce)

	require.Eventually(t, func() bool { return h.src.polls() == 2 }, 5*time.Second, time.Millisecond)
	h.settled(2, 1)
	assert.Empty(t, h.triggers())
}

// The resync read is the safety net for a notification that never arrived.
func TestStream_ResyncFindsAMissedChange(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce, ResyncInterval: time.Minute})
	h.settled(1, 1)

	h.src.set(fp("g0", "s1", "d0"))
	h.clock.Advance(time.Minute - time.Second)
	assert.Empty(t, h.triggers())

	h.clock.Advance(time.Second)
	h.eventuallyTriggers(1)
	assert.Equal(t, [][]string{{ldapwatch.ComponentServers}}, h.triggers())
	h.settled(2, 1)
}

// A lost stream is re-opened with growing back-off, and a change made while it
// was down triggers after the reconnect.
func TestStream_LostStreamReconnectsWithBackoffAndCatchesUp(t *testing.T) {
	boom := errors.New("connection reset")
	drop := make(chan error)
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce},
		liveUntil(drop), liveUntil(drop))
	h.settled(1, 1)

	h.src.set(fp("g1", "s0", "d0")) // changed while the stream is down
	drop <- boom

	// Once the loss is counted the resync timer is stopped, so a single armed
	// timer is the back-off sleep.
	h.lostAndSleeping(1)
	h.clock.Advance(time.Second - time.Millisecond)
	assert.Equal(t, 1, h.sub.subscribed(), "back-off is 1s")
	h.clock.Advance(time.Millisecond)

	h.eventuallyTriggers(1) // the catch-up read of the second stream
	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.triggers())

	drop <- boom // the second stream dies at once: the back-off keeps growing

	h.lostAndSleeping(2)
	h.clock.Advance(2*time.Second - time.Millisecond)
	assert.Equal(t, 2, h.sub.subscribed(), "back-off doubled to 2s")
	h.clock.Advance(time.Millisecond)

	h.settled(3, 3)
	assert.Len(t, h.triggers(), 1, "the change is reported once")
	assert.Equal(t, []string{
		"INFO LDAP change stream lost, reconnecting", // routine once...
		"WARN LDAP change stream lost, reconnecting", // ...a problem when it repeats
	}, h.logs.with("stream lost"))
	assert.Equal(t, []string{
		ldapwatch.ModePoll, ldapwatch.ModeSync, ldapwatch.ModePoll,
		ldapwatch.ModeSync, ldapwatch.ModePoll, ldapwatch.ModeSync,
	}, h.modeLog())
}

// lostAndSleeping waits until n streams have been lost and the watcher is in
// its back-off sleep.
func (h *streamHarness) lostAndSleeping(n int) {
	h.t.Helper()

	require.Eventually(h.t, func() bool { return h.streamLosses() == n && h.clock.Waiters() == 1 },
		5*time.Second, time.Millisecond)
}

// A failing fingerprint read ends the stream like a lost connection: counted,
// backed off, retried.
func TestStream_FailedFingerprintReadReconnects(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce})
	h.settled(1, 1)

	h.src.fail(errors.New("search timed out"))
	h.notify()
	h.waiters(2)
	h.clock.Advance(debounce)

	h.lostAndSleeping(1)

	h.mu.Lock()
	assert.Equal(t, 1, h.perrs, "counted as a failed poll")
	h.mu.Unlock()

	h.src.set(fp("g0", "s0", "d0"))
	h.clock.Advance(time.Second)
	h.settled(3, 2)
	assert.Empty(t, h.triggers(), "nothing changed: no trigger after the reconnect")
}

// A directory without syncprov: the watcher falls back to polling, keeps
// detecting changes, and looks again after the probe interval.
func TestStream_UnsupportedFallsBackToPollingAndProbesAgain(t *testing.T) {
	probe := 10 * interval
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: 0, ProbeInterval: probe},
		unsupported, unsupported)

	// tick moves the clock by one poll interval and waits for the next sleep.
	tick := func() {
		h.clock.Advance(interval)
		h.waiters(1)
	}

	require.Eventually(t, func() bool { return h.sub.subscribed() == 1 && h.src.polls() == 1 && h.clock.Waiters() == 1 },
		5*time.Second, time.Millisecond)
	assert.Equal(t, 0, h.streamLosses(), "unsupported is not a lost stream")
	assert.Equal(t, []string{
		"INFO LDAP content sync is not available, falling back to polling " +
			"(the syncprov overlay is not loaded on this directory)",
	}, h.logs.with("falling back"), "the normal single-node case: one info line, no warning")
	assert.Equal(t, []string{ldapwatch.ModePoll}, h.modeLog())

	h.src.set(fp("g1", "s0", "d0"))
	tick()
	h.eventuallyTriggers(1)

	// The poll that outlasts the probe interval tries the stream again, and
	// the directory still has no syncprov.
	for range 9 {
		tick()
	}

	require.Eventually(t, func() bool { return h.sub.subscribed() == 2 }, 5*time.Second, time.Millisecond)

	// Another probe interval of polling; the next attempt succeeds, so the
	// stream goes live and polling stops.
	for range 10 {
		tick()
	}

	h.settled(h.src.polls(), 3)
	assert.Equal(t, []string{ldapwatch.ModePoll, ldapwatch.ModeSync}, h.modeLog())

	polls := h.src.polls()

	h.clock.Advance(interval)
	h.clock.Advance(interval)
	h.settled(polls, 3)
	assert.Len(t, h.triggers(), 1)
}

// Without a Subscriber the watcher is exactly the poller it always was.
func TestStream_NoSubscriberPolls(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval})

	h.src.set(fp("g1", "s0", "d0"))
	h.advance(interval)

	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.fired())
}

func TestStream_CancelStopsRunAndLeavesNoGoroutine(t *testing.T) {
	h := startStream(t, ldapwatch.Options{Interval: interval, Debounce: debounce})
	h.settled(1, 1)

	h.notify()
	h.waiters(2) // a quiet window is armed as well

	require.True(t, watcherGoroutineRunning(t), "the scan must see the watcher, or it proves nothing")

	h.cancel()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	requireNoWatcherGoroutine(t)
	assert.Zero(t, h.clock.Waiters(), "every timer is stopped")
	assert.Zero(t, h.streamLosses(), "a shutdown is not a lost stream")
}
