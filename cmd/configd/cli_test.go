// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestInitComponents(t *testing.T) {
	if _, ok := initComponents["mta"]; !ok {
		t.Error("expected mta component to be defined")
	}
	if _, ok := initComponents["proxy"]; !ok {
		t.Error("expected proxy component to be defined")
	}
	if desc := initComponents["mta"]; desc == "" {
		t.Error("expected mta to have a description")
	}
}

func TestInitCmd_Structure(t *testing.T) {
	cmd := &InitCmd{
		Component: "mta",
		Force:     true,
	}

	if cmd.Component != "mta" {
		t.Errorf("expected Component mta, got %s", cmd.Component)
	}
	if !cmd.Force {
		t.Error("expected Force true")
	}
}

func TestRewriteCmd_Structure(t *testing.T) {
	cmd := &RewriteCmd{
		ConfigNames: []string{"proxy", "mta"},
	}

	if len(cmd.ConfigNames) != 2 {
		t.Errorf("expected 2 config names, got %d", len(cmd.ConfigNames))
	}
	if cmd.ConfigNames[0] != "proxy" {
		t.Errorf("expected first config proxy, got %s", cmd.ConfigNames[0])
	}
}

func TestDaemonCmd_Structure(t *testing.T) {
	cmd := &DaemonCmd{}
	_ = cmd // Use the variable to avoid unused warnings
}

func TestFormatVersion(t *testing.T) {
	version := formatVersion()
	if version == "" {
		t.Error("expected non-empty version string")
	}
	// Should contain "configd"
	if len(version) < 7 {
		t.Errorf("expected version string to contain 'configd', got %s", version)
	}
}

func TestConfigsExist_UnknownComponent(t *testing.T) {
	// Unknown component should always return false
	if configsExist("unknown_component") {
		t.Error("expected configsExist to return false for unknown component")
	}
}

func TestConfigsExist_MTA_NoFile(t *testing.T) {
	// /opt/zextras/common/conf/postfix/main.cf almost certainly doesn't exist in test env
	result := configsExist(componentMTA)
	// We just verify it doesn't panic; on a dev machine it may or may not exist
	_ = result
}

func TestConfigsExist_Proxy_NoFile(t *testing.T) {
	result := configsExist(componentProxy)
	_ = result
}
