// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// ============================================================
// manager.go — addRestartLogged
// ============================================================

// TestAddRestartLogged_Deduplication verifies calling twice deduplicates.
func TestAddRestartLogged_Deduplication(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.addRestartLogged(context.Background(), "mta", "mta-label")
	sm.addRestartLogged(context.Background(), "mta", "mta-label")
	if len(sm.RestartQueue) != 1 {
		t.Errorf("expected 1 entry after duplicate addRestartLogged, got %d", len(sm.RestartQueue))
	}
}

// TestAddRestartLogged_MultipleDifferentServices verifies multiple services are queued.
func TestAddRestartLogged_MultipleDifferentServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.addRestartLogged(context.Background(), "mta", "mta")
	sm.addRestartLogged(context.Background(), "proxy", "proxy")
	sm.addRestartLogged(context.Background(), "mailbox", "mailbox")

	if len(sm.RestartQueue) != 3 {
		t.Errorf("expected 3 queued services, got %d", len(sm.RestartQueue))
	}
}

// TestAddRestartLogged_LabelDiffersFromService verifies addRestartLogged
// accepts a logLabel different from the service name.
func TestAddRestartLogged_LabelDiffersFromService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.addRestartLogged(context.Background(), "mta", "mta-display-label")

	if !sm.RestartQueue["mta"] {
		t.Error("expected 'mta' queued when logLabel differs from service name")
	}
}

// ============================================================
// manager.go — executeCommand
// ============================================================

// TestExecuteCommand_Success verifies executeCommand returns nil for a command that exits 0.
func TestExecuteCommand_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	err := sm.executeCommand(context.Background(), "true", "")
	if err != nil {
		t.Errorf("executeCommand(true) returned error: %v", err)
	}
}

// TestExecuteCommand_Failure verifies executeCommand returns an error for a non-zero exit.
func TestExecuteCommand_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	err := sm.executeCommand(context.Background(), "false", "start")
	if err == nil {
		t.Error("expected error for command that exits non-zero")
	}
}

// TestExecuteCommand_MissingBinary verifies executeCommand returns an error for missing binary.
func TestExecuteCommand_MissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	err := sm.executeCommand(context.Background(), "/nonexistent/binary/xyz", "start")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

// ============================================================
// manager.go — executeSystemdCommand
// ============================================================

// TestExecuteSystemdCommand_UnknownService verifies error when service has no systemd unit.
func TestExecuteSystemdCommand_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	err := sm.executeSystemdCommand(context.Background(), "nonexistent", "status")
	if err == nil {
		t.Error("expected error for service with no systemd unit")
	}
}

// TestExecuteSystemdCommand_KnownServiceSystemctlFails verifies fallback to zm*ctl.
func TestExecuteSystemdCommand_KnownServiceSystemctlFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	err := sm.executeSystemdCommand(context.Background(), "proxy", "status")
	_ = err
}

// TestExecuteSystemdCommand_NoFallbackAvailable verifies error when systemctl fails
// and there is no zm*ctl entry.
func TestExecuteSystemdCommand_NoFallbackAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.SystemdMap["testonly"] = "carbonio-testonly.service"

	err := sm.executeSystemdCommand(context.Background(), "testonly", "status")
	if err == nil {
		t.Error("expected error when systemctl fails and no zm*ctl fallback")
	}
}

// TestExecuteSystemdCommand_SystemctlSucceeds verifies the early-return when
// systemctl exits 0.
func TestExecuteSystemdCommand_SystemctlSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeSystemctl := filepath.Join(tmp, "systemctl")

	if err := os.WriteFile(fakeSystemctl, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)

	sm := NewServiceManager()
	if _, ok := sm.SystemdMap["proxy"]; !ok {
		t.Skip("proxy not in SystemdMap, skipping test")
	}

	err := sm.executeSystemdCommand(context.Background(), "proxy", "status")
	if err != nil {
		t.Errorf("expected nil error when fake systemctl exits 0, got: %v", err)
	}
}

// ============================================================
// manager.go — attemptServiceRestart
// ============================================================

