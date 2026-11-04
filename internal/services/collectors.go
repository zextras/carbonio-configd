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
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const zmstatEnabledTrue = "TRUE"

// statsCollectors is the CollectorSpec of stats, mirroring legacy statctl.pl:
// the base collectors every host runs (zmstat-io twice: extended and plain)
// plus the ones whose localconfig key is TRUE. Each collector writes its own
// PID file under the stats run directory.
func statsCollectors() *CollectorSpec {
	return &CollectorSpec{
		Always: []string{
			zmstatProc,
			zmstatCPU,
			zmstatVM,
			zmstatIO + " -x",
			zmstatDF,
			zmstatIO,
			zmstatFD,
			zmstatAllprocs,
		},
		Optional: []OptionalCollector{
			{Cmd: "zmstat-mysql", LCKey: "zmstat_mysql_enabled"},
			{Cmd: "zmstat-nginx", LCKey: "zmstat_nginx_enabled"},
			{Cmd: "zmstat-mtaqueue", LCKey: "zmstat_mtaqueue_enabled"},
		},
	}
}

// startCollectors starts the collectors of def.Collectors; a collector that
// is missing or fails to start is logged and skipped, none starting is an
// error.
func (c *Controller) startCollectors(ctx context.Context, def *ServiceDef) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	collectors := slices.Clone(def.Collectors.Always)

	for _, opt := range def.Collectors.Optional {
		if lc[opt.LCKey] == zmstatEnabledTrue {
			collectors = append(collectors, opt.Cmd)
		}
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

// stopCollectors kills all collectors by reading their individual PID files
// from the stats run directory. Always finishes with a process-name scan to
// catch any survivors (e.g. collectors that restarted or self-elevated).
func (c *Controller) stopCollectors(ctx context.Context, def *ServiceDef) error {
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
	_ = c.killProcess(ctx, def.ProcessName)

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

// readStatsPid reads a stats pidfile, logging and reporting false when it is
// unreadable or holds no valid pid.
func (c *Controller) readStatsPid(ctx context.Context, pidPath string) (int, bool) {
	data, err := c.env.FS.ReadFile(pidPath)
	if err != nil {
		logger.WarnContext(ctx, "Failed to read stats PID file", "path", pidPath, "error", err)

		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		logger.WarnContext(ctx, "Invalid PID in stats PID file", "path", pidPath)

		return 0, false
	}

	return pid, true
}

// hupCollector sends SIGHUP to pid (through sudo when not permitted) and
// reports whether the process was signaled; false means it is not running.
func (c *Controller) hupCollector(ctx context.Context, pid int) bool {
	if !c.processAlive(pid) {
		return false
	}

	if sigErr := c.env.Proc.Signal(pid, syscall.SIGHUP); sigErr != nil {
		if !errors.Is(sigErr, syscall.EPERM) {
			return false
		}

		c.sudoKill(ctx, pid, "HUP", false)
	}

	return true
}

// rotateCollectors sends SIGHUP to the pid of every collector
// pidfile (legacy statctl.pl rotate). Pids that are gone are reported, not an
// error. A root-owned collector (sudo-spawned zmstat-fd) is signaled through
// sudo.
func (c *Controller) rotateCollectors(ctx context.Context) (SignalReport, error) {
	entries, err := c.env.FS.ReadDir(c.p.statsPidDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return SignalReport{}, fmt.Errorf("read stats PID dir %s: %w", c.p.statsPidDir, err)
	}

	var (
		report SignalReport
		found  bool
	)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}

		pid, ok := c.readStatsPid(ctx, filepath.Join(c.p.statsPidDir, entry.Name()))
		if !ok {
			continue
		}

		found = true

		if c.hupCollector(ctx, pid) {
			report.Signaled = append(report.Signaled, pid)
		} else {
			report.NotRunning = append(report.NotRunning, pid)
		}
	}

	if !found {
		return report, ErrNoStatsPidFiles
	}

	return report, nil
}
