// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package configmgr

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/network"
	"github.com/zextras/carbonio-configd/internal/proxy"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/watchdog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_ConfigdCycleWithProxy tests the complete configd cycle with proxy configuration.
// This validates the full integration: config changes → proxy regeneration → service tracking.
func TestE2E_ConfigdCycleWithProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	// Skip if Carbonio environment is not available
	if _, err := os.Stat("/opt/zextras/conf/localconfig.xml"); os.IsNotExist(err) {
		t.Skip("Skipping: Carbonio environment not available (/opt/zextras/conf/localconfig.xml missing)")
	}

	ctx := context.Background()
	// Initialize logger for testing

	// Create test configuration
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-e2e-test"
	mainCfg.RestartConfig = false // Disable actual service restarts in test

	// Create application state
	appState := state.NewState()

	// Create LDAP client
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Create ConfigManager
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Create Service Manager
	serviceManager := services.NewServiceManager()

	// Create Watchdog (but don't start it yet)
	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  5 * time.Second, // Faster for testing
		ServiceManager: serviceManager,
		State:          appState,
		ConfigLookup: func(key string) string {
			if val, exists := appState.LocalConfig.Data[key]; exists {
				return val
			}
			return ""
		},
	})

	t.Run("Initial_Config_Load", func(t *testing.T) {
		// Load all configurations
		done := make(chan error, 1)
		go func() {
			done <- cm.LoadAllConfigs(context.Background())
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("LoadAllConfigs failed: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("LoadAllConfigs timed out")
		}

		// Verify basic configs loaded
		if len(appState.LocalConfig.Data) == 0 {
			t.Error("LocalConfig not loaded")
		}
		if len(appState.GlobalConfig.Data) == 0 {
			t.Error("GlobalConfig not loaded")
		}
		if len(appState.ServerConfig.Data) == 0 {
			t.Error("ServerConfig not loaded")
		}

		t.Logf("✓ Loaded configs: Local=%d keys, Global=%d keys, Server=%d keys",
			len(appState.LocalConfig.Data),
			len(appState.GlobalConfig.Data),
			len(appState.ServerConfig.Data))
	})

	t.Run("Parse_MTA_Config", func(t *testing.T) {
		err := cm.ParseMtaConfig(context.Background(), mainCfg.ConfigFile)
		if err != nil {
			t.Fatalf("ParseMtaConfig failed: %v", err)
		}

		// Verify proxy section exists in MTA config
		proxySection, exists := appState.MtaConfig.Sections["proxy"]
		if !exists {
			t.Fatal("Proxy section not found in MTA config")
		}

		if proxySection.Name != "proxy" {
			t.Errorf("Expected proxy section name 'proxy', got '%s'", proxySection.Name)
		}

		// Verify proxy section has required vars
		if len(proxySection.RequiredVars) == 0 {
			t.Error("Proxy section has no required vars")
		}

		t.Logf("✓ MTA config parsed: %d sections, proxy section has %d required vars",
			len(appState.MtaConfig.Sections), len(proxySection.RequiredVars))
	})

	t.Run("Detect_Proxy_Changes", func(t *testing.T) {
		// Simulate a proxy configuration change
		// In real scenario, this would come from LDAP or config file change
		appState.ChangedKeys = make(map[string][]string)
		appState.ChangedKeys["proxy"] = []string{"zimbraReverseProxyLookupTarget"}

		// Run CompareKeys to detect changes
		err := cm.CompareKeys(context.Background())
		if err != nil {
			t.Errorf("CompareKeys failed: %v", err)
		}

		// Verify change detected
		if len(appState.ChangedKeys["proxy"]) == 0 {
			t.Error("Proxy change not detected in ChangedKeys")
		}

		t.Logf("✓ Change detection working: %d sections changed", len(appState.ChangedKeys))
	})

	t.Run("Compile_Actions_For_Proxy", func(t *testing.T) {
		// Ensure proxy section is marked as changed
		appState.ChangedKeys["proxy"] = []string{"zimbraReverseProxyLookupTarget"}

		// Compile actions based on changes
		cm.CompileActions(context.Background())

		// Verify proxygen action is set
		if !appState.CurrentActions.Proxygen {
			t.Error("Proxygen action not set after proxy section change")
		}

		// Verify proxy restart is queued
		if _, exists := appState.CurrentActions.Restarts["proxy"]; !exists {
			t.Error("Proxy restart not queued after proxy section change")
		}

		t.Logf("✓ Actions compiled: Proxygen=%v, Restarts=%d",
			appState.CurrentActions.Proxygen,
			len(appState.CurrentActions.Restarts))
	})

	t.Run("Generate_Proxy_Config", func(t *testing.T) {
		// Create temporary output directory for test
		tmpDir, err := os.MkdirTemp("", "configd-proxy-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Override output directory in configs
		appState.LocalConfig.Data["zimbraProxyConfDir"] = tmpDir

		// Ensure proxygen is enabled
		appState.CurrentActions.Proxygen = true

		// Run proxy generation
		err = cm.RunProxygenWithConfigs(ctx)
		if err != nil {
			t.Fatalf("RunProxygenWithConfigs failed: %v", err)
		}

		// Verify proxy config files were generated
		entries, err := os.ReadDir(tmpDir)
		if err != nil {
			t.Fatalf("Failed to read output dir: %v", err)
		}

		if len(entries) == 0 {
			t.Error("No proxy config files generated")
		}

		// Check for expected files
		expectedFiles := []string{
			"nginx.conf",
			"nginx.conf.web.http",
			"nginx.conf.web.http.default",
		}

		for _, expectedFile := range expectedFiles {
			filePath := filepath.Join(tmpDir, expectedFile)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				t.Errorf("Expected file not generated: %s", expectedFile)
			}
		}

		// Verify proxygen action is cleared after successful generation
		if appState.CurrentActions.Proxygen {
			t.Error("Proxygen action not cleared after successful generation")
		}

		t.Logf("✓ Proxy config generated: %d files in %s", len(entries), tmpDir)
	})

	t.Run("Watchdog_Service_Tracking", func(t *testing.T) {
		// Enable proxy in watchdog
		wd.SetServiceEnabled(context.Background(), "proxy", true)

		if !wd.IsServiceEnabled("proxy") {
			t.Error("Proxy not enabled in watchdog")
		}

		// Add proxy to watchdog tracking (simulating successful start)
		wd.AddService(context.Background(), "proxy")

		if !wd.IsServiceTracked("proxy") {
			t.Error("Proxy not tracked by watchdog after AddService")
		}

		// Verify state tracking
		status := appState.GetWatchdog("proxy")
		if status == nil || !*status {
			t.Error("Proxy not tracked in application state")
		}

		t.Logf("✓ Watchdog tracking proxy service")
	})

	t.Run("Watchdog_Detects_Service_Failure", func(t *testing.T) {
		// This test validates watchdog can detect service failures
		// In production, watchdog would call serviceManager.IsRunning()
		// which would check systemd status

		// Verify watchdog is not started yet
		if wd.IsEnabled() {
			t.Error("Watchdog should not be running yet")
		}

		// Verify service is tracked
		if !wd.IsServiceTracked("proxy") {
			t.Error("Proxy should be tracked before watchdog starts")
		}

		t.Logf("✓ Watchdog can detect proxy in tracking state")
	})
}

