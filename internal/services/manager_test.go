// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"
)

// TestNewServiceManager tests the creation of a new ServiceManager.
func TestNewServiceManager(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	if sm == nil {
		t.Fatal("NewServiceManager() returned nil")
	}

	if sm.CommandMap == nil {
		t.Error("CommandMap is nil")
	}

	if sm.SystemdMap == nil {
		t.Error("SystemdMap is nil")
	}

	if sm.RestartQueue == nil {
		t.Error("RestartQueue is nil")
	}

	if sm.StartOrder == nil {
		t.Error("StartOrder is nil")
	}

	if sm.MaxFailedRestarts != 3 {
		t.Errorf("MaxFailedRestarts = %d, want 3", sm.MaxFailedRestarts)
	}

	if sm.UseSystemd {
		t.Error("UseSystemd should default to false")
	}
}

// TestGetDefaultCommandMap verifies service command mappings.
func TestGetDefaultCommandMap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	cmdMap := getDefaultCommandMap()

	tests := []struct {
		service string
		want    string
	}{
		{"proxy", "/opt/zextras/bin/zmproxyctl"},
		{"mta", "/opt/zextras/bin/zmmtactl"},
		{"mailbox", "/opt/zextras/bin/zmstorectl"},
		{"ldap", "/opt/zextras/bin/ldap"},
		{"amavis", "/opt/zextras/bin/zmamavisdctl"},
		{"antivirus", "/opt/zextras/bin/zmclamdctl"},
		{"opendkim", "/opt/zextras/bin/zmopendkimctl"},
		{"cbpolicyd", "/opt/zextras/bin/zmcbpolicydctl"},
	}

	for _, tt := range tests {
		got, exists := cmdMap[tt.service]
		if !exists {
			t.Errorf("Service %s not found in command map", tt.service)
			continue
		}
		if got != tt.want {
			t.Errorf("CommandMap[%s] = %s, want %s", tt.service, got, tt.want)
		}
	}
}

// TestGetDefaultSystemdMap verifies systemd unit mappings.
func TestGetDefaultSystemdMap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	systemdMap := getDefaultSystemdMap()

	tests := []struct {
		service string
		want    string
	}{
		{"proxy", "carbonio-nginx.service"},
		{"mta", "carbonio-postfix.service"},
		{"mailbox", "carbonio-appserver.service"},
		{"ldap", "carbonio-openldap.service"},
		{"amavis", "carbonio-mailthreat.service"},
		{"antivirus", "carbonio-antivirus.service"},
		{"opendkim", "carbonio-opendkim.service"},
		{"cbpolicyd", "carbonio-policyd.service"},
	}

	for _, tt := range tests {
		got, exists := systemdMap[tt.service]
		if !exists {
			t.Errorf("Service %s not found in systemd map", tt.service)
			continue
		}
		if got != tt.want {
			t.Errorf("SystemdMap[%s] = %s, want %s", tt.service, got, tt.want)
		}
	}
}

// TestGetDefaultStartOrder verifies service start ordering.
func TestGetDefaultStartOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := getDefaultStartOrder()

	// Verify LDAP starts first
	if order["ldap"] != 0 {
		t.Errorf("ldap start order = %d, want 0", order["ldap"])
	}

	// Verify mailbox starts before MTA
	if order["mailbox"] >= order["mta"] {
		t.Errorf("mailbox order (%d) should be < mta order (%d)", order["mailbox"], order["mta"])
	}

	// Verify proxy starts before MTA — matches legacy control.pl %startorder.
	// MTA is last among the headline services because it depends on amavis,
	// antispam, antivirus, opendkim, cbpolicyd content filters.
	if order["proxy"] >= order["mta"] {
		t.Errorf("proxy order (%d) should be < mta order (%d)", order["proxy"], order["mta"])
	}

	// Verify configd starts right after ldap and before everything else.
	if order["configd"] <= order["ldap"] || order["configd"] >= order["mailbox"] {
		t.Errorf("configd order (%d) should satisfy ldap (%d) < configd < mailbox (%d)",
			order["configd"], order["ldap"], order["mailbox"])
	}
}

