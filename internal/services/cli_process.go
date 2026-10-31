// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// startService starts a service. Bifurcated on c.IsSystemdMode():
//
//   - strict systemd: every unit started via systemctl. Errors are returned.
//     No fallback — a failed systemctl start is a real error in this mode.
//   - legacy: direct binary spawn (CustomStart hook, then BinaryPath).
//     systemctl is never invoked.
func (c *Controller) startService(ctx context.Context, name string, def *ServiceDef) error {
	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not starting", "service", name)

		return nil
	}

	if !c.IsSystemdMode() {
		return c.startWithoutSystemd(ctx, name, def)
	}

	for _, unit := range def.SystemdUnits {
		logger.InfoContext(ctx, "Starting service via systemctl", "service", name, "unit", unit)

		if err := c.Systemctl(ctx, "start", unit); err != nil {
			return fmt.Errorf("failed to start %s (%s): %w", name, unit, err)
		}
	}

	return nil
}

// startWithoutSystemd is the fast-path for hosts where /run/systemd/system is
// missing. We never invoke systemctl — its failure mode is verbose and noisy.
// Precedence: CustomStart hook (fully custom launch) > BinaryPath direct spawn.
func (c *Controller) startWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if def.CustomStart != nil {
		logger.InfoContext(ctx, "Starting service via custom launcher (legacy mode)",
			"service", name)

		return def.CustomStart(ctx, def)
	}

	if def.BinaryPath == "" {
		if def.ProcessName != "" {
			logger.DebugContext(ctx, "No direct launcher; service managed via dependencies",
				"service", name)

			return nil
		}

		return fmt.Errorf(
			"cannot start %s in legacy mode: no direct launcher registered "+
				"(set ServiceDef.BinaryPath or ServiceDef.CustomStart, "+
				"or enable a Carbonio systemd target)", name)
	}

	logger.InfoContext(ctx, "Starting service directly (legacy mode)",
		"service", name, "binary", def.BinaryPath)

	return c.startDirect(ctx, name, def)
}

// stopService stops a service. Mirrors startService's bifurcation on
// c.IsSystemdMode(). Legacy mode calls CustomStop or pkill by ProcessName
// and never invokes systemctl — critical for services like stats whose
// workers run outside any systemd cgroup (e.g. spawned by statsCustomStart
// directly into /init.scope in container installs).
func (c *Controller) stopService(ctx context.Context, name string, def *ServiceDef) error {
	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not stopping", "service", name)

		return nil
	}

	if !c.IsSystemdMode() {
		return c.stopWithoutSystemd(ctx, name, def)
	}

	for i := range slices.Backward(def.SystemdUnits) {
		unit := def.SystemdUnits[i]

		logger.InfoContext(ctx, "Stopping service via systemctl", "service", name, "unit", unit)

		if err := c.Systemctl(ctx, "stop", unit); err != nil {
			return fmt.Errorf("failed to stop %s (%s): %w", name, unit, err)
		}
	}

	return nil
}

// stopWithoutSystemd is the fast-path stop for non-systemd hosts.
// Precedence: CustomStop hook (fully custom shutdown) > GracefulStop
// (SIGTERM the identity-validated pidfile process, bounded wait, SIGKILL) >
// ProcessName pkill.
//
// For services with UseSDNotify, an sd_notify shutdown observer is started
// in parallel with killProcess: it listens on the persistent NOTIFY_SOCKET
// and logs a "Graceful shutdown acknowledged" entry the moment the daemon
// sends STOPPING=1. The observer never blocks or alters the shutdown
// critical path; it exists purely to distinguish, in the operator-facing
// log, a clean graceful shutdown from an SIGTERM-ignoring process that only
// died via the SIGKILL escalation.
func (c *Controller) stopWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if def.CustomStop != nil {
		logger.InfoContext(ctx, "Stopping service via custom shutdown (legacy mode)",
			"service", name)

		return def.CustomStop(ctx, def)
	}

	if def.GracefulStop > 0 && def.PidFile != "" {
		return c.gracefulStopViaPidfile(ctx, def.PidFile, name, def.ProcessName, def.GracefulStop)
	}

	if def.ProcessName == "" {
		return fmt.Errorf(
			"cannot stop %s in legacy mode: no ProcessName registered "+
				"(set ServiceDef.ProcessName or ServiceDef.CustomStop, "+
				"or enable a Carbonio systemd target)", name)
	}

	if def.UseSDNotify {
		observerCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		go c.awaitSDNotifyStopping(observerCtx, name)
	}

	logger.InfoContext(ctx, "Stopping service via pkill (legacy mode)",
		"service", name, "process", def.ProcessName)

	return c.killProcess(ctx, def.ProcessName)
}

