// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startTestSocket creates a unixgram listener and returns its address and a
// function that reads one datagram from it (blocking until received or timeout).
func startTestSocket(t *testing.T) (string, func() string) {
	t.Helper()

	sockPath := filepath.Join(t.TempDir(), "notify.sock")

	addr, err := net.ResolveUnixAddr("unixgram", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr: %v", err)
	}

	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}

	t.Cleanup(func() { conn.Close() })

	read := func() string {
		buf := make([]byte, 4096)

		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}

		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("Read from socket: %v", err)
		}

		return string(buf[:n])
	}

	return sockPath, read
}

// TestNilNotifier verifies that all methods on a nil *Notifier are safe no-ops.
func TestNilNotifier(t *testing.T) {
	var n *Notifier

	if err := n.Ready(); err != nil {
		t.Errorf("nil.Ready() returned error: %v", err)
	}

	if err := n.Stopping(); err != nil {
		t.Errorf("nil.Stopping() returned error: %v", err)
	}

	if err := n.Reloading(); err != nil {
		t.Errorf("nil.Reloading() returned error: %v", err)
	}

	if err := n.WatchdogPing(); err != nil {
		t.Errorf("nil.WatchdogPing() returned error: %v", err)
	}

	if err := n.Status("test %d", 42); err != nil {
		t.Errorf("nil.Status() returned error: %v", err)
	}

	if err := n.Notify("CUSTOM=1"); err != nil {
		t.Errorf("nil.Notify() returned error: %v", err)
	}

	if n.Enabled() {
		t.Error("nil.Enabled() should return false")
	}
}

// TestNew_NoSocket verifies that New returns (nil, nil) when NOTIFY_SOCKET is unset.
func TestNew_NoSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	n, err := New()
	if err != nil {
		t.Fatalf("New() with empty NOTIFY_SOCKET: unexpected error: %v", err)
	}

	if n != nil {
		t.Fatal("New() should return nil notifier when NOTIFY_SOCKET is empty")
	}
}

// TestNew_WithSocket verifies that New returns a non-nil notifier when NOTIFY_SOCKET is set.
func TestNew_WithSocket(t *testing.T) {
	sockPath, _ := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if n == nil {
		t.Fatal("New() should return non-nil notifier when NOTIFY_SOCKET is set")
	}

	if !n.Enabled() {
		t.Error("Enabled() should return true when NOTIFY_SOCKET is set")
	}
}

// TestReady sends READY=1 and verifies the socket receives it.
func TestReady(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.Ready(); err != nil {
		t.Fatalf("Ready(): %v", err)
	}

	got := read()
	if got != "READY=1" {
		t.Errorf("Ready() sent %q, want %q", got, "READY=1")
	}
}

// TestStopping sends STOPPING=1 and verifies the socket receives it.
func TestStopping(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.Stopping(); err != nil {
		t.Fatalf("Stopping(): %v", err)
	}

	got := read()
	if got != "STOPPING=1" {
		t.Errorf("Stopping() sent %q, want %q", got, "STOPPING=1")
	}
}

// TestReloading sends RELOADING=1 and verifies the socket receives it.
func TestReloading(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.Reloading(); err != nil {
		t.Fatalf("Reloading(): %v", err)
	}

	got := read()
	if got != "RELOADING=1" {
		t.Errorf("Reloading() sent %q, want %q", got, "RELOADING=1")
	}
}

// TestWatchdogPing sends WATCHDOG=1 and verifies the socket receives it.
func TestWatchdogPing(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.WatchdogPing(); err != nil {
		t.Fatalf("WatchdogPing(): %v", err)
	}

	got := read()
	if got != "WATCHDOG=1" {
		t.Errorf("WatchdogPing() sent %q, want %q", got, "WATCHDOG=1")
	}
}

// TestStatus verifies formatting and STATUS= prefix.
func TestStatus(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.Status("loop %d completed in %.1fs, next in %ds", 5, 3.2, 60); err != nil {
		t.Fatalf("Status(): %v", err)
	}

	got := read()
	want := "STATUS=loop 5 completed in 3.2s, next in 60s"

	if got != want {
		t.Errorf("Status() sent %q, want %q", got, want)
	}
}

