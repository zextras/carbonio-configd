// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import "testing"

// TestNewRegistry_Empty tests that NewRegistry starts with no commands and no typed loaders.
func TestNewRegistry_Empty(t *testing.T) {
	registry := NewRegistry()

	if len(registry.Commands) != 0 {
		t.Errorf("registry.Commands = %v, want empty", registry.Commands)
	}

	if registry.ServerAttrs != nil || registry.GlobalAttrs != nil {
		t.Error("typed LDAP loaders should be unset before RegisterLDAPCommands")
	}
}

// TestRegistry_RegisterLDAPCommands tests that RegisterLDAPCommands adds exactly the LDAP lookups.
func TestRegistry_RegisterLDAPCommands(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterLDAPCommands(NewCommandExecutor(nil))

	if registry.ServerAttrs == nil || registry.GlobalAttrs == nil {
		t.Error("typed LDAP loaders should be set after RegisterLDAPCommands")
	}

	for _, cmd := range []string{"gamau", "garpb", "garpu"} {
		if _, ok := registry.Commands[cmd]; !ok {
			t.Errorf("registry.Commands missing LDAP command: %s", cmd)
		}
	}

	if len(registry.Commands) != 3 {
		t.Errorf("registry.Commands has %d entries, want 3", len(registry.Commands))
	}
}

// TestRegistry_MultipleInstances tests that multiple registry instances are independent
func TestRegistry_MultipleInstances(t *testing.T) {
	registry1 := NewRegistry()
	registry2 := NewRegistry()

	registry1.RegisterLDAPCommands(NewCommandExecutor(nil))

	if _, ok := registry2.Commands["garpu"]; ok {
		t.Error("registry2 should not have garpu after registering only in registry1")
	}

	if _, ok := registry1.Commands["garpu"]; !ok {
		t.Error("registry1 should have garpu after RegisterLDAPCommands")
	}
}