// reloadWithoutSystemd is the fast-path reload for non-systemd hosts.
// systemctl is never invoked. MTA has a native reload ("postfix reload",
// mirroring mta_launcher.go's direct sudo-postfix pattern); every other
// service falls back to stopService+startService, which — already being in
// legacy mode — resolve to CustomStop/CustomStart or ProcessName/BinaryPath,
// never systemctl.
func (c *Controller) reloadWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if name == serviceMTA {
		// Legacy zmmtactl reload runs "reload" on each START_SCRIPTS entry:
		// zmmilterctl and zmsaslauthdctl implement reload as stop+start, then
		// postfix reload (carbonio-core 4.6.7 src/bin/mtactl.sh).
		for _, dep := range def.Dependencies {
			if !c.isDepEnabled(ctx, dep) {
				continue
			}

			if err := c.ServiceRestart(ctx, dep); err != nil {
				logger.WarnContext(ctx, "Dependency restart failed during MTA reload",
					"dependency", dep, "error", err)
			}
		}

		logger.InfoContext(ctx, "Reloading service via postfix reload (legacy mode)", "service", name)

		return c.sudoRun(ctx, c.p.postfixBin, actionReload)
	}

	logger.InfoContext(ctx, "No native reload for service, restarting (legacy mode)", "service", name)

	if err := c.stopService(ctx, name, def); err != nil {
		return fmt.Errorf("failed to stop %s during reload fallback: %w", name, err)
	}

	return c.startService(ctx, name, def)
}

// selfOrParentFilter returns a predicate reporting whether a pid is the
// current process or its parent, computed once so scanning many candidate
// pids doesn't re-syscall Getpid/Getppid per candidate. Shared by every
// /proc scan that must not match the CLI's own invocation:
// pidFromProcessName, pidsByIdentity, and killProcess.
func (c *Controller) selfOrParentFilter() func(pid int) bool {
	self, parent := c.env.Proc.Pid(), c.env.Proc.Ppid()

	return func(pid int) bool { return pid == self || pid == parent }
}

// startDirect starts a service binary directly (non-systemd fallback).
//
// For services that self-daemonize (postfix master, slapd -d 0, opendkim, etc.)
// the launcher returns immediately and we capture the exit code synchronously.
// For services marked Detached (configd's own daemon), we spawn with a new
// session and detach stdin/stdout/stderr to a log file so the configd CLI can
// exit without taking down its own daemon child.
func (c *Controller) startDirect(ctx context.Context, name string, def *ServiceDef) error {
	if def.BinaryPath == "" {
		return fmt.Errorf("no binary path defined for %s", name)
	}

	if _, err := c.env.FS.Stat(def.BinaryPath); err != nil {
		return fmt.Errorf("%s binary not found at %s: %w", name, def.BinaryPath, err)
	}

	binary := def.BinaryPath
	args := def.BinaryArgs

	// Services like postfix require root. If current user is not root, use sudo.
	if def.NeedsRoot && c.env.Proc.UID() != 0 {
		args = append([]string{binary}, args...)
		binary = sudoBin
	}

	logger.InfoContext(ctx, "Starting service directly",
		"service", name, "binary", binary, "args", args, "detached", def.Detached)

	if def.Detached {
		return c.startDetached(ctx, name, def)
	}

	if def.UseSDNotify {
		_, err := c.startWithSDNotify(ctx, &sys.Cmd{Path: binary, Args: args}, name)

		return err
	}

	output, err := sys.CombinedOutput(ctx, c.env.Exec, binary, args...)
	if err != nil {
		return fmt.Errorf("failed to start %s directly: %s: %w", name, strings.TrimSpace(string(output)), err)
	}

	return nil
}

