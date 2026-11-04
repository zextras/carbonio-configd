// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Tests for the cutover contracts of the daemon cycle: the listener and READY=1
// come only after the first completed config pass (configd.py:253), a handled
// reload is acknowledged with READY=1, a failed rewrite skips the restarts of
// its cycle, and REWRITE clients are released once the loop processed them.
package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/network"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// notifyRecorder is a NOTIFY_SOCKET that records the datagrams it receives.
type notifyRecorder struct {
	conn     *net.UnixConn
	notifier *sdnotify.Notifier
	sentinel atomic.Int64

	mu   sync.Mutex
	msgs []string
}

// newNotifyRecorder binds a datagram socket and returns it with the Notifier
// that talks to it. The socket lives in /tmp: unix socket paths are short.
func newNotifyRecorder(t *testing.T) (*notifyRecorder, *sdnotify.Notifier) {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "notify")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "s")

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	t.Setenv("NOTIFY_SOCKET", path)

	notifier, err := sdnotify.New()
	require.NoError(t, err)

	rec := &notifyRecorder{conn: conn, notifier: notifier}

	go func() {
		buf := make([]byte, 512)

		for {
			n, _, err := conn.ReadFromUnix(buf)
			if err != nil {
				return
			}

			rec.mu.Lock()
			rec.msgs = append(rec.msgs, string(buf[:n]))
			rec.mu.Unlock()
		}
	}()

	return rec, notifier
}

// count returns how many datagrams equal msg. Datagrams on a unix socket are
// delivered in order, so it sends a unique sentinel and waits for it to be
// recorded: everything sent before it has been recorded too.
func (r *notifyRecorder) count(t *testing.T, msg string) int {
	t.Helper()

	sentinel := "TEST_SENTINEL=" + strconv.FormatInt(r.sentinel.Add(1), 10)
	require.NoError(t, r.notifier.Notify(sentinel))
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()

		return slices.Contains(r.msgs, sentinel)
	}, 5*time.Second, time.Millisecond, "sentinel datagram never recorded")

	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for _, m := range r.msgs {
		if m == msg {
			n++
		}
	}

	return n
}

// cycleFixture is a daemon loop wired to fakes: the fake filesystem and command
// runner record what the cycle does, the clock is real (Interval 0 sleeps return
// at once).
type cycleFixture struct {
	deps  *daemonLoopDeps
	fakes *systest.Fakes
	ready *notifyRecorder
}

func newCycleFixture(t *testing.T) *cycleFixture {
	t.Helper()

	ctx := context.Background()
	rec, notifier := newNotifyRecorder(t)
	fakes := systest.New()
	fakes.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, "")) // legacy (non-systemd) restart mode
	env := fakes.System()
	env.Clock = sys.Real().Clock

	appState := state.NewState()
	// Port 0 binds an ephemeral port, keeping the test parallel-safe.
	appState.Publish(snapshot.New(snapshot.Parts{Local: map[string]string{"zmconfigd_listen_port": "0"}}))

	cfg := &config.Config{
		Progname:      "configd-test",
		BaseDir:       "/opt/zextras",
		ConfigFile:    "/opt/zextras/conf/zmconfigd.cf",
		Interval:      0,
		RestartConfig: true,
	}
	ctl := services.NewController(env)
	sm := services.NewServiceManager(ctl)
	cm := configmgr.NewConfigManager(ctx, cfg, appState, nil, cache.New(ctx, true), env, sm)
	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  time.Minute,
		ServiceManager: sm,
		State:          appState,
		Clock:          env.Clock,
		Snapshots:      ctl,
		ConfigLookup:   func(string) string { return "" },
	})

	reloadChan := make(chan struct{}, 1)

	return &cycleFixture{
		fakes: fakes,
		ready: rec,
		deps: &daemonLoopDeps{
			env:            env,
			cfg:            cfg,
			appState:       appState,
			configManager:  cm,
			serviceManager: sm,
			wd:             wd,
			args:           &Args{},
			notifier:       notifier,
			trigger:        &MainLoopActionTrigger{ReloadChan: reloadChan, State: appState},
			reloadChan:     reloadChan,
		},
	}
}

// restartSvc is the service the tests restart: memcached has no ConfigRewrite
// step, so restarting it never reaches out to a real configd on localhost.
const restartSvc = "memcached"

