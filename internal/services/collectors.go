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

const statEnabledTrue = "TRUE"

// statsCollectors is the CollectorSpec of stats, mirroring legacy statctl.pl:
// the base collectors every host runs (io twice: extended and plain) plus the
// ones whose localconfig key is TRUE, all run by one process, "setup stat run
// <collector>...". Every collector keeps its own PID file under the stats run
// directory, all naming that process.
//
// They share a process because they share a user: every collector runs as the
// zextras user configd runs as (fd included: its columns are readable without
// root), so none needs a process of its own. ldap is not started by configd:
// it has no localconfig key and is run by hand.
func statsCollectors() *CollectorSpec {
	return &CollectorSpec{
		Binary:  collectorBinary,
		Args:    []string{collectorCommand, collectorRun},
		LogName: collectorLog,
		Always: []string{
			"proc",
			"cpu",
			"vm",
			"io -x",
			"df",
			"io",
			"fd",
			"allprocs",
		},
		Optional: []OptionalCollector{
			{Cmd: "mysql", LCKey: "zmstat_mysql_enabled"},
			{Cmd: "nginx", LCKey: "zmstat_nginx_enabled"},
			{Cmd: "mtaqueue", LCKey: "zmstat_mtaqueue_enabled"},
		},
	}
}

// startCollectors starts the one process that runs the collectors of
// def.Collectors (the Always ones and the Optional ones that are enabled),
// logging to <log>/zmstat.out. The process reports on its own log a collector
// that cannot start and keeps the others running.
func (c *Controller) startCollectors(ctx context.Context, def *ServiceDef) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	spec := def.Collectors
	collectors := slices.Clone(spec.Always)

	for _, opt := range spec.Optional {
		if lc[opt.LCKey] == statEnabledTrue {
			collectors = append(collectors, opt.Cmd)
		}
	}

	binaryPath := filepath.Join(c.p.libexec, spec.Binary)

	if _, statErr := c.env.FS.Stat(binaryPath); statErr != nil {
		logger.WarnContext(ctx, "Stats collectors binary is missing", "path", binaryPath)

		return fmt.Errorf("no stats collectors started: %s is missing", binaryPath)
	}

	logFile := filepath.Join(c.p.log, spec.LogName)

	logFd, err := c.openLogFile(logFile)
	if err != nil {
		return fmt.Errorf("no stats collectors started: open log %s: %w", logFile, err)
	}

	defer func() { _ = logFd.Close() }()

	// One argument per collector: its command line, as the zmstat script took it.
	proc, err := c.env.Exec.Start(ctx, &sys.Cmd{
		Path:   binaryPath,
		Args:   append(slices.Clone(spec.Args), collectors...),
		Stdout: logFd,
		Stderr: logFd,
		Setsid: true,
	})
	if err != nil {
		return fmt.Errorf("no stats collectors started: %w", err)
	}

	logger.InfoContext(ctx, "Started stats collectors",
		"collectors", collectors, "pid", proc.Pid(), "log", logFile, "piddir", c.p.statsPidDir)

	return nil
}

// stopCollectors kills the collectors by reading their PID files from the
// stats run directory (all naming the one process of this release, one per
// process of an earlier one). Always finishes with a process-name scan to
// catch any survivors (e.g. collectors that restarted or self-elevated).
func (c *Controller) stopCollectors(ctx context.Context, def *ServiceDef) error {
	entries, err := c.env.FS.ReadDir(c.p.statsPidDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read stats PID dir %s: %w", c.p.statsPidDir, err)
	}

	files := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}

		c.killStatsPidFile(ctx, filepath.Join(c.p.statsPidDir, entry.Name()), def.ProcessName)

		files++
	}

	// Final pass: send SIGTERM to any collector survivors found in /proc,
	// including the Perl ones of a release before the upgrade.
	_ = c.killProcess(ctx, def.ProcessName)
	_ = c.killProcess(ctx, c.p.statsGlob)

	logger.InfoContext(ctx, "Stopped stats collectors", "pidfiles", files)

	return nil
}

// killStatsPidFile reads the PID from pidPath, kills its process group (so
// grandchildren such as postqueue and the mysql client, or the iostat/vmstat of
// the perl workers of an earlier release, die with the parent), escalates to
// SIGKILL after a grace period, and escalates via sudo when the PID is
// root-owned (the sudo-spawned zmstat-fd of an earlier release). Removes the
// pid file regardless. Returns true when the leader is confirmed gone.
//
// The group kill is signaled only for a positive PID that is a live process of
// the collectors' identity (processName, see pidBelongsTo): 0 and negative
// values would address configd's own group or every process, and a PID
// recycled by an unrelated process must not have its whole group killed. Such
// a stale or invalid pid file is just removed.
func (c *Controller) killStatsPidFile(ctx context.Context, pidPath, processName string) bool {
	data, readErr := c.env.FS.ReadFile(pidPath)
	if readErr != nil {
		logger.WarnContext(ctx, "Failed to read stats PID file", "path", pidPath, "error", readErr)

		return false
	}

	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil || pid <= 0 {
		logger.WarnContext(ctx, "Invalid PID in stats PID file", "path", pidPath, "error", parseErr)
		_ = c.env.FS.Remove(pidPath)

		return false
	}

	if !c.pidBelongsTo(ctx, pid, processName) && !c.pidBelongsTo(ctx, pid, c.p.statsGlob) {
		logger.InfoContext(ctx, "Stats PID file does not name a running collector, treating it as stale",
			"path", pidPath, "pid", pid)
		_ = c.env.FS.Remove(pidPath)

		return true
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

// rotateCollectors sends SIGHUP to the pid of every collector pidfile (legacy
// statctl.pl rotate), once per process although every collector of the one
// process of this release has a pidfile naming it. Pids that are gone are
// reported, not an error. A root-owned collector (sudo-spawned zmstat-fd) is signaled through
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

	seen := map[int]bool{}

	for _, entry := range entries {
		pid, ok := c.statsPidOf(ctx, entry)
		if !ok {
			continue
		}

		found = true

		if seen[pid] {
			continue
		}

		seen[pid] = true

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

// statsPidOf is the pid of a stats run directory entry that is a valid
// pidfile.
func (c *Controller) statsPidOf(ctx context.Context, entry fs.DirEntry) (int, bool) {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
		return 0, false
	}

	return c.readStatsPid(ctx, filepath.Join(c.p.statsPidDir, entry.Name()))
}
