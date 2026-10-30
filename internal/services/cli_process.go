// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// killProcessTimeout is the grace period between SIGTERM and SIGKILL when
// reaping a service's processes. Long enough for well-behaved daemons to
// flush state (mailboxd's JVM shutdown hook needs ~30 s); short enough that
// zmcontrol stop still completes in a reasonable time.
const killProcessTimeout = 30 * time.Second

// startService starts a service. Bifurcated on IsSystemdMode():
//
//   - strict systemd: every unit started via systemctl. Errors are returned.
//     No fallback — a failed systemctl start is a real error in this mode.
//   - legacy: direct binary spawn (CustomStart hook, then BinaryPath).
//     systemctl is never invoked.
func startService(ctx context.Context, name string, def *ServiceDef) error {
	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not starting", "service", name)

		return nil
	}

	if !IsSystemdMode() {
		return startWithoutSystemd(ctx, name, def)
	}

	for _, unit := range def.SystemdUnits {
		logger.InfoContext(ctx, "Starting service via systemctl", "service", name, "unit", unit)

		if err := Systemctl(ctx, "start", unit); err != nil {
			return fmt.Errorf("failed to start %s (%s): %w", name, unit, err)
		}
	}

	return nil
}

// startWithoutSystemd is the fast-path for hosts where /run/systemd/system is
// missing. We never invoke systemctl — its failure mode is verbose and noisy.
// Precedence: CustomStart hook (fully custom launch) > BinaryPath direct spawn.
func startWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
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

	return startDirect(ctx, name, def)
}

// stopService stops a service. Mirrors startService's bifurcation on
// IsSystemdMode(). Legacy mode calls CustomStop or pkill by ProcessName
// and never invokes systemctl — critical for services like stats whose
// workers run outside any systemd cgroup (e.g. spawned by statsCustomStart
// directly into /init.scope in container installs).
func stopService(ctx context.Context, name string, def *ServiceDef) error {
	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not stopping", "service", name)

		return nil
	}

	if !IsSystemdMode() {
		return stopWithoutSystemd(ctx, name, def)
	}

	for i := range slices.Backward(def.SystemdUnits) {
		unit := def.SystemdUnits[i]

		logger.InfoContext(ctx, "Stopping service via systemctl", "service", name, "unit", unit)

		if err := Systemctl(ctx, "stop", unit); err != nil {
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
func stopWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if def.CustomStop != nil {
		logger.InfoContext(ctx, "Stopping service via custom shutdown (legacy mode)",
			"service", name)

		return def.CustomStop(ctx, def)
	}

	if def.GracefulStop > 0 && def.PidFile != "" {
		return gracefulStopViaPidfile(ctx, def.PidFile, name, def.ProcessName, def.GracefulStop)
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

		go awaitSDNotifyStopping(observerCtx, name)
	}

	logger.InfoContext(ctx, "Stopping service via pkill (legacy mode)",
		"service", name, "process", def.ProcessName)

	return killProcess(ctx, def.ProcessName)
}

// reloadWithoutSystemd is the fast-path reload for non-systemd hosts.
// systemctl is never invoked. MTA has a native reload ("postfix reload",
// mirroring mta_launcher.go's direct sudo-postfix pattern); every other
// service falls back to stopService+startService, which — already being in
// legacy mode — resolve to CustomStop/CustomStart or ProcessName/BinaryPath,
// never systemctl.
func reloadWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if name == serviceMTA {
		// Legacy zmmtactl reload runs "reload" on each START_SCRIPTS entry:
		// zmmilterctl and zmsaslauthdctl implement reload as stop+start, then
		// postfix reload (carbonio-core 4.6.7 src/bin/mtactl.sh).
		for _, dep := range def.Dependencies {
			if !isDepEnabled(ctx, dep) {
				continue
			}

			if err := ServiceRestart(ctx, dep); err != nil {
				logger.WarnContext(ctx, "Dependency restart failed during MTA reload",
					"dependency", dep, "error", err)
			}
		}

		logger.InfoContext(ctx, "Reloading service via postfix reload (legacy mode)", "service", name)

		return sudoRun(ctx, postfixBin, actionReload)
	}

	logger.InfoContext(ctx, "No native reload for service, restarting (legacy mode)", "service", name)

	if err := stopService(ctx, name, def); err != nil {
		return fmt.Errorf("failed to stop %s during reload fallback: %w", name, err)
	}

	return startService(ctx, name, def)
}

