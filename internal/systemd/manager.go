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
	"os"
	"os/exec"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// IsBooted reports whether systemd is the init system on this host. The check
// follows libsystemd's sd_booted(3): /run/systemd/system must be a directory.
// Cached for the process lifetime — sd_booted state never changes at runtime.
func IsBooted() bool {
	bootedOnce.Do(func() {
		st, err := os.Stat("/run/systemd/system")
		booted = err == nil && st.IsDir()
	})

	return booted
}

var (
	booted     bool
	bootedOnce sync.Once
)

// Manager provides an interface for interacting with systemd.
type Manager struct{}

// Carbonio systemd targets used to determine if systemd is enabled
var carbonioTargets = []string{
	"carbonio-directory-server.target",
	"carbonio-appserver.target",
	"carbonio-proxy.target",
	"carbonio-mta.target",
	"service-discover.target",
}

// NewManager creates a new Systemd Manager.
func NewManager() *Manager {
	return &Manager{}
}

// IsEnabled checks if a systemd unit is enabled.
func (m *Manager) IsEnabled(ctx context.Context, unit string) bool {
	ctx = logger.ContextWithComponent(ctx, "systemd")
	cmd := exec.CommandContext(ctx, "systemctl", "is-enabled", unit) //nolint:gosec // fixed binary, unit from registry
	err := cmd.Run()

	return err == nil
}

// IsSystemdEnabled checks if Carbonio is running with systemd.
// Returns true if at least one of the five Carbonio systemd targets is enabled.
// carbonio.target is deliberately excluded — it is a top-level umbrella target
// that may be enabled on any install regardless of orchestration mode, so it
// is not a reliable signal that services are managed via systemctl.
// This matches the logic in carbonio-core-utils/src/bin/shutil.sh:is_systemd()
func (m *Manager) IsSystemdEnabled(ctx context.Context) bool {
	ctx = logger.ContextWithComponent(ctx, "systemd")
	for _, target := range carbonioTargets {
		if m.IsEnabled(ctx, target) {
			logger.DebugContext(ctx, "Detected enabled systemd target",
				"target", target)

			return true
		}
	}

	logger.DebugContext(ctx, "No Carbonio systemd targets enabled, using traditional zm*ctl scripts")

	return false
}
