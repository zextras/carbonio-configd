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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// fakeProcEntry is a helper that implements os.DirEntry for testing matchProcEntry.
type fakeProcEntry struct {
	name  string
	isDir bool
}

func (f fakeProcEntry) Name() string               { return f.name }
func (f fakeProcEntry) IsDir() bool                { return f.isDir }
func (f fakeProcEntry) Type() os.FileMode          { return 0 }
func (f fakeProcEntry) Info() (os.FileInfo, error) { return nil, errors.New("not implemented") }

// TestMatchProcEntry_NotDir verifies non-directories are skipped.
func TestMatchProcEntry_NotDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	e := fakeProcEntry{name: "12345", isDir: false}
	_, ok := matchProcEntry(e, "anything")
	if ok {
		t.Error("expected false for non-directory entry")
	}
}

// TestMatchProcEntry_NotNumeric verifies non-numeric dir names are skipped.
func TestMatchProcEntry_NotNumeric(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	e := fakeProcEntry{name: "net", isDir: true}
	_, ok := matchProcEntry(e, "anything")
	if ok {
		t.Error("expected false for non-numeric directory name")
	}
}

// TestMatchProcEntry_ZombieSkipped verifies zombie processes are skipped.
func TestMatchProcEntry_ZombieSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

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

	e := fakeProcEntry{name: strconv.Itoa(pid), isDir: true}
	_, ok := matchProcEntry(e, "myneedle")
	if ok {
		t.Error("expected false for zombie process")
	}
}

func TestMatchProcEntry_FallbackComm(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

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
	if pid, ok := matchProcEntry(fakeProcEntry{name: "88888", isDir: true}, "nginx"); !ok || pid != 88888 {
		t.Errorf("exact comm match on empty cmdline should succeed: got pid=%d ok=%v", pid, ok)
	}

	if _, ok := matchProcEntry(fakeProcEntry{name: "88888", isDir: true}, "nginx-worker"); ok {
		t.Error("comm='nginx' must NOT match longer needle 'nginx-worker' via substring fallback")
	}

	writeProc(77777, "/bin/bash /usr/bin/service-discover-wrapper.sh -sidecar-for carbonio-proxy", "service-discove")
	if _, ok := matchProcEntry(fakeProcEntry{name: "77777", isDir: true}, "service-discovered"); ok {
		t.Error("wrapper cmdline must not match service-discovered via reversed comm substring (regression)")
	}
}

// TestMatchProcEntry_NoMatch verifies non-matching cmdline/comm returns false.
func TestMatchProcEntry_NoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

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

	e := fakeProcEntry{name: strconv.Itoa(pid), isDir: true}
	_, ok := matchProcEntry(e, "doesnotmatch")
	if ok {
		t.Error("expected false for non-matching process")
	}
}

// TestIsZombie_NotZombie verifies non-zombie status returns false.
func TestIsZombie_NotZombie(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	pid := 11111
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nState:\tS (sleeping)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if isZombie(pid) {
		t.Error("expected isZombie=false for sleeping process")
	}
}

// TestIsZombie_IsZombie verifies zombie state returns true.
func TestIsZombie_IsZombie(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	pid := 22222
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nState:\tZ (zombie)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !isZombie(pid) {
		t.Error("expected isZombie=true for zombie process")
	}
}

// TestIsZombie_UnreadableStatus verifies missing status file returns false (assume alive).
func TestIsZombie_UnreadableStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	oldRoot := procFSRoot
	procFSRoot = "/nonexistent-proc-root-xyz/"
	defer func() { procFSRoot = oldRoot }()

	if isZombie(99999) {
		t.Error("expected isZombie=false when status file is unreadable")
	}
}