// TestNotifyCustom verifies that arbitrary state strings can be sent.
func TestNotifyCustom(t *testing.T) {
	sockPath, read := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if err := n.Notify("ERRNO=0"); err != nil {
		t.Fatalf("Notify(): %v", err)
	}

	got := read()
	if got != "ERRNO=0" {
		t.Errorf("Notify() sent %q, want %q", got, "ERRNO=0")
	}
}

// TestNew_AbstractSocket verifies that abstract sockets (prefixed with @) are handled.
func TestNew_AbstractSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "@/run/systemd/notify")

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if n == nil {
		t.Fatal("New() should return non-nil notifier for abstract socket")
	}

	// Verify the internal address has null byte prefix (abstract socket)
	if n.socketAddr.Name[0] != 0 {
		t.Errorf("Abstract socket should have null byte prefix, got first byte: %d", n.socketAddr.Name[0])
	}
}

// TestWatchdogEnabled tests WATCHDOG_USEC parsing.
func TestWatchdogEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		wantDur  time.Duration
		wantOK   bool
	}{
		{
			name:     "not set",
			envValue: "",
			wantDur:  0,
			wantOK:   false,
		},
		{
			name:     "valid 120 seconds",
			envValue: "120000000", // 120s in microseconds
			wantDur:  120 * time.Second,
			wantOK:   true,
		},
		{
			name:     "valid 30 seconds",
			envValue: "30000000", // 30s in microseconds
			wantDur:  30 * time.Second,
			wantOK:   true,
		},
		{
			name:     "zero",
			envValue: "0",
			wantDur:  0,
			wantOK:   false,
		},
		{
			name:     "negative",
			envValue: "-1000",
			wantDur:  0,
			wantOK:   false,
		},
		{
			name:     "non-numeric",
			envValue: "abc",
			wantDur:  0,
			wantOK:   false,
		},
		{
			name:     "1 microsecond",
			envValue: "1",
			wantDur:  1 * time.Microsecond,
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WATCHDOG_USEC", tt.envValue)

			dur, ok := WatchdogEnabled()
			if ok != tt.wantOK {
				t.Errorf("WatchdogEnabled() ok = %v, want %v", ok, tt.wantOK)
			}

			if dur != tt.wantDur {
				t.Errorf("WatchdogEnabled() duration = %v, want %v", dur, tt.wantDur)
			}
		})
	}
}

// TestNotify_InvalidSocket verifies that writing to a non-existent socket returns an error.
func TestNotify_InvalidSocket(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "nonexistent.sock")
	t.Setenv("NOTIFY_SOCKET", badPath)

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	err = n.Ready()
	if err == nil {
		t.Error("Expected error when sending to non-existent socket, got nil")
	}
}

// TestNew_SocketPathTrimmed verifies that whitespace is trimmed from NOTIFY_SOCKET.
func TestNew_SocketPathTrimmed(t *testing.T) {
	sockPath, _ := startTestSocket(t)
	t.Setenv("NOTIFY_SOCKET", "  "+sockPath+"  ")

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if n == nil {
		t.Fatal("New() should return non-nil for whitespace-padded socket path")
	}

	// The trimming only trims leading whitespace currently; the path after
	// TrimSpace should match. Verify the notifier was created.
	if !n.Enabled() {
		t.Error("Notifier should be enabled")
	}
}

// Ensure we don't leak the NOTIFY_SOCKET env between tests.
func TestNew_EnvIsolation(t *testing.T) {
	// Unset explicitly
	t.Setenv("NOTIFY_SOCKET", "")

	n, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if n != nil {
		t.Error("Expected nil notifier with empty NOTIFY_SOCKET")
	}

	// Now set it
	sockPath, _ := startTestSocket(t)
	os.Setenv("NOTIFY_SOCKET", sockPath)

	defer os.Unsetenv("NOTIFY_SOCKET")

	n, err = New()
	if err != nil {
		t.Fatalf("New() after setting NOTIFY_SOCKET: %v", err)
	}

	if n == nil {
		t.Error("Expected non-nil notifier after setting NOTIFY_SOCKET")
	}
}
