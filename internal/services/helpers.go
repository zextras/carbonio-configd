// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

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

// isTruthy returns true for "TRUE" (case-insensitive) or "1".
func isTruthy(val string) bool {
	return strings.EqualFold(val, "TRUE") || val == "1"
}
