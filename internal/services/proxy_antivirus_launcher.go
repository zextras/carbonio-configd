// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// daemonReadyTimeout bounds how long startThenProbePidfile waits for a
// self-daemonizing service to write a live pidfile before declaring the start
// failed. Declared as var so tests can shorten it.
var daemonReadyTimeout = 30 * time.Second

// daemonProbeInterval is the poll cadence for the pidfile readiness probe.
const daemonProbeInterval = 100 * time.Millisecond

// nginxStopTimeout / clamdStopTimeout / freshclamStopTimeout bound the graceful
// SIGTERM wait before escalating to SIGKILL. nginx fast-shuts on SIGTERM; clamd
// flushes its scan state; freshclam interrupts any in-flight DB update. All
// close within a few seconds.
const (
	nginxStopTimeout     = 10 * time.Second
	clamdStopTimeout     = 10 * time.Second
	freshclamStopTimeout = 10 * time.Second
)

// nginxCustomStart launches nginx and waits until it has written a live
// pidfile, instead of waiting for an sd_notify READY=1 datagram. The bundled
// nginx runs with `daemon on`: the launched process forks the master and
// exits, so an sd_notify wait would mis-read that clean parent exit as a
// startup failure (and the bundle does not emit READY=1 anyway). Mirrors the
// slapd approach in ldap_launcher.go (startThenProbe).
func nginxCustomStart(ctx context.Context, def *ServiceDef) error {
	return startThenProbePidfile(ctx, def, commonPath+"/sbin/nginx",
		[]string{"-c", confPath + "/nginx.conf"})
}

// nginxCustomStop stops nginx via its pidfile, then sweeps any survivors by
// ProcessName. Signalling the master by PID reaps its workers; the sweep covers
// the cases where the pidfile is missing/stale or points at a child rather than
// the master (clamd in particular rewrites/removes its own pidfile).
func nginxCustomStop(ctx context.Context, def *ServiceDef) error {
	return stopViaPidfileThenSweep(ctx, def, nginxStopTimeout)
}

// clamdCustomStart launches clamd and waits for its pidfile, for the same
// reason as nginx: clamd daemonizes (Foreground is off) and does not emit
// sd_notify READY=1, so a notify wait would false-fail on the clean parent
// exit.
func clamdCustomStart(ctx context.Context, def *ServiceDef) error {
	return startThenProbePidfile(ctx, def, commonPath+"/sbin/clamd",
		[]string{"--config-file=" + confPath + "/clamd.conf"})
}

// clamdCustomStop stops clamd via its pidfile, then sweeps survivors.
func clamdCustomStop(ctx context.Context, def *ServiceDef) error {
	return stopViaPidfileThenSweep(ctx, def, clamdStopTimeout)
}

// freshclamCustomStart launches freshclam and waits for its pidfile. freshclam
// runs in the foreground (--foreground=true) and does NOT emit sd_notify
// READY=1, so an sd_notify wait burned the full 30s and failed the start (which
// in turn blocked the antivirus start, since antivirus depends on freshclam).
func freshclamCustomStart(ctx context.Context, def *ServiceDef) error {
	return startThenProbePidfile(ctx, def, commonPath+"/bin/freshclam",
		[]string{
			"--config-file=" + confPath + "/freshclam.conf",
			"--quiet", "-d", "--checks=12", "--foreground=true",
		})
}

// freshclamCustomStop stops freshclam via its pidfile, then sweeps survivors.
func freshclamCustomStop(ctx context.Context, def *ServiceDef) error {
	return stopViaPidfileThenSweep(ctx, def, freshclamStopTimeout)
}

// stopViaPidfileThenSweep stops a self-daemonizing thirdparty cleanly: a
// graceful SIGTERM via the pidfile (so the daemon flushes), then a sweep of any
// remaining processes matching def.ProcessName.
//
// The sweep is essential because these bundled daemons have unreliable pidfile
// lifecycles: clamd writes a child PID and removes the pidfile on its own during
// some operations, and a missing pidfile would otherwise make the pidfile-only
// stop report "already stopped" while the real master keeps running. killProcess
// (SIGTERM, bounded wait, SIGKILL) is idempotent and a no-op when nothing
// matches, so running it unconditionally after the pidfile stop is safe.
func stopViaPidfileThenSweep(ctx context.Context, def *ServiceDef, timeout time.Duration) error {
	if err := gracefulStopViaPidfile(ctx, def.PidFile, def.DisplayName, timeout); err != nil {
		logger.WarnContext(ctx, "Pidfile stop failed, falling back to process sweep",
			"service", def.Name, "error", err)
	}

	if def.ProcessName == "" {
		return nil
	}

	return killProcess(ctx, def.ProcessName)
}

// startThenProbePidfile spawns a self-daemonizing binary detached and waits
// until def.PidFile names a live process (or the readiness timeout elapses).
//
// Unlike startWithSDNotify, the launched process exiting is NOT a failure: a
// daemon that forks a master and exits its parent is the expected shape here.
// Readiness is the pidfile pointing at a live PID, which means the daemon has
// completed enough initialization to record its master PID. The daemon's
// lifetime is bound to context.Background(), not the start request's ctx, so a
// later cancellation cannot SIGKILL it out from under the running system.
func startThenProbePidfile(ctx context.Context, def *ServiceDef, binary string, args []string) error {
	if def.PidFile == "" {
		return fmt.Errorf("%s: no pidfile configured for readiness probe", def.Name)
	}

	logFile := logPath + "/" + def.Name + ".out"

	logFd, err := openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	//nolint:gosec // binary and args come from the internal service registry
	cmd := exec.CommandContext(context.Background(), binary, args...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = detachedSysProcAttr()

	logger.InfoContext(ctx, "Starting service (probe readiness via pidfile)",
		"service", def.Name, "binary", binary, "pidfile", def.PidFile, "log", logFile)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", def.Name, err)
	}

	// Reap the launcher process once it forks-and-exits so it does not linger
	// as a zombie. The daemonized master is reparented to init and unaffected.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(daemonReadyTimeout)
	ticker := time.NewTicker(daemonProbeInterval)

	defer ticker.Stop()

	for {
		if pid := pidFromPidFile(def.PidFile); pid > 0 {
			logger.InfoContext(ctx, "Service is ready (live pidfile)",
				"service", def.Name, "pid", pid)

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("%s did not write a live pidfile within %s", def.Name, daemonReadyTimeout)
			}
		}
	}
}
