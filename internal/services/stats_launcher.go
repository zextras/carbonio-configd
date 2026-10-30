// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	zmstatEnabledTrue = "TRUE"
)

// statsCustomStart orchestrates the zmstat-* collector suite.
// Mirrors legacy statctl.pl logic: spawns ~11 collectors, each of which
// writes its own PID file under /run/carbonio/stats/.
func (c *Controller) statsCustomStart(ctx context.Context, def *ServiceDef) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Base collectors (always run)
	collectors := []string{
		zmstatProc,
		zmstatCPU,
		zmstatVM,
		zmstatIO + " -x",
		zmstatDF,
		zmstatIO,
		zmstatFD,
		zmstatAllprocs,
	}

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

	for _, collectorCmd := range collectors {
		parts := strings.Fields(collectorCmd)
		binary := parts[0]
		args := parts[1:]

		binaryPath := filepath.Join(c.p.libexec, binary)
		if _, statErr := c.env.FS.Stat(binaryPath); statErr != nil {
			logger.WarnContext(ctx, "Skipping missing stats collector",
				"collector", binary, "path", binaryPath)

			continue
		}

		logFile := filepath.Join(c.p.log, binary+".out")

		logFd, openErr := c.openLogFile(logFile)
		if openErr != nil {
			logger.WarnContext(ctx, "Failed to open log for stats collector",
				"collector", binary, "log", logFile, "error", openErr)

			continue
		}

		proc, startErr := c.env.Exec.Start(ctx, &sys.Cmd{
			Path:   binaryPath,
			Args:   args,
			Stdout: logFd,
			Stderr: logFd,
			Setsid: true,
		})
		if startErr != nil {
			_ = logFd.Close()

			logger.WarnContext(ctx, "Failed to start stats collector",
				"collector", binary, "error", startErr)

			continue
		}

		_ = logFd.Close()

		started++

		logger.InfoContext(ctx, "Started stats collector",
			"collector", binary, "pid", proc.Pid(), "log", logFile)
	}

	if started == 0 {
		return fmt.Errorf("no stats collectors started")
	}

	logger.InfoContext(ctx, "Stats collectors started", "count", started, "piddir", c.p.statsPidDir)

	return nil
}

// statsCustomStop kills all zmstat-* collectors by reading their individual
// PID files from /run/carbonio/stats/. Always finishes with a process-name
// scan to catch any survivors (e.g. collectors that restarted or self-elevated).
func (c *Controller) statsCustomStop(ctx context.Context, _ *ServiceDef) error {
	entries, err := c.env.FS.ReadDir(c.p.statsPidDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read stats PID dir %s: %w", c.p.statsPidDir, err)
	}

	killed := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}

		if c.killStatsPidFile(ctx, filepath.Join(c.p.statsPidDir, entry.Name())) {
			killed++
		}
	}

	// Final pass: send SIGTERM to any zmstat-* survivors found in /proc.
	_ = c.killProcess(ctx, c.p.statsGlob)

	logger.InfoContext(ctx, "Stopped stats collectors", "killed", killed)

	return nil
}

// killStatsPidFile reads the PID from pidPath, kills its process group (so
// grandchildren like iostat/vmstat spawned by the zmstat-* perl workers die
// with the parent), escalates to SIGKILL after a grace period, and escalates
// via sudo when the PID is root-owned (sudo-spawned zmstat-fd). Removes the
// pid file regardless. Returns true when the leader is confirmed gone.
func (c *Controller) killStatsPidFile(ctx context.Context, pidPath string) bool {
	data, readErr := c.env.FS.ReadFile(pidPath)
	if readErr != nil {
		logger.WarnContext(ctx, "Failed to read stats PID file", "path", pidPath, "error", readErr)

		return false
	}

	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		logger.WarnContext(ctx, "Invalid PID in stats PID file", "path", pidPath, "error", parseErr)
		_ = c.env.FS.Remove(pidPath)

		return false
	}

	killed := c.killByPIDWithGroupAndSudo(ctx, pid)

	_ = c.env.FS.Remove(pidPath)

	return killed
}