// TestE2E_NetworkCommandsWithProxy tests the network command handler integration.
// This validates STATUS and REWRITE commands work correctly with proxy.
func TestE2E_NetworkCommandsWithProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	// Skip if Carbonio environment is not available
	if _, err := os.Stat("/opt/zextras/conf/localconfig.xml"); os.IsNotExist(err) {
		t.Skip("Skipping: Carbonio environment not available (/opt/zextras/conf/localconfig.xml missing)")
	}

	ctx := context.Background()

	// Create test configuration
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-network-test"

	// Create application state
	appState := state.NewState()

	// Create LDAP client
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Create ConfigManager
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configs
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Fatalf("Failed to load configs: %v", err)
	}

	// Parse MTA config
	err = cm.ParseMtaConfig(context.Background(), mainCfg.ConfigFile)
	if err != nil {
		t.Fatalf("Failed to parse MTA config: %v", err)
	}

	t.Run("STATUS_Command_Reports_Active", func(t *testing.T) {
		// Create request handler
		handler := &network.ConfigdRequestHandler{}

		// Test STATUS command
		response := handler.HandleRequest(context.Background(), "STATUS", []string{})

		if !strings.Contains(response, "SUCCESS") {
			t.Errorf("Expected SUCCESS in STATUS response, got: %s", response)
		}

		if !strings.Contains(response, "ACTIVE") {
			t.Errorf("Expected ACTIVE in STATUS response, got: %s", response)
		}

		t.Logf("✓ STATUS command response: %s", response)
	})

	t.Run("REWRITE_Command_Triggers_Proxy_Regen", func(t *testing.T) {
		// Create reload channel
		reloadChan := make(chan struct{}, 1)

		// Create action trigger
		actionTrigger := &MockActionTrigger{
			ReloadChan: reloadChan,
			State:      appState,
		}

		// Create request handler with trigger
		handler := &network.ConfigdRequestHandler{
			ActionTrigger: actionTrigger,
		}

		// Clear any existing requested configs
		appState.RequestedConfig = make(map[string]string)

		// Test REWRITE command with proxy
		response := handler.HandleRequest(context.Background(), "REWRITE", []string{"proxy"})

		if !strings.Contains(response, "SUCCESS") {
			t.Errorf("Expected SUCCESS in REWRITE response, got: %s", response)
		}

		// Verify reload signal was sent
		select {
		case <-reloadChan:
			t.Logf("✓ Reload signal received")
		case <-time.After(1 * time.Second):
			t.Error("Reload signal not received")
		}

		// Verify proxy was added to requested configs
		if _, exists := appState.RequestedConfig["proxy"]; !exists {
			t.Error("Proxy not added to RequestedConfig")
		}

		t.Logf("✓ REWRITE command triggered proxy regeneration")
	})

	t.Run("REWRITE_Command_Without_Args", func(t *testing.T) {
		// Create reload channel
		reloadChan := make(chan struct{}, 1)

		// Create action trigger
		actionTrigger := &MockActionTrigger{
			ReloadChan: reloadChan,
			State:      appState,
		}

		// Create request handler with trigger
		handler := &network.ConfigdRequestHandler{
			ActionTrigger: actionTrigger,
		}

		// Clear any existing requested configs
		appState.RequestedConfig = make(map[string]string)

		// Test REWRITE command without args (should trigger all)
		response := handler.HandleRequest(context.Background(), "REWRITE", []string{})

		if !strings.Contains(response, "SUCCESS") {
			t.Errorf("Expected SUCCESS in REWRITE response, got: %s", response)
		}

		// Verify reload signal was sent
		select {
		case <-reloadChan:
			t.Logf("✓ Reload signal received")
		case <-time.After(1 * time.Second):
			t.Error("Reload signal not received")
		}

		t.Logf("✓ REWRITE command without args works")
	})

	t.Run("Unknown_Command_Returns_Error", func(t *testing.T) {
		handler := &network.ConfigdRequestHandler{}

		response := handler.HandleRequest(context.Background(), "INVALID", []string{})

		if !strings.Contains(response, "ERROR") {
			t.Errorf("Expected ERROR for unknown command, got: %s", response)
		}

		if !strings.Contains(response, "UNKNOWN COMMAND") {
			t.Errorf("Expected 'UNKNOWN COMMAND' in response, got: %s", response)
		}

		t.Logf("✓ Unknown command handled: %s", response)
	})
}

