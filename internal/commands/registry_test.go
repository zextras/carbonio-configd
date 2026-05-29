// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"testing"
)

// TestNewRegistry_DifferentPaths tests that two registries with different ZEXTRAS_HOME
// paths have different executable paths.
func TestNewRegistry_DifferentPaths(t *testing.T) {
	registry1 := NewRegistry("/opt/zextras")
	registry2 := NewRegistry("/custom/zextras")

	// Verify that the registries have different paths
	if registry1.Exe["AMAVIS"] == registry2.Exe["AMAVIS"] {
		t.Errorf("Expected different AMAVIS paths for different ZEXTRAS_HOME, got same: %s", registry1.Exe["AMAVIS"])
	}

	// Verify the paths are correct
	if registry1.Exe["AMAVIS"] != "/opt/zextras/bin/zmamavisdctl" {
		t.Errorf("registry1.Exe[AMAVIS] = %q, want %q", registry1.Exe["AMAVIS"], "/opt/zextras/bin/zmamavisdctl")
	}

	if registry2.Exe["AMAVIS"] != "/custom/zextras/bin/zmamavisdctl" {
		t.Errorf("registry2.Exe[AMAVIS] = %q, want %q", registry2.Exe["AMAVIS"], "/custom/zextras/bin/zmamavisdctl")
	}
}

// TestNewRegistry_DefaultPath tests that NewRegistry with empty string defaults to /opt/zextras
func TestNewRegistry_DefaultPath(t *testing.T) {
	registry := NewRegistry("")

	if registry.Exe["AMAVIS"] != "/opt/zextras/bin/zmamavisdctl" {
		t.Errorf("registry.Exe[AMAVIS] = %q, want %q", registry.Exe["AMAVIS"], "/opt/zextras/bin/zmamavisdctl")
	}
}

// TestNewRegistry_CommandsInitialized tests that NewRegistry initializes the Commands map
func TestNewRegistry_CommandsInitialized(t *testing.T) {
	registry := NewRegistry("/opt/zextras")

	expectedCommands := []string{
		"amavis", "antispam", "antivirus", "cbpolicyd", "ldap",
		"localconfig", "mailbox", "mailboxd", "memcached", "mta",
		"opendkim", "postconf", "postconfd", "proxy", "proxygen",
		"sasl", "service", "stats",
	}

	for _, cmd := range expectedCommands {
		if _, ok := registry.Commands[cmd]; !ok {
			t.Errorf("registry.Commands missing command: %s", cmd)
		}
	}
}

// TestRegistry_RegisterLDAPCommands tests that RegisterLDAPCommands adds LDAP commands to the registry
func TestRegistry_RegisterLDAPCommands(t *testing.T) {
	registry := NewRegistry("/opt/zextras")
	executor := NewCommandExecutor(nil)

	// Before registering, LDAP commands should not be present
	if _, ok := registry.Commands["gacf"]; ok {
		t.Error("gacf should not be in registry before RegisterLDAPCommands")
	}

	// Register LDAP commands
	registry.RegisterLDAPCommands(executor)

	// After registering, LDAP commands should be present
	expectedLDAPCommands := []string{"gacf", "gamau", "garpb", "garpu", "gs", "gs:enabled"}
	for _, cmd := range expectedLDAPCommands {
		if _, ok := registry.Commands[cmd]; !ok {
			t.Errorf("registry.Commands missing LDAP command: %s", cmd)
		}
	}
}

// TestRegistry_MultipleInstances tests that multiple registry instances are independent
func TestRegistry_MultipleInstances(t *testing.T) {
	registry1 := NewRegistry("/opt/zextras")
	registry2 := NewRegistry("/custom/zextras")

	executor := NewCommandExecutor(nil)

	// Register LDAP commands in registry1
	registry1.RegisterLDAPCommands(executor)

	// registry2 should not have LDAP commands
	if _, ok := registry2.Commands["gacf"]; ok {
		t.Error("registry2 should not have gacf after registering only in registry1")
	}

	// registry1 should have LDAP commands
	if _, ok := registry1.Commands["gacf"]; !ok {
		t.Error("registry1 should have gacf after RegisterLDAPCommands")
	}
}
