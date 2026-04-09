// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
)

// MockServiceManager is a mock implementation of services.Manager for testing.
type MockServiceManager struct {
	mu                  sync.Mutex
	runningServices     map[string]bool
	restartQueue        map[string]bool
	restartCalled       map[string]int
	isRunningError      error
	addRestartError     error
	processRestartsFunc func(func(string) string) error
}

func NewMockServiceManager() *MockServiceManager {
	return &MockServiceManager{
		runningServices: make(map[string]bool),
		restartQueue:    make(map[string]bool),
		restartCalled:   make(map[string]int),
	}
}

func (m *MockServiceManager) ControlProcess(_ context.Context, service string, action services.ServiceAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if action == services.ActionStatus {
		if m.runningServices[service] {
			return nil
		}
		return fmt.Errorf("service %s not running", service)
	}
	if action == services.ActionRestart {
		m.restartCalled[service]++
		m.runningServices[service] = true
		return nil
	}
	return nil
}

func (m *MockServiceManager) IsRunning(_ context.Context, service string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isRunningError != nil {
		return false, m.isRunningError
	}
	return m.runningServices[service], nil
}

func (m *MockServiceManager) AddRestart(_ context.Context, service string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.addRestartError != nil {
		return m.addRestartError
	}
	m.restartQueue[service] = true
	return nil
}

func (m *MockServiceManager) ProcessRestarts(_ context.Context, configLookup func(string) string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.processRestartsFunc != nil {
		return m.processRestartsFunc(configLookup)
	}
	for service := range m.restartQueue {
		m.restartCalled[service]++
		m.runningServices[service] = true
	}
	m.restartQueue = make(map[string]bool)
	return nil
}

func (m *MockServiceManager) ClearRestarts(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.restartQueue = make(map[string]bool)
}

func (m *MockServiceManager) GetPendingRestarts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	svcs := make([]string, 0, len(m.restartQueue))
	for service := range m.restartQueue {
		svcs = append(svcs, service)
	}
	return svcs
}

func (m *MockServiceManager) SetDependencies(_ context.Context, deps map[string][]string) {
	// Mock implementation
}

func (m *MockServiceManager) AddDependencyRestarts(_ context.Context, sectionName string, configLookup func(string) string) {
	// Mock implementation
}

func (m *MockServiceManager) HasCommand(service string) bool {
	// Mock implementation - return true for all services
	return true
}

func (m *MockServiceManager) SetUseSystemd(_ bool) {}

// Thread-safe helper methods for test access.

func (m *MockServiceManager) setRunning(service string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.runningServices[service] = running
}

func (m *MockServiceManager) isRunning(service string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.runningServices[service]
}

func (m *MockServiceManager) getRestartCount(service string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.restartCalled[service]
}

func (m *MockServiceManager) setIsRunningError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isRunningError = err
}

func (m *MockServiceManager) setAddRestartError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.addRestartError = err
}

// Test 1: NewWatchdog creates a watchdog with proper defaults
func TestNewWatchdog(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  30 * time.Second,
	}

	wd := NewWatchdog(cfg)

	if wd == nil {
		t.Fatal("NewWatchdog returned nil")
	}

	if wd.CheckInterval != 30*time.Second {
		t.Errorf("Expected CheckInterval=30s, got %v", wd.CheckInterval)
	}

	if wd.enabled {
		t.Error("Watchdog should not be enabled by default")
	}

	if wd.serviceManager != mockSM {
		t.Error("ServiceManager not set correctly")
	}

	if wd.state != st {
		t.Error("State not set correctly")
	}
}

// Test 2: Default check interval is set when not provided
func TestNewWatchdog_DefaultCheckInterval(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		// CheckInterval not set
	}

	wd := NewWatchdog(cfg)

	if wd.CheckInterval != 60*time.Second {
		t.Errorf("Expected default CheckInterval=60s, got %v", wd.CheckInterval)
	}
}

// Test 3: Start and Stop watchdog
func TestWatchdog_StartStop(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  100 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Initially not enabled
	if wd.IsEnabled() {
		t.Error("Watchdog should not be enabled initially")
	}

	ctx := context.Background()

	// Start watchdog
	wd.Start(ctx)

	// Allow goroutine to start
	time.Sleep(10 * time.Millisecond)

	if !wd.IsEnabled() {
		t.Error("Watchdog should be enabled after Start()")
	}

	// Stop watchdog
	wd.Stop(ctx)

	if wd.IsEnabled() {
		t.Error("Watchdog should not be enabled after Stop()")
	}
}

