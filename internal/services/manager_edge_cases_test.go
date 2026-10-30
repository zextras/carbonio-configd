// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// ============================================================
// manager.go — addRestartLogged
// ============================================================

// TestAddRestartLogged_Deduplication verifies calling twice deduplicates.
func TestAddRestartLogged_Deduplication(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager(ctl)
	sm.addRestartLogged(context.Background(), "mta", "r", "mta-label")
	sm.addRestartLogged(context.Background(), "mta", "r", "mta-label")
	if len(sm.RestartQueue) != 1 {
		t.Errorf("expected 1 entry after duplicate addRestartLogged, got %d", len(sm.RestartQueue))
	}
}

// TestAddRestartLogged_MultipleDifferentServices verifies multiple services are queued.
func TestAddRestartLogged_MultipleDifferentServices(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager(ctl)
	sm.addRestartLogged(context.Background(), "mta", "r", "mta")
	sm.addRestartLogged(context.Background(), "proxy", "r", "proxy")
	sm.addRestartLogged(context.Background(), "mailbox", "r", "mailbox")

	if len(sm.RestartQueue) != 3 {
		t.Errorf("expected 3 queued services, got %d", len(sm.RestartQueue))
	}
}

// TestAddRestartLogged_LabelDiffersFromService verifies addRestartLogged
// accepts a logLabel different from the service name.
func TestAddRestartLogged_LabelDiffersFromService(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager(ctl)
	sm.addRestartLogged(context.Background(), "mta", "r", "mta-display-label")

	if !sm.RestartQueue["mta"] {
		t.Error("expected 'mta' queued when logLabel differs from service name")
	}
}

// ============================================================
// manager.go — attemptServiceRestart
// ============================================================

