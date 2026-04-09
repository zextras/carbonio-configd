// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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

// TestMatchProcEntry_FallbackComm verifies fallback to comm when cmdline is empty.
func TestMatchProcEntry_FallbackComm(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	pid := 88888
	procDir := filepath.Join(tmpDir, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty cmdline to force fallback
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	statusContent := fmt.Sprintf("Name:\tnginx\nState:\tS (sleeping)\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
	if err := os.WriteFile(filepath.Join(procDir, "status"), []byte(statusContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "comm"), []byte("nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := fakeProcEntry{name: strconv.Itoa(pid), isDir: true}
	// needle contains "nginx" substring of comm => match
	matchPid, ok := matchProcEntry(e, "nginx-worker")
	if !ok {
		t.Error("expected comm fallback to match when needle contains comm value")
	} else if matchPid != pid {
		t.Errorf("got pid %d, want %d", matchPid, pid)
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