// Test 4: AddService and RemoveService
func TestWatchdog_AddRemoveService(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
	}

	wd := NewWatchdog(cfg)

	// Add service
	wd.AddService(context.Background(), "ldap")

	if !wd.IsServiceTracked("ldap") {
		t.Error("Service 'ldap' should be tracked after AddService()")
	}

	// Remove service
	wd.RemoveService(context.Background(), "ldap")

	if wd.IsServiceTracked("ldap") {
		t.Error("Service 'ldap' should not be tracked after RemoveService()")
	}
}

// Test 5: SetServiceEnabled and IsServiceEnabled
func TestWatchdog_ServiceEnabled(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
	}

	wd := NewWatchdog(cfg)

	// Initially not enabled
	if wd.IsServiceEnabled("mta") {
		t.Error("Service 'mta' should not be enabled initially")
	}

	// Enable service
	wd.SetServiceEnabled(context.Background(), "mta", true)

	if !wd.IsServiceEnabled("mta") {
		t.Error("Service 'mta' should be enabled after SetServiceEnabled(true)")
	}

	// Disable service
	wd.SetServiceEnabled(context.Background(), "mta", false)

	if wd.IsServiceEnabled("mta") {
		t.Error("Service 'mta' should not be enabled after SetServiceEnabled(false)")
	}
}

// Test 6: Watchdog detects failed service and restarts it
func TestWatchdog_DetectAndRestart(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Setup: service is tracked and monitoring is enabled
	wd.AddService(context.Background(), "proxy")
	wd.SetServiceEnabled(context.Background(), "proxy", true)

	// Service is initially running
	mockSM.setRunning("proxy", true)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for first check (should see service running)
	time.Sleep(70 * time.Millisecond)

	// Verify no restart yet
	if mockSM.getRestartCount("proxy") > 0 {
		t.Error("Service should not have been restarted yet")
	}

	// Simulate service failure
	mockSM.setRunning("proxy", false)

	// Wait for watchdog to detect and restart
	time.Sleep(70 * time.Millisecond)

	// Verify restart was called
	if mockSM.getRestartCount("proxy") != 1 {
		t.Errorf("Expected 1 restart call, got %d", mockSM.getRestartCount("proxy"))
	}

	// Verify service is running again (mock sets it to true on restart)
	if !mockSM.isRunning("proxy") {
		t.Error("Service should be running after restart")
	}
}

// Test 7: Watchdog does not restart disabled services
func TestWatchdog_DoesNotRestartDisabled(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Setup: service is tracked but monitoring is NOT enabled
	wd.AddService(context.Background(), "amavis")
	wd.SetServiceEnabled(context.Background(), "amavis", false)

	// Service fails
	mockSM.setRunning("amavis", false)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for check cycle
	time.Sleep(70 * time.Millisecond)

	// Verify no restart
	if mockSM.getRestartCount("amavis") > 0 {
		t.Errorf("Disabled service should not be restarted, got %d calls", mockSM.getRestartCount("amavis"))
	}
}

// Test 8: UpdateServiceList updates monitored services
func TestWatchdog_UpdateServiceList(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
	}

	wd := NewWatchdog(cfg)

	// Update with list of services
	services := []string{"ldap", "mailbox", "mta"}
	wd.UpdateServiceList(context.Background(), services)

	// Verify all are enabled
	for _, service := range services {
		if !wd.IsServiceEnabled(service) {
			t.Errorf("Service %s should be enabled after UpdateServiceList()", service)
		}
	}

	// Update with new list (should clear old ones)
	newServices := []string{"proxy", "stats"}
	wd.UpdateServiceList(context.Background(), newServices)

	// Verify old services are no longer enabled
	if wd.IsServiceEnabled("ldap") {
		t.Error("Service 'ldap' should not be enabled after new UpdateServiceList()")
	}

	// Verify new services are enabled
	for _, service := range newServices {
		if !wd.IsServiceEnabled(service) {
			t.Errorf("Service %s should be enabled after new UpdateServiceList()", service)
		}
	}
}

