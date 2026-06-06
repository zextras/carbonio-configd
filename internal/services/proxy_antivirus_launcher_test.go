// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStartThenProbePidfile_NoPidfileConfigured verifies the guard rejects a
// def without a PidFile (the readiness signal would be undefined).
func TestStartThenProbePidfile_NoPidfileConfigured(t *testing.T) {
	def := &ServiceDef{Name: "test-nopid"}

	err := startThenProbePidfile(context.Background(), def, "/bin/true", nil)
	if err == nil || !strings.Contains(err.Error(), "no pidfile") {
		t.Errorf("expected 'no pidfile' error, got %v", err)
	}
}

// TestStartThenProbePidfile_ReadyOnLivePidfile verifies that once the launched
// binary writes a pidfile pointing at a live process, the probe returns nil
// without waiting out the timeout. The launcher process forking-and-exiting is
// NOT treated as failure (mirrors nginx/clamd self-daemonizing).
func TestStartThenProbePidfile_ReadyOnLivePidfile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: spawns a child process")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "svc.pid")

	oldLog := logPath
	logPath = tmp

	defer func() { logPath = oldLog }()

	// A long-lived helper (sleep) records its own PID into the pidfile, then
	// the launcher returns immediately (no fork needed — sleep stays alive, so
	// pidFromPidFile sees a live PID). We write the pidfile to the sleeper's PID
	// by launching a shell that prints $$ and execs sleep.
	def := &ServiceDef{Name: "test-ready", PidFile: pidFile}

	script := "echo $$ > " + pidFile + "; exec sleep 30"

	start := time.Now()
	err := startThenProbePidfile(context.Background(), def, "/bin/sh", []string{"-c", script})

	if err != nil {
		t.Fatalf("startThenProbePidfile returned error: %v", err)
	}

	if elapsed := time.Since(start); elapsed > daemonReadyTimeout {
		t.Errorf("probe took %v, expected fast return on live pidfile", elapsed)
	}

	// Clean up the spawned sleeper.
	if data, readErr := os.ReadFile(pidFile); readErr == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil {
			if proc, _ := os.FindProcess(pid); proc != nil {
				_ = proc.Kill()
			}
		}
	}
}

// TestStartThenProbePidfile_TimeoutWhenNoPidfile verifies the probe fails (does
// not hang forever) when the binary never writes a pidfile.
func TestStartThenProbePidfile_TimeoutWhenNoPidfile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: spawns a child process and waits for a bounded timeout")
	}

	tmp := t.TempDir()

	oldLog := logPath
	oldTimeout := daemonReadyTimeout
	logPath = tmp
	daemonReadyTimeout = 300 * time.Millisecond

	defer func() {
		logPath = oldLog
		daemonReadyTimeout = oldTimeout
	}()

	def := &ServiceDef{Name: "test-timeout", PidFile: filepath.Join(tmp, "never.pid")}

	start := time.Now()
	err := startThenProbePidfile(context.Background(), def, "/bin/sleep", []string{"5"})

	if err == nil || !strings.Contains(err.Error(), "did not write a live pidfile") {
		t.Errorf("expected timeout error, got %v", err)
	}

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("probe took %v, expected to fail near the (shortened) timeout", elapsed)
	}
}
