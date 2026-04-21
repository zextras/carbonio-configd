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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// killProcessTimeout is the grace period between SIGTERM and SIGKILL when
// reaping a service's processes. Long enough for well-behaved daemons to
// flush state (mailboxd's JVM shutdown hook needs ~30 s); short enough that
// zmcontrol stop still completes in a reasonable time. Overridable for tests.
var killProcessTimeout = 30 * time.Second

// procFSRoot is the path prefix for Linux process entries in /proc.
// Declared as var so tests can override it with a temporary directory.
var procFSRoot = "/proc/"

// startService starts a service. Bifurcated on IsSystemdMode():
//
//   - strict systemd: every unit started via systemctl. Errors are returned.
//     No fallback — a failed systemctl start is a real error in this mode.
//   - legacy: direct binary spawn (CustomStart hook, then BinaryPath).
//     systemctl is never invoked.
func startService(ctx context.Context, name string, def *ServiceDef) error {
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
	if !IsSystemdMode() {
		return stopWithoutSystemd(ctx, name, def)
	}

	for i := len(def.SystemdUnits) - 1; i >= 0; i-- {
		unit := def.SystemdUnits[i]

		logger.InfoContext(ctx, "Stopping service via systemctl", "service", name, "unit", unit)

		if err := Systemctl(ctx, "stop", unit); err != nil {
			return fmt.Errorf("failed to stop %s (%s): %w", name, unit, err)
		}
	}

	return nil
}

// stopWithoutSystemd is the fast-path stop for non-systemd hosts.
// Precedence: CustomStop hook (fully custom shutdown) > ProcessName pkill.
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