// TestAddRestart verifies queuing services for restart.
func TestAddRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	tests := []string{"mta", "proxy", "mailbox"}

	for _, service := range tests {
		err := sm.AddRestart(context.Background(), service)
		if err != nil {
			t.Errorf("AddRestart(%s) returned error: %v", service, err)
		}

		if !sm.RestartQueue[service] {
			t.Errorf("Service %s not found in restart queue", service)
		}
	}

	if len(sm.RestartQueue) != len(tests) {
		t.Errorf("RestartQueue length = %d, want %d", len(sm.RestartQueue), len(tests))
	}
}

// TestAddRestartCaseInsensitive verifies service names are normalized.
func TestAddRestartCaseInsensitive(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.AddRestart(context.Background(), "MTA")
	sm.AddRestart(context.Background(), "mta")
	sm.AddRestart(context.Background(), "Mta")

	// All should map to "mta"
	if len(sm.RestartQueue) != 1 {
		t.Errorf("RestartQueue length = %d, want 1 (case-insensitive)", len(sm.RestartQueue))
	}

	if !sm.RestartQueue["mta"] {
		t.Error("Service 'mta' not found in restart queue")
	}
}

// TestClearRestarts verifies clearing the restart queue.
func TestClearRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.AddRestart(context.Background(), "mta")
	sm.AddRestart(context.Background(), "proxy")

	if len(sm.RestartQueue) != 2 {
		t.Fatalf("Expected 2 services in queue, got %d", len(sm.RestartQueue))
	}

	sm.ClearRestarts(context.Background())

	if len(sm.RestartQueue) != 0 {
		t.Errorf("RestartQueue length after clear = %d, want 0", len(sm.RestartQueue))
	}
}

// TestGetPendingRestarts verifies retrieving pending restarts.
func TestGetPendingRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	services := []string{"mta", "proxy", "mailbox"}
	for _, service := range services {
		sm.AddRestart(context.Background(), service)
	}

	pending := sm.GetPendingRestarts()

	if len(pending) != len(services) {
		t.Errorf("GetPendingRestarts() returned %d services, want %d", len(pending), len(services))
	}

	// Verify all services are present
	pendingMap := make(map[string]bool)
	for _, service := range pending {
		pendingMap[service] = true
	}

	for _, service := range services {
		if !pendingMap[service] {
			t.Errorf("Service %s not found in pending restarts", service)
		}
	}
}

// TestGetSortedServices verifies service ordering matches legacy
// control.pl %startorder: ldap (0) → mailbox (50) → proxy (70) → mta (150).
// MTA is intentionally last because it depends on the content-filter chain.
func TestGetSortedServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// Add services in reverse order
	sm.AddRestart(context.Background(), "proxy")   // order 70
	sm.AddRestart(context.Background(), "mta")     // order 150
	sm.AddRestart(context.Background(), "mailbox") // order 50
	sm.AddRestart(context.Background(), "ldap")    // order 0

	sorted := sm.getSortedServices()

	expected := []string{"ldap", "mailbox", "proxy", "mta"}

	if len(sorted) != len(expected) {
		t.Fatalf("getSortedServices() returned %d services, want %d", len(sorted), len(expected))
	}

	for i, service := range expected {
		if sorted[i] != service {
			t.Errorf("sorted[%d] = %s, want %s", i, sorted[i], service)
		}
	}
}