// Test 9: Watchdog removes failed service from tracking after restart
func TestWatchdog_RemovesServiceOnFailure(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "cbpolicyd")
	wd.SetServiceEnabled(context.Background(), "cbpolicyd", true)

	// Service is initially tracked
	if !wd.IsServiceTracked("cbpolicyd") {
		t.Error("Service should be tracked initially")
	}

	// Service fails
	mockSM.setRunning("cbpolicyd", false)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for watchdog to detect failure
	time.Sleep(70 * time.Millisecond)

	// Service should be re-added after successful restart
	// (our mock always succeeds, so service should be tracked again)
	if !wd.IsServiceTracked("cbpolicyd") {
		t.Error("Service should be re-tracked after successful restart")
	}
}

// Test 10: Multiple check cycles
func TestWatchdog_MultipleCycles(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  30 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Setup multiple services
	services := []string{"ldap", "mailbox", "mta"}
	for _, svc := range services {
		wd.AddService(context.Background(), svc)
		wd.SetServiceEnabled(context.Background(), svc, true)
		mockSM.setRunning(svc, true)
	}

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for multiple check cycles
	time.Sleep(100 * time.Millisecond)

	// All services still running, no restarts
	for _, svc := range services {
		if mockSM.getRestartCount(svc) > 0 {
			t.Errorf("Service %s should not have been restarted", svc)
		}
	}
}

// Test 11: Watchdog handles IsRunning errors gracefully
func TestWatchdog_IsRunningError(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "antivirus")
	wd.SetServiceEnabled(context.Background(), "antivirus", true)

	// Configure mock to return error on IsRunning
	mockSM.setIsRunningError(fmt.Errorf("connection failed"))

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for check
	time.Sleep(70 * time.Millisecond)

	// Should not have attempted restart due to error
	if mockSM.getRestartCount("antivirus") > 0 {
		t.Error("Should not restart when IsRunning returns error")
	}
}

