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
	"syscall"
	"testing"
	"time"
)

// --- isTruthy ---

func TestIsTruthy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"TRUE", true},
		{"true", true},
		{"True", true},
		{"1", true},
		{"FALSE", false},
		{"false", false},
		{"0", false},
		{"yes", false},
		{"", false},
		{"2", false},
		{"TRUE ", false}, // trailing space — not trimmed by isTruthy
	}

	for _, tt := range tests {
		got := isTruthy(tt.input)
		if got != tt.want {
			t.Errorf("isTruthy(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

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

// --- signalViaPidfile ---

func TestSignalViaPidfile_NoSuchFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctx := context.Background()
	err := signalViaPidfile(ctx, "/nonexistent/path/service.pid", "testsvc", syscall.SIGTERM)
	// Missing pidfile is treated as "already stopped" — no error.
	if err != nil {
		t.Errorf("expected nil for missing pidfile, got: %v", err)
	}
}

func TestSignalViaPidfile_InvalidPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "bad.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err := signalViaPidfile(ctx, pidFile, "testsvc", syscall.SIGTERM)
	if err == nil {
		t.Error("expected error for non-numeric pid in pidfile")
	}
}

func TestSignalViaPidfile_ValidPidSignalSent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Use the current process PID — sending SIGCONT to ourselves is safe (no-op).
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "self.pid")
	selfPid := os.Getpid()

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(selfPid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err := signalViaPidfile(ctx, pidFile, "testsvc", syscall.SIGCONT)
	if err != nil {
		t.Errorf("unexpected error sending SIGCONT to self: %v", err)
	}

	// pidfile should be removed on success
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after successful signal")
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