// startDetached spawns a long-running daemon in its own session, releasing the
// controlling terminal so the parent CLI can exit cleanly. stdout+stderr are
// redirected to def.LogFile (default: c.p.base/log/<name>.out).
func (c *Controller) startDetached(_ context.Context, name string, def *ServiceDef) error {
	logFilePath := def.LogFile
	if logFilePath == "" {
		logFilePath = c.p.log + "/" + name + ".out"
	}

	logFile, err := c.env.FS.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open log file %s for %s: %w", logFilePath, name, err)
	}

	// Note: the daemon is started with context.Background(), not the CLI's
	// context — it must outlive the CLI. The daemon is supervised by its own
	// logic (systemd-style sd_notify, watchdog, signal handlers).
	cmd := &sys.Cmd{
		Path:   def.BinaryPath,
		Args:   def.BinaryArgs,
		Stdout: logFile,
		Stderr: logFile,
		Setsid: true,
	}

	// For sd_notify-capable detached daemons, wait for READY=1 before returning.
	// startWithSDNotify starts the process internally.
	if def.UseSDNotify {
		proc, err := c.startWithSDNotify(context.Background(), cmd, name)
		_ = logFile.Close()

		if err != nil {
			return fmt.Errorf("failed to start %s: %w", name, err)
		}

		if proc != nil {
			if releaseErr := proc.Release(); releaseErr != nil {
				logger.WarnContext(context.Background(), "Failed to release child handle",
					"service", name, "pid", proc.Pid(), "error", releaseErr)
			}
		}

		return nil
	}

	proc, err := c.env.Exec.Start(context.Background(), cmd)
	if err != nil {
		_ = logFile.Close()

		return fmt.Errorf("failed to spawn %s: %w", name, err)
	}

	// Once the daemon has its own session it owns the fd; the parent can drop it.
	_ = logFile.Close()
	// Reap eventually — Release lets the kernel clean up when daemon exits.
	if err := proc.Release(); err != nil {
		logger.WarnContext(context.Background(), "Failed to release child handle",
			"service", name, "pid", proc.Pid(), "error", err)
	}

	return nil
}

// killProcess terminates every process of the identity processName (exe/argv
// match, see procident.go — never a cmdline substring), always resolved from a
// fresh /proc listing. Two-phase shutdown: SIGTERM to all, wait up to
// the kill timeout for each to exit, then SIGKILL survivors. Self and parent
// PIDs are excluded so we never SIGTERM our own CLI invocation.
//
// Without the wait-then-SIGKILL phase, `zmcontrol stop` returns "Done (1ms)"
// the instant SIGTERM is dispatched, even when the target ignores/slow-handles
// the signal. mailboxd's JVM caught SIGTERM but took >80 s to exit via its
// shutdown hook, leaving the CLI to falsely report success.
func (c *Controller) killProcess(ctx context.Context, processName string) error {
	pids, err := c.scanProcesses(processName)
	if err != nil {
		return fmt.Errorf("scan processes for %s: %w", processName, err)
	}

	isSelfOrParent := c.selfOrParentFilter()

	targets := make([]int, 0, len(pids))

	for _, pid := range pids {
		if isSelfOrParent(pid) {
			continue
		}

		targets = append(targets, pid)
	}

	if len(targets) == 0 {
		return nil
	}

	for _, pid := range targets {
		c.signalPID(pid, syscall.SIGTERM)
	}

	c.waitAndEscalate(ctx, targets, c.killTimeout)

	return nil
}

// signalPID best-effort sends sig to pid, ignoring ESRCH (already gone) and
// EPERM (cross-UID; handled separately where needed).
func (c *Controller) signalPID(pid int, sig syscall.Signal) {
	_ = c.env.Proc.Signal(pid, sig)
}

// waitAndEscalate polls each pid in parallel until it exits or the deadline
// expires, then SIGKILLs any survivors. Parallel so that N slow-exiting
// processes do not sum to N*timeout.
func (c *Controller) waitAndEscalate(ctx context.Context, pids []int, timeout time.Duration) {
	done := make(chan struct{}, len(pids))

	for _, pid := range pids {
		go func(p int) {
			if !c.pollUntilExit(ctx, p, timeout, 200*time.Millisecond) {
				logger.WarnContext(ctx, "SIGTERM grace expired, escalating to SIGKILL",
					"pid", p, "timeout", timeout)
				c.signalPID(p, syscall.SIGKILL)
			}

			done <- struct{}{}
		}(pid)
	}

	for range pids {
		<-done
	}
}

