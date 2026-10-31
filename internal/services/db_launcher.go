// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// startAppserverDB spawns mysqld_safe for the mailbox mariadb. Invoked by
// mailboxCustomStart before the JVM so mailboxd finds port 7306 ready.
// Replaces legacy zmstorectl's call to `/opt/zextras/bin/mysql.server start`
// with a direct exec so the daemon has no runtime dependency on the legacy
// shell wrapper.
func (c *Controller) startAppserverDB(ctx context.Context) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	return c.spawnMysqldSafe(ctx, "appserver-db",
		lc["mysql_mycnf"], lc["mysql_errlogfile"], c.p.appserverDBPidFile)
}

// stopAppserverDB flushes InnoDB dirty pages and then terminates the mailbox
// mariadb via its pidfile with SIGTERM→SIGKILL escalation. mysqld_safe
// (the supervising shell wrapper) exits on its own when its child mariadbd
// is gone, so no separate signal is needed.
func (c *Controller) stopAppserverDB(ctx context.Context) error {
	c.flushAppserverDBDirtyPages(ctx)

	return c.stopMysqldByPidFile(ctx, "appserver-db", c.p.appserverDBPidFile)
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
// DB content is transient scoring state, not user mail.
func (c *Controller) stopAntispamDB(ctx context.Context) error {
	return c.stopMysqldByPidFile(ctx, "antispam-db", c.p.antispamDBPidFile)
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

// stopMysqldByPidFile reads the PID recorded by mariadbd, sends SIGTERM with
// escalation to SIGKILL on timeout (the kill timeout), and removes the
// pidfile. Signaling the PID directly rather than the process group is
// intentional: mysqld_safe exits on its own once its supervised child is
// gone, so a group kill would only add risk of cross-signal collisions when
// appserver and antispam DBs coexist.
func (c *Controller) stopMysqldByPidFile(ctx context.Context, name, pidfile string) error {
	data, err := c.env.FS.ReadFile(pidfile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.InfoContext(ctx, "DB already stopped (no pidfile)",
				"name", name, "pidfile", pidfile)

			return nil
		}

		return fmt.Errorf("%s: read pidfile %s: %w", name, pidfile, err)
	}

	pidStr, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")

	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		_ = c.env.FS.Remove(pidfile)

		return fmt.Errorf("%s: invalid pid in %s: %q", name, pidfile, pidStr)
	}

	c.signalPID(pid, syscall.SIGTERM)
	c.waitAndEscalate(ctx, []int{pid}, c.killTimeout)

	_ = c.env.FS.Remove(pidfile)

	logger.InfoContext(ctx, "DB stopped", "name", name, "pid", pid)

	return nil
}

// flushAppserverDBDirtyPages tells InnoDB to flush buffered writes to disk
// by setting innodb_max_dirty_pages_pct to 0. Best effort: skipped when the
// password is missing or mysql is already unreachable. Matches the intent
// of legacy zmstorectl.flushDirtyPages without shelling out to its script.
func (c *Controller) flushAppserverDBDirtyPages(ctx context.Context) {
	lc, err := c.loadConfig()
	if err != nil {
		return
	}

	password := lc["zimbra_mysql_password"]
	if password == "" {
		return
	}

	//nolint:gosec // password is from localconfig, mysql binary path is fixed
	_, err = c.env.Exec.Run(ctx, &sys.Cmd{
		Path: c.p.bin + "/mysql",
		Args: []string{
			"-u", "zextras", "--password=" + password,
			"-e", "set global innodb_max_dirty_pages_pct=0;",
		},
	})
	if err != nil {
		logger.WarnContext(ctx, "Could not signal InnoDB to flush dirty pages; proceeding to stop",
			"error", err)
	}
}

// antispamCustomStart is the antispam-service hook: it starts the amavisd
// mariadb when configured locally. amavisd itself is managed by the amavis
// service's own launcher (they share the same ProcessName), so this hook is
// strictly about the DB side of antispam.
func (c *Controller) antispamCustomStart(ctx context.Context, _ *ServiceDef) error {
	if !c.antispamDBEnabled(ctx) {
		return nil
	}

	return c.startAntispamDB(ctx)
}

// antispamCustomStop is the symmetric stop-side of antispamCustomStart.
func (c *Controller) antispamCustomStop(ctx context.Context, _ *ServiceDef) error {
	if !c.antispamDBEnabled(ctx) {
		return nil
	}

	return c.stopAntispamDB(ctx)
}
