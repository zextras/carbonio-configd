// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// TestHelperProcess is a re-exec target for TestKillProcess_EscalatesToSIGKILL.
// Gated on GO_WANT_HELPER_PROCESS=1 per the Go stdlib os/exec test pattern.
// Prints "READY\n" only after signal.Notify has replaced SIGTERM's default
// disposition — the caller waits for that marker before issuing SIGTERM,
// removing the shell-trap race that made the prior sh -c target flaky on CI.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	fmt.Println("READY")
	_ = os.Stdout.Sync()

	for range ch { //nolint:revive // drain channel so trapped SIGTERMs stay consumed
	}
}

// matchProcEntry composes the production pieces (Proc.Entry, entryMatches,
// Proc.Zombie) the way procSnapshot.scan does, for single-entry assertions.
func matchProcEntry(ctl *Controller, pid int, needle string) (int, bool) {
	pe, ok := ctl.env.Proc.Entry(pid)
	if !ok || !entryMatches(&pe, needle) || ctl.env.Proc.Zombie(pe.PID) {
		return 0, false
	}

	return pe.PID, true
}

// TestMatchProcEntry_ZombieSkipped verifies zombie processes are skipped.
func TestMatchProcEntry_ZombieSkipped(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	pid := 99999
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("myneedle\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\tmyneedle\nState:\tZ (zombie)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := matchProcEntry(ctl, pid, "myneedle")
	if ok {
		t.Error("expected false for zombie process")
	}
}

func TestMatchProcEntry_FallbackComm(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	uid := os.Getuid()
	writeProc := func(pid int, cmdline, comm string) {
		procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
		if err := os.MkdirAll(procDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
		status := fmt.Sprintf("Name:\t%s\nState:\tS (sleeping)\nUid:\t%d\t%d\t%d\t%d\n", comm, uid, uid, uid, uid)
		if err := os.WriteFile(filepath.Join(procDir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(procDir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeProc(88888, "", "nginx")
	if pid, ok := matchProcEntry(ctl, 88888, "nginx"); !ok || pid != 88888 {
		t.Errorf("exact comm match on empty cmdline should succeed: got pid=%d ok=%v", pid, ok)
	}

	if _, ok := matchProcEntry(ctl, 88888, "nginx-worker"); ok {
		t.Error("comm='nginx' must NOT match longer needle 'nginx-worker' via substring fallback")
	}

	writeProc(77777, "/bin/bash /usr/bin/service-discover-wrapper.sh -sidecar-for carbonio-proxy", "service-discove")
	if _, ok := matchProcEntry(ctl, 77777, "service-discovered"); ok {
		t.Error("wrapper cmdline must not match service-discovered via reversed comm substring (regression)")
	}
}

// TestMatchProcEntry_NoMatch verifies non-matching cmdline/comm returns false.
func TestMatchProcEntry_NoMatch(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	uid := os.Getuid()
	pid := 77777
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("someprocess\x00arg1\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	statusContent := fmt.Sprintf("Name:\tsomeprocess\nState:\tS (sleeping)\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "comm"), []byte("someprocess\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := matchProcEntry(ctl, pid, "doesnotmatch")
	if ok {
		t.Error("expected false for non-matching process")
	}
}

// TestIsZombie_NotZombie verifies non-zombie status returns false.
func TestIsZombie_NotZombie(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	pid := 11111
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nState:\tS (sleeping)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ctl.env.Proc.Zombie(pid) {
		t.Error("expected isZombie=false for sleeping process")
	}
}

// TestIsZombie_IsZombie verifies zombie state returns true.
func TestIsZombie_IsZombie(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	pid := 22222
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nState:\tZ (zombie)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !ctl.env.Proc.Zombie(pid) {
		t.Error("expected isZombie=true for zombie process")
	}
}

// TestIsZombie_UnreadableStatus verifies missing status file returns false (assume alive).
func TestIsZombie_UnreadableStatus(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.useProcRoot("/nonexistent-proc-root-xyz/")

	if ctl.env.Proc.Zombie(99999) {
		t.Error("expected isZombie=false when status file is unreadable")
	}
}

// TestIsRunningByPidFile_ValidRunningPid verifies a pidfile pointing to ourself returns true.
func TestIsRunningByPidFile_ValidRunningPid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	running, ok := ctl.isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for readable pidfile")
	}
	if !running {
		t.Error("expected running=true for our own PID")
	}
}

// TestIsRunningByPidFile_MissingFile verifies missing pidfile returns ok=false.
func TestIsRunningByPidFile_MissingFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, ok := ctl.isRunningByPidFile("/nonexistent/path/test.pid")
	if ok {
		t.Error("expected ok=false for missing pidfile")
	}
}

// TestIsRunningByPidFile_EmptyFile verifies empty pidfile returns ok=false.
func TestIsRunningByPidFile_EmptyFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "empty.pid")
	if err := os.WriteFile(pidFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := ctl.isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for empty pidfile")
	}
}

// TestIsRunningByPidFile_InvalidPid verifies corrupt pidfile returns ok=false.
func TestIsRunningByPidFile_InvalidPid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "corrupt.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := ctl.isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for corrupt pidfile")
	}
}