// publishMTA makes restartSvc enabled with one rewrite (source -> target)
// and a restart of it, the rewrite being due for the requested sections.
func (f *cycleFixture) publishMTA(source string) {
	section := &config.MtaConfigSection{
		Name:         restartSvc,
		Depends:      map[string]bool{},
		Rewrites:     map[string]config.RewriteEntry{source: {Value: "conf/out.conf", Mode: "0644"}},
		Restarts:     map[string]bool{restartSvc: true},
		RequiredVars: map[string]string{},
		Postconf:     map[string]string{},
		Postconfd:    map[string]string{},
		Ldap:         map[string]string{},
	}

	f.deps.appState.SetMtaConfig(&config.MtaConfig{Sections: map[string]*config.MtaConfigSection{restartSvc: section}})
	f.deps.appState.UpdateSnapshot(func(s *snapshot.Snapshot) *snapshot.Snapshot {
		return s.WithServer(nil, map[string]string{restartSvc: "zimbraServiceEnabled"})
	})
}

func (f *cycleFixture) restartsRequested() bool {
	return containsRestartMetric(string(f.deps.configManager.Metrics().Render()))
}

func containsRestartMetric(rendered string) bool {
	for line := range strings.SplitSeq(rendered, "\n") {
		if line != "" && line[0] != '#' && strings.Contains(line, "restart") && strings.Contains(line, `service="`+restartSvc+`"`) {
			return true
		}
	}

	return false
}

// runFailingCycle is runCycleOnce for a cycle that is going to back off: a
// token in the reload channel makes the 60s sleep return at once.
func runFailingCycle(t *testing.T, f *cycleFixture, st *loopIterState) iterOutcome {
	t.Helper()

	f.deps.reloadChan <- struct{}{}

	return runCycleOnce(t, f, st)
}

func runCycleOnce(t *testing.T, f *cycleFixture, st *loopIterState) iterOutcome {
	t.Helper()

	return runCycle(context.Background(), f.deps, st, &loopPhaseDurations{t1: time.Now()})
}

// TestRunCycle_ReadyAfterFirstPass: the listener starts and READY=1 is sent
// when the first config pass completed, not before, and once only.
func TestRunCycle_ReadyAfterFirstPass(t *testing.T) {
	f := newCycleFixture(t)
	st := &loopIterState{}

	require.Nil(t, st.server)
	assert.Equal(t, 0, f.ready.count(t, sdnotify.Ready), "READY=1 before any pass")

	outcome := runCycleOnce(t, f, st)
	require.Equal(t, iterContinue, outcome)

	defer st.server.Shutdown(context.Background())

	require.NotNil(t, st.server, "the listener starts after the first completed pass")
	assert.True(t, st.ready)
	assert.Equal(t, 1, f.ready.count(t, sdnotify.Ready))

	// A later pass without reload sends nothing more.
	runCycleOnce(t, f, st)
	assert.Equal(t, 1, f.ready.count(t, sdnotify.Ready))
}

// TestRunCycle_NoReadyWhenFirstPassFails: a first pass whose rewrite or plan
// failed leaves the daemon not ready (no listener, no READY=1), as legacy does
// by sleeping 60s and retrying before it starts the listener.
func TestRunCycle_NoReadyWhenFirstPassFails(t *testing.T) {
	t.Run("rewrite fails", func(t *testing.T) {
		f := newCycleFixture(t)
		f.publishMTA("conf/missing.in")
		f.deps.appState.AddRequestedConfigs(context.Background(), []string{restartSvc})
		st := &loopIterState{}

		runFailingCycle(t, f, st)

		assert.Nil(t, st.server, "no listener after a failed rewrite")
		assert.False(t, st.ready)
		assert.Equal(t, 0, f.ready.count(t, sdnotify.Ready))
		assert.True(t, f.deps.appState.IsFirstRun(), "the retry is still the first pass")
	})

	t.Run("plan fails", func(t *testing.T) {
		f := newCycleFixture(t)
		f.deps.appState.Publish(snapshot.New(snapshot.Parts{
			Local:    map[string]string{"zmconfigd_listen_port": "0"},
			Services: map[string]string{"mta": "TRUE", "proxy": "TRUE"},
		}))
		_, _, result := runConfigPhases(context.Background(), f.deps)
		require.Equal(t, phasesCompleted, result)

		f.deps.appState.Publish(snapshot.New(snapshot.Parts{Local: map[string]string{"zmconfigd_listen_port": "0"}}))
		st := &loopIterState{}

		runFailingCycle(t, f, st)

		assert.Nil(t, st.server)
		assert.Equal(t, 0, f.ready.count(t, sdnotify.Ready))
	})
}

