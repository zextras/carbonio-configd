// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
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

func TestGetServiceDetail_NotRunning(t *testing.T) {
	result := getServiceDetail(context.Background(), "mta", false)
	if result != "" {
		t.Errorf("expected empty string for not-running service, got %q", result)
	}
}

func TestGetServiceDetail_UnknownService(t *testing.T) {
	result := getServiceDetail(context.Background(), "nonexistent-xyz", true)
	if result != "" {
		t.Errorf("expected empty string for unknown service, got %q", result)
	}
}

func TestGetServiceDetail_KnownService(t *testing.T) {
	_ = getServiceDetail(context.Background(), "mta", true)
}

func TestServiceDetailFromSystemd_NoUnits(t *testing.T) {
	def := &services.ServiceDef{
		Name:         "test",
		DisplayName:  "Test",
		SystemdUnits: []string{},
	}
	result := serviceDetailFromSystemd(context.Background(), def)
	if result != "" {
		t.Errorf("expected empty string for service with no units, got %q", result)
	}
}

func TestServiceDetailFromProc_ZeroPID(t *testing.T) {
	def := &services.ServiceDef{
		Name:        "test",
		DisplayName: "Test",
		ProcessName: "nonexistent-process-xyz",
		PidFile:     "",
	}
	result := serviceDetailFromProc(def)
	if result != "" {
		t.Errorf("expected empty string for service with PID 0, got %q", result)
	}
}

func TestServiceDetailFromProc_WithPID(t *testing.T) {
	self := os.Getpid()
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := &services.ServiceDef{
		Name:        "test",
		DisplayName: "Test",
		PidFile:     pidFile,
		ProcessName: "nonexistent-for-test-xyz",
	}
	result := serviceDetailFromProc(def)
	if !strings.Contains(result, "pid") {
		t.Errorf("expected detail to contain 'pid', got %q", result)
	}
}

func TestServiceDetailFromSystemd_NilUnits(t *testing.T) {
	def := &services.ServiceDef{
		Name:         "testsvc",
		DisplayName:  "Test Service",
		SystemdUnits: nil,
	}
	result := serviceDetailFromSystemd(context.Background(), def)
	if result != "" {
		t.Errorf("expected empty string for nil SystemdUnits, got %q", result)
	}
}

func TestGetServiceDetail_NilDef(t *testing.T) {
	result := getServiceDetail(context.Background(), "nonexistent-xyz-abc", true)
	if result != "" {
		t.Errorf("expected empty string for nil def, got %q", result)
	}
}

func TestStopLDAPIfLocal_WithNonLocalEnforcedSet(t *testing.T) {
	enabledSet := map[string]bool{"ldap": true}
	rc := stopLDAPIfLocal(context.Background(), enabledSet)
	if rc != 0 {
		t.Errorf("expected rc=0 when ldap not local, got %d", rc)
	}
}

func TestCliHeaderPrintedOnce(t *testing.T) {
	origHeaderPrinted := cliHeaderPrinted
	cliHeaderPrinted = false
	defer func() { cliHeaderPrinted = origHeaderPrinted }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliHeader()
	cliHeader()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	hostCount := strings.Count(output, "Host")
	if hostCount != 1 {
		t.Errorf("expected exactly 1 'Host' line, got %d", hostCount)
	}
}

func TestParseAdvancedStatus_WithRunningModules(t *testing.T) {
	input := `[{"commercialName":"TeamChatting","running":true},{"commercialName":"VideoMeeting","running":false}]`

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	parseAdvancedStatus(input)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "TeamChatting") {
		t.Errorf("expected TeamChatting in output, got %q", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("expected 'running' in output, got %q", output)
	}
}

func TestParseAdvancedStatus_AllStoppedModules(t *testing.T) {
	input := `[{"commercialName":"Modules","running":false}]`

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	parseAdvancedStatus(input)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "NOT running") {
		t.Errorf("expected 'NOT running' in output, got %q", output)
	}
}

