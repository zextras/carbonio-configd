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

// gracefulStopViaPidfile stops a process cleanly: it sends SIGTERM, waits up to
// timeout for the process to exit (polling with signal 0), and escalates to
// SIGKILL only if the process is still alive at the deadline. The pidfile is
// removed once the process is gone.
//
// A clean SIGTERM lets daemons like slapd close their backing store (LMDB)
// properly, avoiding recovery work on the next start that can stall readiness.
// If the pidfile is absent the service is considered already stopped.
//
// The initial SIGTERM and the SIGKILL escalation are intentionally NOT
// hoisted to the shared signalPID helper (used by killProcess and
// stopMysqldByPidFile): signalPID is silent/best-effort by design — its
// targets already come from a fresh /proc scan, so a signal failure just
// means the process raced away — whereas callers here (ldapCustomStop)
// need genuine failures (e.g. EPERM signaling the wrong UID) surfaced as
// an error rather than swallowed. Only the polling loop is shared, via
// waitForProcessExit -> pollUntilExit (cli_process.go).
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
// Thin wrapper around the shared pollUntilExit core (cli_process.go) at the
// original 50ms cadence.
func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	return pollUntilExit(ctx, pid, timeout, 50*time.Millisecond)
}