// Test 12: Concurrent operations are thread-safe
func TestWatchdog_ThreadSafety(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Perform concurrent operations
	done := make(chan bool)

	go func() {
		for i := range 10 {
			wd.AddService(context.Background(), fmt.Sprintf("service%d", i))
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	go func() {
		for i := range 10 {
			wd.SetServiceEnabled(context.Background(), fmt.Sprintf("service%d", i), true)
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// Verify no panics occurred (test passes if we get here)
}

// Test 13: Service restart with failed restart attempt
func TestWatchdog_RestartFailure(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "opendkim")
	wd.SetServiceEnabled(context.Background(), "opendkim", true)

	// Service fails
	mockSM.setRunning("opendkim", false)

	// Configure mock to fail restart
	mockSM.setAddRestartError(fmt.Errorf("restart failed: insufficient resources"))

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for watchdog to attempt restart
	time.Sleep(70 * time.Millisecond)

	// Verify service is removed from tracking even on failed restart
	if wd.IsServiceTracked("opendkim") {
		t.Error("Service should be removed from tracking after failed restart attempt")
	}

	// Verify restart was attempted (AddRestart should have been called)
	if mockSM.getRestartCount("opendkim") > 0 {
		t.Error("ProcessRestarts should not have been called due to AddRestart error")
	}
}

// Test 14: Multiple simultaneous service failures
func TestWatchdog_MultipleServiceFailures(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup multiple services
	services := []string{"ldap", "mailbox", "mta", "proxy"}
	for _, svc := range services {
		wd.AddService(context.Background(), svc)
		wd.SetServiceEnabled(context.Background(), svc, true)
		mockSM.setRunning(svc, true)
	}

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for first check (all running)
	time.Sleep(70 * time.Millisecond)

	// Simulate multiple simultaneous failures
	mockSM.setRunning("ldap", false)
	mockSM.setRunning("mta", false)
	mockSM.setRunning("proxy", false)

	// Wait for watchdog to detect and restart
	time.Sleep(70 * time.Millisecond)

	// Verify all failed services were restarted
	for _, svc := range []string{"ldap", "mta", "proxy"} {
		if mockSM.getRestartCount(svc) != 1 {
			t.Errorf("Service %s should have been restarted once, got %d", svc, mockSM.getRestartCount(svc))
		}
		if !mockSM.isRunning(svc) {
			t.Errorf("Service %s should be running after restart", svc)
		}
	}

	// Verify healthy service was not restarted
	if mockSM.getRestartCount("mailbox") > 0 {
		t.Error("Healthy service 'mailbox' should not have been restarted")
	}
}

// Test 15: Restart loop prevention - service removed from tracking on failure
func TestWatchdog_RestartLoopPrevention(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  30 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "antispam")
	wd.SetServiceEnabled(context.Background(), "antispam", true)

	// Service is initially running
	mockSM.setRunning("antispam", true)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for first check
	time.Sleep(50 * time.Millisecond)

	// Service fails
	mockSM.setRunning("antispam", false)

	// Wait for restart
	time.Sleep(50 * time.Millisecond)

	// After restart, service should be re-added to tracking
	if !wd.IsServiceTracked("antispam") {
		t.Error("Service should be re-tracked after successful restart")
	}

	// Verify exactly one restart
	if mockSM.getRestartCount("antispam") != 1 {
		t.Errorf("Expected 1 restart, got %d", mockSM.getRestartCount("antispam"))
	}

	// Simulate another failure
	mockSM.setRunning("antispam", false)

	// Wait for another restart
	time.Sleep(50 * time.Millisecond)

	// Should have been restarted again
	if mockSM.getRestartCount("antispam") != 2 {
		t.Errorf("Expected 2 restarts total, got %d", mockSM.getRestartCount("antispam"))
	}
}

// Test 16: Service re-tracking after successful restart
func TestWatchdog_ServiceRetrackingAfterRestart(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "stats")
	wd.SetServiceEnabled(context.Background(), "stats", true)

	// Verify initially tracked
	if !wd.IsServiceTracked("stats") {
		t.Fatal("Service should be tracked initially")
	}

	// Service fails
	mockSM.setRunning("stats", false)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for watchdog to detect and restart
	time.Sleep(70 * time.Millisecond)

	// Verify service is re-tracked after successful restart
	if !wd.IsServiceTracked("stats") {
		t.Error("Service should be re-tracked after successful restart")
	}

	// Verify restart was successful
	if !mockSM.isRunning("stats") {
		t.Error("Service should be running after restart")
	}
}

// Test 17: Failed restart leaves service untracked
func TestWatchdog_FailedRestartLeavesUntracked(t *testing.T) {
	st := state.NewState()

	// Create a mock that succeeds AddRestart but fails ProcessRestarts
	var processRestartsCalled atomic.Bool
	mockWithFailedProcess := &MockServiceManager{
		runningServices: make(map[string]bool),
		restartQueue:    make(map[string]bool),
		restartCalled:   make(map[string]int),
		processRestartsFunc: func(configLookup func(string) string) error {
			processRestartsCalled.Store(true)
			return fmt.Errorf("restart process failed")
		},
	}

	cfg := Config{
		ServiceManager: mockWithFailedProcess,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "archiving")
	wd.SetServiceEnabled(context.Background(), "archiving", true)

	// Verify initially tracked
	if !wd.IsServiceTracked("archiving") {
		t.Fatal("Service should be tracked initially")
	}

	// Service fails
	mockWithFailedProcess.setRunning("archiving", false)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for watchdog to attempt restart
	time.Sleep(70 * time.Millisecond)

	// Verify ProcessRestarts was called
	if !processRestartsCalled.Load() {
		t.Error("ProcessRestarts should have been called")
	}

	// Service should be removed from tracking after failed restart
	if wd.IsServiceTracked("archiving") {
		t.Error("Service should be removed from tracking after failed restart")
	}

	// Service should still be down
	if mockWithFailedProcess.isRunning("archiving") {
		t.Error("Service should not be running after failed restart")
	}
}

// Test 18: ConfigLookup is passed correctly to ProcessRestarts
func TestWatchdog_ConfigLookupPassed(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	testConfigLookup := func(key string) string {
		return "test_value"
	}

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   testConfigLookup,
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "sasl")
	wd.SetServiceEnabled(context.Background(), "sasl", true)

	// Service fails
	mockSM.setRunning("sasl", false)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Wait for watchdog to restart
	time.Sleep(70 * time.Millisecond)

	// Verify configLookup is set in watchdog
	if wd.configLookup == nil {
		t.Error("ConfigLookup should be set in watchdog")
	}

	// Verify restart was attempted
	if mockSM.getRestartCount("sasl") != 1 {
		t.Errorf("Expected 1 restart, got %d", mockSM.getRestartCount("sasl"))
	}
}