func TestServiceDetailFromSystemd_WithUnits(t *testing.T) {
	def := &services.ServiceDef{
		Name:         "mta",
		DisplayName:  "mta",
		SystemdUnits: []string{"carbonio-postfix.service"},
	}
	_ = serviceDetailFromSystemd(context.Background(), def)
}

func TestStartEnabledServices_EmptySet(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	rc := startEnabledServices(context.Background(), map[string]bool{}, 100)
	if rc != 0 {
		t.Errorf("expected rc=0 with empty enabledSet, got %d", rc)
	}
}

func TestControlStatus_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_ = controlStatus(context.Background())
}

func TestVersionCmd_ShowsVersion(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &VersionCmd{Packages: false}
	err := cmd.Run()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		// /opt/zextras/.version may not exist in CI; just verify it doesn't panic
		t.Logf("VersionCmd.Run() returned error (expected in CI): %v", err)

		return
	}

	if !strings.Contains(output, "Carbonio") {
		t.Errorf("expected output to contain 'Carbonio', got %q", output)
	}
}

func TestCliProgress_FailureWithTiming(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := cliProgress("Starting", "TestSvc")
	done(fmt.Errorf("test error: not found"))

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Starting") {
		t.Errorf("expected 'Starting' in output, got %q", output)
	}
	if !strings.Contains(output, "Failed") {
		t.Errorf("expected 'Failed' in output, got %q", output)
	}
}

func TestGetDistroID(t *testing.T) {
	result := getDistroID()
	_ = result
}

func TestServiceDetailFromProc_NotRunning(t *testing.T) {
	def := &services.ServiceDef{
		Name:        "test-not-running",
		ProcessName: "nonexistent-process-xyz",
	}
	detail := serviceDetailFromProc(def)
	if detail != "" {
		t.Errorf("expected empty detail for not-running service, got %q", detail)
	}
}

func TestGetServiceDetail_NotRunning2(t *testing.T) {
	detail := getServiceDetail(context.Background(), "test-svc", false)
	if detail != "" {
		t.Errorf("expected empty detail for not-running service, got %q", detail)
	}
}

func TestParseAdvancedStatus_EmptyInput(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	parseAdvancedStatus("")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if output != "" {
		t.Errorf("expected empty output for empty input, got %q", output)
	}
}

func TestStopLDAPIfLocal_DisabledService(t *testing.T) {
	enabledSet := map[string]bool{"ldap": false}
	result := stopLDAPIfLocal(context.Background(), enabledSet)
	_ = result
}

func TestControlStatus_Integration2(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cliHeaderPrinted = false
	controlStatus(context.Background())
}

func TestGetServiceDetail_FromSystemd(t *testing.T) {
	if services.IsSystemdMode() {
		def := services.LookupService("configd")
		if def == nil {
			t.Skip("configd service not in registry")
		}
		detail := serviceDetailFromSystemd(context.Background(), def)
		_ = detail
	}
}

func TestCheckAdvancedStatus_NoJarDir(t *testing.T) {
	cliHeaderPrinted = false
	checkAdvancedStatus(context.Background())
}

func TestStopLDAPIfLocal_NotLocal(t *testing.T) {
	if services.IsLDAPLocal() {
		t.Skip("LDAP is local on this host")
	}
	result := stopLDAPIfLocal(context.Background(), nil)
	if result != 0 {
		t.Errorf("expected 0 when LDAP not local, got %d", result)
	}
}

func TestStartEnabledServices_EmptySet_ZeroTimeout(t *testing.T) {
	result := startEnabledServices(context.Background(), map[string]bool{}, 0)
	if result != 0 {
		t.Errorf("expected 0 for empty set, got %d", result)
	}
}

func TestServiceDetailFromSystemd_NoSystemdUnits(t *testing.T) {
	def := &services.ServiceDef{
		Name:         "test-no-units",
		DisplayName:  "Test No Units",
		SystemdUnits: []string{},
	}
	detail := serviceDetailFromSystemd(context.Background(), def)
	if detail != "" {
		t.Errorf("expected empty detail for service with no systemd units, got %q", detail)
	}
}
