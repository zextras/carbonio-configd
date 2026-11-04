// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"

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

	if def.Collectors == nil {
		return SignalReport{}, fmt.Errorf("%w: %s has no rotate", ErrUnsupportedVerb, def.Name)
	}

	return c.rotateCollectors(ctx)
}
