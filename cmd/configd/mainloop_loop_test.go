// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Tests for the daemon main-loop helpers: iteration control flow, idle
// handling, listener startup, and finalization. The full success path of
// runLoadAndParse requires a live Carbonio environment and is exercised by
// the integration suite; here we pin down the control-flow contracts.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// newLoopFixture builds daemonLoopDeps with an isolated state, a config
// manager without LDAP, and a zero interval so sleeps return immediately.
func newLoopFixture(t *testing.T) *daemonLoopDeps {
	t.Helper()

	ctx := context.Background()
	appState := state.NewState()
	cfg := &config.Config{
		Progname:   "configd-test",
		ConfigFile: t.TempDir() + "/missing-zmconfigd.cf",
		Interval:   0,
	}
	cm := configmgr.NewConfigManager(ctx, cfg, appState, nil, cache.New(ctx, true))
	sm := services.NewServiceManager()
	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  time.Minute,
		ServiceManager: sm,
		State:          appState,
		ConfigLookup:   func(string) string { return "" },
	})
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	reloadChan := make(chan struct{}, 1)

	return &daemonLoopDeps{
		cfg:            cfg,
		appState:       appState,
		configManager:  cm,
		serviceManager: sm,
		wd:             wd,
		args:           &Args{},
		notifier:       notifier,
		trigger: &MainLoopActionTrigger{
			ReloadChan: reloadChan,
			State:      appState,
		},
		reloadChan: reloadChan,
	}
}

