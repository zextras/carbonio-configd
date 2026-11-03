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
	"syscall"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// ErrUnsupportedVerb is returned (wrapped) when a service has no refresh or
// rotate action. The CLI turns it into a usage failure.
var ErrUnsupportedVerb = errors.New("verb not supported by this service")

// ErrNoStatsPidFiles is returned by the stats rotate action when no collector
// left a pidfile, i.e. there is nothing to signal (legacy statctl.pl rotate
// exits 1 then).
var ErrNoStatsPidFiles = errors.New("no stats collector pid files found")

// ServiceRefresh delivers the service's RefreshSignal to its main process
// (milter: SIGCONT, which makes MilterServer re-read its configuration). The
// report's Signaled list is empty when the service is not running — a
// successful no-op, as for legacy zmmilterctl refresh. It signals the process
// directly in both orchestration modes: neither systemd nor the legacy
// scripts offer a unit-level refresh.
func (c *Controller) ServiceRefresh(ctx context.Context, name string) (SignalReport, error) {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return SignalReport{}, fmt.Errorf(errUnknownService, name)
	}

	if def.RefreshSignal == 0 {
		return SignalReport{}, fmt.Errorf("%w: %s has no refresh", ErrUnsupportedVerb, def.Name)
	}

	pid := c.runningPID(ctx, def)
	if pid == 0 {
		return SignalReport{}, nil
	}

	if err := c.env.Proc.Signal(pid, def.RefreshSignal); err != nil {
		return SignalReport{}, fmt.Errorf("signal %s (pid %d) with %s: %w", def.Name, pid, def.RefreshSignal, err)
	}

	return SignalReport{Signaled: []int{pid}}, nil
}

// ServiceRotate runs the service's rotate action (stats: SIGHUP to every
// collector so they reopen their logs).
func (c *Controller) ServiceRotate(ctx context.Context, name string) (SignalReport, error) {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return SignalReport{}, fmt.Errorf(errUnknownService, name)
	}

	if def.Rotate == nil {
		return SignalReport{}, fmt.Errorf("%w: %s has no rotate", ErrUnsupportedVerb, def.Name)
	}

	return def.Rotate(ctx)
}

// statsRotate sends SIGHUP to the pid of every stats collector pidfile (legacy
// statctl.pl rotate). Pids that are gone are reported, not an error. A
// root-owned collector (sudo-spawned zmstat-fd) is signaled through sudo.
func (c *Controller) statsRotate(ctx context.Context) (SignalReport, error) {
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

		pidPath := filepath.Join(c.p.statsPidDir, entry.Name())

		data, readErr := c.env.FS.ReadFile(pidPath)
		if readErr != nil {
			logger.WarnContext(ctx, "Failed to read stats PID file", "path", pidPath, "error", readErr)

			continue
		}

		pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if convErr != nil || pid <= 0 {
			logger.WarnContext(ctx, "Invalid PID in stats PID file", "path", pidPath)

			continue
		}

		found = true

		if !c.processAlive(pid) {
			report.NotRunning = append(report.NotRunning, pid)

			continue
		}

		if sigErr := c.env.Proc.Signal(pid, syscall.SIGHUP); sigErr != nil {
			if !errors.Is(sigErr, syscall.EPERM) {
				report.NotRunning = append(report.NotRunning, pid)

				continue
			}

			c.sudoKill(ctx, pid, "HUP", false)
		}

		report.Signaled = append(report.Signaled, pid)
	}

	if !found {
		return report, ErrNoStatsPidFiles
	}

	return report, nil
}
