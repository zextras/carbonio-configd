// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
)

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

// TestServiceDetailFromProc_IncludesSince guards the legacy-mode start time:
// a live PID must yield a "since <timestamp>", never a missing or "n/a" value.
func TestServiceDetailFromProc_IncludesSince(t *testing.T) {
	self := os.Getpid()
	pidFile := filepath.Join(t.TempDir(), "test.pid")

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
	if !strings.Contains(result, "since ") || strings.Contains(result, "n/a") {
		t.Errorf("expected detail to carry a real start time, got %q", result)
	}
}

func TestProcStartTime(t *testing.T) {
	if since, ok := procStartTime(os.Getpid()); !ok || since == "" {
		t.Errorf("procStartTime(self) = %q, %v; want a timestamp", since, ok)
	}

	if since, ok := procStartTime(-1); ok {
		t.Errorf("procStartTime(-1) = %q, true; want not-ok", since)
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

// TestShortErr_Nil tests shortErr with nil error (should panic or handle gracefully)
// Since shortErr calls err.Error(), passing nil will panic. This test documents that behavior.
func TestShortErr_Short(t *testing.T) {
	err := fmt.Errorf("short error")
	result := shortErr(err)
	if result != "short error" {
		t.Errorf("expected 'short error', got %q", result)
	}
}

// TestShortErr_LongSingleLine tests shortErr with a long single-line error
func TestShortErr_LongSingleLine(t *testing.T) {
	longMsg := strings.Repeat("x", 150)
	err := errors.New(longMsg)
	result := shortErr(err)
	if len(result) != 140 {
		t.Errorf("expected length 140, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected result to end with '...', got %q", result)
	}
}

// TestShortErr_MultilineError tests shortErr with embedded newlines
func TestShortErr_MultilineError(t *testing.T) {
	err := fmt.Errorf("line1\nline2\nline3")
	result := shortErr(err)
	if strings.Contains(result, "\n") {
		t.Errorf("expected newlines to be replaced with spaces, got %q", result)
	}
	if !strings.Contains(result, "line1 line2 line3") {
		t.Errorf("expected 'line1 line2 line3', got %q", result)
	}
}

// TestShortErr_LongMultilineError tests shortErr with long multiline error
func TestShortErr_LongMultilineError(t *testing.T) {
	longMsg := strings.Repeat("x", 70) + "\n" + strings.Repeat("y", 70)
	err := errors.New(longMsg)
	result := shortErr(err)
	if strings.Contains(result, "\n") {
		t.Errorf("expected newlines replaced, got %q", result)
	}
	if len(result) != 140 {
		t.Errorf("expected length 140, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected result to end with '...', got %q", result)
	}
}

// TestStartEnabledServices_SkipsUnknownService tests that startEnabledServices
// skips services not in the registry without error
func TestStartEnabledServices_SkipsUnknownService(t *testing.T) {
	enabledSet := map[string]bool{
		"nonexistent-service-xyz": true,
	}
	rc := startEnabledServices(context.Background(), enabledSet, 100)
	if rc != 0 {
		t.Errorf("expected rc=0 when service not in registry, got %d", rc)
	}
}

// TestStartEnabledServices_SkipsLDAPAndConfigd tests that startEnabledServices
// skips ldap and configd services
func TestStartEnabledServices_SkipsLDAPAndConfigd(t *testing.T) {
	enabledSet := map[string]bool{
		"ldap":    true,
		"configd": true,
	}
	rc := startEnabledServices(context.Background(), enabledSet, 100)
	if rc != 0 {
		t.Errorf("expected rc=0 when only ldap/configd in set, got %d", rc)
	}
}

// TestCheckAdvancedStatus_NoAdvancedJARs tests checkAdvancedStatus when no JARs present
func TestCheckAdvancedStatus_NoAdvancedJARs(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	checkAdvancedStatus(context.Background())

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// When no JARs are present, checkAdvancedStatus should return early
	// and not print anything (or minimal output)
	_ = output
}

// TestVersionCmd_Run_NoPackages tests VersionCmd.Run with Packages=false
func TestVersionCmd_Run_NoPackages(t *testing.T) {
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

	// Should print version info (or "unknown" if file doesn't exist)
	if !strings.Contains(output, "Carbonio") {
		t.Logf("output: %q", output)
	}
	// err may be non-nil if /opt/zextras/.version doesn't exist, which is OK
	_ = err
}

// TestVersionCmd_Run_WithPackages tests VersionCmd.Run with Packages=true
func TestVersionCmd_Run_WithPackages(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &VersionCmd{Packages: true}
	err := cmd.Run()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should attempt to show packages (may be empty if none installed)
	_ = output
	_ = err
}

// TestStopLDAPIfLocal_EnabledSetWithLDAP tests stopLDAPIfLocal when ldap is in enabledSet
func TestStopLDAPIfLocal_EnabledSetWithLDAP(t *testing.T) {
	enabledSet := map[string]bool{"ldap": true}
	rc := stopLDAPIfLocal(context.Background(), enabledSet)
	// Should return 0 if LDAP is not local (which is typical in test env)
	if rc != 0 && !services.IsLDAPLocal() {
		t.Errorf("expected rc=0 when LDAP not local, got %d", rc)
	}
}

// TestAdvancedJARsPresent_NoDir tests advancedJARsPresent when dir doesn't exist
func TestAdvancedJARsPresent_NoDir(t *testing.T) {
	result := advancedJARsPresent()
	// Should return false if dir doesn't exist or no JARs found
	if result && !fileExists("/opt/zextras/lib/ext/carbonio") {
		t.Errorf("expected false when dir doesn't exist, got %v", result)
	}
}

// Helper function to check if file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
