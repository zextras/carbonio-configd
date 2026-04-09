// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	zmstatEnabledTrue = "TRUE"
)

var (
	libexecDir  = basePath + "/libexec"
	statsPidDir = pidDir + "/stats"
)

// statsCustomStart orchestrates the zmstat-* collector suite.
// Mirrors legacy statctl.pl logic: spawns ~11 collectors, each of which
// writes its own PID file under /run/carbonio/stats/.
func statsCustomStart(ctx context.Context, def *ServiceDef) error {
	lc, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Base collectors (always run)
	collectors := []string{
		"zmstat-proc",
		"zmstat-cpu",
		"zmstat-vm",
		"zmstat-io -x",
		"zmstat-df",
		"zmstat-io",
		"zmstat-fd",
		"zmstat-allprocs",
	}

	// Conditional collectors
	if lc["zmstat_mysql_enabled"] == zmstatEnabledTrue {
		collectors = append(collectors, "zmstat-mysql")
	}

	if lc["zmstat_nginx_enabled"] == zmstatEnabledTrue {
		collectors = append(collectors, "zmstat-nginx")
	}

	if lc["zmstat_mtaqueue_enabled"] == zmstatEnabledTrue {
		collectors = append(collectors, "zmstat-mtaqueue")
	}

	var started int

	logDir := logPath

	for _, collectorCmd := range collectors {
		parts := strings.Fields(collectorCmd)
		binary := parts[0]
		args := parts[1:]

		binaryPath := filepath.Join(libexecDir, binary)
		if _, statErr := os.Stat(binaryPath); statErr != nil {
			logger.WarnContext(ctx, "Skipping missing stats collector",
				"collector", binary, "path", binaryPath)

			continue
		}

		logFile := filepath.Join(logDir, binary+".out")

		logFd, openErr := openLogFile(logFile)
		if openErr != nil {
			logger.WarnContext(ctx, "Failed to open log for stats collector",
				"collector", binary, "log", logFile, "error", openErr)

			continue
		}

		cmd := exec.CommandContext(ctx, binaryPath, args...)
		cmd.Stdout = logFd
		cmd.Stderr = logFd
		cmd.SysProcAttr = detachedSysProcAttr()

		if startErr := cmd.Start(); startErr != nil {
			_ = logFd.Close()

			logger.WarnContext(ctx, "Failed to start stats collector",
				"collector", binary, "error", startErr)

			continue
		}

		_ = logFd.Close()

		started++

		logger.InfoContext(ctx, "Started stats collector",
			"collector", binary, "pid", cmd.Process.Pid, "log", logFile)
	}

	if started == 0 {
		return fmt.Errorf("no stats collectors started")
	}

	logger.InfoContext(ctx, "Stats collectors started", "count", started, "piddir", statsPidDir)

	return nil
}

// statsCustomStop kills all zmstat-* collectors by reading their individual
// PID files from /run/carbonio/stats/. Always finishes with a process-name
// scan to catch any survivors (e.g. collectors that restarted or self-elevated).
func statsCustomStop(ctx context.Context, _ *ServiceDef) error {
	entries, err := os.ReadDir(statsPidDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read stats PID dir %s: %w", statsPidDir, err)
	}

	killed := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}

		if killStatsPidFile(ctx, filepath.Join(statsPidDir, entry.Name())) {
			killed++
		}
	}

	// Final pass: send SIGTERM to any zmstat-* survivors found in /proc.
	_ = killProcess(ctx, "zmstat-")

	logger.InfoContext(ctx, "Stopped stats collectors", "killed", killed)

	return nil
}

// killStatsPidFile reads the PID from pidPath, kills the process, and removes the file.
// Returns true when the process was successfully killed.
func killStatsPidFile(ctx context.Context, pidPath string) bool {
	data, readErr := os.ReadFile(pidPath) //nolint:gosec // path is constructed from internal registry
	if readErr != nil {
		logger.WarnContext(ctx, "Failed to read stats PID file", "path", pidPath, "error", readErr)

		return false
	}

	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		logger.WarnContext(ctx, "Invalid PID in stats PID file", "path", pidPath, "error", parseErr)
		_ = os.Remove(pidPath)

		return false
	}

	killed := false

	proc, findErr := os.FindProcess(pid)
	if findErr == nil {
		if killErr := proc.Kill(); killErr != nil {
			logger.WarnContext(ctx, "Failed to kill stats collector", "pid", pid, "path", pidPath, "error", killErr)
		} else {
			killed = true
		}
	}

	_ = os.Remove(pidPath)

	return killed
}