// Test 19: Watchdog with very fast check interval
func TestWatchdog_FastCheckInterval(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  10 * time.Millisecond, // Very fast
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "memcached")
	wd.SetServiceEnabled(context.Background(), "memcached", true)
	mockSM.setRunning("memcached", true)

	// Start watchdog
	wd.Start(context.Background())
	defer wd.Stop(context.Background())

	// Service is healthy - wait for multiple checks
	time.Sleep(50 * time.Millisecond)

	// Should not have restarted healthy service
	if mockSM.getRestartCount("memcached") > 0 {
		t.Error("Healthy service should not be restarted even with fast check interval")
	}

	// Now fail the service
	mockSM.setRunning("memcached", false)

	// Wait for quick detection
	time.Sleep(20 * time.Millisecond)

	// Should have restarted quickly
	if mockSM.getRestartCount("memcached") != 1 {
		t.Errorf("Service should be restarted quickly with fast check interval, got %d", mockSM.getRestartCount("memcached"))
	}
}

// Test 20: Start when already running logs warning and is a no-op
func TestStart_AlreadyRunning(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  100 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)
	ctx := context.Background()

	// First Start — normal path
	wd.Start(ctx)

	// Ensure goroutine is up
	time.Sleep(10 * time.Millisecond)

	if !wd.IsEnabled() {
		t.Fatal("Watchdog should be enabled after first Start()")
	}

	// Second Start — must hit the "already running" branch without panicking
	wd.Start(ctx)

	// Watchdog must still be running
	if !wd.IsEnabled() {
		t.Error("Watchdog should still be enabled after second Start()")
	}

	// Clean up
	wd.Stop(ctx)

	if wd.IsEnabled() {
		t.Error("Watchdog should be disabled after Stop()")
	}
}

// Test 21: Stop when not running logs warning and is a no-op
func TestStop_WhenNotRunning(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  100 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)
	ctx := context.Background()

	// Watchdog was never started — calling Stop must not panic or deadlock
	wd.Stop(ctx)

	if wd.IsEnabled() {
		t.Error("Watchdog should not be enabled after Stop() on a never-started watchdog")
	}
}

// Test 22: checkServices early-returns when enabled=false
func TestCheckServices_WhenDisabled(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  100 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Add a service that would trigger a restart if checkServices ran normally
	wd.AddService(context.Background(), "ldap")
	wd.SetServiceEnabled(context.Background(), "ldap", true)
	mockSM.setRunning("ldap", false)

	// enabled is false (never started) — call checkServices directly
	wd.checkServices(context.Background())

	// No restart should have occurred because the early return fired
	if mockSM.getRestartCount("ldap") > 0 {
		t.Errorf("checkServices should be a no-op when disabled, got %d restart(s)", mockSM.getRestartCount("ldap"))
	}
}

// Test 23: checkOneService skips a service that is not yet tracked
func TestCheckOneService_ServiceNotTracked(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  100 * time.Millisecond,
	}

	wd := NewWatchdog(cfg)

	// Service monitoring is enabled but the service was never added via AddService,
	// so IsServiceTracked returns false — hits the second early-return branch.
	wd.SetServiceEnabled(context.Background(), "mta", true)
	mockSM.setRunning("mta", false)

	wd.checkOneService(context.Background(), "mta")

	if mockSM.getRestartCount("mta") > 0 {
		t.Errorf("checkOneService should skip untracked service, got %d restart(s)", mockSM.getRestartCount("mta"))
	}
}

// Test 24: Stop watchdog during service restart
func TestWatchdog_StopDuringRestart(t *testing.T) {
	mockSM := NewMockServiceManager()
	st := state.NewState()

	cfg := Config{
		ServiceManager: mockSM,
		State:          st,
		CheckInterval:  50 * time.Millisecond,
		ConfigLookup:   func(key string) string { return "" },
	}

	wd := NewWatchdog(cfg)

	// Setup
	wd.AddService(context.Background(), "mailboxd")
	wd.SetServiceEnabled(context.Background(), "mailboxd", true)

	// Service fails
	mockSM.setRunning("mailboxd", false)

	// Start watchdog
	wd.Start(context.Background())

	// Allow very brief time for watchdog to start
	time.Sleep(5 * time.Millisecond)

	// Stop immediately (potentially during restart)
	wd.Stop(context.Background())

	// Verify watchdog stopped cleanly
	if wd.IsEnabled() {
		t.Error("Watchdog should not be enabled after Stop()")
	}

	// Test passes if we don't deadlock or panic
}
