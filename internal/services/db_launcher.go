// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// startAppserverDB spawns mysqld_safe for the mailbox mariadb (the
// appserver-db service; the store composite starts it before the JVM so
// mailboxd finds port 7306 ready). Replaces legacy zmstorectl's call to
// `/opt/zextras/bin/mysql.server start` with a direct exec so the daemon has
// no runtime dependency on the legacy shell wrapper.
func (c *Controller) startAppserverDB(ctx context.Context) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	return c.spawnMysqldSafe(ctx, "appserver-db",
		lc["mysql_mycnf"], lc["mysql_errlogfile"], c.p.appserverDBPidFile)
}

// stopAppserverDB shuts the mailbox mariadb down cleanly. The InnoDB
// dirty-page flush runs first (flushAppserverDBDirtyPages, also a PreStop hook
// so the systemd stop path gets it too), then the server gets SIGTERM and a
// bounded wait — never SIGKILL: killing mariadbd mid-flush forces InnoDB
// crash recovery on the next start. Legacy mysql.server stop is the same: a
// plain `kill`, then up to zimbra_mysql_shutdown_timeout seconds of polling.
func (c *Controller) stopAppserverDB(ctx context.Context, def *ServiceDef) error {
	return c.stopMysqld(ctx, def, "zimbra_mysql_shutdown_timeout", c.p.appserverDBPidFile)
}

// startAntispamDB spawns mysqld_safe for the amavisd mariadb. Uses
// antispam_mysql_mycnf so multi-server installs can point at a different
// defaults file. Guarded by antispamDBEnabled at the caller; this function
// does the minimal correctness check (non-empty mycnf) and returns a no-op
// result when the install is not configured for a local antispam DB.
func (c *Controller) startAntispamDB(ctx context.Context) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	mycnf := lc["antispam_mysql_mycnf"]
	if mycnf == "" {
		logger.InfoContext(ctx, "antispam_mysql_mycnf not set; skipping antispam DB start")

		return nil
	}

	return c.spawnMysqldSafe(ctx, "antispam-db",
		mycnf, lc["antispam_mysql_errlogfile"], c.p.antispamDBPidFile)
}

// stopAntispamDB terminates the amavisd mariadb. No InnoDB flush — antispam
// DB content is transient scoring state, not user mail — but the same
// never-SIGKILL rule applies.
func (c *Controller) stopAntispamDB(ctx context.Context, def *ServiceDef) error {
	return c.stopMysqld(ctx, def, "zimbra_mysql_shutdown_timeout", c.p.antispamDBPidFile)
}

// antispamDBEnabled gates the antispam lifecycle hooks. Mirrors the guard at
// the top of legacy antispam-mysql.server: run the DB locally only when
// antispam_mysql_enabled is truthy AND antispam_mysql_host resolves to this
// host. Elsewhere the DB is managed by another node and our hook is a no-op.
func (c *Controller) antispamDBEnabled(ctx context.Context) bool {
	lc, err := c.loadConfig()
	if err != nil {
		return false
	}

	if !config.IsTruthy(lc["antispam_mysql_enabled"]) {
		return false
	}

	host := strings.TrimSpace(lc["antispam_mysql_host"])
	if host == "" {
		return false
	}

	if host == loopbackIPv4 || host == localhostName {
		return true
	}

	out, err := sys.Output(ctx, c.env.Exec, c.p.bin+"/zmhostname")
	if err != nil {
		return false
	}

	return host == strings.TrimSpace(string(out))
}

