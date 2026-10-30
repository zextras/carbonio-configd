// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Tests for the opt-in LDAP change trigger glue: it is off by default (no
// goroutine at all), starts and stops with zmconfigd_ldap_change_trigger, and
// wakes the main loop through the same reload channel SIGHUP uses.
package main

import (
	"context"
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

// watchSource is a ldapwatch.Source whose fingerprint the test controls.
type watchSource struct {
	mu    sync.Mutex
	fp    ldapwatch.Fingerprint
	calls int
}

func (s *watchSource) Fingerprint(context.Context) (ldapwatch.Fingerprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return s.fp, nil
}

func (s *watchSource) set(fp ldapwatch.Fingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fp = fp
}

func (s *watchSource) polls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// newWatchFixture is the loop fixture on a manual clock with an injected
// source and the watcher key as configuration says (off unless the test sets
// it), with 5s polls and no debounce.
func newWatchFixture(t *testing.T) (*daemonLoopDeps, *systest.Clock, *watchSource) {
	t.Helper()

	deps := newLoopFixture(t)
	fakes := systest.New()
	src := &watchSource{fp: ldapwatch.Fingerprint{ldapwatch.ComponentGlobal: "g0"}}

	deps.env = fakes.System()
	deps.ldapSource = src
	deps.cfg.LdapChangePollSeconds = 5
	deps.cfg.LdapChangeDebounceSeconds = 0

	return deps, fakes.Clock, src
}

// watcherGoroutines reports whether any goroutine runs LDAP watcher code:
// the watcher itself, its source, or the glue that owns them.
func watcherGoroutines(t *testing.T) bool {
	t.Helper()

	var sb strings.Builder

	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&sb, 1))

	dump := sb.String()

	return strings.Contains(dump, "internal/ldapwatch.") || strings.Contains(dump, "main.startLDAPWatch")
}

func requireNoWatcherGoroutines(t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool { return !watcherGoroutines(t) },
		5*time.Second, 5*time.Millisecond, "an LDAP watcher goroutine is running")
}

func waitForWaiters(t *testing.T, clock *systest.Clock, n int) {
	t.Helper()

	require.Eventually(t, func() bool { return clock.Waiters() == n }, 5*time.Second, time.Millisecond)
}

// The default: with zmconfigd_ldap_change_trigger unset or FALSE nothing is
// started, whatever the iteration count.
func TestSyncLDAPWatch_OffStartsNoWatcherAtAll(t *testing.T) {
	deps, clock, src := newWatchFixture(t)
	ctx := context.Background()

	require.False(t, deps.cfg.LdapChangeTrigger, "the key is off unless localconfig turns it on")
	requireNoWatcherGoroutines(t) // let goroutines of earlier tests finish exiting

	var w *ldapWatch
	for range 3 {
		w = syncLDAPWatch(ctx, deps, w)
	}

	assert.Nil(t, w, "no handle: nothing was started")
	assert.Zero(t, clock.Waiters(), "nothing is waiting on the clock")
	assert.Zero(t, src.polls(), "the directory is never polled")
	assert.False(t, watcherGoroutines(t), "no watcher goroutine exists")
}

func TestSyncLDAPWatch_SingleRunNeverStartsAWatcher(t *testing.T) {
	deps, _, src := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true
	deps.args.Once = true
	requireNoWatcherGoroutines(t)

	assert.Nil(t, syncLDAPWatch(context.Background(), deps, nil))
	assert.Zero(t, src.polls())
	assert.False(t, watcherGoroutines(t))
}

func TestSyncLDAPWatch_WithoutLDAPClientStartsNothingAndWarnsOnce(t *testing.T) {
	deps, clock, _ := newWatchFixture(t)
	deps.ldapSource = nil // the fixture's config manager has no native LDAP client
	deps.cfg.LdapChangeTrigger = true
	requireNoWatcherGoroutines(t)

	w := syncLDAPWatch(context.Background(), deps, nil)
	require.NotNil(t, w, "a never-started handle is returned so the warning is not repeated every cycle")
	assert.Same(t, w, syncLDAPWatch(context.Background(), deps, w))
	assert.Zero(t, clock.Waiters())
	assert.False(t, watcherGoroutines(t))

	w.stop() // must be a no-op
}

func TestSyncLDAPWatch_FollowsTheKey(t *testing.T) {
	deps, clock, src := newWatchFixture(t)
	ctx := context.Background()

	deps.cfg.LdapChangeTrigger = true
	w := syncLDAPWatch(ctx, deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)

	waitForWaiters(t, clock, 1) // baseline taken, parked on the injected clock
	assert.Equal(t, 1, src.polls())
	assert.True(t, watcherGoroutines(t))
	assert.Same(t, w, syncLDAPWatch(ctx, deps, w), "a running watcher is not restarted every cycle")

	deps.cfg.LdapChangeTrigger = false
	assert.Nil(t, syncLDAPWatch(ctx, deps, w), "turning the key off stops the watcher")
	requireNoWatcherGoroutines(t)
	assert.Zero(t, clock.Waiters())

	// And on again: a fresh watcher with a fresh baseline.
	deps.cfg.LdapChangeTrigger = true
	w = syncLDAPWatch(ctx, deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)
	waitForWaiters(t, clock, 1)
	assert.Equal(t, 2, src.polls())
}