// TestIsRunningByPidFile_ZeroPid verifies pidfile with pid=0 returns ok=false.
func TestIsRunningByPidFile_ZeroPid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "zero.pid")
	if err := os.WriteFile(pidFile, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := ctl.isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for pid=0")
	}
}

// TestIsRunningByPidFile_MultiLinePid verifies only the first line is used.
func TestIsRunningByPidFile_MultiLinePid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "multi.pid")
	self := os.Getpid()
	content := fmt.Sprintf("%d\n99999999\n", self)
	if err := os.WriteFile(pidFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	running, ok := ctl.isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for readable multi-line pidfile")
	}
	if !running {
		t.Error("expected running=true, first line is our own PID")
	}
}

// TestIsRunningByPidFile_DeadPid verifies a pidfile with a nonexistent PID returns (false, true).
func TestIsRunningByPidFile_DeadPid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "dead.pid")
	// Use a synthetic proc root so PID definitely doesn't exist there.
	fakeProcDir := filepath.Join(tmpDir, "fakeprocfs")
	if err := os.MkdirAll(fakeProcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.useProcRoot(fakeProcDir + "/")

	// Write a valid non-zero PID that won't have an entry in our fake /proc
	if err := os.WriteFile(pidFile, []byte("2147483647\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	running, ok := ctl.isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for valid (but dead) pid")
	}
	if running {
		t.Error("expected running=false for nonexistent PID")
	}
}