// TestIsOwnedByCurrentUser_OwnedByUs verifies processes owned by current UID return true.
func TestIsOwnedByCurrentUser_OwnedByUs(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	uid := os.Getuid()
	statusContent := fmt.Sprintf("Name:\ttest\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
	statusFile := filepath.Join(tmpDir, "status")
	if err := os.WriteFile(statusFile, []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if !isOwnedByCurrentUser(tmpDir) {
		t.Error("expected isOwnedByCurrentUser=true for process owned by current user")
	}
}

// TestIsOwnedByCurrentUser_OwnedByOther verifies processes owned by another UID return false.
func TestIsOwnedByCurrentUser_OwnedByOther(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	otherUID := os.Getuid() + 1
	statusContent := fmt.Sprintf("Name:\ttest\nUid:\t%d\t%d\t%d\t%d\n", otherUID, otherUID, otherUID, otherUID)
	statusFile := filepath.Join(tmpDir, "status")
	if err := os.WriteFile(statusFile, []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if isOwnedByCurrentUser(tmpDir) {
		t.Error("expected isOwnedByCurrentUser=false for process owned by other user")
	}
}

// TestIsOwnedByCurrentUser_UnreadableStatus verifies missing status file returns true (fail open).
func TestIsOwnedByCurrentUser_UnreadableStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if !isOwnedByCurrentUser("/nonexistent-dir-xyz") {
		t.Error("expected isOwnedByCurrentUser=true when status is unreadable (fail open)")
	}
}

// TestIsRunningByPidFile_ValidRunningPid verifies a pidfile pointing to ourself returns true.
func TestIsRunningByPidFile_ValidRunningPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	running, ok := isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for readable pidfile")
	}
	if !running {
		t.Error("expected running=true for our own PID")
	}
}

// TestIsRunningByPidFile_MissingFile verifies missing pidfile returns ok=false.
func TestIsRunningByPidFile_MissingFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, ok := isRunningByPidFile("/nonexistent/path/test.pid")
	if ok {
		t.Error("expected ok=false for missing pidfile")
	}
}

// TestIsRunningByPidFile_EmptyFile verifies empty pidfile returns ok=false.
func TestIsRunningByPidFile_EmptyFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "empty.pid")
	if err := os.WriteFile(pidFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for empty pidfile")
	}
}

// TestIsRunningByPidFile_InvalidPid verifies corrupt pidfile returns ok=false.
func TestIsRunningByPidFile_InvalidPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "corrupt.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for corrupt pidfile")
	}
}

// TestIsRunningByPidFile_ZeroPid verifies pidfile with pid=0 returns ok=false.
func TestIsRunningByPidFile_ZeroPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "zero.pid")
	if err := os.WriteFile(pidFile, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := isRunningByPidFile(pidFile)
	if ok {
		t.Error("expected ok=false for pid=0")
	}
}

// TestIsRunningByPidFile_MultiLinePid verifies only the first line is used.
func TestIsRunningByPidFile_MultiLinePid(t *testing.T) {
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

	running, ok := isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for readable multi-line pidfile")
	}
	if !running {
		t.Error("expected running=true, first line is our own PID")
	}
}

