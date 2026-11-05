// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package systemd provides an interface for interacting with systemd service management.
// It detects systemd availability, manages service units (start, stop, restart, status),
// and provides an abstraction layer over systemctl commands. The manager automatically
// falls back to traditional init.d scripts when systemd is unavailable.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// runSystemdDir is the directory whose presence marks a systemd-booted host
// (libsystemd's sd_booted(3)).
const runSystemdDir = "/run/systemd/system"

// Manager provides an interface for interacting with systemd. Every host
// effect goes through the injected sys.System.
type Manager struct {
	env sys.System

	bootedOnce sync.Once
	booted     bool
}

// Carbonio systemd targets used to determine if systemd is enabled
var carbonioTargets = []string{
	"carbonio-directory-server.target",
	"carbonio-appserver.target",
	"carbonio-proxy.target",
	"carbonio-mta.target",
	"service-discover.target",
}

// NewManager creates a Systemd Manager acting through env.
func NewManager(env sys.System) *Manager {
	return &Manager{env: env}
}

// IsBooted reports whether systemd is the init system on this host. The check
// follows libsystemd's sd_booted(3): /run/systemd/system must be a directory.
// Cached per Manager — sd_booted state never changes at runtime.
func (m *Manager) IsBooted() bool {
	m.bootedOnce.Do(func() {
		st, err := m.env.FS.Stat(runSystemdDir)
		m.booted = err == nil && st.IsDir()
	})

	return m.booted
}

// IsEnabled checks if a systemd unit is enabled. "systemctl is-enabled"
// exiting non-zero means the unit is not enabled (disabled, masked or
// unknown): that is an answer, (false, nil). Any other failure (systemctl
// missing, cancelled or timed-out context) means the question could not be
// answered and is returned as an error instead of a silent "not enabled".
func (m *Manager) IsEnabled(ctx context.Context, unit string) (bool, error) {
	ctx = logger.ContextWithComponent(ctx, "systemd")

	_, err := m.env.Exec.Run(ctx, &sys.Cmd{Path: "systemctl", Args: []string{"is-enabled", unit}})
	if err == nil {
		return true, nil
	}

	if _, notEnabled := errors.AsType[*sys.ExitError](err); notEnabled {
		return false, nil
	}

	return false, fmt.Errorf("systemctl is-enabled %s: %w", unit, err)
}

// probeTimeout bounds the whole IsSystemdEnabled probe: a hung systemctl (dbus
// not ready at boot) must not stall the caller indefinitely.
const probeTimeout = 15 * time.Second

// IsSystemdEnabled checks if Carbonio is running with systemd.
// Returns true if at least one of the five Carbonio systemd targets is enabled.
// carbonio.target is deliberately excluded — it is a top-level umbrella target
// that may be enabled on any install regardless of orchestration mode, so it
// is not a reliable signal that services are managed via systemctl.
// This matches the logic in carbonio-core-utils/src/bin/shutil.sh:is_systemd()
//
// The probe is bounded by probeTimeout. When no target is found enabled and at
// least one probe failed (systemctl missing or hung, timeout), the false result
// is not a verdict and the joined probe errors are returned: callers must not
// cache it.
func (m *Manager) IsSystemdEnabled(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(logger.ContextWithComponent(ctx, "systemd"), probeTimeout)
	defer cancel()

	var probeErr error

	for _, target := range carbonioTargets {
		enabled, err := m.IsEnabled(ctx, target)
		if err != nil {
			logger.WarnContext(ctx, "Cannot tell whether a Carbonio systemd target is enabled",
				"target", target, "error", err)

			probeErr = errors.Join(probeErr, err)

			continue
		}

		if enabled {
			logger.DebugContext(ctx, "Detected enabled systemd target",
				"target", target)

			return true, nil
		}
	}

	if probeErr != nil {
		return false, probeErr
	}

	logger.DebugContext(ctx, "No Carbonio systemd targets enabled, using traditional zm*ctl scripts")

	return false, nil
}
