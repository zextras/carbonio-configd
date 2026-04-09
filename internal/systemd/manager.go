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
	"os"
	"os/exec"
	"strings"
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
type Manager struct {
	// No fields needed for now, as we'll be calling external commands.
}

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

// IsActive checks if a systemd service is active.
func (m *Manager) IsActive(ctx context.Context, service string) (bool, error) {
	ctx = logger.ContextWithComponent(ctx, "systemd")
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", service)
	output, err := cmd.Output()
	outputStr := strings.TrimSpace(string(output))

	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) { // Exit code 3 means inactive
			return false, nil
		}

		logger.ErrorContext(ctx, "Failed to check status of service",
			"service", service,
			"error", err,
			"output", outputStr)

		return false, fmt.Errorf("failed to check status of service %s: %w", service, err)
	}

	return outputStr == "active", nil
}

// IsEnabled checks if a systemd unit is enabled.
func (m *Manager) IsEnabled(ctx context.Context, unit string) bool {
	ctx = logger.ContextWithComponent(ctx, "systemd")
	cmd := exec.CommandContext(ctx, "systemctl", "is-enabled", unit)
	err := cmd.Run()

	return err == nil
}

// IsSystemdEnabled checks if Carbonio is running with systemd.
// Returns true if at least one of the four Carbonio systemd targets is enabled.
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