func TestIsIdlePoll(t *testing.T) {
	trigger := &MainLoopActionTrigger{}
	trigger.EventCounter.Store(5)

	tests := []struct {
		name           string
		skipIdlePolls  bool
		firstRun       bool
		reloadSignaled bool
		lastEventCount int64
		want           bool
	}{
		{"idle when everything quiet", true, false, false, 5, true},
		{"never on first run", true, true, false, 5, false},
		{"never when reload signaled", true, false, true, 5, false},
		{"never when events arrived", true, false, false, 4, false},
		{"never when feature disabled", false, false, false, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{SkipIdlePolls: tt.skipIdlePolls}
			appState := state.NewState()
			appState.FirstRun = tt.firstRun

			got := isIdlePoll(cfg, appState, trigger, tt.lastEventCount, tt.reloadSignaled)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCtxDone(t *testing.T) {
	assert.False(t, ctxDone(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.True(t, ctxDone(ctx))
}

func TestConsumeReloadSignal(t *testing.T) {
	t.Run("default path leaves state untouched", func(t *testing.T) {
		st := &loopIterState{}
		outcome := consumeReloadSignal(context.Background(), make(chan struct{}, 1), st)
		assert.Equal(t, iterContinue, outcome)
		assert.False(t, st.reloadSignaled)
	})

	t.Run("reload token sets reloadSignaled", func(t *testing.T) {
		st := &loopIterState{}
		ch := make(chan struct{}, 1)
		ch <- struct{}{}

		outcome := consumeReloadSignal(context.Background(), ch, st)
		assert.Equal(t, iterContinue, outcome)
		assert.True(t, st.reloadSignaled)
	})

	t.Run("cancelled context exits", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		st := &loopIterState{}
		outcome := consumeReloadSignal(ctx, make(chan struct{}, 1), st)
		assert.Equal(t, iterExit, outcome)
	})
}

func TestHandleIdleSleep(t *testing.T) {
	t.Run("zero interval returns without reload", func(t *testing.T) {
		deps := newLoopFixture(t)
		st := &loopIterState{}

		handleIdleSleep(context.Background(), deps, st)
		assert.False(t, st.reloadSignaled)
	})

	t.Run("reload interrupt flags state", func(t *testing.T) {
		deps := newLoopFixture(t)
		deps.cfg.Interval = 60
		deps.reloadChan <- struct{}{}

		st := &loopIterState{}
		done := make(chan struct{})

		go func() {
			handleIdleSleep(context.Background(), deps, st)
			close(done)
		}()

		select {
		case <-done:
			assert.True(t, st.reloadSignaled)
		case <-time.After(5 * time.Second):
			t.Fatal("handleIdleSleep did not return after reload signal")
		}
	})
}

func TestRunRestartsPhase(t *testing.T) {
	deps := newLoopFixture(t)
	ctx := context.Background()

	t.Run("disabled returns zero", func(t *testing.T) {
		deps.cfg.RestartConfig = false
		assert.Equal(t, time.Duration(0), runRestartsPhase(ctx, deps.cfg, deps.configManager))
	})

	t.Run("enabled runs DoRestarts on empty state", func(t *testing.T) {
		deps.cfg.RestartConfig = true
		// No pending restarts: must complete quickly and return elapsed time.
		dur := runRestartsPhase(ctx, deps.cfg, deps.configManager)
		assert.GreaterOrEqual(t, dur, time.Duration(0))
	})
}

func TestNotifyReady_NotifierDisabled(t *testing.T) {
	// Without NOTIFY_SOCKET the notifier is disabled and Ready() is a no-op.
	notifier, err := sdnotify.New()
	require.NoError(t, err)
	notifyReady(context.Background(), notifier, 0)
	notifyReady(context.Background(), notifier, 1)
}

func TestRunLoadAndParse_LoadFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr load retries take ~5s")
	}

	deps := newLoopFixture(t)

	loadDur, parseDur, err := runLoadAndParse(context.Background(), deps.configManager, deps.cfg)
	require.Error(t, err, "load must fail without a Carbonio environment")
	assert.GreaterOrEqual(t, loadDur, time.Duration(0))
	assert.Equal(t, time.Duration(0), parseDur)
}

func TestMaybeStartListener_SkipConditions(t *testing.T) {
	ctx := context.Background()
	trigger := &MainLoopActionTrigger{State: state.NewState()}

	t.Run("not first run", func(t *testing.T) {
		appState := state.NewState()
		appState.FirstRun = false
		assert.Nil(t, maybeStartListener(ctx, appState, &Args{}, trigger, nil))
	})

	t.Run("forced mode", func(t *testing.T) {
		appState := state.NewState()
		appState.Forced = 1
		assert.Nil(t, maybeStartListener(ctx, appState, &Args{}, trigger, nil))
	})

	t.Run("once mode", func(t *testing.T) {
		appState := state.NewState()
		assert.Nil(t, maybeStartListener(ctx, appState, &Args{Once: true}, trigger, nil))
	})
}

func TestMaybeStartListener_StartsServer(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	// Port 0 binds an ephemeral port, keeping the test parallel-safe.
	appState.LocalConfig.Data.Set("zmconfigd_listen_port", "0")

	trigger := &MainLoopActionTrigger{ReloadChan: make(chan struct{}, 1), State: appState}

	srv := maybeStartListener(ctx, appState, &Args{}, trigger, nil)
	require.NotNil(t, srv)

	defer srv.Shutdown(ctx)

	// A second call must return the existing server untouched.
	assert.Same(t, srv, maybeStartListener(ctx, appState, &Args{}, trigger, srv))
}

func TestRunConfigPhases_EmptyState(t *testing.T) {
	deps := newLoopFixture(t)
	// Arm the reload channel so a CompareKeys back-off sleep cannot hang the test.
	deps.reloadChan <- struct{}{}

	timings, skipIter := runConfigPhases(
		context.Background(), deps.cfg, deps.appState, deps.configManager,
		deps.serviceManager, deps.wd, deps.reloadChan,
	)
	assert.False(t, skipIter, "empty state must not trigger the back-off path")
	assert.GreaterOrEqual(t, timings.compareKeys, time.Duration(0))
}

func TestFinalizeIteration(t *testing.T) {
	t.Run("once mode exits", func(t *testing.T) {
		deps := newLoopFixture(t)
		deps.args.Once = true
		st := &loopIterState{}

		outcome := finalizeIteration(context.Background(), deps, st, &loopPhaseDurations{t1: time.Now()})
		assert.Equal(t, iterExit, outcome)
		assert.Equal(t, 1, st.loopCount)
		assert.False(t, deps.appState.FirstRun, "first-run flag must be cleared")
	})

	t.Run("daemon mode continues after interval sleep", func(t *testing.T) {
		deps := newLoopFixture(t)
		st := &loopIterState{reloadSignaled: true}

		outcome := finalizeIteration(context.Background(), deps, st, &loopPhaseDurations{t1: time.Now()})
		assert.Equal(t, iterContinue, outcome)
		assert.False(t, st.reloadSignaled, "reload flag must be reset")
	})

	t.Run("reload during interval sleep re-flags state", func(t *testing.T) {
		deps := newLoopFixture(t)
		deps.cfg.Interval = 60
		deps.reloadChan <- struct{}{}

		st := &loopIterState{}
		outcome := finalizeIteration(context.Background(), deps, st, &loopPhaseDurations{t1: time.Now()})
		assert.Equal(t, iterContinue, outcome)
		assert.True(t, st.reloadSignaled)
	})
}

func TestRunLoopIteration_IdlePath(t *testing.T) {
	deps := newLoopFixture(t)
	deps.cfg.SkipIdlePolls = true
	deps.appState.SetFirstRun(false)

	st := &loopIterState{lastEventCount: deps.trigger.EventCounter.Load()}

	outcome := runLoopIteration(context.Background(), deps, st)
	assert.Equal(t, iterContinue, outcome)
}

func TestRunLoopIteration_LoadErrorBacksOff(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr load retries take ~5s")
	}

	deps := newLoopFixture(t)

	// consumeReloadSignal drains one token at iteration start, so a single
	// pre-armed token never reaches the 60s back-off sleep. Feed the channel
	// continuously until the iteration returns.
	stop := make(chan struct{})
	defer close(stop)

	go func() {
		for {
			select {
			case deps.reloadChan <- struct{}{}:
			case <-stop:
				return
			}
		}
	}()

	st := &loopIterState{}
	outcome := runLoopIteration(context.Background(), deps, st)
	assert.Equal(t, iterContinue, outcome)
}

func TestRunLoopIteration_CancelledContextExits(t *testing.T) {
	deps := newLoopFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := runLoopIteration(ctx, deps, &loopIterState{})
	assert.Equal(t, iterExit, outcome)
}

func TestRunLoopIterationSafe_RecoverFromPanic(t *testing.T) {
	// nil deps panics inside the iteration; the safe wrapper must absorb it.
	outcome := runLoopIterationSafe(context.Background(), nil, &loopIterState{})
	assert.Equal(t, iterContinue, outcome, "recovered panic falls through to zero value")
}

func TestRunDaemonLoop_ExitsOnCancelledContext(t *testing.T) {
	deps := newLoopFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})

	go func() {
		runDaemonLoop(ctx, deps)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemonLoop did not exit on cancelled context")
	}
}

func TestRunMainLoop_CancelledContextReturnsZero(t *testing.T) {
	appState := state.NewState()
	cfg := &config.Config{
		Progname:   "configd-test",
		ConfigFile: t.TempDir() + "/missing-zmconfigd.cf",
		Interval:   0,
	}
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := RunMainLoop(ctx, cfg, appState, nil, &Args{Once: true}, notifier)
	assert.Equal(t, 0, code)
}
