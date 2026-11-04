// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

// Mode answers the one question every orchestration decision asks: are the
// services managed through systemd units (a Carbonio role target is enabled)?
// The answer is detected once per process, by the service controller behind
// the daemon's service manager (see services.Controller.IsSystemdMode); the
// restart policy, the config manager and the watchdog all consult that one
// answer through this interface instead of detecting it themselves. A nil Mode
// is a host without systemd management.
type Mode interface {
	// IsSystemdMode reports whether services are managed through systemd units.
	IsSystemdMode() bool
}

// Managed reports whether m says the services are managed through systemd
// units; a nil m (a test double, a hook backend) never does.
func Managed(m Mode) bool {
	return m != nil && m.IsSystemdMode()
}
