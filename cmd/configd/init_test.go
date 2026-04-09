// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestInitCmd_Defaults(t *testing.T) {
	cmd := &InitCmd{}
	if cmd.Component != "" {
		t.Errorf("expected empty default Component, got %q", cmd.Component)
	}
	if cmd.Force {
		t.Error("expected Force default to be false")
	}
}

func TestInitCmd_ForceFlag(t *testing.T) {
	cmd := &InitCmd{
		Component: "proxy",
		Force:     true,
	}
	if cmd.Component != "proxy" {
		t.Errorf("Component = %q, want %q", cmd.Component, "proxy")
	}
	if !cmd.Force {
		t.Error("expected Force=true")
	}
}

func TestInitComponents_AllHaveDescriptions(t *testing.T) {
	for name, desc := range initComponents {
		t.Run(name, func(t *testing.T) {
			if desc == "" {
				t.Errorf("component %q has empty description", name)
			}
		})
	}
}

func TestInitComponents_ExpectedEntries(t *testing.T) {
	expected := []string{componentMTA, componentProxy}
	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if _, ok := initComponents[name]; !ok {
				t.Errorf("expected component %q in initComponents", name)
			}
		})
	}
}

func TestConfigsExist_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		component string
	}{
		{name: "mta component", component: componentMTA},
		{name: "proxy component", component: componentProxy},
		{name: "unknown component", component: "unknown_xyz_component"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configsExist should not panic for any input.
			_ = configsExist(tt.component)
		})
	}
}

func TestConfigsExist_UnknownReturnsFalse(t *testing.T) {
	if configsExist("nonexistent_component_xyz") {
		t.Error("expected false for unknown component")
	}
}
