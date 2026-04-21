// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// notifySocketDir is the directory for sd_notify sockets. Overridden in
// tests to use a writable temp directory.
var notifySocketDir = pidDir

// sdNotifySocketPath returns the NOTIFY_SOCKET path used for a service. The
// path is service-addressable (not per-invocation) so the stop-side listener
// can recreate it and receive STOPPING=1 datagrams from the daemon that was
// started by an earlier, already-exited configd CLI invocation.
func sdNotifySocketPath(service string) string {
	return fmt.Sprintf("%s/notify-%s.sock", notifySocketDir, service)
}

// startWithSDNotify starts cmd and waits for the process to signal readiness via
// the sd_notify protocol (READY=1 datagram). It:
//  1. Creates a Unix datagram socket at pidDir/notify-<service>.sock
//  2. Injects NOTIFY_SOCKET into the child environment (overriding any inherited value)
//  3. Starts the process
//  4. Blocks until READY=1 is received or the 30-second timeout expires
//
// After this function returns (success or failure), cmd.Process is set if the
// process was launched, so callers can still write a PID file.
//
// This mirrors systemd's Type=notify readiness detection for the legacy
// (non-systemd) control path.
func startWithSDNotify(ctx context.Context, cmd *exec.Cmd, service string) error {
	socketPath := sdNotifySocketPath(service)

	// A stale socket from a previous configd invocation would make
	// ListenUnixgram fail with EADDRINUSE; the start-side owns the path so
	// clearing it here is safe.
	_ = os.Remove(socketPath)

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("create notify socket for %s: %w", service, err)
	}

	defer func() {
		_ = conn.Close()
		_ = os.Remove(socketPath)
	}()

	env := make([]string, 0, len(os.Environ())+1)

	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NOTIFY_SOCKET=") {
			env = append(env, e)
		}
	}

	env = append(env, "NOTIFY_SOCKET="+socketPath)
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", service, err)
	}

	return waitForSDNotify(ctx, conn, service)
}

// awaitSDNotifyStopping opens the service's persistent notify socket and
// waits for a STOPPING=1 datagram from the daemon, logging the observation
// as soon as it arrives. Run as a goroutine from the stop path alongside
// killProcess — it provides operator visibility ("the daemon engaged its
// graceful-shutdown hook") without altering the SIGTERM→SIGKILL critical
// path; ctx cancellation bounds the goroutine's lifetime to the stop call.
//
// Best effort: if the socket cannot be opened (another observer racing,
// filesystem perms) we return silently. The shutdown still proceeds via
// killProcess.
func awaitSDNotifyStopping(ctx context.Context, service string) {
	socketPath := sdNotifySocketPath(service)
	// Drop any stale file left by the start-side's defer before the daemon
	// had a chance to call sendmsg — stale inodes would rebind to an old
	// sender lineage. The daemon's address is path-based, so recreating is
	// transparent to it.
	_ = os.Remove(socketPath)

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		logger.DebugContext(ctx, "sd_notify shutdown observer not started",
			"service", service, "error", err)

		return
	}

	defer func() {
		_ = conn.Close()
		_ = os.Remove(socketPath)
	}()

	buf := make([]byte, 512)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}

		if strings.Contains(string(buf[:n]), "STOPPING=1") {
			logger.InfoContext(ctx, "Graceful shutdown acknowledged by daemon", "service", service)

			return
		}
	}
}

// waitForSDNotify reads datagrams from conn until READY=1 is received,
// the context is cancelled, or 30 seconds elapse. A 1-second read deadline
// is used per iteration so context cancellation is checked regularly.
func waitForSDNotify(ctx context.Context, conn *net.UnixConn, service string) error {
	deadline := time.Now().Add(30 * time.Second)
	buf := make([]byte, 512)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(time.Second))

		n, _, err := conn.ReadFrom(buf)
		if err == nil {
			if strings.Contains(string(buf[:n]), "READY=1") {
				return nil
			}

			continue
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not signal READY=1 within 30 seconds", service)
		}

		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}

		return fmt.Errorf("notify socket read error for %s: %w", service, err)
	}
}
