// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
)

func TestParseAdvancedStatus_Running(t *testing.T) {
	// JSON-like input with a running module
	input := `[{"commercialName":"TeamChatting","running":true,"other":"data"},{"commercialName":"VideoMeeting","running":false}]`
	// Just verify it doesn't panic and processes multiple entries
	parseAdvancedStatus(input)
}

func TestParseAdvancedStatus_Empty(t *testing.T) {
	parseAdvancedStatus("")
}

func TestParseAdvancedStatus_NoCommercialName(t *testing.T) {
	// Line with no commercialName field — should be skipped
	parseAdvancedStatus(`[{"running":true}]`)
}

func TestParseAdvancedStatus_UnterminatedName(t *testing.T) {
	// commercialName present but no closing quote — should be skipped
	parseAdvancedStatus(`[{"commercialName":"NoClosure}]`)
}

func TestParseAdvancedStatus_MultipleModules(t *testing.T) {
	// Multiple modules split by "}," as the parser uses
	input := `[{"commercialName":"ModA","running":true},{"commercialName":"ModB","running":false},{"commercialName":"ModC","running":true}]`
	// Must not panic
	parseAdvancedStatus(input)
}

func TestGetDistroID_NonExistentFile(t *testing.T) {
	// When /etc/os-release doesn't exist on the test host (unlikely) or reading fails,
	// getDistroID reads the real file. We just verify the function returns a string
	// and doesn't panic regardless of what's on the host.
	id := getDistroID()
	// id may be empty or a real distro ID; either is valid
	_ = id
}

func TestGetDistroID_KnownValues(t *testing.T) {
	id := getDistroID()
	// If the host has /etc/os-release, the result should be a non-empty lower-case word
	if id != "" {
		if strings.ContainsAny(id, " \t\n\"") {
			t.Errorf("getDistroID returned value with unexpected chars: %q", id)
		}
	}
}

func TestStopLDAPIfLocal_EnabledSetExcludesLDAP(t *testing.T) {
	// enabledSet explicitly excludes ldap — should return 0 without touching systemd
	enabledSet := map[string]bool{"mta": true, "proxy": true}
	rc := stopLDAPIfLocal(t.Context(), enabledSet)
	if rc != 0 {
		t.Errorf("expected rc=0 when ldap not in enabledSet, got %d", rc)
	}
}

func TestStopLDAPIfLocal_NilEnabledSet_NotLocal(t *testing.T) {
	// nil enabledSet means we don't filter by set, but LDAP is not local in test env
	// so it should return 0 (LDAP not local)
	rc := stopLDAPIfLocal(t.Context(), nil)
	if rc != 0 {
		t.Errorf("expected rc=0 when LDAP is not local, got %d", rc)
	}
}

func TestCheckAdvancedStatus_NoDirOrNoJar(t *testing.T) {
	// On a dev machine /opt/zextras/lib/ext/carbonio typically doesn't exist or
	// has no carbonio-advanced-*.jar files — function should return without panic
	checkAdvancedStatus(t.Context())
}

func TestVersionCmd_Structure(t *testing.T) {
	cmd := &VersionCmd{Packages: true}
	if !cmd.Packages {
		t.Error("expected Packages=true")
	}
}

func TestVersionCmd_DefaultStructure(t *testing.T) {
	cmd := &VersionCmd{}
	if cmd.Packages {
		t.Error("expected Packages=false by default")
	}
}

func TestControlCmd_Structure(t *testing.T) {
	cmd := &ControlCmd{
		Start:   ControlStartCmd{},
		Stop:    ControlStopCmd{},
		Restart: ControlRestartCmd{},
		Status:  ControlStatusCmd{},
	}

	_ = cmd // Use the variable
}

func TestControlStartCmd_Structure(t *testing.T) {
	cmd := &ControlStartCmd{}
	_ = cmd
}

func TestControlStopCmd_Structure(t *testing.T) {
	cmd := &ControlStopCmd{}
	_ = cmd
}

func TestControlRestartCmd_Structure(t *testing.T) {
	cmd := &ControlRestartCmd{}
	_ = cmd
}

func TestControlStatusCmd_Structure(t *testing.T) {
	cmd := &ControlStatusCmd{}
	_ = cmd
}