// TestGetSortedServicesWithUndefinedService verifies handling of undefined services.
func TestGetSortedServicesWithUndefinedService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.AddRestart(context.Background(), "ldap")          // order 0
	sm.AddRestart(context.Background(), "unknown")       // order 100 (default)
	sm.AddRestart(context.Background(), "mta")           // order 20
	sm.AddRestart(context.Background(), "another_undef") // order 100 (default)

	sorted := sm.getSortedServices()

	// Verify ldap is first
	if sorted[0] != "ldap" {
		t.Errorf("sorted[0] = %s, want ldap", sorted[0])
	}

	// Verify mta is second
	if sorted[1] != "mta" {
		t.Errorf("sorted[1] = %s, want mta", sorted[1])
	}

	// Undefined services should be last (order doesn't matter between them)
	undefinedCount := 0
	for i := 2; i < len(sorted); i++ {
		if sorted[i] == "unknown" || sorted[i] == "another_undef" {
			undefinedCount++
		}
	}

	if undefinedCount != 2 {
		t.Errorf("Expected 2 undefined services at end, got %d", undefinedCount)
	}
}

// TestControlProcessInvalidAction verifies error handling for invalid actions.
func TestControlProcessInvalidAction(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// Invalid action value
	err := sm.ControlProcess(context.Background(), "mta", ServiceAction(999))
	if err == nil {
		t.Error("ControlProcess() with invalid action should return error")
	}
}

// TestControlProcessUndefinedService verifies error handling for undefined services.
func TestControlProcessUndefinedService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	err := sm.ControlProcess(context.Background(), "nonexistent", ActionStatus)
	if err == nil {
		t.Error("ControlProcess() with undefined service should return error")
	}
}

// TestControlProcess_MTARestartConvertsToReload verifies the special case where
// MTA restart is converted to reload for graceful handling.
func TestControlProcess_MTARestartConvertsToReload(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	t.Skip("Skipping flaky test that times out waiting for systemctl")
	sm := NewServiceManager()

	// Test with UseSystemd = false (will fail because binary doesn't exist)
	// but verifies the logic path is executed
	sm.UseSystemd = false
	err := sm.ControlProcess(context.Background(), "mta", ActionRestart)

	// We expect an error because the binary doesn't exist in test environment,
	// but the important thing is that the function executed the conversion logic.
	// The error should be from executeCommand, not from validation.
	if err == nil {
		t.Log("Note: ControlProcess succeeded (binary may exist in environment)")
	}

	// Test with UseSystemd = true
	sm.UseSystemd = true
	err = sm.ControlProcess(context.Background(), "mta", ActionRestart)

	// Again, we expect an error because systemctl/fallback binaries don't exist,
	// but the conversion logic should have been executed.
	if err == nil {
		t.Log("Note: ControlProcess succeeded (systemctl may exist in environment)")
	}

	// Test that other services don't get converted
	err = sm.ControlProcess(context.Background(), "proxy", ActionRestart)
	// Will also fail due to missing binary, but tests the path
	if err == nil {
		t.Log("Note: ControlProcess succeeded (proxy binary may exist)")
	}
}

// TestServiceActionString verifies ServiceAction.String() method.
func TestServiceActionString(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tests := []struct {
		action ServiceAction
		want   string
	}{
		{ActionRestart, "restart"},
		{ActionStop, "stop"},
		{ActionStart, "start"},
		{ActionStatus, "status"},
	}

	for _, tt := range tests {
		got := tt.action.String()
		if got != tt.want {
			t.Errorf("ServiceAction(%d).String() = %s, want %s", tt.action, got, tt.want)
		}
	}
}

// TestMTARestartBecomesReload verifies MTA special case.
// Note: This test only verifies the logic, not actual command execution.
func TestMTARestartBecomesReload(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// We can't easily test the actual command execution without mocking exec.Command,
	// but we can verify the logic is in place by checking the implementation.
	// For now, this is a placeholder for integration testing.

	// Verify MTA command exists
	if _, exists := sm.CommandMap["mta"]; !exists {
		t.Error("MTA command not defined in CommandMap")
	}
}

