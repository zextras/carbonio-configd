// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

import (
	"context"
	"os/exec"
	"testing"
)

// TestNewManager verifies that NewManager creates a valid Manager instance
func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
}

// TestIsEnabled tests the IsEnabled method
func TestIsEnabled(t *testing.T) {
	m := NewManager()

	tests := []struct {
		name    string
		unit    string
		skip    bool
		skipMsg string
	}{
		{
			name:    "Check non-existing unit",
			unit:    "carbonio-nonexistent-unit-12345.target",
			skip:    !commandExists("systemctl"),
			skipMsg: "systemctl not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip(tt.skipMsg)
			}

			got := m.IsEnabled(context.Background(), tt.unit)

			// For non-existent unit, we expect false
			if got {
				t.Errorf("IsEnabled(%s) = true, want false for non-existent unit", tt.unit)
			}

			t.Logf("IsEnabled(%s) = %v (expected false for non-existent unit)", tt.unit, got)
		})
	}
}

// TestIsSystemdEnabled tests systemd environment detection
func TestIsSystemdEnabled(t *testing.T) {
	if !commandExists("systemctl") {
		t.Skip("systemctl not available")
	}

	m := NewManager()

	// Call IsSystemdEnabled - this will check if any Carbonio targets are enabled
	result := m.IsSystemdEnabled(context.Background())

	// Log the result for debugging
	t.Logf("IsSystemdEnabled() = %v", result)

	// We can't make strong assertions about the result since it depends on the environment
	// But we can verify that the function runs without panicking
	// In a real Carbonio environment with systemd, this should return true
	// In a container or non-Carbonio system, this should return false

	// Check each individual target for debugging
	for _, target := range carbonioTargets {
		enabled := m.IsEnabled(context.Background(), target)
		t.Logf("  %s: enabled=%v", target, enabled)
	}
}

// TestIsSystemdEnabled_ChecksAllTargets verifies that the function checks all targets
func TestIsSystemdEnabled_ChecksAllTargets(t *testing.T) {
	if len(carbonioTargets) != 5 {
		t.Errorf("carbonioTargets has %d targets, expected 5", len(carbonioTargets))
	}

	expectedTargets := map[string]bool{
		"carbonio-directory-server.target": false,
		"carbonio-appserver.target":        false,
		"carbonio-proxy.target":            false,
		"carbonio-mta.target":              false,
		"service-discover.target":          false,
	}

	for _, target := range carbonioTargets {
		if _, ok := expectedTargets[target]; !ok {
			t.Errorf("Unexpected target in carbonioTargets: %s", target)
		}
		expectedTargets[target] = true
	}

	// Verify all expected targets are present
	for target, found := range expectedTargets {
		if !found {
			t.Errorf("Expected target not found in carbonioTargets: %s", target)
		}
	}
}

// TestIsActive tests the IsActive method
func TestIsActive(t *testing.T) {
	if !commandExists("systemctl") {
		t.Skip("systemctl not available")
	}

	m := NewManager()

	tests := []struct {
		name    string
		service string
		wantErr bool
	}{
		{
			name:    "Check systemd service status",
			service: "systemd-journald.service",
			wantErr: false,
		},
		{
			name:    "Check non-existent service",
			service: "carbonio-nonexistent-service-12345.service",
			wantErr: false, // Should return false, not error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active, err := m.IsActive(context.Background(), tt.service)

			if (err != nil) != tt.wantErr {
				t.Errorf("IsActive(%s) error = %v, wantErr %v", tt.service, err, tt.wantErr)
				return
			}

			t.Logf("IsActive(%s) = %v, err = %v", tt.service, active, err)
		})
	}
}

// TestSystemdCommands tests basic systemd command execution
// These tests are skipped by default as they require root privileges
func TestSystemdCommands(t *testing.T) {
	if !commandExists("systemctl") {
		t.Skip("systemctl not available")
	}

	t.Skip("Skipping systemd command tests - require root privileges and running services")

	m := NewManager()

	// Example test - uncomment and modify for actual testing in appropriate environment
	_ = m
	// err := m.Restart("carbonio-nginx.service")
	// if err != nil {
	//     t.Logf("Restart failed (expected if not running as root): %v", err)
	// }
}

// commandExists checks if a command is available in PATH
func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// BenchmarkIsSystemdEnabled benchmarks the systemd detection logic
func BenchmarkIsSystemdEnabled(b *testing.B) {
	if !commandExists("systemctl") {
		b.Skip("systemctl not available")
	}

	m := NewManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.IsSystemdEnabled(context.Background())
	}
}

// BenchmarkIsEnabled benchmarks the IsEnabled check for a single unit
func BenchmarkIsEnabled(b *testing.B) {
	if !commandExists("systemctl") {
		b.Skip("systemctl not available")
	}

	m := NewManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.IsEnabled(context.Background(), "carbonio-appserver.target")
	}
}