// spawnMysqldSafe starts a detached mysqld_safe wrapper that in turn forks
// mariadbd with the flags read from defaultsFile. Matches the invocation
// observed in production containers:
//
//	mysqld_safe --defaults-file=... --external-locking
//	            --log-error=... --malloc-lib=... --ledir=...
//
// Returns nil (skip) when the pidfile already points at a live process —
// idempotent start behavior matching mysql.server's check_running branch.
func (c *Controller) spawnMysqldSafe(ctx context.Context, name, defaultsFile, errlogFile, pidfile string) error {
	if defaultsFile == "" {
		return fmt.Errorf("%s: defaults file path not set in localconfig", name)
	}

	if running, ok := c.isRunningByPidFile(pidfile); ok && running {
		logger.InfoContext(ctx, "DB already running", "name", name, "pidfile", pidfile)

		return nil
	}

	args := []string{
		"--defaults-file=" + defaultsFile,
		"--external-locking",
	}

	if errlogFile != "" {
		args = append(args, "--log-error="+errlogFile)
	}

	args = append(args,
		"--malloc-lib="+c.p.mysqldMallocLib,
		"--ledir="+c.p.mysqldSafeLedir,
	)

	logFd, err := c.openLogFile(c.p.log + "/mysqld_safe_" + name + ".out")
	if err != nil {
		return fmt.Errorf("%s: open log file: %w", name, err)
	}

	// mysqld_safe must outlive the CLI invocation — see the same pattern in
	// startDetached (cli_process.go). Starting it with ctx would tie the
	// daemon's lifetime to this function's ctx, which is the exact opposite
	// of what we want.
	proc, startErr := c.env.Exec.Start(context.Background(), &sys.Cmd{
		Path:   c.p.mysqldSafeBin,
		Args:   args,
		Stdout: logFd,
		Stderr: logFd,
		Setsid: true,
	})
	if startErr != nil {
		_ = logFd.Close()

		return fmt.Errorf("%s: spawn mysqld_safe: %w", name, startErr)
	}

	_ = logFd.Close()

	if releaseErr := proc.Release(); releaseErr != nil {
		logger.WarnContext(ctx, "Failed to release mysqld_safe child handle",
			"name", name, "pid", proc.Pid(), "error", releaseErr)
	}

	logger.InfoContext(ctx, "Spawned mysqld_safe",
		"name", name, "defaults", defaultsFile, "log", c.p.log+"/mysqld_safe_"+name+".out")

	return nil
}

// Safe-stop tuning of the mariadb launchers.
const (
	// mysqlShutdownTimeoutDefault is the wait for mariadbd to exit after
	// SIGTERM when zimbra_mysql_shutdown_timeout is unset (the localconfig
	// default is 60).
	mysqlShutdownTimeoutDefault = 60 * time.Second
	// innodbFlushTimeout is how long the pre-stop flush waits for InnoDB to
	// write its dirty pages (legacy storectl.sh: 600 x 1s).
	innodbFlushTimeout = 600 * time.Second
	mysqlPollInterval  = time.Second
)

// stopMysqld terminates the mariadbd of def: SIGTERM to its pid, then a
// bounded wait for it to exit. It never escalates to SIGKILL — a mariadbd
// killed while flushing needs InnoDB crash recovery at the next start — so a
// server that outlives the wait is reported as an error and left running.
// A server that is not running is a successful stop.
func (c *Controller) stopMysqld(ctx context.Context, def *ServiceDef, timeoutKey, pidfile string) error {
	pid := c.runningPID(ctx, def)
	if pid == 0 {
		logger.InfoContext(ctx, "DB already stopped", "name", def.Name)

		_ = c.env.FS.Remove(pidfile)

		return nil
	}

	timeout := mysqlShutdownTimeoutDefault

	if lc, err := c.loadConfig(); err == nil {
		if secs, convErr := strconv.Atoi(strings.TrimSpace(lc[timeoutKey])); convErr == nil && secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
	}

	c.signalPID(pid, syscall.SIGTERM)

	if !c.pollUntilExit(ctx, pid, timeout, mysqlPollInterval) {
		return fmt.Errorf("%s (pid %d) still running %s after SIGTERM; not forcing termination "+
			"(SIGKILL would require InnoDB crash recovery)", def.Name, pid, timeout)
	}

	_ = c.env.FS.Remove(pidfile)

	logger.InfoContext(ctx, "DB stopped", "name", def.Name, "pid", pid)

	return nil
}

// appserverDBFlushHook is the PreStop hook of appserver-db: it flushes InnoDB
// before the stop in both orchestration modes.
func (c *Controller) appserverDBFlushHook(ctx context.Context, _ *ServiceManager) error {
	c.flushAppserverDBDirtyPages(ctx)

	return nil
}