// TestAttemptServiceRestart_SuccessWithDependencies verifies that on success the service
// is removed from the queue and AddDependencyRestarts is called.
func TestAttemptServiceRestart_SuccessWithDependencies(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	ctl.registry["testservice"] = &ServiceDef{
		Name:        "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return nil },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(ctl.registry, "testservice")

	sm := NewServiceManager(ctl)
	sm.RestartQueue["testservice"] = true

	sm.SetDependencies(context.Background(), map[string][]string{
		"testservice": {"amavis"},
	})

	failedRestarts := make(map[string]int)
	result := sm.attemptServiceRestart(
		context.Background(), "testservice", failedRestarts,
		func(key string) string { return "enabled" },
	)

	if !result {
		t.Error("expected attemptServiceRestart to return true on success")
	}
	if sm.RestartQueue["testservice"] {
		t.Error("expected 'testservice' to be removed from queue on success")
	}
	if !sm.RestartQueue["amavis"] {
		t.Error("expected 'amavis' to be queued as dependency after successful restart")
	}
}

// TestAttemptServiceRestart_SuccessNilConfigLookup verifies success path without configLookup.
func TestAttemptServiceRestart_SuccessNilConfigLookup(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	ctl.registry["testservice"] = &ServiceDef{
		Name:        "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return nil },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(ctl.registry, "testservice")

	sm := NewServiceManager(ctl)
	sm.RestartQueue["testservice"] = true

	failedRestarts := make(map[string]int)
	result := sm.attemptServiceRestart(context.Background(), "testservice", failedRestarts, nil)

	if !result {
		t.Error("expected true on success")
	}
	if sm.RestartQueue["testservice"] {
		t.Error("expected service removed from queue")
	}
}

// TestAttemptServiceRestart_FailBelowMax verifies partial failure (< MaxFailedRestarts)
// returns false and keeps the service in the queue.
func TestAttemptServiceRestart_FailBelowMax(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	ctl.registry["testservice"] = &ServiceDef{
		Name:        "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return errors.New("start failed intentionally") },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(ctl.registry, "testservice")

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 3
	sm.RestartQueue["testservice"] = true

	failedRestarts := make(map[string]int)
	result := sm.attemptServiceRestart(context.Background(), "testservice", failedRestarts, nil)

	if result {
		t.Error("expected false when failure count < MaxFailedRestarts")
	}
	if !sm.RestartQueue["testservice"] {
		t.Error("expected service to remain in queue when failure count < MaxFailedRestarts")
	}
	if failedRestarts["testservice"] != 1 {
		t.Errorf("expected failedRestarts=1, got %d", failedRestarts["testservice"])
	}
}

// TestAttemptServiceRestart_FailAtMax verifies that reaching MaxFailedRestarts removes
// the service from the queue and returns true.
func TestAttemptServiceRestart_FailAtMax(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	ctl.registry["testservice"] = &ServiceDef{
		Name:        "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return errors.New("start failed intentionally") },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(ctl.registry, "testservice")

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 3
	sm.RestartQueue["testservice"] = true

	failedRestarts := map[string]int{"testservice": 2}

	result := sm.attemptServiceRestart(context.Background(), "testservice", failedRestarts, nil)

	if !result {
		t.Error("expected true when MaxFailedRestarts is reached")
	}
	if sm.RestartQueue["testservice"] {
		t.Error("expected service removed from queue at MaxFailedRestarts")
	}
}

// ============================================================
// manager.go — processRestartRound
// ============================================================

// TestProcessRestartRound_AllSucceed verifies that when all services succeed,
// madeProgress is true and the queue is emptied.
func TestProcessRestartRound_AllSucceed(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	succeed := &ServiceDef{
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return nil },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	ctl.registry["svc1"] = succeed
	ctl.registry["svc2"] = succeed
	defer delete(ctl.registry, "svc1")
	defer delete(ctl.registry, "svc2")

	sm := NewServiceManager(ctl)
	sm.RestartQueue["svc1"] = true
	sm.RestartQueue["svc2"] = true

	failedRestarts := make(map[string]int)
	processedThisRound := make(map[string]bool)

	madeProgress := sm.processRestartRound(context.Background(), failedRestarts, processedThisRound, nil)
	if !madeProgress {
		t.Error("expected madeProgress=true when services succeed")
	}
	if len(sm.RestartQueue) != 0 {
		t.Errorf("expected empty queue, got %d entries", len(sm.RestartQueue))
	}
}

// TestProcessRestartRound_AlreadyProcessed verifies that a service already in
// processedThisRound is skipped (attemptServiceRestart is never invoked, so no
// ctl.registry entry is needed for "svc1").
func TestProcessRestartRound_AlreadyProcessed(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager(ctl)
	sm.RestartQueue["svc1"] = true

	failedRestarts := make(map[string]int)
	processedThisRound := map[string]bool{"svc1": true}

	madeProgress := sm.processRestartRound(context.Background(), failedRestarts, processedThisRound, nil)
	if madeProgress {
		t.Error("expected madeProgress=false when all services already processed this round")
	}
	if !sm.RestartQueue["svc1"] {
		t.Error("expected service to remain in queue when skipped")
	}
}

// TestProcessRestartRound_EmptyQueue verifies no progress with empty queue.
func TestProcessRestartRound_EmptyQueue(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager(ctl)
	failedRestarts := make(map[string]int)
	processedThisRound := make(map[string]bool)

	madeProgress := sm.processRestartRound(context.Background(), failedRestarts, processedThisRound, nil)
	if madeProgress {
		t.Error("expected madeProgress=false for empty queue")
	}
}

// ============================================================
// manager.go — ProcessRestarts
// ============================================================

// TestProcessRestarts_ServiceSucceedsEventually verifies the loop resets processedThisRound
// when progress is made.
func TestProcessRestarts_ServiceSucceedsEventually(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	succeed := &ServiceDef{
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return nil },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	ctl.registry["svc1"] = succeed
	ctl.registry["svc2"] = succeed
	defer delete(ctl.registry, "svc1")
	defer delete(ctl.registry, "svc2")

	sm := NewServiceManager(ctl)
	sm.RestartQueue["svc1"] = true
	sm.RestartQueue["svc2"] = true

	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts returned error: %v", err)
	}
	if len(sm.RestartQueue) != 0 {
		t.Errorf("expected empty queue, got %d entries", len(sm.RestartQueue))
	}
}

// TestProcessRestarts_NoProgressBreaksLoop verifies the "no progress" break path.
func TestProcessRestarts_NoProgressBreaksLoop(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	ctl.registry["svc1"] = &ServiceDef{
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return errors.New("start failed intentionally") },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(ctl.registry, "svc1")

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 5
	sm.RestartQueue["svc1"] = true

	err := sm.ProcessRestarts(context.Background(), nil)
	if !errors.Is(err, ErrRestartsFailed) {
		t.Errorf("ProcessRestarts error = %v, want ErrRestartsFailed", err)
	}
	if !sm.RestartQueue["svc1"] {
		t.Error("expected service to remain in queue (no-progress break)")
	}
}

// ============================================================
// manager.go — milterEnabled (from manager_additional_test.go)
// ============================================================

// TestMilterEnabled verifies milterEnabled reads the options file correctly.
func TestMilterEnabled(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "mta_milter_options")
	ctl.p.milterOptions = filePath

	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=TRUE\n"), 0o644); err != nil {
		t.Fatalf("failed writing enabled milter file: %v", err)
	}
	if !ctl.milterEnabled(context.Background()) {
		t.Fatal("expected milterEnabled to return true")
	}

	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=FALSE\n"), 0o644); err != nil {
		t.Fatalf("failed writing disabled milter file: %v", err)
	}
	if ctl.milterEnabled(context.Background()) {
		t.Fatal("expected milterEnabled to return false")
	}
}