// selfOrParentFilter returns a predicate reporting whether a pid is the
// current process or its parent, computed once so scanning many candidate
// pids doesn't re-syscall Getpid/Getppid per candidate. Shared by every
// /proc scan that must not match the CLI's own invocation:
// pidFromProcessName, pidsByIdentity, and killProcess.
func selfOrParentFilter() func(pid int) bool {
	self, parent := os.Getpid(), os.Getppid()

	return func(pid int) bool { return pid == self || pid == parent }
}

// startDirect starts a service binary directly (non-systemd fallback).
//
// For services that self-daemonize (postfix master, slapd -d 0, opendkim, etc.)
// the launcher returns immediately and we capture the exit code synchronously.
// For services marked Detached (configd's own daemon), we spawn with a new
// session and detach stdin/stdout/stderr to a log file so the configd CLI can
// exit without taking down its own daemon child.
func startDirect(ctx context.Context, name string, def *ServiceDef) error {
	if def.BinaryPath == "" {
		return fmt.Errorf("no binary path defined for %s", name)
	}

	if _, err := os.Stat(def.BinaryPath); err != nil {
		return fmt.Errorf("%s binary not found at %s: %w", name, def.BinaryPath, err)
	}

	binary := def.BinaryPath
	args := def.BinaryArgs

	// Services like postfix require root. If current user is not root, use sudo.
	if def.NeedsRoot && os.Getuid() != 0 {
		args = append([]string{binary}, args...)
		binary = "/usr/bin/sudo"
	}

	logger.InfoContext(ctx, "Starting service directly",
		"service", name, "binary", binary, "args", args, "detached", def.Detached)

	if def.Detached {
		return startDetached(ctx, name, def)
	}

	cmd := exec.CommandContext(ctx, binary, args...)

	if def.UseSDNotify {
		return startWithSDNotify(ctx, cmd, name)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start %s directly: %s: %w", name, strings.TrimSpace(string(output)), err)
	}

	return nil
}

// startDetached spawns a long-running daemon in its own session, releasing the
// controlling terminal so the parent CLI can exit cleanly. stdout+stderr are
// redirected to def.LogFile (default: basePath/log/<name>.out).
func startDetached(_ context.Context, name string, def *ServiceDef) error {
	logPath := def.LogFile
	if logPath == "" {
		logPath = basePath + "/log/" + name + ".out"
	}

	//nolint:gosec // path is from internal registry
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open log file %s for %s: %w", logPath, name, err)
	}

	// Note: we deliberately do NOT use exec.CommandContext — the daemon must
	// outlive the CLI's context. The daemon is supervised by its own logic
	// (systemd-style sd_notify, watchdog, signal handlers).
	//nolint:noctx,gosec // detached daemon must outlive CLI context; path is from internal registry
	cmd := exec.Command(def.BinaryPath, def.BinaryArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = detachedSysProcAttr()

	// For sd_notify-capable detached daemons, wait for READY=1 before returning.
	// startWithSDNotify calls cmd.Start() internally.
	if def.UseSDNotify {
		err := startWithSDNotify(context.Background(), cmd, name)
		_ = logFile.Close()

		if err != nil {
			return fmt.Errorf("failed to start %s: %w", name, err)
		}

		if cmd.Process != nil {
			if releaseErr := cmd.Process.Release(); releaseErr != nil {
				logger.WarnContext(context.Background(), "Failed to release child handle",
					"service", name, "pid", cmd.Process.Pid, "error", releaseErr)
			}
		}

		return nil
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()

		return fmt.Errorf("failed to spawn %s: %w", name, err)
	}

	// Once the daemon has its own session it owns the fd; the parent can drop it.
	_ = logFile.Close()
	// Reap eventually — Release lets the kernel clean up when daemon exits.
	if err := cmd.Process.Release(); err != nil {
		logger.WarnContext(context.Background(), "Failed to release child handle",
			"service", name, "pid", cmd.Process.Pid, "error", err)
	}

	return nil
}

// killProcess terminates every process of the identity processName (exe/argv
// match, see procident.go — never a cmdline substring), always resolved from a
// fresh /proc listing. Two-phase shutdown: SIGTERM to all, wait up to
// killProcessTimeout for each to exit, then SIGKILL survivors. Self and parent
// PIDs are excluded so we never SIGTERM our own CLI invocation.
//
// Without the wait-then-SIGKILL phase, `zmcontrol stop` returns "Done (1ms)"
// the instant SIGTERM is dispatched, even when the target ignores/slow-handles
// the signal. mailboxd's JVM caught SIGTERM but took >80 s to exit via its
// shutdown hook, leaving the CLI to falsely report success.
func killProcess(ctx context.Context, processName string) error {
	return killProcessWithGrace(ctx, processName, killProcessTimeout)
}