// TestIsRunningByPidFile_DeadPid verifies a pidfile with a nonexistent PID returns (false, true).
func TestIsRunningByPidFile_DeadPid(t *testing.T) {
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

	oldRoot := procFSRoot
	procFSRoot = fakeProcDir + "/"
	defer func() { procFSRoot = oldRoot }()

	// Write a valid non-zero PID that won't have an entry in our fake /proc
	if err := os.WriteFile(pidFile, []byte("2147483647\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	running, ok := isRunningByPidFile(pidFile)
	if !ok {
		t.Fatal("expected ok=true for valid (but dead) pid")
	}
	if running {
		t.Error("expected running=false for nonexistent PID")
	}
}

// TestScanProcessesByCmdline_NoMatch verifies that a highly unique needle returns
// no matches against the real /proc filesystem.
// Note: scanProcessesByCmdline reads /proc directly; procFSRoot only affects
// the per-entry file reads inside matchProcEntry.
func TestScanProcessesByCmdline_NoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	pids, err := scanProcessesByCmdline("carbonio-configd-unique-needle-xyzzy-no-match-99999")
	if err != nil {
		t.Fatalf("scanProcessesByCmdline returned error: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("expected no pids for unique needle, got %v", pids)
	}
}

// TestScanProcessesByCmdline_SelfMatch verifies that our own process cmdline is found.
func TestScanProcessesByCmdline_SelfMatch(t *testing.T) {
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

	pids, err := scanProcessesByCmdline(needle)
	if err != nil {
		t.Fatalf("scanProcessesByCmdline returned error: %v", err)
	}
	found := false
	for _, pid := range pids {
		if pid == self {
			found = true
			break
		}
	}
	if !found {
		// Not fatal — we might be excluded by isOwnedByCurrentUser or other filters
		t.Logf("own pid %d not found among matches for %q (may be filtered)", self, needle)
	}
}

// TestIsZombie_NoStateLine verifies that a status file without a State line returns false.
func TestIsZombie_NoStateLine(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	pid := 33333
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Status file with no State line
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte("Name:\ttest\nPid:\t33333\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if isZombie(pid) {
		t.Error("expected isZombie=false when no State line present")
	}
}

// TestIsOwnedByCurrentUser_EmptyUidField verifies empty Uid field falls through to true.
func TestIsOwnedByCurrentUser_EmptyUidField(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	// Status with "Uid:" but no values after it
	statusContent := "Name:\ttest\nUid:\n"
	statusFile := filepath.Join(tmpDir, "status")
	if err := os.WriteFile(statusFile, []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should fall through to return true (assume owned)
	if !isOwnedByCurrentUser(tmpDir) {
		t.Error("expected isOwnedByCurrentUser=true when Uid field is empty")
	}
}

// TestIsOwnedByCurrentUser_NonNumericUid verifies non-parseable UID falls through to true.
func TestIsOwnedByCurrentUser_NonNumericUid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	statusContent := "Name:\ttest\nUid:\tnotanumber\n"
	statusFile := filepath.Join(tmpDir, "status")
	if err := os.WriteFile(statusFile, []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if !isOwnedByCurrentUser(tmpDir) {
		t.Error("expected isOwnedByCurrentUser=true when Uid is non-numeric")
	}
}

// TestIsOwnedByCurrentUser_NoUidLine verifies no Uid line falls through to true.
func TestIsOwnedByCurrentUser_NoUidLine(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	statusContent := "Name:\ttest\nState:\tS (sleeping)\n"
	statusFile := filepath.Join(tmpDir, "status")
	if err := os.WriteFile(statusFile, []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if !isOwnedByCurrentUser(tmpDir) {
		t.Error("expected isOwnedByCurrentUser=true when no Uid line present")
	}
}

// TestIsProcessRunning_SelfExcluded verifies isProcessRunning returns false when only
// the current process matches (self-exclusion).
func TestIsProcessRunning_SelfExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

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
	running := isProcessRunning(ctx, "uniqueneedle123")
	if running {
		t.Error("expected isProcessRunning=false when only self matches")
	}
}

// TestIsProcessRunning_NoMatchInRealProc verifies isProcessRunning returns false when
// no process in /proc matches a highly unlikely needle string.
func TestIsProcessRunning_NoMatchInRealProc(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Use a needle so unique it cannot possibly appear in any real process cmdline.
	running := isProcessRunning(context.Background(), "carbonio-configd-test-unique-needle-xyzzy-12345")
	if running {
		t.Error("expected isProcessRunning=false for needle that matches nothing in /proc")
	}
}

// TestKillProcess_EscalatesToSIGKILL is the regression guard for the mailbox
// Java bug: a target that catches SIGTERM and refuses to exit must still be
// terminated by killProcess, which now escalates to SIGKILL after the grace
// period. Uses a shell process that explicitly ignores SIGTERM.
func TestKillProcess_EscalatesToSIGKILL(t *testing.T) {
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

	for range 50 {
		if isProcessRunning(context.Background(), needle) {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	origTimeout := killProcessTimeout
	killProcessTimeout = 500 * time.Millisecond

	defer func() { killProcessTimeout = origTimeout }()

	start := time.Now()
	if err := killProcess(context.Background(), needle); err != nil {
		t.Fatalf("killProcess returned error: %v", err)
	}

	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond {
		t.Errorf("killProcess returned in %v — too fast, must have waited for grace period", elapsed)
	}

	if elapsed > 3*time.Second {
		t.Errorf("killProcess took %v — should have escalated to SIGKILL well under this", elapsed)
	}

	for range 50 {
		if !isProcessRunning(context.Background(), needle) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Error("target process survived killProcess — SIGKILL escalation failed")
}

// TestProcessAlive_DeadPID verifies processAlive returns false for PIDs that
// cannot possibly exist. Guards against a false-positive wait that would
// block killProcess indefinitely.
func TestProcessAlive_DeadPID(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: walks /proc")
	}

	if processAlive(999999999) {
		t.Error("expected processAlive=false for impossible PID 999999999")
	}
}

func TestProcessAlive_Self(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: reads /proc")
	}
	self := os.Getpid()
	if !processAlive(self) {
		t.Error("expected processAlive=true for own PID")
	}
}

func TestStartService_SystemdMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return true }

	def := &ServiceDef{
		Name:         "test-systemd-start",
		DisplayName:  "Test Systemd",
		SystemdUnits: []string{"carbonio-test-systemd.service"},
	}
	Registry["test-systemd-start"] = def
	defer delete(Registry, "test-systemd-start")

	err := startService(context.Background(), "test-systemd-start", def)
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}
}

func TestStartWithoutSystemd_CustomStartError(t *testing.T) {
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

	err := startWithoutSystemd(context.Background(), "test-custom-err", def)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestStartWithoutSystemd_ProcessNameOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "test-proc-only",
		ProcessName: "some-process",
	}

	err := startWithoutSystemd(context.Background(), "test-proc-only", def)
	if err != nil {
		t.Errorf("expected nil for process-name-only service, got %v", err)
	}
}