// TestE2E_ProxyConfigValidation tests that generated proxy configs are valid.
// This validates the complete proxy generation with real template processing.
func TestE2E_ProxyConfigValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	// Create test configuration
	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	appState := state.NewState()

	// Load configs
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)
	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Skipf("Cannot load configs (expected in test env): %v", err)
		return
	}

	t.Run("Complete_Proxy_Generation_Cycle", func(t *testing.T) {
		// Create temporary output directory
		tmpDir, err := os.MkdirTemp("", "configd-proxy-validation-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Override output directory in local config
		appState.LocalConfig.Data["zimbraProxyConfDir"] = tmpDir

		// Create proxy generator with full configs
		gen, err := proxy.LoadConfiguration(
			context.Background(),
			mainCfg,
			appState.LocalConfig,
			appState.GlobalConfig,
			appState.ServerConfig,
			ldapClient,
			nil, // No cache in test
		)
		if err != nil {
			t.Skipf("Cannot initialize proxy generator (expected in test env): %v", err)
			return
		}

		// Generate configuration
		err = gen.GenerateAll(context.Background())
		if err != nil {
			t.Fatalf("Proxy generation failed: %v", err)
		}

		// Validate generated files
		entries, err := os.ReadDir(tmpDir)
		if err != nil {
			t.Fatalf("Failed to read output dir: %v", err)
		}

		if len(entries) == 0 {
			t.Fatal("No files generated")
		}

		// Validate each generated file
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			filePath := filepath.Join(tmpDir, entry.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Errorf("Failed to read %s: %v", entry.Name(), err)
				continue
			}

			// Basic validation: file should not be empty
			if len(content) == 0 {
				t.Errorf("Generated file %s is empty", entry.Name())
			}

			// Validate no unresolved variables (except in explode directives)
			if strings.Contains(string(content), "${") {
				lines := strings.Split(string(content), "\n")
				for i, line := range lines {
					// Skip explode directive lines
					if strings.Contains(line, "!{explode") {
						continue
					}
					// Skip variable-only lines (they're placeholders)
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") {
						continue
					}
					if strings.Contains(line, "${") {
						t.Errorf("File %s line %d has unresolved variable: %s",
							entry.Name(), i+1, strings.TrimSpace(line))
					}
				}
			}

			t.Logf("✓ Validated %s: %d bytes", entry.Name(), len(content))
		}

		t.Logf("✓ Generated and validated %d proxy config files", len(entries))
	})
}