// killProcess terminates every process whose cmdline contains processName.
// Two-phase shutdown: SIGTERM to all, wait up to killProcessTimeout for each
// to exit, then SIGKILL survivors. Self and parent PIDs are excluded so we
// never SIGTERM our own CLI invocation.
//
// Without the wait-then-SIGKILL phase, `zmcontrol stop` returns "Done (1ms)"
// the instant SIGTERM is dispatched, even when the target ignores/slow-handles
// the signal. mailboxd's JVM caught SIGTERM but took >80 s to exit via its
// shutdown hook, leaving the CLI to falsely report success.
func killProcess(ctx context.Context, processName string) error {
	pids, err := scanProcessesByCmdline(processName)
	if err != nil {
		return fmt.Errorf("scan processes for %s: %w", processName, err)
	}

	self := os.Getpid()
	parent := os.Getppid()

	targets := make([]int, 0, len(pids))

	for _, pid := range pids {
		if pid == self || pid == parent {
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

	waitAndEscalate(ctx, targets, killProcessTimeout)

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
	deadline := time.Now().Add(timeout)

	done := make(chan struct{}, len(pids))

	for _, pid := range pids {
		go func(p int) {
			for time.Now().Before(deadline) {
				if !processAlive(p) {
					done <- struct{}{}

					return
				}

				select {
				case <-ctx.Done():
					done <- struct{}{}

					return
				case <-time.After(200 * time.Millisecond):
				}
			}

			if processAlive(p) {
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
	if _, err := os.Stat(procFSRoot + strconv.Itoa(pid)); err != nil {
		return false
	}

	return !isZombie(pid)
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

	deadline := time.Now().Add(killProcessTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}

		select {
		case <-ctx.Done():
			return !processAlive(pid)
		case <-time.After(200 * time.Millisecond):
		}
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

// scanProcessesByCmdline walks /proc/<pid> looking for processes whose command
// line or short name contains the given substring. Returns the matching PIDs.
//
// Primary match: /proc/<pid>/cmdline (NUL-delimited, replaced with spaces).
// Fallback: /proc/<pid>/comm (15-char short name). The fallback is needed for
// daemons like nginx that replace their argv after forking, leaving cmdline
// empty while comm still contains the binary name.
func scanProcessesByCmdline(needle string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	var pids []int

	for _, e := range entries {
		if pid, ok := matchProcEntry(e, needle); ok {
			pids = append(pids, pid)
		}
	}

	return pids, nil
}

// matchProcEntry returns (pid, true) when the /proc entry represents a live process
// that matches needle either in its cmdline or comm name.
func matchProcEntry(e os.DirEntry, needle string) (int, bool) {
	if !e.IsDir() {
		return 0, false
	}

	pid, convErr := strconv.Atoi(e.Name())
	if convErr != nil {
		return 0, false
	}

	procDir := procFSRoot + e.Name()

	// Zombies have already exited and cannot be signaled — never report
	// them as matches. Ownership is intentionally NOT checked: services
	// we register legitimately run as root (postfix master, sudo-spawned
	// zmstat-fd) and must be visible to status. Cross-UID signaling is
	// handled by the caller via sudo fallback in killByPIDWithGroupAndSudo.
	if isZombie(pid) {
		return 0, false
	}

	// Primary: check cmdline. When cmdline is readable and non-empty the
	// verdict is authoritative — a negative match here must NOT fall through
	// to comm, because the 15-char comm truncation (TASK_COMM_LEN-1) collides
	// unrelated processes. Example: service-discover-wrapper.sh has
	// comm="service-discove" which is a prefix of the service-discover daemon
	// binary name "service-discovered"; matching either direction against
	// comm would falsely identify wrapper sidecars as the real daemon.
	data, readErr := os.ReadFile(procDir + "/cmdline") //nolint:gosec // path is /proc/<pid>/cmdline, not user-controlled
	if readErr == nil && len(data) > 0 {
		cmdline := strings.ReplaceAll(string(data), "\x00", " ")
		if strings.Contains(cmdline, needle) {
			return pid, true
		}

		return 0, false
	}

	// Fallback: cmdline unreadable or empty (some kernel threads, or daemons
	// that zero their argv after fork). Only an *exact* comm match counts —
	// substring semantics are unsafe because of the 15-char truncation.
	comm, readErr := os.ReadFile(procDir + "/comm") //nolint:gosec // path is /proc/<pid>/comm, not user-controlled
	if readErr == nil {
		commStr := strings.TrimSpace(string(comm))
		if commStr != "" && commStr == needle {
			return pid, true
		}
	}

	return 0, false
}

// isRunningByPidFile reads a PID from the given file and checks if
// /proc/<pid> exists. Returns (running, ok) where ok=false means the file
// could not be read (permission denied, missing, empty) — caller should
// fall through to an alternative detection method.
func isRunningByPidFile(pidFile string) (running bool, ok bool) {
	data, err := os.ReadFile(pidFile) //nolint:gosec // path from internal registry
	if err != nil {
		return false, false // unreadable — caller should fall through
	}

	// Read only the first line — some services (stats) write multiple PIDs
	// (one per collector). The first is the primary/orchestrator process.
	pidStr, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	pidStr = strings.TrimSpace(pidStr)

	if pidStr == "" {
		return false, false // empty file — fall through
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return false, false // corrupt — fall through
	}

	_, err = os.Stat(procFSRoot + strconv.Itoa(pid))

	return err == nil && !isZombie(pid), true
}

// isZombie reports whether a process is in the zombie state (Z).
// Zombies have already exited — they appear in /proc until their parent reaps
// them, but they are not running. Returns false if /proc/<pid>/status is
// unreadable (caller assumes the process is alive in that case).
func isZombie(pid int) bool {
	data, err := os.ReadFile(procFSRoot + strconv.Itoa(pid) + "/status") //nolint:gosec // path is /proc/<pid>/status
	if err != nil {
		return false
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if state, ok := strings.CutPrefix(line, "State:"); ok {
			return strings.Contains(state, "Z")
		}
	}

	return false
}

// isOwnedByCurrentUser returns true if the process in procDir is owned by the
// current effective UID. Processes owned by other users (e.g. root-elevated
// children that self-re-exec via sudo) are excluded from status scans.
func isOwnedByCurrentUser(procDir string) bool {
	data, err := os.ReadFile(procDir + "/status") //nolint:gosec // path is /proc/<pid>/status
	if err != nil {
		return true // assume owned if unreadable
	}

	uid := os.Getuid()

	for line := range strings.SplitSeq(string(data), "\n") {
		val, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}

		fields := strings.Fields(val)
		if len(fields) == 0 {
			break
		}

		realUID, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil {
			break
		}

		return realUID == uid
	}

	return true
}

// isProcessRunning checks if a process with the given name is running.
// It reports whether any *other* process has a command line that contains
// processName. The current PID and its parent are excluded so a
// `configd control status` invocation does not self-match its own ProcessName.
// Implemented directly against /proc to avoid forking pgrep.
func isProcessRunning(_ context.Context, processName string) bool {
	pids, err := scanProcessesByCmdline(processName)
	if err != nil || len(pids) == 0 {
		return false
	}

	self := os.Getpid()
	parent := os.Getppid()

	for _, pid := range pids {
		if pid != self && pid != parent {
			return true
		}
	}

	return false
}
