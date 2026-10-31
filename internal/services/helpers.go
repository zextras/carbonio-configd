// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// openLogFile opens (or creates) a log file for append writing with mode 0640.
func (c *Controller) openLogFile(path string) (sys.File, error) {
	fd, err := c.env.FS.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}

	return fd, nil
}

// gracefulStopViaPidfile stops a service cleanly: it sends SIGTERM to the
// process named by pidFile, waits up to timeout for it to exit (polling with
// signal 0), and escalates to SIGKILL only if it is still alive at the
// deadline. The pidfile is removed once the process is gone.
//
// A clean SIGTERM lets daemons like slapd close their backing store (LMDB)
// properly, avoiding recovery work on the next start that can stall readiness.
// If the pidfile is absent the service is considered already stopped.
//
// processName is the service's identity (ServiceDef.ProcessName, see
// procident.go). The pidfile PID is signaled only when it is alive AND of that
// identity, so a stale pidfile whose PID was recycled by an unrelated process
// never gets that process killed. A stale pidfile is removed; if the service
// is nevertheless running (its pidfile lost or overwritten), the processes
// found by identity are stopped the same graceful way. An empty processName
// means no identity is declared and the pidfile PID is trusted when alive.
func (c *Controller) gracefulStopViaPidfile(
	ctx context.Context, pidFile, serviceName, processName string, timeout time.Duration,
) error {
	data, err := c.env.FS.ReadFile(pidFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.InfoContext(ctx, "Service already stopped (no pidfile)", "service", serviceName, "pidfile", pidFile)

			return nil
		}

		return fmt.Errorf("failed to read pidfile %s: %w", pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid pid in %s: %s", pidFile, strings.TrimSpace(string(data)))
	}

	pids := []int{pid}

	if !c.pidBelongsTo(ctx, pid, processName) {
		logger.InfoContext(ctx, "Pidfile does not name a running process of this service, treating it as stale",
			"service", serviceName, "pidfile", pidFile, "pid", pid)

		pids = c.pidsByIdentity(processName)
	}

	var stopErr error

	for _, p := range pids {
		stopErr = errors.Join(stopErr, c.gracefulStopPID(ctx, p, serviceName, timeout))
	}

	if stopErr != nil {
		return stopErr
	}

	_ = c.env.FS.Remove(pidFile)

	return nil
}

// pidsByIdentity lists the live processes of the given identity from a fresh
// /proc listing, excluding the current process and its parent. A scan failure
// yields none: the caller already has nothing better to act on.
func (c *Controller) pidsByIdentity(name string) []int {
	pids, err := c.scanProcesses(name)
	if err != nil {
		return nil
	}

	isSelfOrParent := c.selfOrParentFilter()

	return slices.DeleteFunc(pids, isSelfOrParent)
}

// gracefulStopPID is the SIGTERM → bounded wait → SIGKILL sequence for one PID.
// An already-gone process counts as stopped.
//
// The initial SIGTERM and the SIGKILL escalation are intentionally NOT
// hoisted to the shared signalPID helper (used by killProcess and
// stopMysqldByPidFile): signalPID is silent/best-effort by design — its
// targets already come from a fresh /proc scan, so a signal failure just
// means the process raced away — whereas callers here need genuine failures
// (e.g. EPERM signaling the wrong UID) surfaced as an error rather than
// swallowed. Only the polling loop is shared, via waitForProcessExit ->
// pollUntilExit (cli_process.go).
func (c *Controller) gracefulStopPID(ctx context.Context, pid int, serviceName string, timeout time.Duration) error {
	logger.InfoContext(ctx, "Stopping service gracefully (SIGTERM)", "service", serviceName, "pid", pid)

	if err := c.env.Proc.Signal(pid, syscall.SIGTERM); err != nil {
		// ESRCH: process already gone — treat as stopped.
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}

		return fmt.Errorf("failed to SIGTERM process %d: %w", pid, err)
	}

	if c.waitForProcessExit(ctx, pid, timeout) {
		return nil
	}

	// Still alive at the deadline: escalate to SIGKILL.
	logger.WarnContext(ctx, "Service did not exit after SIGTERM, sending SIGKILL",
		"service", serviceName, "pid", pid, "timeout", timeout)

	if err := c.env.Proc.Signal(pid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to SIGKILL process %d: %w", pid, err)
	}

	return nil
}

// waitForProcessExit polls (signal 0) until pid is gone, ctx is cancelled, or
// timeout elapses. Returns true if the process exited within the budget.
// Thin wrapper around the shared pollUntilExit core (cli_process.go) at the
// original 50ms cadence.
func (c *Controller) waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	return c.pollUntilExit(ctx, pid, timeout, 50*time.Millisecond)
}