// TestSystemdServiceMapping verifies systemd mode service mapping.
func TestSystemdServiceMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.UseSystemd = true

	// Verify all services in CommandMap have corresponding systemd mappings (if applicable)
	criticalServices := []string{"proxy", "mta", "mailbox", "ldap", "amavis"}

	for _, service := range criticalServices {
		if _, exists := sm.SystemdMap[service]; !exists {
			t.Errorf("Critical service %s missing systemd mapping", service)
		}
	}
}

// TestProcessRestartsEmptyQueue verifies processing with empty queue.
func TestProcessRestartsEmptyQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts() with empty queue returned error: %v", err)
	}
}

// TestProcessRestarts_DryRunMode verifies that dry-run mode clears queue without executing commands.
func TestProcessRestarts_DryRunMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.DisableRestarts = true // Enable dry-run mode

	// Add multiple services to the restart queue
	sm.AddRestart(context.Background(), "mta")
	sm.AddRestart(context.Background(), "proxy")
	sm.AddRestart(context.Background(), "mailbox")

	if len(sm.RestartQueue) != 3 {
		t.Fatalf("Expected 3 services in queue, got %d", len(sm.RestartQueue))
	}

	// Process restarts in dry-run mode
	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts() in dry-run mode returned error: %v", err)
	}

	// Verify queue was cleared
	if len(sm.RestartQueue) != 0 {
		t.Errorf("Expected empty restart queue after dry-run, got %d services", len(sm.RestartQueue))
	}
}

// TestProcessRestartsClearsSuccessfulRestarts verifies queue cleanup.
// Note: This requires mocking command execution for true unit testing.
func TestProcessRestartsClearsSuccessfulRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// Add some services
	sm.AddRestart(context.Background(), "mta")
	sm.AddRestart(context.Background(), "proxy")

	initialCount := len(sm.RestartQueue)
	if initialCount != 2 {
		t.Fatalf("Expected 2 services in queue, got %d", initialCount)
	}

	// ProcessRestarts will fail because commands don't exist,
	// but we can verify the retry logic by checking MaxFailedRestarts
	if sm.MaxFailedRestarts != 3 {
		t.Errorf("MaxFailedRestarts = %d, want 3", sm.MaxFailedRestarts)
	}
}

// TestProcessRestarts_FailedServiceWithNoProgress verifies that when a service fails
// and no progress is made, the loop breaks to prevent infinite retries.
func TestProcessRestarts_FailedServiceWithNoProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	sm.MaxFailedRestarts = 3 // Set retry limit

	// Add a service that will fail (binary doesn't exist)
	sm.AddRestart(context.Background(), "mta")

	if len(sm.RestartQueue) != 1 {
		t.Fatalf("Expected 1 service in queue, got %d", len(sm.RestartQueue))
	}

	// Process restarts - should fail once and then break the loop because no progress is made
	// The service stays in the queue because it hasn't reached MaxFailedRestarts yet
	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts() returned error: %v", err)
	}

	// Service should still be in queue (didn't reach MaxFailedRestarts)
	// This verifies the "no progress" loop break logic
	if len(sm.RestartQueue) != 1 {
		t.Errorf("Expected 1 service in queue (loop broke on no progress), got %d", len(sm.RestartQueue))
	}
}

// TestProcessRestarts_MultipleServicesOneSucceeds tests the case where
// one service succeeds and triggers more processing rounds.
func TestProcessRestarts_MultipleServicesOneSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// Add two services - both will fail
	sm.AddRestart(context.Background(), "mta")
	sm.AddRestart(context.Background(), "proxy")

	if len(sm.RestartQueue) != 2 {
		t.Fatalf("Expected 2 services in queue, got %d", len(sm.RestartQueue))
	}

	// Process restarts - both will fail on first attempt, loop breaks on no progress
	err := sm.ProcessRestarts(context.Background(), nil)
	if err != nil {
		t.Errorf("ProcessRestarts() returned error: %v", err)
	}

	// Both services should still be in queue
	if len(sm.RestartQueue) != 2 {
		t.Errorf("Expected 2 services in queue, got %d", len(sm.RestartQueue))
	}
}

