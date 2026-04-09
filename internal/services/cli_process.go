// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/systemd"
)

// procFSRoot is the path prefix for Linux process entries in /proc.
// Declared as var so tests can override it with a temporary directory.
var procFSRoot = "/proc/"

// startService starts a service. Behavior depends on host init system:
//
//   - systemd is PID 1: use systemctl exclusively. In strict systemd mode
//     (IsSystemdMode), failures are returned as-is. In legacy mode, fall back
//     to direct binary if systemctl fails and a BinaryPath is configured.
//   - systemd is NOT PID 1: skip systemctl entirely (would surface noisy
//     "System has not been booted" stderr); go straight to direct binary,
//     or return a clean "no direct launcher" error if BinaryPath is empty.
func startService(ctx context.Context, name string, def *ServiceDef) error {
	if !systemd.IsBooted() {
		return startWithoutSystemd(ctx, name, def)
	}

	if IsSystemdMode() {
		for _, unit := range def.SystemdUnits {
			logger.InfoContext(ctx, "Starting service via systemctl", "service", name, "unit", unit)

			if err := Systemctl(ctx, "start", unit); err != nil {
				return fmt.Errorf("failed to start %s (%s): %w", name, unit, err)
			}
		}

		return nil
	}

	// Legacy mode: try systemctl first, fall back to direct launcher.
	for _, unit := range def.SystemdUnits {
		logger.InfoContext(ctx, "Starting service", "service", name, "unit", unit)

		if err := Systemctl(ctx, "start", unit); err != nil {
			logger.WarnContext(ctx, "systemctl failed, trying direct launcher",
				"service", name, "error", err)

			return startWithoutSystemd(ctx, name, def)
		}
	}

	return nil
}

// startWithoutSystemd is the fast-path for hosts where /run/systemd/system is
// missing. We never invoke systemctl — its failure mode is verbose and noisy.
// Precedence: CustomStart hook (fully custom launch) > BinaryPath direct spawn.
func startWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if def.CustomStart != nil {
		logger.InfoContext(ctx, "Starting service via custom launcher (systemd not booted)",
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
			"cannot start %s without systemd: no direct launcher registered "+
				"(set ServiceDef.BinaryPath or ServiceDef.CustomStart, "+
				"or run on a systemd-booted host)", name)
	}

	logger.InfoContext(ctx, "Starting service directly (systemd not booted)",
		"service", name, "binary", def.BinaryPath)

	return startDirect(ctx, name, def)
}

// stopService stops a service. Mirrors startService's tri-mode dispatch:
// non-systemd hosts skip systemctl entirely and pkill by ProcessName.
func stopService(ctx context.Context, name string, def *ServiceDef) error {
	if !systemd.IsBooted() {
		return stopWithoutSystemd(ctx, name, def)
	}

	if IsSystemdMode() {
		for i := len(def.SystemdUnits) - 1; i >= 0; i-- {
			unit := def.SystemdUnits[i]

			logger.InfoContext(ctx, "Stopping service via systemctl", "service", name, "unit", unit)

			if err := Systemctl(ctx, "stop", unit); err != nil {
				return fmt.Errorf("failed to stop %s (%s): %w", name, unit, err)
			}
		}

		return nil
	}

	// Legacy mode: try systemctl first, fall back to direct shutdown.
	for i := len(def.SystemdUnits) - 1; i >= 0; i-- {
		unit := def.SystemdUnits[i]

		logger.InfoContext(ctx, "Stopping service", "service", name, "unit", unit)

		if err := Systemctl(ctx, "stop", unit); err != nil {
			logger.WarnContext(ctx, "systemctl failed, trying direct shutdown",
				"service", name, "error", err)

			return stopWithoutSystemd(ctx, name, def)
		}
	}

	return nil
}

// stopWithoutSystemd is the fast-path stop for non-systemd hosts.
// Precedence: CustomStop hook (fully custom shutdown) > ProcessName pkill.
func stopWithoutSystemd(ctx context.Context, name string, def *ServiceDef) error {
	if def.CustomStop != nil {
		logger.InfoContext(ctx, "Stopping service via custom shutdown (systemd not booted)",
			"service", name)

		return def.CustomStop(ctx, def)
	}

	if def.ProcessName == "" {
		return fmt.Errorf(
			"cannot stop %s without systemd: no ProcessName registered "+
				"(set ServiceDef.ProcessName or ServiceDef.CustomStop, "+
				"or run on a systemd-booted host)", name)
	}

	logger.InfoContext(ctx, "Stopping service via pkill (systemd not booted)",
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

// killProcess sends SIGTERM to every process whose cmdline contains processName.
// The current PID and its parent are excluded so we never SIGTERM ourselves.
// Implemented directly against /proc to avoid forking pkill.
func killProcess(_ context.Context, processName string) error {
	pids, err := scanProcessesByCmdline(processName)
	if err != nil {
		return fmt.Errorf("scan processes for %s: %w", processName, err)
	}

	self := os.Getpid()
	parent := os.Getppid()

	for _, pid := range pids {
		if pid == self || pid == parent {
			continue
		}

		proc, ferr := os.FindProcess(pid)
		if ferr != nil {
			continue
		}

		_ = proc.Signal(syscall.SIGTERM)
	}

	return nil
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

	if isZombie(pid) || !isOwnedByCurrentUser(procDir) {
		return 0, false
	}

	// Primary: check cmdline
	data, readErr := os.ReadFile(procDir + "/cmdline") //nolint:gosec // path is /proc/<pid>/cmdline, not user-controlled
	if readErr == nil && len(data) > 0 {
		cmdline := strings.ReplaceAll(string(data), "\x00", " ")
		if strings.Contains(cmdline, needle) {
			return pid, true
		}
	}

	// Fallback: check comm (short process name, max 15 chars).
	// Matches daemons that clear/replace their cmdline after fork.
	comm, readErr := os.ReadFile(procDir + "/comm") //nolint:gosec // path is /proc/<pid>/comm, not user-controlled
	if readErr == nil {
		commStr := strings.TrimSpace(string(comm))
		if commStr != "" && strings.Contains(needle, commStr) {
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

	// Check if /proc/<pid> exists and is not a zombie
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
