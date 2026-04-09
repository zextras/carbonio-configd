// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
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