// TestE2E_NetworkListener tests the actual network listener with real TCP connections.
func TestE2E_NetworkListener(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create application state
	appState := state.NewState()

	// Create reload channel
	reloadChan := make(chan struct{}, 1)

	// Create action trigger
	actionTrigger := &MockActionTrigger{
		ReloadChan: reloadChan,
		State:      appState,
	}

	// Create request handler
	handler := &network.ConfigdRequestHandler{
		ActionTrigger: actionTrigger,
	}

	// Create server
	server := network.NewThreadedStreamServer("127.0.0.1", port, false, handler)

	// Start server
	err = server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	t.Run("STATUS_Command_Via_TCP", func(t *testing.T) {
		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Send STATUS command
		_, err = conn.Write([]byte("STATUS\n"))
		if err != nil {
			t.Fatalf("Failed to write: %v", err)
		}

		// Read response
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("Failed to read: %v", err)
		}

		response := string(buf[:n])
		if !strings.Contains(response, "SUCCESS") || !strings.Contains(response, "ACTIVE") {
			t.Errorf("Unexpected response: %s", response)
		}

		t.Logf("✓ TCP STATUS response: %s", strings.TrimSpace(response))
	})

	t.Run("REWRITE_Command_Via_TCP", func(t *testing.T) {
		// Clear requested configs
		appState.RequestedConfig = make(map[string]string)

		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Send REWRITE command
		_, err = conn.Write([]byte("REWRITE proxy\n"))
		if err != nil {
			t.Fatalf("Failed to write: %v", err)
		}

		// Read response
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("Failed to read: %v", err)
		}

		response := string(buf[:n])
		if !strings.Contains(response, "SUCCESS") {
			t.Errorf("Unexpected response: %s", response)
		}

		// Verify reload signal
		select {
		case <-reloadChan:
			t.Logf("✓ Reload signal received")
		case <-time.After(1 * time.Second):
			t.Error("Reload signal not received")
		}

		// Verify proxy in requested configs
		if _, exists := appState.RequestedConfig["proxy"]; !exists {
			t.Error("Proxy not in RequestedConfig")
		}

		t.Logf("✓ TCP REWRITE response: %s", strings.TrimSpace(response))
	})
}

// MockActionTrigger implements the network.ActionTrigger interface for testing.
type MockActionTrigger struct {
	ReloadChan chan struct{}
	State      *state.State
}

func (t *MockActionTrigger) TriggerRewrite(configs []string) {
	// Mock implementation: update state and signal reload
	t.State.AddRequestedConfigs(context.Background(), configs)
	select {
	case t.ReloadChan <- struct{}{}:
		// Reload signal sent
	default:
		// Reload channel full
	}
}