// TestRunCycle_ReloadSendsReadyAgain (GCR-04): SIGHUP makes the signal handler
// send RELOADING=1; the cycle that handles it ends with READY=1, whether it
// succeeded or backed off, so systemd leaves the "reloading" state.
func TestRunCycle_ReloadSendsReadyAgain(t *testing.T) {
	t.Run("successful reload cycle", func(t *testing.T) {
		f := newCycleFixture(t)
		st := &loopIterState{}

		runCycleOnce(t, f, st)

		defer st.server.Shutdown(context.Background())

		require.Equal(t, 1, f.ready.count(t, sdnotify.Ready))

		st.reloadSignaled = true
		runCycleOnce(t, f, st)

		assert.Equal(t, 2, f.ready.count(t, sdnotify.Ready), "READY=1 acknowledges the reload")
		assert.False(t, st.reloadSignaled)
	})

	t.Run("failed reload cycle", func(t *testing.T) {
		f := newCycleFixture(t)
		st := &loopIterState{}

		runCycleOnce(t, f, st)

		defer st.server.Shutdown(context.Background())

		f.publishMTA("conf/missing.in")
		f.deps.appState.AddRequestedConfigs(context.Background(), []string{restartSvc})
		st.reloadSignaled = true

		runFailingCycle(t, f, st)

		assert.Equal(t, 2, f.ready.count(t, sdnotify.Ready), "a failed reload is acknowledged too")
	})
}

// TestRunCycle_RewriteFailureSkipsRestarts (GCR-07): configd.py skips doRestarts
// when doConfigRewrites raised, so a service is not restarted on the old
// configuration. The same cycle restarts the service when the rewrite works.
func TestRunCycle_RewriteFailureSkipsRestarts(t *testing.T) {
	t.Run("failed rewrite", func(t *testing.T) {
		f := newCycleFixture(t)
		f.deps.appState.SetFirstRun(false)
		f.publishMTA("conf/missing.in")
		f.deps.appState.AddRequestedConfigs(context.Background(), []string{restartSvc})

		runFailingCycle(t, f, &loopIterState{})

		assert.False(t, f.restartsRequested(), "restart queued although the rewrite failed")
	})

	t.Run("successful rewrite", func(t *testing.T) {
		f := newCycleFixture(t)
		f.deps.appState.SetFirstRun(false)
		f.fakes.FS.Put("/opt/zextras/conf/in.conf", []byte("x"), 0o644)
		f.publishMTA("conf/in.conf")
		f.deps.appState.AddRequestedConfigs(context.Background(), []string{restartSvc})

		runCycleOnce(t, f, &loopIterState{})

		assert.True(t, f.restartsRequested(), "the control case must queue the restart")
	})
}

// TestBackOffSleepsSixtySeconds pins the legacy 60s sleep after a failed cycle
// and that a reload interrupts it.
func TestBackOffSleepsSixtySeconds(t *testing.T) {
	deps := newLoopFixture(t)
	fake := systest.New()
	deps.env.Clock = fake.Clock
	deps.reloadChan = make(chan struct{}, 1)

	st := &loopIterState{}
	done := make(chan struct{})

	go func() {
		backOff(context.Background(), deps, st)
		close(done)
	}()

	require.Eventually(t, func() bool { return fake.Clock.Waiters() == 1 },
		5*time.Second, time.Millisecond, "60s back-off must be armed on the fake clock")

	fake.Clock.Advance(59 * time.Second)

	select {
	case <-done:
		t.Fatal("returned before the 60s back-off elapsed")
	case <-time.After(50 * time.Millisecond):
	}

	fake.Clock.Advance(time.Second)
	<-done
	assert.False(t, st.reloadSignaled, "an elapsed sleep is not a reload")

	go func() {
		deps.reloadChan <- struct{}{}
	}()

	backOff(context.Background(), deps, st)
	assert.True(t, st.reloadSignaled, "a reload interrupting the sleep is remembered")
}