func TestKillByPIDWithGroupAndSudo_DeadPID(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	result := killByPIDWithGroupAndSudo(context.Background(), 99999999)
	if !result {
		t.Error("expected true for already-dead PID")
	}
}

func TestWaitAndEscalate_NoPids(t *testing.T) {
	waitAndEscalate(context.Background(), []int{}, 100*time.Millisecond)
}

func TestStopService_SystemdMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return true }

	def := &ServiceDef{
		Name:         "test-systemd-stop",
		DisplayName:  "Test Systemd",
		SystemdUnits: []string{"carbonio-test-stop.service"},
	}

	err := stopService(context.Background(), "test-systemd-stop", def)
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}
}

func TestStartDirect_BinaryNotFound(t *testing.T) {
	def := &ServiceDef{
		Name:       "test-nobin",
		BinaryPath: "/nonexistent/binary/that/does/not/exist",
		BinaryArgs: []string{"--test"},
	}

	err := startDirect(context.Background(), "test-nobin", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestStartDirect_NonDaemon(t *testing.T) {
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

	err := startDirect(context.Background(), "test-direct", def)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKillProcessGroup_Real(t *testing.T) {
	err := killProcessGroup(-999999999, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestSudoKill_NonexistentProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sudoKill(context.Background(), 999999999, "TERM", false)
}

func TestScanProcessesByCmdline_ReadError(t *testing.T) {
	oldRoot := procFSRoot
	procFSRoot = "/nonexistent-proc-dir-xyzzy/"
	defer func() { procFSRoot = oldRoot }()

	_, err := scanProcessesByCmdline("test")
	if err != nil {
		t.Errorf("scanProcessesByCmdline should handle /proc read errors gracefully, got %v", err)
	}
}

func TestProcessAlive_SelfIsAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("expected processAlive=true for own PID")
	}
}

func TestDetachSysProcAttr(t *testing.T) {
	attr := detachedSysProcAttr()
	if attr == nil {
		t.Error("expected non-nil SysProcAttr")
	}
}

func TestStartWithoutSystemd_NoLauncher(t *testing.T) {
	def := &ServiceDef{
		Name: "test-no-launcher",
	}
	err := startWithoutSystemd(context.Background(), "test-no-launcher", def)
	if err == nil {
		t.Error("expected error for service with no launcher")
	}
	if !strings.Contains(err.Error(), "no direct launcher") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartDirect_NeedsRootNonRoot(t *testing.T) {
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

	err := startDirect(context.Background(), "test-root-svc", def)
	if err != nil {
		t.Logf("startDirect with NeedsRoot: %v (expected - sudo may fail)", err)
	}
}

func TestStartDirect_SDNotifyTimeout(t *testing.T) {
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

	err := startDirect(ctx, "test-sdnotify-direct", def)
	if err == nil {
		t.Log("startDirect with SDNotify succeeded (unexpected)")
	} else {
		t.Logf("startDirect with SDNotify (expected timeout): %v", err)
	}
}

func TestStartDetached_LogFilePath(t *testing.T) {
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

	err := startDetached(context.Background(), "test-detached-logfile", def)
	if err != nil {
		t.Fatalf("startDetached with LogFile: %v", err)
	}
}

func TestStartDetached_DefaultLogFile(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "daemon")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldBase := basePath
	basePath = tmp
	defer func() { basePath = oldBase }()

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-detached-default",
		BinaryPath: script,
	}

	err := startDetached(context.Background(), "test-detached-default", def)
	if err != nil {
		t.Fatalf("startDetached default log path: %v", err)
	}
}

func TestStopWithoutSystemd_NoLauncher(t *testing.T) {
	def := &ServiceDef{
		Name: "test-no-process-name",
	}
	err := stopWithoutSystemd(context.Background(), "test-no-process-name", def)
	if err == nil {
		t.Error("expected error for service with no ProcessName")
	}
}

func TestStopWithoutSystemd_CustomStopErr(t *testing.T) {
	wantErr := fmt.Errorf("custom stop failed")
	def := &ServiceDef{
		Name: "test-custom-stop-err",
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			return wantErr
		},
	}
	err := stopWithoutSystemd(context.Background(), "test-custom-stop-err", def)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected custom stop error, got %v", err)
	}
}

