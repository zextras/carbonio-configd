// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// startWithSDNotify starts cmd and waits for the process to signal readiness via
// the sd_notify protocol (READY=1 datagram). It:
//  1. Creates a Unix datagram socket at c.p.run/notify-<service>.sock
//  2. Injects NOTIFY_SOCKET into the child environment (overriding any inherited value)
//  3. Starts the process
//  4. Blocks until READY=1 is received or the 30-second timeout expires
//
// After this function returns (success or failure of the readiness wait) the
// started process is returned, so callers can still release it or write a PID
// file; it is nil only when the process could not be started.
//
// This mirrors systemd's Type=notify readiness detection for the legacy
// (non-systemd) control path.
func (c *Controller) startWithSDNotify(ctx context.Context, cmd *sys.Cmd, service string) (sys.Process, error) {
	socketPath := c.p.sdNotifySocket(service)

	// A stale socket from a previous configd invocation would make
	// ListenUnixgram fail with EADDRINUSE; the start-side owns the path so
	// clearing it here is safe.
	_ = c.env.FS.Remove(socketPath)

	conn, err := net.ListenUnixgram(socketFamilyUnixgram, &net.UnixAddr{Name: socketPath, Net: socketFamilyUnixgram})
	if err != nil {
		return nil, fmt.Errorf("create notify socket for %s: %w", service, err)
	}

	defer func() {
		_ = conn.Close()
		_ = c.env.FS.Remove(socketPath)
	}()

	// Env is layered over the inherited environment and replaces any
	// inherited NOTIFY_SOCKET.
	cmd.Env = append(cmd.Env, "NOTIFY_SOCKET="+socketPath)

	proc, err := c.env.Exec.Start(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", service, err)
	}

	// Watch for early child exit so we fail fast instead of burning the full
	// readiness timeout when slapd dies during startup (e.g. it cannot bind its
	// listener because a previous instance has not released the port/socket).
	exited := make(chan error, 1)

	go func() { exited <- proc.Wait() }()

	return proc, c.waitForSDNotify(ctx, conn, service, exited)
}

// sdNotifyReadyTimeout bounds how long startWithSDNotify waits for READY=1.
const sdNotifyReadyTimeout = 30 * time.Second

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
func (c *Controller) awaitSDNotifyStopping(ctx context.Context, service string) {
	socketPath := c.p.sdNotifySocket(service)
	// Drop any stale file left by the start-side's defer before the daemon
	// had a chance to call sendmsg — stale inodes would rebind to an old
	// sender lineage. The daemon's address is path-based, so recreating is
	// transparent to it.
	_ = c.env.FS.Remove(socketPath)

	conn, err := net.ListenUnixgram(socketFamilyUnixgram, &net.UnixAddr{Name: socketPath, Net: socketFamilyUnixgram})
	if err != nil {
		logger.DebugContext(ctx, "sd_notify shutdown observer not started",
			"service", service, "error", err)

		return
	}

	defer func() {
		_ = conn.Close()
		_ = c.env.FS.Remove(socketPath)
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

		if strings.Contains(string(buf[:n]), sdnotify.Stopping) {
			logger.InfoContext(ctx, "Graceful shutdown acknowledged by daemon", "service", service)

			return
		}
	}
}

// waitForSDNotify reads datagrams from conn until READY=1 is received,
// the context is cancelled, or 30 seconds elapse. A 1-second read deadline
// is used per iteration so context cancellation is checked regularly.
func (c *Controller) waitForSDNotify(
	ctx context.Context, conn *net.UnixConn, service string, exited <-chan error,
) error {
	deadline := c.env.Clock.Now().Add(sdNotifyReadyTimeout)
	buf := make([]byte, 512)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exited:
			// Child process exited before signaling READY=1. Fail immediately
			// rather than waiting out the full deadline.
			return fmt.Errorf("%s exited during startup before signaling READY=1: %w", service, waitErr)
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(time.Second))

		n, _, err := conn.ReadFrom(buf)
		if err == nil {
			if strings.Contains(string(buf[:n]), sdnotify.Ready) {
				return nil
			}

			continue
		}

		if c.env.Clock.Now().After(deadline) {
			return fmt.Errorf("%s did not signal READY=1 within 30 seconds", service)
		}

		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}

		return fmt.Errorf("notify socket read error for %s: %w", service, err)
	}
}