// flushAppserverDBDirtyPages makes InnoDB write its buffered pages to disk
// before the server is stopped, like legacy storectl.sh flushDirtyPages:
//
//  1. only when the server runs and InnoDB is not DISABLED;
//  2. set global innodb_max_dirty_pages_pct=0;
//  3. poll `show engine innodb status` until every buffer pool instance reports
//     "Modified db pages 0", for up to innodbFlushTimeout.
//
// Best effort: a failed step is logged and the stop proceeds.
func (c *Controller) flushAppserverDBDirtyPages(ctx context.Context) {
	def := c.registry[svcAppserverDB]
	if def == nil || c.runningPID(ctx, def) == 0 {
		return
	}

	lc, err := c.loadConfig()
	if err != nil {
		logger.WarnContext(ctx, "InnoDB flush skipped: localconfig load failed", "error", err)

		return
	}

	password := lc["zimbra_mysql_password"]
	if password == "" {
		logger.WarnContext(ctx, "InnoDB flush skipped: zimbra_mysql_password is not set")

		return
	}

	query := func(stmt string) (string, error) {
		//nolint:gosec // password is from localconfig, mysql binary path is fixed
		out, qErr := sys.Output(ctx, c.env.Exec, c.p.bin+"/mysql",
			"-u", "zextras", "--password="+password, "-e", stmt)

		return string(out), qErr
	}

	if engines, qErr := query("show engines;"); qErr == nil && innodbDisabled(engines) {
		return
	}

	if _, qErr := query("set global innodb_max_dirty_pages_pct=0;"); qErr != nil {
		logger.WarnContext(ctx, "Could not signal InnoDB to flush dirty pages; proceeding to stop",
			"error", qErr)

		return
	}

	deadline := c.env.Clock.Now().Add(innodbFlushTimeout)

	for c.env.Clock.Now().Before(deadline) {
		status, qErr := query(`show engine innodb status\G`)
		if qErr != nil {
			logger.WarnContext(ctx, "Could not read InnoDB status while flushing; proceeding to stop",
				"error", qErr)

			return
		}

		if modifiedDBPages(status) == 0 {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-c.env.Clock.After(mysqlPollInterval):
		}
	}

	logger.WarnContext(ctx, "InnoDB still has dirty pages after the flush wait; proceeding to stop",
		"timeout", innodbFlushTimeout)
}

// innodbDisabled reports whether `show engines` lists InnoDB as DISABLED.
func innodbDisabled(engines string) bool {
	for line := range strings.SplitSeq(engines, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) > 1 && fields[0] == "InnoDB" {
			return strings.TrimSpace(fields[1]) == "DISABLED"
		}
	}

	return false
}

// modifiedDBPages sums the "Modified db pages N" lines of `show engine innodb
// status\G` (one per buffer pool instance). An output without any such line
// counts as nothing to wait for.
func modifiedDBPages(status string) int {
	total := 0

	for line := range strings.SplitSeq(status, "\n") {
		if !strings.HasPrefix(line, "Modified db pages") {
			continue
		}

		fields := strings.Fields(line)

		if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			total += n
		}
	}

	return total
}

// appserverDBCustomStart launches the mailbox mariadb (legacy mysql.server
// start).
func (c *Controller) appserverDBCustomStart(ctx context.Context, _ *ServiceDef) error {
	return c.startAppserverDB(ctx)
}

// appserverDBCustomStop is the stop of appserver-db in legacy mode: a clean,
// never-killed shutdown (the InnoDB flush ran as a PreStop hook).
func (c *Controller) appserverDBCustomStop(ctx context.Context, def *ServiceDef) error {
	return c.stopAppserverDB(ctx, def)
}

// antispamDBCustomStart starts the amavisd mariadb when it is configured to
// run on this host (see antispamDBEnabled); elsewhere it is a no-op.
func (c *Controller) antispamDBCustomStart(ctx context.Context, _ *ServiceDef) error {
	if !c.antispamDBEnabled(ctx) {
		return nil
	}

	return c.startAntispamDB(ctx)
}

// antispamDBCustomStop is the symmetric stop of antispamDBCustomStart.
func (c *Controller) antispamDBCustomStop(ctx context.Context, def *ServiceDef) error {
	if !c.antispamDBEnabled(ctx) {
		return nil
	}

	return c.stopAntispamDB(ctx, def)
}
