// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

// GetWatchdog returns the watchdog status for a service.
func (s *State) GetWatchdog(service string) *bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if val, ok := s.WatchdogProcess[service]; ok {
		return &val
	}

	return nil
}

// SetWatchdog sets the watchdog status for a service.
func (s *State) SetWatchdog(service string, status bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.WatchdogProcess[service] = status
}

// DelWatchdog removes a service from watchdog tracking.
func (s *State) DelWatchdog(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.WatchdogProcess, service)
}
