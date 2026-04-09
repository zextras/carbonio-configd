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
)

// notifySocketDir is the directory for temporary sd_notify sockets.
// Overridden in tests to use a writable temp directory.
var notifySocketDir = pidDir

// startWithSDNotify starts cmd and waits for the process to signal readiness via
// the sd_notify protocol (READY=1 datagram). It:
//  1. Creates a temporary Unix datagram socket at pidDir/notify-<service>-<pid>.sock
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
	socketPath := fmt.Sprintf("%s/notify-%s-%d.sock", notifySocketDir, service, os.Getpid())

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("create notify socket for %s: %w", service, err)
	}

	defer func() {
		_ = conn.Close()
		_ = os.Remove(socketPath)
	}()

	// Override any inherited NOTIFY_SOCKET (e.g. if configd itself runs under systemd).
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
