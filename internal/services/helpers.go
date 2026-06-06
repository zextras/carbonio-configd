// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// openLogFile opens (or creates) a log file for append writing with mode 0640.
func openLogFile(path string) (*os.File, error) {
	//nolint:gosec // log file path is from internal service registry
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}

	return fd, nil
}

// signalViaPidfile reads a pid from a file, sends the specified signal to the
// process, and removes the pidfile on success. If the pidfile does not exist,
// the service is considered already stopped and nil is returned.
//
//nolint:unparam // serviceName varies in real callers; tests happen to share one value
func signalViaPidfile(ctx context.Context, pidFile, serviceName string, sig syscall.Signal) error {
	//nolint:gosec // pidfile path is from internal service registry
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.InfoContext(ctx, "Service already stopped (no pidfile)", "service", serviceName, "pidfile", pidFile)

			return nil
		}

		return fmt.Errorf("failed to read pidfile %s: %w", pidFile, err)
	}

	pidStr := strings.TrimSpace(string(data))

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return fmt.Errorf("invalid pid in %s: %s", pidFile, pidStr)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	logger.InfoContext(ctx, "Sending signal to service via pidfile", "service", serviceName, "pid", pid, "signal", sig)

	if err := proc.Signal(sig); err != nil {
		return fmt.Errorf("failed to signal process %d: %w", pid, err)
	}

	_ = os.Remove(pidFile)

	return nil
}

// gracefulStopViaPidfile stops a process cleanly: it sends SIGTERM, waits up to
// timeout for the process to exit (polling with signal 0), and escalates to
// SIGKILL only if the process is still alive at the deadline. The pidfile is
// removed once the process is gone.
//
// A clean SIGTERM lets daemons like slapd close their backing store (LMDB)
// properly, avoiding recovery work on the next start that can stall readiness.
// If the pidfile is absent the service is considered already stopped.
func gracefulStopViaPidfile(ctx context.Context, pidFile, serviceName string, timeout time.Duration) error {
	//nolint:gosec // pidfile path is from internal service registry
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.InfoContext(ctx, "Service already stopped (no pidfile)", "service", serviceName, "pidfile", pidFile)

			return nil
		}

		return fmt.Errorf("failed to read pidfile %s: %w", pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid pid in %s: %s", pidFile, strings.TrimSpace(string(data)))
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	logger.InfoContext(ctx, "Stopping service gracefully (SIGTERM)", "service", serviceName, "pid", pid)

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// ESRCH: process already gone — treat as stopped.
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			_ = os.Remove(pidFile)

			return nil
		}

		return fmt.Errorf("failed to SIGTERM process %d: %w", pid, err)
	}

	if waitForProcessExit(ctx, pid, timeout) {
		_ = os.Remove(pidFile)

		return nil
	}

	// Still alive at the deadline: escalate to SIGKILL.
	logger.WarnContext(ctx, "Service did not exit after SIGTERM, sending SIGKILL",
		"service", serviceName, "pid", pid, "timeout", timeout)

	if err := proc.Signal(syscall.SIGKILL); err != nil &&
		!errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to SIGKILL process %d: %w", pid, err)
	}

	_ = os.Remove(pidFile)

	return nil
}

// waitForProcessExit polls (signal 0) until pid is gone, ctx is cancelled, or
// timeout elapses. Returns true if the process exited within the budget.
func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)

	defer ticker.Stop()

	for {
		if !processAlive(pid) {
			return true
		}

		if time.Now().After(deadline) {
			return false
		}

		select {
		case <-ctx.Done():
			return !processAlive(pid)
		case <-ticker.C:
		}
	}
}

// isTruthy returns true for "TRUE" (case-insensitive) or "1".
func isTruthy(val string) bool {
	return strings.EqualFold(val, "TRUE") || val == "1"
}
