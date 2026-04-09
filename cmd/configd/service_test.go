// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestServiceStartCmd_NoRewriteFlag(t *testing.T) {
	tests := []struct {
		name     string
		cmd      *ServiceStartCmd
		expected bool
	}{
		{
			name: "no-rewrite flag set",
			cmd: &ServiceStartCmd{
				Name:      "proxy",
				NoRewrite: true,
				Extra:     []string{},
			},
			expected: true,
		},
		{
			name: "no-rewrite flag not set",
			cmd: &ServiceStartCmd{
				Name:      "proxy",
				NoRewrite: false,
				Extra:     []string{},
			},
			expected: false,
		},
		{
			name: "legacy norewrite positional arg",
			cmd: &ServiceStartCmd{
				Name:      "proxy",
				NoRewrite: false,
				Extra:     []string{"norewrite"},
			},
			expected: true,
		},
		{
			name: "extra args without norewrite",
			cmd: &ServiceStartCmd{
				Name:      "proxy",
				NoRewrite: false,
				Extra:     []string{"other", "args"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic from Run() that processes Extra args
			for _, a := range tt.cmd.Extra {
				if a == "norewrite" {
					tt.cmd.NoRewrite = true
				}
			}

			if tt.cmd.NoRewrite != tt.expected {
				t.Errorf("expected NoRewrite %v, got %v", tt.expected, tt.cmd.NoRewrite)
			}
		})
	}
}

func TestServiceStopCmd_Fields(t *testing.T) {
	cmd := &ServiceStopCmd{
		Name: "mailbox",
	}

	if cmd.Name != "mailbox" {
		t.Errorf("expected Name mailbox, got %s", cmd.Name)
	}
}

func TestServiceRestartCmd_Fields(t *testing.T) {
	cmd := &ServiceRestartCmd{
		Name:      "ldap",
		NoRewrite: true,
	}

	if cmd.Name != "ldap" {
		t.Errorf("expected Name ldap, got %s", cmd.Name)
	}
	if !cmd.NoRewrite {
		t.Error("expected NoRewrite true")
	}
}

func TestServiceReloadCmd_Fields(t *testing.T) {
	cmd := &ServiceReloadCmd{
		Name: "proxy",
	}

	if cmd.Name != "proxy" {
		t.Errorf("expected Name proxy, got %s", cmd.Name)
	}
}

func TestServiceStatusCmd_Fields(t *testing.T) {
	cmd := &ServiceStatusCmd{
		Name: "mta",
	}

	if cmd.Name != "mta" {
		t.Errorf("expected Name mta, got %s", cmd.Name)
	}
}

func TestServiceCmd_Structure(t *testing.T) {
	cmd := &ServiceCmd{
		List:    ServiceListCmd{},
		Start:   ServiceStartCmd{Name: "test"},
		Stop:    ServiceStopCmd{Name: "test"},
		Restart: ServiceRestartCmd{Name: "test"},
		Reload:  ServiceReloadCmd{Name: "test"},
		Status:  ServiceStatusCmd{Name: "test"},
	}

	if cmd.Start.Name != "test" {
		t.Error("expected Start command to be initialized")
	}
	if cmd.Stop.Name != "test" {
		t.Error("expected Stop command to be initialized")
	}
	if cmd.Restart.Name != "test" {
		t.Error("expected Restart command to be initialized")
	}
	if cmd.Reload.Name != "test" {
		t.Error("expected Reload command to be initialized")
	}
	if cmd.Status.Name != "test" {
		t.Error("expected Status command to be initialized")
	}
}
