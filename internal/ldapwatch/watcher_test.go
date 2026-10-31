// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch_test

import (
	"context"
	"errors"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldapwatch"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	interval = 5 * time.Second
	debounce = 2 * time.Second
)

// scriptedSource returns whatever fingerprint or error the test last set.
type scriptedSource struct {
	mu    sync.Mutex
	fp    ldapwatch.Fingerprint
	err   error
	calls int
}

func (s *scriptedSource) Fingerprint(ctx context.Context) (ldapwatch.Fingerprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	if s.err != nil {
		return nil, s.err
	}

	return s.fp, nil
}

func (s *scriptedSource) set(fp ldapwatch.Fingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fp, s.err = fp, nil
}

func (s *scriptedSource) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.err = err
}

func (s *scriptedSource) polls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// blockingSource blocks in Fingerprint until its context is done.
type blockingSource struct{ entered chan struct{} }

func (b *blockingSource) Fingerprint(ctx context.Context) (ldapwatch.Fingerprint, error) {
	b.entered <- struct{}{}

	<-ctx.Done()

	return nil, ctx.Err()
}

type harness struct {
	t      *testing.T
	clock  *systest.Clock
	src    *scriptedSource
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	triggers [][]string
}

func fp(global, servers, domains string) ldapwatch.Fingerprint {
	return ldapwatch.Fingerprint{
		ldapwatch.ComponentGlobal:  global,
		ldapwatch.ComponentServers: servers,
		ldapwatch.ComponentDomains: domains,
	}
}

// start runs a watcher on a manual clock and waits until it has taken the
// baseline poll and parked on the clock.
func start(t *testing.T, opts ldapwatch.Options) *harness {
	t.Helper()

	h := &harness{t: t, clock: systest.New().Clock, src: &scriptedSource{fp: fp("g0", "s0", "d0")}, done: make(chan struct{})}

	opts.Source = h.src
	opts.Clock = h.clock
	opts.Trigger = func(_ context.Context, changed []string) {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.triggers = append(h.triggers, changed)
	}

	w := ldapwatch.New(&opts)

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() {
		defer close(h.done)

		w.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-h.done
	})

	h.parked()

	return h
}

// parked waits until Run is waiting on the clock, i.e. it has finished
// everything it does for the current instant.
func (h *harness) parked() {
	h.t.Helper()

	require.Eventually(h.t, func() bool { return h.clock.Waiters() == 1 }, 5*time.Second, time.Millisecond)
}

// advance moves the clock and waits for Run to park again.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()

	h.clock.Advance(d)
	h.parked()
}

func (h *harness) fired() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([][]string(nil), h.triggers...)
}

func TestBaselineDoesNotTrigger(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, Debounce: debounce})

	assert.Equal(t, 1, h.src.polls(), "the baseline is polled once at start")

	h.advance(interval)
	h.advance(interval)

	assert.Equal(t, 3, h.src.polls())
	assert.Empty(t, h.fired(), "an unchanged directory never triggers")
}

func TestChangeTriggersOnceAfterQuietWindow(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, Debounce: debounce})

	h.src.set(fp("g1", "s0", "d0"))
	h.advance(interval) // the poll sees the change

	assert.Empty(t, h.fired(), "the change must first survive the quiet window")

	h.advance(debounce - time.Millisecond)
	assert.Empty(t, h.fired(), "one millisecond short of the window")

	h.advance(time.Millisecond)
	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.fired())

	h.advance(interval)
	assert.Len(t, h.fired(), 1, "the baseline moved on, no further trigger")
}

func TestBurstIsCoalescedIntoOneTrigger(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, Debounce: debounce})

	h.src.set(fp("g1", "s0", "d0"))
	h.advance(interval)
	h.src.set(fp("g1", "s1", "d0"))
	h.advance(debounce) // second change restarts the quiet window
	h.src.set(fp("g1", "s1", "d1"))
	h.advance(debounce) // third change restarts it again

	assert.Empty(t, h.fired(), "a burst keeps postponing the trigger")

	h.advance(debounce)

	assert.Equal(t,
		[][]string{{ldapwatch.ComponentDomains, ldapwatch.ComponentGlobal, ldapwatch.ComponentServers}},
		h.fired(), "three changes, one trigger naming every changed component")
}

func TestContinuousChangesTriggerAtMaxWait(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, Debounce: debounce, MaxWait: 6 * time.Second})

	h.src.set(fp("g1", "s0", "d0")) // seen at t=5s, burst starts
	h.advance(interval)
	h.src.set(fp("g2", "s0", "d0")) // t=7s
	h.advance(debounce)
	h.src.set(fp("g3", "s0", "d0")) // t=9s
	h.advance(debounce)

	assert.Empty(t, h.fired())

	h.src.set(fp("g4", "s0", "d0")) // t=11s: 6s since the burst began
	h.advance(debounce)

	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.fired(),
		"a stream of writes must not postpone the trigger forever")
}

func TestZeroDebounceTriggersInThePollThatSeesTheChange(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval})

	h.src.set(fp("g0", "s1", "d0"))
	h.advance(interval)

	assert.Equal(t, [][]string{{ldapwatch.ComponentServers}}, h.fired())
}