func TestSyncLDAPWatch_NoChangeNoReload(t *testing.T) {
	deps, clock, _ := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true

	w := syncLDAPWatch(context.Background(), deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)

	waitForWaiters(t, clock, 1)

	for range 3 {
		clock.Advance(5 * time.Second)
		waitForWaiters(t, clock, 1)
	}

	assert.Empty(t, deps.reloadChan, "an unchanged directory never wakes the main loop")
}

// A change wakes a main loop that is sleeping out its 300s interval, using the
// very channel SIGHUP and REWRITE use, long before the interval elapses.
func TestLDAPChangeWakesTheSleepingMainLoop(t *testing.T) {
	deps, clock, src := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true
	ctx := context.Background()

	w := syncLDAPWatch(ctx, deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)

	waitForWaiters(t, clock, 1)

	slept := make(chan bool, 1)

	go func() { slept <- SleepWithContext(ctx, deps.env.Clock, 300*time.Second, deps.reloadChan) }()

	waitForWaiters(t, clock, 2) // the watcher's poll wait and the loop's interval timer

	src.set(ldapwatch.Fingerprint{ldapwatch.ComponentGlobal: "g1"})
	clock.Advance(5 * time.Second)

	select {
	case interrupted := <-slept:
		assert.True(t, interrupted, "the sleep was cut short by the change, not by the interval")
	case <-time.After(5 * time.Second):
		t.Fatal("an LDAP change did not wake the sleeping main loop")
	}
}

// The token an LDAP change leaves is consumed like a SIGHUP one: the next
// iteration sees reloadSignaled, so it is never skipped as an idle poll and
// runs the full cycle.
func TestLDAPChangeRunsAFullCycle(t *testing.T) {
	deps, clock, src := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true

	w := syncLDAPWatch(context.Background(), deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)

	waitForWaiters(t, clock, 1)
	src.set(ldapwatch.Fingerprint{ldapwatch.ComponentServers: "s1", ldapwatch.ComponentGlobal: "g0"})
	clock.Advance(5 * time.Second)
	waitForWaiters(t, clock, 1)

	require.Len(t, deps.reloadChan, 1, "one token for the change")

	before := deps.trigger.EventCounter.Load()
	st := &loopIterState{}

	assert.Equal(t, iterContinue, consumeReloadSignal(context.Background(), deps.reloadChan, st))
	assert.True(t, st.reloadSignaled)
	assert.Equal(t, before, deps.trigger.EventCounter.Load(), "no requested sections: a plain cycle")

	deps.cfg.SkipIdlePolls = true
	deps.appState.FirstRun = false
	assert.False(t, isIdlePoll(deps.cfg, deps.appState, deps.trigger, before, st.reloadSignaled),
		"even with zmconfigd_skip_idle_polls the cycle runs")
}

// A burst of writes leaves at most one token: the channel holds one and the
// watcher coalesces what it sees into one trigger anyway.
func TestLDAPChangeBurstLeavesOneToken(t *testing.T) {
	deps, clock, src := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true
	deps.cfg.LdapChangeDebounceSeconds = 2

	w := syncLDAPWatch(context.Background(), deps, nil)
	require.NotNil(t, w)
	t.Cleanup(w.stop)

	waitForWaiters(t, clock, 1)
	src.set(ldapwatch.Fingerprint{ldapwatch.ComponentGlobal: "g1"})
	clock.Advance(5 * time.Second)
	waitForWaiters(t, clock, 1)
	src.set(ldapwatch.Fingerprint{ldapwatch.ComponentGlobal: "g2"})
	clock.Advance(2 * time.Second)
	waitForWaiters(t, clock, 1)

	assert.Empty(t, deps.reloadChan, "still inside the quiet window")

	clock.Advance(2 * time.Second)
	waitForWaiters(t, clock, 1)

	assert.Len(t, deps.reloadChan, 1)
}

func TestLDAPWatchStopsWhenTheDaemonContextIsCancelled(t *testing.T) {
	deps, clock, _ := newWatchFixture(t)
	deps.cfg.LdapChangeTrigger = true

	ctx, cancel := context.WithCancel(context.Background())

	w := syncLDAPWatch(ctx, deps, nil)
	require.NotNil(t, w)

	waitForWaiters(t, clock, 1)
	require.True(t, watcherGoroutines(t), "the scan must see a live watcher, or it proves nothing")

	cancel()

	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher outlived the daemon context")
	}

	requireNoWatcherGoroutines(t)
	w.stop() // joining an already stopped watcher returns at once
}

func TestMainLoopActionTrigger_TriggerReloadDoesNotBlock(t *testing.T) {
	tr := &MainLoopActionTrigger{ReloadChan: make(chan struct{}, 1)}

	tr.TriggerReload(context.Background(), "first")
	tr.TriggerReload(context.Background(), "second, channel full")

	assert.Len(t, tr.ReloadChan, 1)
}