// ============================================================
// cli_process.go — killProcess (OtherPidSignaled) + killStatsPidFile + statsCustomStop
// ============================================================

// TestKillProcess_OtherPidSignaled verifies killProcess signals a non-self, non-parent PID.
func TestKillProcess_OtherPidSignaled(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	pumpClock(t, f.Clock, time.Second)

	const needle = "killprocess-sleep-test-needle-xyzzy"

	f.Proc.Add(sys.ProcEntry{PID: 4321, Argv: []string{needle}, Comm: "sleep"})

	if err := ctl.killProcess(context.Background(), needle); err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}

	sigs := f.Proc.Signals()
	if len(sigs) == 0 || sigs[0].PID != 4321 || sigs[0].Sig != syscall.SIGTERM {
		t.Errorf("signals = %v, want SIGTERM to pid 4321 first", sigs)
	}
}

// TestKillStatsPidFile_ValidPidKilled verifies killStatsPidFile kills a real process.
func TestKillStatsPidFile_ValidPidKilled(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start background sleep: %v", err)
	}

	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "zmstat-test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := ctl.killStatsPidFile(context.Background(), pidFile)
	if !result {
		t.Error("expected killStatsPidFile to return true for a live process")
	}

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("expected pidFile to be removed after successful kill")
	}
}

// TestStatsCustomStop_WithValidPidFile verifies statsCustomStop kills via .pid files.
func TestStatsCustomStop_WithValidPidFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start background sleep: %v", err)
	}

	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "zmstat-cpu.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.statsPidDir = tmp

	err := ctl.statsCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("statsCustomStop returned error: %v", err)
	}
}

// ============================================================
// sd_notify.go — waitForSDNotify
// ============================================================

// listenUnixgramAt creates a Unix datagram socket at the given path.
func listenUnixgramAt(path string) (*net.UnixConn, error) {
	return net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
}

// dialUnixgram connects to a Unix datagram socket.
func dialUnixgram(path string) (*net.UnixConn, error) {
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	return net.DialUnix("unixgram", nil, addr)
}

// TestWaitForSDNotify_NonReadyDatagram verifies that non-READY datagrams are
// ignored and waiting continues until context cancel.
func TestWaitForSDNotify_NonReadyDatagram(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	socketPath := filepath.Join(tmp, "test-nonready.sock")

	conn, err := listenUnixgramAt(socketPath)
	if err != nil {
		t.Fatalf("listenUnixgram: %v", err)
	}
	defer conn.Close()
	defer os.Remove(socketPath)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- ctl.waitForSDNotify(ctx, conn, "test-service", nil)
	}()

	sender, dialErr := dialUnixgram(socketPath)
	if dialErr == nil {
		_, _ = sender.Write([]byte("STATUS=starting\n"))
		_ = sender.Close()
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-context.Background().Done():
		t.Fatal("timed out")
	}
}