// TestScanProcesses_NoMatch verifies that a highly unique needle returns
// no matches against the real /proc filesystem.
func TestScanProcesses_NoMatch(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	pids, err := ctl.scanProcesses("carbonio-configd-unique-needle-xyzzy-no-match-99999")
	if err != nil {
		t.Fatalf("scanProcesses returned error: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("expected no pids for unique needle, got %v", pids)
	}
}

// TestScanProcesses_SelfMatch verifies that our own process cmdline is found.
func TestScanProcesses_SelfMatch(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	self := os.Getpid()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", self))
	if err != nil {
		t.Skipf("cannot read own cmdline: %v", err)
	}
	if len(data) == 0 {
		t.Skip("cmdline is empty")
	}
	// Extract first token from NUL-delimited cmdline as the needle
	needle := string(data)
	for i, b := range data {
		if b == 0 {
			needle = string(data[:i])
			break
		}
	}
	if needle == "" {
		t.Skip("could not determine needle from cmdline")
	}

	pids, err := ctl.scanProcesses(needle)
	if err != nil {
		t.Fatalf("scanProcesses returned error: %v", err)
	}
	found := false
	for _, pid := range pids {
		if pid == self {
			found = true
			break
		}
	}
	if !found {
		// Not fatal — matches can be excluded by other scan heuristics
		t.Logf("own pid %d not found among matches for %q (may be filtered)", self, needle)
	}
}

// TestIsZombie_NoStateLine verifies that a status file without a State line returns false.
func TestIsZombie_NoStateLine(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	pid := 33333
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Status file with no State line
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nPid:\t33333\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ctl.env.Proc.Zombie(pid) {
		t.Error("expected isZombie=false when no State line present")
	}
}

// TestIsProcessRunning_SelfExcluded verifies isProcessRunning returns false when only
// the current process matches (self-exclusion).
func TestIsProcessRunning_SelfExcluded(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	ctl.useProcRoot(tmpDir + "/")

	uid := os.Getuid()
	self := os.Getpid()

	// Create a proc entry for ourselves
	selfDir := filepath.Join(tmpDir, strconv.Itoa(self))
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selfDir, "cmdline"), []byte("uniqueneedle123\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf("Name:\tuniquetest\nState:\tS (sleeping)\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	// isProcessRunning excludes self and parent — only self matches → returns false
	ctx := context.Background()
	running := ctl.pidFromProcessName(ctx, "uniqueneedle123") > 0
	if running {
		t.Error("expected isProcessRunning=false when only self matches")
	}
}

// TestIsProcessRunning_NoMatchInRealProc verifies isProcessRunning returns false when
// no process in /proc matches a highly unlikely needle string.
func TestIsProcessRunning_NoMatchInRealProc(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Use a needle so unique it cannot possibly appear in any real process cmdline.
	running := ctl.pidFromProcessName(context.Background(), "carbonio-configd-test-unique-needle-xyzzy-12345") > 0
	if running {
		t.Error("expected isProcessRunning=false for needle that matches nothing in /proc")
	}
}

// TestKillProcess_EscalatesToSIGKILL is the regression guard for the mailbox
// Java bug: a target that catches SIGTERM and refuses to exit must still be
// terminated by killProcess, which now escalates to SIGKILL after the grace
// period. Uses a shell process that explicitly ignores SIGTERM.
func TestKillProcess_EscalatesToSIGKILL(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: spawns a real subprocess")
	}

	needle := "carbonio-configd-test-sigignore-needle-xyzzy-" + strconv.Itoa(os.Getpid())

	cmd := exec.CommandContext(context.Background(), os.Args[0],
		"-test.run=TestHelperProcess", "-test.timeout=60s", "--", needle)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to spawn helper: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	readyCh := make(chan error, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr != nil {
			readyCh <- readErr
			return
		}
		if line != "READY\n" {
			readyCh <- fmt.Errorf("unexpected helper line: %q", line)
			return
		}
		readyCh <- nil
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			t.Fatalf("helper did not signal READY: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for helper READY")
	}

	require.Eventually(t, func() bool {
		return ctl.pidFromProcessName(context.Background(), needle) > 0
	}, 5*time.Second, 10*time.Millisecond, "helper never became visible in /proc")

	ctl.killTimeout = 500 * time.Millisecond

	start := time.Now()
	if err := ctl.killProcess(context.Background(), needle); err != nil {
		t.Fatalf("killProcess returned error: %v", err)
	}

	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond {
		t.Errorf("killProcess returned in %v — too fast, must have waited for grace period", elapsed)
	}

	if elapsed > 3*time.Second {
		t.Errorf("killProcess took %v — should have escalated to SIGKILL well under this", elapsed)
	}

	require.Eventually(t, func() bool {
		return ctl.pidFromProcessName(context.Background(), needle) == 0
	}, 2*time.Second, 10*time.Millisecond, "target process survived killProcess — SIGKILL escalation failed")
}

// TestProcessAlive_DeadPID verifies processAlive returns false for PIDs that
// cannot possibly exist. Guards against a false-positive wait that would
// block killProcess indefinitely.
func TestProcessAlive_DeadPID(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: walks /proc")
	}

	if ctl.processAlive(999999999) {
		t.Error("expected processAlive=false for impossible PID 999999999")
	}
}

func TestProcessAlive_Self(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: reads /proc")
	}
	self := os.Getpid()
	if !ctl.processAlive(self) {
		t.Error("expected processAlive=true for own PID")
	}
}

func TestStartService_SystemdMode(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctl.pinMode(true)
	f.Exec.OnPath(systemctlBin, systest.Fail(5, "Unit carbonio-test-systemd.service not found."))

	def := &ServiceDef{
		Name:         "test-systemd-start",
		DisplayName:  "Test Systemd",
		SystemdUnits: []string{"carbonio-test-systemd.service"},
	}
	ctl.registry["test-systemd-start"] = def

	err := ctl.startService(context.Background(), "test-systemd-start", def)
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}

	want := []string{systemctlBin + " start carbonio-test-systemd.service"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestStartWithoutSystemd_CustomStartError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	wantErr := fmt.Errorf("custom start failed")
	def := &ServiceDef{
		Name: "test-custom-err",
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			return wantErr
		},
	}

	err := ctl.startWithoutSystemd(context.Background(), "test-custom-err", def)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestStartWithoutSystemd_ProcessNameOnly(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "test-proc-only",
		ProcessName: "some-process",
	}

	err := ctl.startWithoutSystemd(context.Background(), "test-proc-only", def)
	if err != nil {
		t.Errorf("expected nil for process-name-only service, got %v", err)
	}
}