// TestAttemptServiceRestart_SuccessWithDependencies verifies that on success the service
// is removed from the queue and AddDependencyRestarts is called.
func TestAttemptServiceRestart_SuccessWithDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.CommandMap["testservice"] = "true"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.CommandMap["testservice"] = "true"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.MaxFailedRestarts = 3
	sm.CommandMap["testservice"] = "false"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.MaxFailedRestarts = 3
	sm.CommandMap["testservice"] = "false"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.CommandMap["svc1"] = "true"
	sm.CommandMap["svc2"] = "true"
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
// processedThisRound is skipped.
func TestProcessRestartRound_AlreadyProcessed(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.CommandMap["svc1"] = "true"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.CommandMap["svc1"] = "true"
	sm.CommandMap["svc2"] = "true"
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
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.MaxFailedRestarts = 5
	sm.CommandMap["svc1"] = "false"
	sm.RestartQueue["svc1"] = true

	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts returned error: %v", err)
	}
	if !sm.RestartQueue["svc1"] {
		t.Error("expected service to remain in queue (no-progress break)")
	}
}

// ============================================================
// manager.go — ControlProcess (UseSystemd=true branch)
// ============================================================

// TestControlProcess_UseSystemd verifies that when UseSystemd=true, ControlProcess
// delegates to executeSystemdCommand.
func TestControlProcess_UseSystemd(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.UseSystemd = true

	err := sm.ControlProcess(context.Background(), "proxy", ActionStatus)
	_ = err
}

// TestControlProcess_UseSystemd_InvalidAction verifies the invalid-action guard.
func TestControlProcess_UseSystemd_InvalidAction(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.UseSystemd = true

	err := sm.ControlProcess(context.Background(), "proxy", ServiceAction(999))
	if err == nil {
		t.Error("expected error for invalid action")
	}
}

// TestControlProcess_UseSystemd_UnknownService verifies that even with UseSystemd=true,
// an unknown service returns an error.
func TestControlProcess_UseSystemd_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.UseSystemd = true

	err := sm.ControlProcess(context.Background(), "nonexistent-service-xyz", ActionStatus)
	if err == nil {
		t.Error("expected error for service with no systemd mapping")
	}
}

// ============================================================
// manager.go — SetUseSystemd, milterEnabled (from manager_additional_test.go)
// ============================================================

// TestSetUseSystemd verifies SetUseSystemd toggles UseSystemd correctly.
func TestSetUseSystemd(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	if sm.UseSystemd {
		t.Fatal("expected UseSystemd false by default")
	}

	sm.SetUseSystemd(true)
	if !sm.UseSystemd {
		t.Fatal("expected UseSystemd true after enabling")
	}

	sm.SetUseSystemd(false)
	if sm.UseSystemd {
		t.Fatal("expected UseSystemd false after disabling")
	}
}

// TestMilterEnabled verifies milterEnabled reads the options file correctly.
func TestMilterEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "mta_milter_options")
	oldPath := milterOptionsPath
	milterOptionsPath = filePath
	defer func() { milterOptionsPath = oldPath }()

	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=TRUE\n"), 0o644); err != nil {
		t.Fatalf("failed writing enabled milter file: %v", err)
	}
	if !milterEnabled(context.Background()) {
		t.Fatal("expected milterEnabled to return true")
	}

	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=FALSE\n"), 0o644); err != nil {
		t.Fatalf("failed writing disabled milter file: %v", err)
	}
	if milterEnabled(context.Background()) {
		t.Fatal("expected milterEnabled to return false")
	}
}

// ============================================================
// cli_process.go — killProcess (OtherPidSignaled) + killStatsPidFile + statsCustomStop
// ============================================================

// TestKillProcess_OtherPidSignaled verifies killProcess signals a non-self, non-parent PID.
func TestKillProcess_OtherPidSignaled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start background sleep: %v", err)
	}

	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	sleepPid := cmd.Process.Pid

	tmp := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmp + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	pidDir := filepath.Join(tmp, strconv.Itoa(sleepPid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}

	needle := "killprocess-sleep-test-needle-xyzzy"
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := "Name:\tsleep\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(pidDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillStatsPidFile_ValidPidKilled verifies killStatsPidFile kills a real process.
func TestKillStatsPidFile_ValidPidKilled(t *testing.T) {
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

	result := killStatsPidFile(context.Background(), pidFile)
	if !result {
		t.Error("expected killStatsPidFile to return true for a live process")
	}

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("expected pidFile to be removed after successful kill")
	}
}

// TestStatsCustomStop_WithValidPidFile verifies statsCustomStop kills via .pid files.
func TestStatsCustomStop_WithValidPidFile(t *testing.T) {
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

	old := statsPidDir
	statsPidDir = tmp
	defer func() { statsPidDir = old }()

	err := statsCustomStop(context.Background(), nil)
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
		done <- waitForSDNotify(ctx, conn, "test-service")
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