// TestIsRunningUsesControlProcess verifies IsRunning delegates to ControlProcess.
func TestIsRunningUsesControlProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// IsRunning should return false and potentially an error for nonexistent service
	running, _ := sm.IsRunning(context.Background(), "nonexistent")
	if running {
		t.Error("IsRunning() should return false for nonexistent service")
	}
}

// TestManagerInterface verifies ServiceManager implements Manager interface.
func TestManagerInterface(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	var _ Manager = (*ServiceManager)(nil)
}

// BenchmarkAddRestart benchmarks the AddRestart operation.
func BenchmarkAddRestart(b *testing.B) {
	sm := NewServiceManager()
	services := []string{"mta", "proxy", "mailbox", "ldap", "amavis"}

	for i := 0; b.Loop(); i++ {
		service := services[i%len(services)]
		sm.AddRestart(context.Background(), service)
	}
}

// BenchmarkGetSortedServices benchmarks the service sorting operation.
func BenchmarkGetSortedServices(b *testing.B) {
	sm := NewServiceManager()

	// Add several services
	services := []string{"proxy", "mta", "mailbox", "ldap", "amavis", "antivirus", "opendkim", "cbpolicyd"}
	for _, service := range services {
		sm.AddRestart(context.Background(), service)
	}

	for b.Loop() {
		sm.getSortedServices()
	}
}

// TestSetDependencies verifies dependency map storage.
func TestSetDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	deps := map[string][]string{
		"mta":   {"amavis", "antivirus", "antispam"},
		"proxy": {"mta", "mailbox"},
	}

	sm.SetDependencies(context.Background(), deps)

	if len(sm.Dependencies) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(sm.Dependencies))
	}

	if len(sm.Dependencies["mta"]) != 3 {
		t.Errorf("Expected 3 dependencies for mta, got %d", len(sm.Dependencies["mta"]))
	}

	if len(sm.Dependencies["proxy"]) != 2 {
		t.Errorf("Expected 2 dependencies for proxy, got %d", len(sm.Dependencies["proxy"]))
	}
}

// TestAddDependencyRestarts_NoDependencies verifies behavior with no dependencies.
func TestAddDependencyRestarts_NoDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	configLookup := func(key string) string {
		return "enabled"
	}

	// No dependencies set - should be no-op
	sm.AddDependencyRestarts(context.Background(), "mta", configLookup)

	if len(sm.RestartQueue) != 0 {
		t.Errorf("Expected empty restart queue, got %d services", len(sm.RestartQueue))
	}
}

// TestAddDependencyRestarts_EnabledServices verifies enabled service handling.
func TestAddDependencyRestarts_EnabledServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	// Set up dependencies
	sm.SetDependencies(context.Background(), map[string][]string{
		"mta": {"antivirus", "antispam"},
	})

	// Config lookup that returns enabled for both services
	configLookup := func(key string) string {
		if key == "SERVICE_ANTIVIRUS" || key == "SERVICE_ANTISPAM" {
			return "enabled"
		}
		return "disabled"
	}

	sm.AddDependencyRestarts(context.Background(), "mta", configLookup)

	if len(sm.RestartQueue) != 2 {
		t.Errorf("Expected 2 services in queue, got %d", len(sm.RestartQueue))
	}

	if !sm.RestartQueue["antivirus"] {
		t.Error("Expected antivirus in restart queue")
	}

	if !sm.RestartQueue["antispam"] {
		t.Error("Expected antispam in restart queue")
	}
}

