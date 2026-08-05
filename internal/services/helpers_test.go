// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// --- openLogFile ---

func TestOpenLogFile_CreatesFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.log")

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile() returned error: %v", err)
	}
	defer f.Close()

	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected log file to exist after openLogFile: %v", statErr)
	}
}

func TestOpenLogFile_AppendsToExisting(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "existing.log")

	// Write initial content
	if err := os.WriteFile(path, []byte("initial\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile() returned error: %v", err)
	}
	if _, writeErr := f.WriteString("appended\n"); writeErr != nil {
		t.Fatalf("WriteString failed: %v", writeErr)
	}
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "initial\nappended\n" {
		t.Errorf("expected appended content, got %q", string(data))
	}
}

func TestOpenLogFile_InvalidPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, err := openLogFile("/nonexistent/directory/test.log")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestGracefulStopViaPidfile_NoPidfile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := gracefulStopViaPidfile(context.Background(), "/nonexistent/x.pid", "svc", time.Second)
	if err != nil {
		t.Errorf("missing pidfile should be treated as stopped, got: %v", err)
	}
}

func TestGracefulStopViaPidfile_InvalidPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "bad.pid")
	if err := os.WriteFile(pidFile, []byte("notapid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gracefulStopViaPidfile(context.Background(), pidFile, "svc", time.Second); err == nil {
		t.Error("expected error for non-numeric pid")
	}
}

// TestGracefulStopViaPidfile_TermExits verifies a child that handles SIGTERM
// exits within the timeout (no SIGKILL escalation) and the pidfile is removed.
func TestGracefulStopViaPidfile_TermExits(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: spawns a child process")
	}
	// sh that exits on SIGTERM (default disposition) — sleeps otherwise.
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "child.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	go func() { _, _ = cmd.Process.Wait() }()

	start := time.Now()
	if err := gracefulStopViaPidfile(context.Background(), pidFile, "svc", 5*time.Second); err != nil {
		t.Fatalf("graceful stop: %v", err)
	}

	// SIGTERM kills `sleep` immediately, so this must return well under the
	// timeout (proving it did not wait the full budget / escalate needlessly).
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("graceful stop took %v, expected fast SIGTERM exit", elapsed)
	}

	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile removed after stop")
	}
}

// TestWaitForProcessExit_AlreadyGone verifies a non-existent pid is reported as
// exited immediately.
func TestWaitForProcessExit_AlreadyGone(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// PID 0x7FFFFFFF is effectively never a live process.
	if !waitForProcessExit(context.Background(), 0x7FFFFFFF, time.Second) {
		t.Error("expected non-existent pid to be reported as exited")
	}
}

// TestWaitForProcessExit_ContextCancelled verifies waitForProcessExit returns
// the alive status when the context is cancelled while the process is still
// running.
func TestWaitForProcessExit_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: starts a real process")
	}
	// Start a long-running sleep process
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the ctx.Done() branch fires
	cancel()

	if waitForProcessExit(ctx, cmd.Process.Pid, 5*time.Second) {
		t.Error("expected false when context cancelled while process is alive")
	}
}

// TestGracefulStopViaPidfile_SigTermEsrch verifies that when the target process
// does not exist (ESRCH / ErrProcessDone on SIGTERM), the pidfile is removed
// and nil is returned.
func TestGracefulStopViaPidfile_SigTermEsrch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "gone.pid")
	// PID 2147483647 is the max int32 and will never be a live process.
	if err := os.WriteFile(pidFile, []byte("2147483647\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := gracefulStopViaPidfile(context.Background(), pidFile, "svc", time.Second)
	if err != nil {
		t.Errorf("expected nil for non-existent pid, got: %v", err)
	}
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after ESRCH")
	}
}

// TestGracefulStopViaPidfile_KillEscalation verifies that a process ignoring
// SIGTERM is escalated to SIGKILL after the timeout expires.
func TestGracefulStopViaPidfile_KillEscalation(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: spawns a child process")
	}
	// Shell that explicitly ignores SIGTERM.
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "stubborn.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Very short timeout so SIGKILL escalation is triggered quickly.
	err := gracefulStopViaPidfile(context.Background(), pidFile, "svc", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("expected nil after SIGKILL escalation, got: %v", err)
	}
	// Reap the now-dead process.
	_ = cmd.Wait()
}