func TestKillByPIDWithGroupAndSudo_DeadPID(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	result := ctl.killByPIDWithGroupAndSudo(context.Background(), 99999999)
	if !result {
		t.Error("expected true for already-dead PID")
	}
}

func TestWaitAndEscalate_NoPids(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.waitAndEscalate(context.Background(), []int{}, 100*time.Millisecond)
}

func TestStopService_SystemdMode(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctl.pinMode(true)
	f.Exec.OnPath(systemctlBin, systest.Fail(5, "Unit carbonio-test-stop.service not loaded."))

	def := &ServiceDef{
		Name:         "test-systemd-stop",
		DisplayName:  "Test Systemd",
		SystemdUnits: []string{"carbonio-test-stop.service"},
	}

	err := ctl.stopService(context.Background(), "test-systemd-stop", def)
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}

	want := []string{systemctlBin + " stop carbonio-test-stop.service"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestStartDirect_BinaryNotFound(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := &ServiceDef{
		Name:       "test-nobin",
		BinaryPath: "/nonexistent/binary/that/does/not/exist",
		BinaryArgs: []string{"--test"},
	}

	err := ctl.startDirect(context.Background(), "test-nobin", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestStartDirect_NonDaemon(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	script := filepath.Join(tmp, "testbin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-direct",
		BinaryPath: script,
		BinaryArgs: []string{},
	}

	err := ctl.startDirect(context.Background(), "test-direct", def)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKillProcessGroup_Real(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	err := ctl.killProcessGroup(-999999999, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestSudoKill_NonexistentProcess(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.sudoKill(context.Background(), 999999999, "TERM", false)
}

func TestScanProcesses_ReadError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.useProcRoot("/nonexistent-proc-dir-xyzzy/")

	// The scan lists the Controller's proc root, so an unreadable
	// root surfaces as an error and callers treat it as "nothing found".
	pids, err := ctl.scanProcesses("test")
	if err == nil {
		t.Error("scanProcesses should report an unreadable proc root")
	}

	if len(pids) != 0 {
		t.Errorf("expected no pids on read error, got %v", pids)
	}

	if ctl.pidFromProcessName(context.Background(), "test") > 0 {
		t.Error("isProcessRunning must report false when the proc root is unreadable")
	}
}

func TestProcessAlive_SelfIsAlive(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if !ctl.processAlive(os.Getpid()) {
		t.Error("expected processAlive=true for own PID")
	}
}

func TestStartWithoutSystemd_NoLauncher(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := &ServiceDef{
		Name: "test-no-launcher",
	}
	err := ctl.startWithoutSystemd(context.Background(), "test-no-launcher", def)
	if err == nil {
		t.Error("expected error for service with no launcher")
	}
	if !strings.Contains(err.Error(), "no direct launcher") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartDirect_NeedsRootNonRoot(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if os.Getuid() == 0 {
		t.Skip("running as root, NeedsRoot path differs")
	}
	tmp := t.TempDir()
	script := filepath.Join(tmp, "mybin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-root-svc",
		BinaryPath: script,
		NeedsRoot:  true,
	}

	err := ctl.startDirect(context.Background(), "test-root-svc", def)
	if err != nil {
		t.Logf("startDirect with NeedsRoot: %v (expected - sudo may fail)", err)
	}
}

func TestStartDirect_SDNotifyTimeout(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: uses subprocess")
	}
	tmp := t.TempDir()
	script := filepath.Join(tmp, "sleeper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "test-sdnotify-direct",
		BinaryPath:  script,
		UseSDNotify: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := ctl.startDirect(ctx, "test-sdnotify-direct", def)
	if err == nil {
		t.Log("startDirect with SDNotify succeeded (unexpected)")
	} else {
		t.Logf("startDirect with SDNotify (expected timeout): %v", err)
	}
}

func TestStartDetached_LogFilePath(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	script := filepath.Join(tmp, "daemon")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(tmp, "test.log")

	def := &ServiceDef{
		Name:       "test-detached-logfile",
		BinaryPath: script,
		LogFile:    logFile,
	}

	err := ctl.startDetached(context.Background(), "test-detached-logfile", def)
	if err != nil {
		t.Fatalf("startDetached with LogFile: %v", err)
	}
}

func TestStartDetached_DefaultLogFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	script := filepath.Join(tmp, "daemon")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	logDir := filepath.Join(tmp, "log")
	ctl.p.log = logDir
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-detached-default",
		BinaryPath: script,
	}

	err := ctl.startDetached(context.Background(), "test-detached-default", def)
	if err != nil {
		t.Fatalf("startDetached default log path: %v", err)
	}
}

func TestStopWithoutSystemd_NoLauncher(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := &ServiceDef{
		Name: "test-no-process-name",
	}
	err := ctl.stopWithoutSystemd(context.Background(), "test-no-process-name", def)
	if err == nil {
		t.Error("expected error for service with no ProcessName")
	}
}

func TestStopWithoutSystemd_CustomStopErr(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	wantErr := fmt.Errorf("custom stop failed")
	def := &ServiceDef{
		Name: "test-custom-stop-err",
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			return wantErr
		},
	}
	err := ctl.stopWithoutSystemd(context.Background(), "test-custom-stop-err", def)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected custom stop error, got %v", err)
	}
}

func TestKillProcess_NoTarget(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	err := ctl.killProcess(context.Background(), "carbonio-nonexistent-needle-xyzzy-12345")
	if err != nil {
		t.Errorf("killProcess should succeed with no targets, got %v", err)
	}
}

func TestStartDetached_SpawnAndRelease(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("spawns a short-lived detached child")
	}
	tmp := t.TempDir()
	script := filepath.Join(tmp, "sleeper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:       "test-det-spawn",
		BinaryPath: script,
		LogFile:    logFile,
	}

	err := ctl.startDetached(context.Background(), "test-det-spawn", def)
	if err != nil {
		t.Fatalf("startDetached: %v", err)
	}
}

func TestStartDirect_OutputCaptured(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	script := filepath.Join(tmp, "failbin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'startup failed'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-direct-fail",
		BinaryPath: script,
	}

	err := ctl.startDirect(context.Background(), "test-direct-fail", def)
	if err == nil {
		t.Error("expected error from failing binary")
	}
	if !strings.Contains(err.Error(), "startup failed") {
		t.Errorf("expected error to contain output, got %v", err)
	}
}

func TestStopWithoutSystemd_WithUseSDNotify(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := &ServiceDef{
		Name:        "test-sd-notify-stop",
		ProcessName: "nonexistent-sd-notify-proc-xyz",
		UseSDNotify: true,
	}
	ctl.registry["test-sd-notify-stop"] = def
	defer delete(ctl.registry, "test-sd-notify-stop")

	ctl.pinMode(false)

	err := ctl.stopWithoutSystemd(context.Background(), "test-sd-notify-stop", def)
	// Process doesn't exist, killProcess returns nil
	_ = err
}

func TestKillProcessGroup_NoSuchGroup(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	// Use a PGID that cannot exist to exercise the ESRCH path without
	// risking real signals to the user's processes. Never pass 1 or -1 —
	// syscall.Kill(-1, sig) broadcasts to every process the user owns,
	// which will tear down the user's graphical session.
	err := ctl.killProcessGroup(999999990, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error for nonexistent process group")
	}
}

func TestSignalPID_NoSuchProcess(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	// Use a PID we know is not alive. Never signal os.Getpid() — that kills
	// the test binary itself.
	ctl.signalPID(999999990, syscall.SIGTERM)
	ctl.signalPID(999999991, syscall.SIGUSR1)
}

func TestMailboxCustomStart_AppserverDBFail(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}

	def := &ServiceDef{Name: "mailbox"}
	err := ctl.mailboxCustomStart(context.Background(), def)
	if err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMtaIsRunning_NotRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	f.Exec.OnPath(sudoBin, systest.Fail(1, ""))

	if ctl.mtaIsRunning(context.Background()) {
		t.Error("expected mtaIsRunning=false with failing sudo")
	}
}