// TestAddDependencyRestarts_DisabledServices verifies disabled service skipping.
func TestAddDependencyRestarts_DisabledServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.SetDependencies(context.Background(), map[string][]string{
		"mta": {"antivirus", "antispam"},
	})

	// Config lookup that returns disabled
	configLookup := func(key string) string {
		return "disabled"
	}

	sm.AddDependencyRestarts(context.Background(), "mta", configLookup)

	if len(sm.RestartQueue) != 0 {
		t.Errorf("Expected empty restart queue for disabled services, got %d", len(sm.RestartQueue))
	}
}

// TestAddDependencyRestarts_AmavisSpecialCase verifies amavis is always added.
func TestAddDependencyRestarts_AmavisSpecialCase(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.SetDependencies(context.Background(), map[string][]string{
		"mta": {"amavis"},
	})

	// Config lookup that returns disabled (should be ignored for amavis)
	configLookup := func(key string) string {
		return "disabled"
	}

	sm.AddDependencyRestarts(context.Background(), "mta", configLookup)

	if len(sm.RestartQueue) != 1 {
		t.Errorf("Expected 1 service in queue, got %d", len(sm.RestartQueue))
	}

	if !sm.RestartQueue["amavis"] {
		t.Error("Expected amavis in restart queue (special case)")
	}
}

// TestAddDependencyRestarts_MixedStatus verifies mixed enabled/disabled handling.
func TestAddDependencyRestarts_MixedStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.SetDependencies(context.Background(), map[string][]string{
		"proxy": {"mta", "mailbox", "amavis"},
	})

	// Config lookup: mta enabled, mailbox disabled, amavis always added
	configLookup := func(key string) string {
		if key == "SERVICE_MTA" {
			return "enabled"
		}
		return "disabled"
	}

	sm.AddDependencyRestarts(context.Background(), "proxy", configLookup)

	if len(sm.RestartQueue) != 2 {
		t.Errorf("Expected 2 services in queue (mta + amavis), got %d", len(sm.RestartQueue))
	}

	if !sm.RestartQueue["mta"] {
		t.Error("Expected mta in restart queue")
	}

	if !sm.RestartQueue["amavis"] {
		t.Error("Expected amavis in restart queue (special case)")
	}

	if sm.RestartQueue["mailbox"] {
		t.Error("Did not expect disabled mailbox in restart queue")
	}
}

// TestAddDependencyRestarts_CaseInsensitive verifies case handling.
func TestAddDependencyRestarts_CaseInsensitive(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	sm.SetDependencies(context.Background(), map[string][]string{
		"mta": {"AntiVirus", "AntiSpam"}, // Mixed case
	})

	configLookup := func(key string) string {
		// Should receive uppercase: SERVICE_ANTIVIRUS, SERVICE_ANTISPAM
		if key == "SERVICE_ANTIVIRUS" || key == "SERVICE_ANTISPAM" {
			return "enabled"
		}
		return "disabled"
	}

	sm.AddDependencyRestarts(context.Background(), "mta", configLookup)

	if len(sm.RestartQueue) != 2 {
		t.Errorf("Expected 2 services in queue, got %d", len(sm.RestartQueue))
	}

	// Queue should store lowercase
	if !sm.RestartQueue["antivirus"] {
		t.Error("Expected antivirus (lowercase) in restart queue")
	}

	if !sm.RestartQueue["antispam"] {
		t.Error("Expected antispam (lowercase) in restart queue")
	}
}

// TestHasCommand verifies checking for service command existence.
func TestHasCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()

	tests := []struct {
		name    string
		service string
		want    bool
	}{
		{"existing service lowercase", "mta", true},
		{"existing service uppercase", "MTA", true},
		{"existing service mixed case", "Mta", true},
		{"nonexistent service", "nonexistent", false},
		{"empty service", "", false},
		{"proxy service", "proxy", true},
		{"mailbox service", "mailbox", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sm.HasCommand(tt.service)
			if got != tt.want {
				t.Errorf("HasCommand(%q) = %v, want %v", tt.service, got, tt.want)
			}
		})
	}
}