// processAlive returns true when the process exists and is not a zombie.
// Zombies have already exited and are only waiting for a parent reap.
func (c *Controller) processAlive(pid int) bool {
	return c.env.Proc.Alive(pid)
}

// pollUntilExit polls c.processAlive(pid) at the given interval until it
// exits, ctx is cancelled, or timeout elapses. Returns true once the
// process is confirmed gone. Shared poll core for waitForProcessExit
// (helpers.go, 50ms/single pid), waitAndEscalate (200ms/N pids in
// parallel), and killByPIDWithGroupAndSudo (200ms/process group). What
// happens after a false return (which signal, which target) differs per
// caller and is intentionally left to them.
func (c *Controller) pollUntilExit(ctx context.Context, pid int, timeout, interval time.Duration) bool {
	deadline := c.env.Clock.Now().Add(timeout)

	for c.env.Clock.Now().Before(deadline) {
		if !c.processAlive(pid) {
			return true
		}

		select {
		case <-ctx.Done():
			return !c.processAlive(pid)
		case <-c.env.Clock.After(interval):
		}
	}

	return !c.processAlive(pid)
}

// killProcessGroup sends sig to the process group led by pgid (negative PID
// semantics of syscall.Kill). Returns the raw syscall error so callers can
// distinguish EPERM (cross-UID) from ESRCH (group already gone).
func (c *Controller) killProcessGroup(pgid int, sig syscall.Signal) error {
	return c.env.Proc.SignalGroup(pgid, sig)
}

// sudoKill escalates a signal via sudo when the calling user cannot signal
// the target directly (e.g. root-owned `sudo zmstat-fd` child). No-op error
// on failure — the caller logs. Signal is passed as the literal name ("TERM",
// "KILL") that `kill(1)` accepts.
func (c *Controller) sudoKill(ctx context.Context, pid int, signal string, groupKill bool) {
	target := strconv.Itoa(pid)
	if groupKill {
		target = "-" + target
	}

	// #nosec G204 — arguments are a fixed literal signal name and a numeric PID
	if out, err := sys.CombinedOutput(ctx, c.env.Exec, sudoBin, "-n", "kill", "-"+signal, "--", target); err != nil {
		logger.WarnContext(ctx, "sudo kill failed",
			"pid", pid, "signal", signal, "group", groupKill,
			"error", err, "output", strings.TrimSpace(string(out)))
	}
}

// killByPIDWithGroupAndSudo is the workhorse for pidfile-driven shutdowns of
// services whose workers may spawn uncontrolled children (iostat, vmstat as
// grandchildren of zmstat-*) and whose workers may have been launched with
// elevated privileges (sudo-spawned zmstat-fd). Strategy, in order:
//
//  1. SIGTERM to the whole process group (-pid). Catches all children.
//  2. On EPERM, retry the group SIGTERM via `sudo kill`.
//  3. Wait up to the kill timeout for the group leader to exit.
//  4. On timeout, escalate: SIGKILL the group; on EPERM, sudo SIGKILL.
//
// Returns true once the leader is gone. ESRCH (already dead) counts as success.
func (c *Controller) killByPIDWithGroupAndSudo(ctx context.Context, pid int) bool {
	if err := c.killProcessGroup(pid, syscall.SIGTERM); err != nil {
		switch {
		case errors.Is(err, syscall.ESRCH):
			return true
		case errors.Is(err, syscall.EPERM):
			c.sudoKill(ctx, pid, "TERM", true)
		default:
			logger.WarnContext(ctx, "Group SIGTERM failed",
				"pid", pid, "error", err)
		}
	}

	if c.pollUntilExit(ctx, pid, c.killTimeout, 200*time.Millisecond) {
		return true
	}

	logger.WarnContext(ctx, "SIGTERM grace expired, escalating to SIGKILL on process group",
		"pid", pid, "timeout", c.killTimeout)

	if err := c.killProcessGroup(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.EPERM) {
			c.sudoKill(ctx, pid, "KILL", true)
		}
	}

	return !c.processAlive(pid)
}