func TestRemovedAndAddedComponentsCountAsChanges(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval})

	h.src.set(ldapwatch.Fingerprint{ldapwatch.ComponentGlobal: "g0", ldapwatch.ComponentServers: "s0"})
	h.advance(interval)
	h.src.set(fp("g0", "s0", "d0"))
	h.advance(interval)

	assert.Equal(t, [][]string{{ldapwatch.ComponentDomains}, {ldapwatch.ComponentDomains}}, h.fired())
}

func TestFailedPollsBackOffExponentiallyUpToTheCap(t *testing.T) {
	h := start(t, ldapwatch.Options{
		Interval: interval, BackoffMin: time.Second, BackoffMax: 4 * time.Second,
	})

	h.src.fail(errors.New("ldap down"))
	h.advance(interval) // poll 2 fails, next try in 1s
	require.Equal(t, 2, h.src.polls())

	h.advance(999 * time.Millisecond)
	require.Equal(t, 2, h.src.polls(), "still backing off")

	h.advance(time.Millisecond) // poll 3 fails, next try in 2s
	require.Equal(t, 3, h.src.polls())

	h.advance(2 * time.Second) // poll 4 fails, next try in 4s
	require.Equal(t, 4, h.src.polls())

	h.advance(4 * time.Second) // poll 5 fails, capped at 4s
	require.Equal(t, 5, h.src.polls())

	h.advance(4 * time.Second)
	require.Equal(t, 6, h.src.polls(), "the back-off stays at its cap")
	assert.Empty(t, h.fired(), "failures never trigger")
}

func TestRecoveryResetsBackoffAndDoesNotTriggerWhenNothingChanged(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, BackoffMin: time.Second, BackoffMax: time.Minute})

	h.src.fail(errors.New("ldap down"))
	h.advance(interval)
	h.advance(time.Second) // second failure, next in 2s

	h.src.set(fp("g0", "s0", "d0")) // back, same state
	h.advance(2 * time.Second)      // recovered: unchanged, regular interval again

	polls := h.src.polls()

	h.advance(interval)
	assert.Equal(t, polls+1, h.src.polls(), "polling is back to the regular interval")
	assert.Empty(t, h.fired())

	h.src.fail(errors.New("ldap down again"))
	h.advance(interval)

	polls = h.src.polls()

	h.advance(time.Second)
	assert.Equal(t, polls+1, h.src.polls(), "a new outage starts the back-off from the minimum again")
}

func TestChangeDuringOutageTriggersAfterReconnect(t *testing.T) {
	h := start(t, ldapwatch.Options{
		Interval: interval, Debounce: debounce, BackoffMin: time.Second, BackoffMax: time.Minute,
	})

	h.src.fail(errors.New("ldap down"))
	h.advance(interval)

	h.src.set(fp("g0", "s9", "d0")) // the directory changed while we could not look
	h.advance(time.Second)          // reconnects and sees the change

	assert.Empty(t, h.fired(), "debounced like any other change")

	h.advance(debounce)
	assert.Equal(t, [][]string{{ldapwatch.ComponentServers}}, h.fired())
}

func TestFailureWhileChangePendingKeepsIt(t *testing.T) {
	h := start(t, ldapwatch.Options{
		Interval: interval, Debounce: debounce, BackoffMin: time.Second, BackoffMax: time.Minute,
	})

	h.src.set(fp("g1", "s0", "d0"))
	h.advance(interval)

	h.src.fail(errors.New("ldap down"))
	h.advance(debounce)
	assert.Empty(t, h.fired(), "a failed poll cannot confirm the quiet window")

	h.src.set(fp("g1", "s0", "d0"))
	h.advance(time.Second)
	assert.Equal(t, [][]string{{ldapwatch.ComponentGlobal}}, h.fired())
}

func TestCancelStopsRunPromptlyAndLeavesNoGoroutine(t *testing.T) {
	h := start(t, ldapwatch.Options{Interval: interval, Debounce: debounce})

	require.True(t, watcherGoroutineRunning(t), "the scan must see a live watcher, or it proves nothing")

	h.cancel()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	requireNoWatcherGoroutine(t)
}

func TestCancelInterruptsABlockedPoll(t *testing.T) {
	clock := systest.New().Clock
	src := &blockingSource{entered: make(chan struct{}, 1)}
	w := ldapwatch.New(&ldapwatch.Options{
		Source: src, Clock: clock, Trigger: func(context.Context, []string) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		w.Run(ctx)
	}()

	<-src.entered
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return while the poll was blocked")
	}

	requireNoWatcherGoroutine(t)
}

func TestNewRequiresItsDependencies(t *testing.T) {
	assert.Panics(t, func() { ldapwatch.New(&ldapwatch.Options{}) })
}

// watcherGoroutineRunning reports whether a goroutine of this package is
// alive, which is how a leaked watcher would show up in a goroutine dump.
func watcherGoroutineRunning(t *testing.T) bool {
	t.Helper()

	var sb strings.Builder

	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&sb, 1))

	// "ldapwatch." (package dot) matches the package's own frames, Watcher and
	// LDAPSource alike, but not this test package ("ldapwatch_test.").
	return strings.Contains(sb.String(), "internal/ldapwatch.")
}

// requireNoWatcherGoroutine fails when a watcher goroutine outlives Run.
func requireNoWatcherGoroutine(t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool { return !watcherGoroutineRunning(t) },
		5*time.Second, 5*time.Millisecond, "a watcher goroutine is still running")
}