// TestRewriteWaiters covers the REWRITE client lifecycle on the main loop side:
// released once the cycle's rewrite phase is over, held (and handed to the next
// cycle) when the cycle could not plan, never released by a cycle that started
// before the client arrived.
func TestRewriteWaiters(t *testing.T) {
	released := func(w *network.RewriteWait) bool {
		select {
		case <-w.Done:
			return true
		default:
			return false
		}
	}

	newTrigger := func() *MainLoopActionTrigger {
		return &MainLoopActionTrigger{ReloadChan: make(chan struct{}, 1), State: state.NewState()}
	}

	t.Run("released after the rewrite phase", func(t *testing.T) {
		tr := newTrigger()
		w := tr.TriggerRewrite(context.Background(), []string{"mta"})

		tr.claimWaiters()
		assert.False(t, released(w), "claimed but not yet rewritten")

		tr.completeWaiters()
		assert.True(t, released(w))
		assert.Equal(t, []string{"mta"}, tr.State.TakeRequestedConfigs(), "the loop, not the trigger, takes the sections")
	})

	t.Run("not released by a cycle that started before the request", func(t *testing.T) {
		tr := newTrigger()
		tr.claimWaiters() // the cycle began planning

		w := tr.TriggerRewrite(context.Background(), []string{"mta"})

		tr.completeWaiters()
		assert.False(t, released(w))

		tr.claimWaiters()
		tr.completeWaiters()
		assert.True(t, released(w), "released by the next cycle")
	})

	t.Run("kept for the next cycle when planning failed", func(t *testing.T) {
		tr := newTrigger()
		w := tr.TriggerRewrite(context.Background(), []string{"mta"})

		tr.claimWaiters()
		tr.requeueWaiters()
		assert.False(t, released(w))

		tr.claimWaiters()
		tr.completeWaiters()
		assert.True(t, released(w))
	})

	t.Run("cancel forgets the client and keeps the request queued", func(t *testing.T) {
		tr := newTrigger()
		w := tr.TriggerRewrite(context.Background(), []string{"mta"})
		w.Cancel()

		tr.claimWaiters()
		tr.completeWaiters()
		assert.False(t, released(w))
		assert.Equal(t, []string{"mta"}, tr.State.TakeRequestedConfigs())
	})
}

// TestRewriteOverTCP_ReleasedByCycle: the whole path of a REWRITE request. The
// client is answered only after a cycle processed its sections, with the
// legacy response strings.
func TestRewriteOverTCP_ReleasedByCycle(t *testing.T) {
	f := newCycleFixture(t)
	st := &loopIterState{}

	// Reserve a free port: the server API does not expose its bound address.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	f.deps.appState.Publish(snapshot.New(snapshot.Parts{
		Local: map[string]string{"zmconfigd_listen_port": strconv.Itoa(port)},
	}))

	runCycleOnce(t, f, st)
	require.NotNil(t, st.server)

	t.Cleanup(func() { st.server.Shutdown(context.Background()) })

	// REWRITE only accepts sections of the parsed zmconfigd.cf.
	f.deps.appState.SetMtaConfig(&config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		restartSvc: {Name: restartSvc},
	}})

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	reply := make(chan string, 1)

	go func() {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			reply <- "dial: " + err.Error()

			return
		}
		defer conn.Close()

		_, _ = conn.Write([]byte("REWRITE " + restartSvc + "\n"))
		buf := make([]byte, 128)
		n, _ := conn.Read(buf)
		reply <- string(buf[:n])
	}()

	select {
	case got := <-reply:
		t.Fatalf("reply %q before any cycle processed the request", got)
	case <-time.After(200 * time.Millisecond):
	}

	f.deps.appState.SetFirstRun(false)
	runCycleOnce(t, f, st)

	select {
	case got := <-reply:
		assert.Equal(t, "SUCCESS REWRITES COMPLETE\n", got)
	case <-time.After(5 * time.Second):
		t.Fatal("no reply after the cycle completed")
	}
}

// TestExecuteForced_ExitStatus (D12): a forced run whose plan or rewrite failed
// exits 1 instead of reporting success.
func TestExecuteForced_ExitStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("rewrite fails", func(t *testing.T) {
		f := newCycleFixture(t)
		f.publishMTA("conf/missing.in")
		f.deps.appState.AddRequestedConfigs(ctx, []string{restartSvc})

		assert.Equal(t, 1, executeForced(ctx, f.deps.configManager, "configd-test"))
	})

	t.Run("plan fails", func(t *testing.T) {
		f := newCycleFixture(t)
		f.deps.appState.Publish(snapshot.New(snapshot.Parts{
			Services: map[string]string{"mta": "TRUE", "proxy": "TRUE"},
		}))
		_, _, result := runConfigPhases(ctx, f.deps)
		require.Equal(t, phasesCompleted, result)
		f.deps.appState.Publish(snapshot.New(snapshot.Parts{}))

		assert.Equal(t, 1, executeForced(ctx, f.deps.configManager, "configd-test"))
	})

	t.Run("success", func(t *testing.T) {
		f := newCycleFixture(t)

		assert.Equal(t, 0, executeForced(ctx, f.deps.configManager, "configd-test"))
	})
}
