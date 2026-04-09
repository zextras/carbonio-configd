// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain sets notifySocketDir to a writable temp directory so tests don't
// require /run/carbonio to exist.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "configd-sd-notify-test-*")
	if err != nil {
		panic("failed to create temp dir for sd_notify tests: " + err.Error())
	}

	defer func() { _ = os.RemoveAll(dir) }()

	notifySocketDir = dir

	os.Exit(m.Run())
}

// expectedSocketPath returns the socket path startWithSDNotify will create for
// the given service name (same process, so same PID).
func expectedSocketPath(service string) string {
	return fmt.Sprintf("%s/notify-%s-%d.sock", notifySocketDir, service, os.Getpid())
}

// sendReady sends a READY=1 datagram to the given Unix socket path, retrying
// until the socket exists or the deadline is reached.
func sendReady(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := net.Dial("unixgram", socketPath)
		if err != nil {
			time.Sleep(10 * time.Millisecond)

			continue
		}

		_, writeErr := conn.Write([]byte("READY=1\n"))
		_ = conn.Close()

		if writeErr != nil {
			t.Fatalf("send READY=1 to %s: %v", socketPath, writeErr)
		}

		return
	}

	t.Fatalf("socket %s never appeared within 2 seconds", socketPath)
}

// TestStartWithSDNotify_Ready verifies that startWithSDNotify returns nil when
// READY=1 is sent to the notify socket.
func TestStartWithSDNotify_Ready(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "5")
	socketPath := expectedSocketPath("test-ready")

	done := make(chan error, 1)

	go func() {
		done <- startWithSDNotify(context.Background(), cmd, "test-ready")
	}()

	sendReady(t, socketPath)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startWithSDNotify to return")
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// TestStartWithSDNotify_Timeout verifies that startWithSDNotify returns an error
// when no READY=1 datagram is received within the deadline.
func TestStartWithSDNotify_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 30s timeout test in short mode")
	}

	cmd := exec.Command("sleep", "60")

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	err := startWithSDNotify(ctx, cmd, "test-timeout")
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}

	if !strings.Contains(err.Error(), "READY=1") {
		t.Errorf("expected error about READY=1, got: %v", err)
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// TestStartWithSDNotify_ContextCancel verifies that startWithSDNotify returns
// context.Canceled when the context is cancelled before READY=1 is received.
func TestStartWithSDNotify_ContextCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "60")

	ctx, cancel := context.WithCancel(context.Background())
	socketPath := expectedSocketPath("test-cancel")

	done := make(chan error, 1)

	go func() {
		done <- startWithSDNotify(ctx, cmd, "test-cancel")
	}()

	// Wait until the socket exists (startWithSDNotify is in the wait loop).
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startWithSDNotify to return after cancel")
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// TestStartWithSDNotify_NotifySocketInEnv verifies that cmd.Env contains
// NOTIFY_SOCKET pointing to the expected socket path.
func TestStartWithSDNotify_NotifySocketInEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "5")
	socketPath := expectedSocketPath("test-env")

	done := make(chan error, 1)

	go func() {
		done <- startWithSDNotify(context.Background(), cmd, "test-env")
	}()

	sendReady(t, socketPath)
	<-done

	notifyVal := ""
	for _, e := range cmd.Env {
		if val, ok := strings.CutPrefix(e, "NOTIFY_SOCKET="); ok {
			notifyVal = val

			break
		}
	}

	if notifyVal == "" {
		t.Fatal("NOTIFY_SOCKET not set in cmd.Env")
	}

	if notifyVal != socketPath {
		t.Errorf("NOTIFY_SOCKET = %q, want %q", notifyVal, socketPath)
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// TestStartWithSDNotify_SocketCleanedUp verifies the socket file is removed
// after startWithSDNotify returns.
func TestStartWithSDNotify_SocketCleanedUp(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmd := exec.Command("sleep", "5")
	socketPath := expectedSocketPath("test-cleanup")

	done := make(chan error, 1)

	go func() {
		done <- startWithSDNotify(context.Background(), cmd, "test-cleanup")
	}()

	sendReady(t, socketPath)
	<-done

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket %s still exists after startWithSDNotify returned", socketPath)
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