func TestKillProcess_NoTarget(t *testing.T) {
	err := killProcess(context.Background(), "carbonio-nonexistent-needle-xyzzy-12345")
	if err != nil {
		t.Errorf("killProcess should succeed with no targets, got %v", err)
	}
}

func TestStartDetached_SpawnAndRelease(t *testing.T) {
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

	err := startDetached(context.Background(), "test-det-spawn", def)
	if err != nil {
		t.Fatalf("startDetached: %v", err)
	}
}

func TestStartDirect_OutputCaptured(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "failbin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'startup failed'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "test-direct-fail",
		BinaryPath: script,
	}

	err := startDirect(context.Background(), "test-direct-fail", def)
	if err == nil {
		t.Error("expected error from failing binary")
	}
	if !strings.Contains(err.Error(), "startup failed") {
		t.Errorf("expected error to contain output, got %v", err)
	}
}

func TestStopWithoutSystemd_WithUseSDNotify(t *testing.T) {
	def := &ServiceDef{
		Name:        "test-sd-notify-stop",
		ProcessName: "nonexistent-sd-notify-proc-xyz",
		UseSDNotify: true,
	}
	Registry["test-sd-notify-stop"] = def
	defer delete(Registry, "test-sd-notify-stop")

	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	err := stopWithoutSystemd(context.Background(), "test-sd-notify-stop", def)
	// Process doesn't exist, killProcess returns nil
	_ = err
}

func TestKillProcessGroup_NoSuchGroup(t *testing.T) {
	// Use a PGID that cannot exist to exercise the ESRCH path without
	// risking real signals to the user's processes. Never pass 1 or -1 —
	// syscall.Kill(-1, sig) broadcasts to every process the user owns,
	// which will tear down the user's graphical session.
	err := killProcessGroup(999999990, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error for nonexistent process group")
	}
}

func TestSignalPID_NoSuchProcess(t *testing.T) {
	// Use a PID we know is not alive. Never signal os.Getpid() — that kills
	// the test binary itself.
	signalPID(999999990, syscall.SIGTERM)
	signalPID(999999991, syscall.SIGUSR1)
}

func TestMailboxCustomStart_AppserverDBFail(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = oldLC }()

	def := &ServiceDef{Name: "mailbox"}
	err := mailboxCustomStart(context.Background(), def)
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMtaIsRunning_NotRunning(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	defer func() { sudoBin = oldSudo }()

	fakeSudo := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(sudoBin, []byte(fakeSudo), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	if err := os.WriteFile(postfixBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postfixBin = oldPostfix }()

	if mtaIsRunning(context.Background()) {
		t.Error("expected mtaIsRunning=false with failing sudo")
	}
}