// killProcessWithGrace is killProcess with an explicit SIGTERM→SIGKILL grace
// period, so tests can shorten it without mutating package state.
func killProcessWithGrace(ctx context.Context, processName string, grace time.Duration) error {
	pids, err := scanProcesses(processName)
	if err != nil {
		return fmt.Errorf("scan processes for %s: %w", processName, err)
	}

	isSelfOrParent := selfOrParentFilter()

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
		signalPID(pid, syscall.SIGTERM)
	}

	waitAndEscalate(ctx, targets, grace)

	return nil
}

// signalPID best-effort sends sig to pid, ignoring ESRCH (already gone) and
// EPERM (cross-UID; handled separately where needed).
func signalPID(pid int, sig syscall.Signal) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	_ = proc.Signal(sig)
}

// waitAndEscalate polls each pid in parallel until it exits or the deadline
// expires, then SIGKILLs any survivors. Parallel so that N slow-exiting
// processes do not sum to N*timeout.
func waitAndEscalate(ctx context.Context, pids []int, timeout time.Duration) {
	done := make(chan struct{}, len(pids))

	for _, pid := range pids {
		go func(p int) {
			if !pollUntilExit(ctx, p, timeout, 200*time.Millisecond) {
				logger.WarnContext(ctx, "SIGTERM grace expired, escalating to SIGKILL",
					"pid", p, "timeout", timeout)
				signalPID(p, syscall.SIGKILL)
			}

			done <- struct{}{}
		}(pid)
	}

	for range pids {
		<-done
	}
}

// processAlive returns true when /proc/<pid> exists and the entry is not a
// zombie. Zombies have already exited and are only waiting for a parent reap.
func processAlive(pid int) bool {
	if _, err := os.Stat(procFSRoot + strconv.Itoa(pid)); err != nil { //nolint:gosec // path is /proc/<pid>, pid is an int
		return false
	}

	return !isZombie(pid)
}

// pollUntilExit polls processAlive(pid) at the given interval until it
// exits, ctx is cancelled, or timeout elapses. Returns true once the
// process is confirmed gone. Shared poll core for waitForProcessExit
// (helpers.go, 50ms/single pid), waitAndEscalate (200ms/N pids in
// parallel), and killByPIDWithGroupAndSudo (200ms/process group). What
// happens after a false return (which signal, which target) differs per
// caller and is intentionally left to them.
func pollUntilExit(ctx context.Context, pid int, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}

		select {
		case <-ctx.Done():
			return !processAlive(pid)
		case <-time.After(interval):
		}
	}

	return !processAlive(pid)
}

// killProcessGroup sends sig to the process group led by pgid (negative PID
// semantics of syscall.Kill). Returns the raw syscall error so callers can
// distinguish EPERM (cross-UID) from ESRCH (group already gone).
func killProcessGroup(pgid int, sig syscall.Signal) error {
	return syscall.Kill(-pgid, sig)
}

// sudoKill escalates a signal via sudo when the calling user cannot signal
// the target directly (e.g. root-owned `sudo zmstat-fd` child). No-op error
// on failure — the caller logs. Signal is passed as the literal name ("TERM",
// "KILL") that `kill(1)` accepts.
func sudoKill(ctx context.Context, pid int, signal string, groupKill bool) {
	target := strconv.Itoa(pid)
	if groupKill {
		target = "-" + target
	}

	// #nosec G204 — arguments are a fixed literal signal name and a numeric PID
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "kill", "-"+signal, "--", target)

	if out, err := cmd.CombinedOutput(); err != nil {
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
//  3. Wait up to killProcessTimeout for the group leader to exit.
//  4. On timeout, escalate: SIGKILL the group; on EPERM, sudo SIGKILL.
//
// Returns true once the leader is gone. ESRCH (already dead) counts as success.
func killByPIDWithGroupAndSudo(ctx context.Context, pid int) bool {
	if err := killProcessGroup(pid, syscall.SIGTERM); err != nil {
		switch {
		case errors.Is(err, syscall.ESRCH):
			return true
		case errors.Is(err, syscall.EPERM):
			sudoKill(ctx, pid, "TERM", true)
		default:
			logger.WarnContext(ctx, "Group SIGTERM failed",
				"pid", pid, "error", err)
		}
	}

	if pollUntilExit(ctx, pid, killProcessTimeout, 200*time.Millisecond) {
		return true
	}

	logger.WarnContext(ctx, "SIGTERM grace expired, escalating to SIGKILL on process group",
		"pid", pid, "timeout", killProcessTimeout)

	if err := killProcessGroup(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.EPERM) {
			sudoKill(ctx, pid, "KILL", true)
		}
	}

	return !processAlive(pid)
}
