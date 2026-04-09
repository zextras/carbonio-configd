// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !race

package configmgr

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/state"
	"testing"
)

// BenchmarkConfigManager_LoadAllConfigs benchmarks loading all configurations (local, LDAP, global).
// Requirement: SHALL complete in < 1 second, memory usage SHALL be < 50MB
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_LoadAllConfigs(b *testing.B) {
	ctx := context.Background()

	// Create test components
	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Try initial load to check if Carbonio tools are available
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		err := cm.LoadAllConfigs(context.Background())
		if err != nil {
			b.Fatalf("LoadAllConfigs failed: %v", err)
		}
	}
}

// BenchmarkConfigManager_LoadLocalConfig benchmarks loading localconfig.xml only.
// NOTE: Requires live Carbonio environment with zmlocalconfig available
func BenchmarkConfigManager_LoadLocalConfig(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Try initial load to check if Carbonio tools are available
	err = cm.LoadLocalConfig(context.Background())
	if err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		err := cm.LoadLocalConfig(context.Background())
		if err != nil {
			b.Fatalf("LoadLocalConfig failed: %v", err)
		}
	}
}

// BenchmarkConfigManager_LoadGlobalConfig benchmarks loading global configuration from LDAP.
// NOTE: Requires live Carbonio environment with LDAP connection available
func BenchmarkConfigManager_LoadGlobalConfig(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load local config first (needed for LDAP connection)
	if err := cm.LoadLocalConfig(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	// Try loading global config to check if LDAP is available
	if err := cm.LoadGlobalConfig(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio LDAP connection: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		err := cm.LoadGlobalConfig(context.Background())
		if err != nil {
			b.Fatalf("LoadGlobalConfig failed: %v", err)
		}
	}
}

// BenchmarkConfigManager_CompareKeys benchmarks change detection.
// Requirement: SHALL complete in < 100ms for 100 configuration variables
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_CompareKeys(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configuration
	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		err := cm.CompareKeys(context.Background())
		if err != nil {
			b.Fatalf("CompareKeys failed: %v", err)
		}
	}
}

// BenchmarkConfigManager_LookUpConfig benchmarks configuration value lookups.
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_LookUpConfig(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configuration
	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		_, err := cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
		if err != nil {
			b.Fatalf("LookUpConfig failed: %v", err)
		}
	}
}

// BenchmarkConfigManager_LookUpConfig_Parallel benchmarks concurrent config lookups.
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_LookUpConfig_Parallel(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configuration
	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
			if err != nil {
				b.Fatalf("LookUpConfig failed: %v", err)
			}
		}
	})
}

// BenchmarkConfigManager_FullCycle benchmarks complete configuration cycle.
// Requirement: SHALL complete in < 5 seconds (load, detect, write)
// This simulates the full zmconfigd.cf processing workflow.
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_FullCycle(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Test initial load to check if Carbonio tools are available
	testCM := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)
	if err := testCM.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		// Create fresh ConfigManager for each cycle
		cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

		// Load all configurations
		err := cm.LoadAllConfigs(context.Background())
		if err != nil {
			b.Fatalf("LoadAllConfigs failed: %v", err)
		}

		// Detect configuration changes
		err = cm.CompareKeys(context.Background())
		if err != nil {
			b.Fatalf("CompareKeys failed: %v", err)
		}

		// Perform configuration lookups (simulate template expansion)
		keys := []string{
			"zmconfigd_listen_port",
			"zimbra_server_hostname",
			"zimbra_ldap_password",
			"postfix_enable_smtpd_policyd",
			"av_notify_user",
		}

		for _, key := range keys {
			_, err := cm.LookUpConfig(ctx, "LOCAL", key)
			if err != nil {
				// Some keys may not exist in test environment, continue
				continue
			}
		}
	}
}

// BenchmarkConfigManager_MemoryUsage measures memory footprint during full cycle.
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_MemoryUsage(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Try initial load to check if Carbonio tools are available
	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	// Force memory allocation tracking
	b.ReportAllocs()

	for b.Loop() {
		// Load configurations
		err := cm.LoadAllConfigs(context.Background())
		if err != nil {
			b.Fatalf("LoadAllConfigs failed: %v", err)
		}

		// Perform operations
		_ = cm.CompareKeys(context.Background())

		// Multiple lookups
		for range 100 {
			cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
		}
	}

	// Note: Memory usage requirement is < 50MB
	// Check with: go test -bench=BenchmarkConfigManager_MemoryUsage -benchmem
}

// BenchmarkConfigManager_ConcurrentFullCycle benchmarks full cycle with concurrent operations.
// NOTE: Requires live Carbonio environment with zmlocalconfig and zmprov available
func BenchmarkConfigManager_ConcurrentFullCycle(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Load initial configuration
	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Simulate concurrent configuration reads
			_, _ = cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
			_, _ = cm.LookUpConfig(ctx, "LOCAL", "zimbra_server_hostname")

			// Simulate change detection
			_ = cm.CompareKeys(context.Background())
		}
	})
}

// BenchmarkFullLoop benchmarks the complete main loop iteration.
// This simulates the entire RunMainLoop workflow including:
//   - LoadAllConfigs (local, LDAP, global, misc, server)
//   - ParseMtaConfig (zmconfigd.cf parsing)
//   - CompareKeys (change detection)
//   - CompileActions (action compilation)
//   - DoConfigRewrites (template generation, postconf operations)
//
// This is the most comprehensive benchmark representing real-world performance.
// Requirement: First loop SHALL complete in < 30 seconds, subsequent loops in < 10 seconds
// NOTE: Requires live Carbonio environment with full system access
func BenchmarkFullLoop(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark-full"
	mainCfg.RestartConfig = false // Disable restarts in benchmark
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Try initial load to check if Carbonio environment is available
	testCM := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)
	if err := testCM.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}

	// Try MTA config parse
	if err := testCM.ParseMtaConfig(context.Background(), mainCfg.ConfigFile); err != nil {
		b.Skipf("Skipping benchmark - requires valid MTA config file: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		// Create fresh ConfigManager for each iteration to match main loop behavior
		cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

		// Load all configurations (local, LDAP, global, misc, server)
		err := cm.LoadAllConfigs(context.Background())
		if err != nil {
			b.Fatalf("LoadAllConfigs failed: %v", err)
		}

		// Parse MTA configuration from zmconfigd.cf
		// Loads template definitions, rewrite rules, and service dependencies
		err = cm.ParseMtaConfig(context.Background(), mainCfg.ConfigFile)
		if err != nil {
			b.Fatalf("ParseMtaConfig failed: %v", err)
		}

		// Detect configuration changes
		// Compares current vs. previous configuration state
		err = cm.CompareKeys(context.Background())
		if err != nil {
			b.Fatalf("CompareKeys failed: %v", err)
		}

		// Compile actions based on detected changes
		// Determines which templates need regeneration and which services need restart
		cm.CompileActions(context.Background())

		// Execute configuration rewrites
		// Generates configuration files from templates, runs postconf operations
		// This includes proxy configs (nginx), MTA configs (postfix), etc.
		err = cm.DoConfigRewrites(context.Background())
		if err != nil {
			b.Fatalf("DoConfigRewrites failed: %v", err)
		}

		// Note: We skip DoRestarts() in benchmark as it would restart actual services
		// and is explicitly disabled via mainCfg.RestartConfig = false

		// Mark FirstRun as false for subsequent iterations
		appState.FirstRun = false
	}
}

// BenchmarkFullLoop_FirstRun benchmarks the first loop iteration separately.
// The first loop is typically slower as caches are cold and all services are rewritten.
// NOTE: Requires live Carbonio environment with full system access
func BenchmarkFullLoop_FirstRun(b *testing.B) {
	ctx := context.Background()

	mainCfg, err := config.NewConfig()
	if err != nil {
		b.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-benchmark-firstrun"
	mainCfg.RestartConfig = false
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	// Check environment availability
	appState := state.NewState()
	testCM := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)
	if err := testCM.LoadAllConfigs(context.Background()); err != nil {
		b.Skipf("Skipping benchmark - requires live Carbonio environment: %v", err)
	}
	if err := testCM.ParseMtaConfig(context.Background(), mainCfg.ConfigFile); err != nil {
		b.Skipf("Skipping benchmark - requires valid MTA config file: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		// Create fresh state and manager for each iteration to simulate first run
		freshState := state.NewState()
		freshState.FirstRun = true // Explicitly set FirstRun flag
		cm := NewConfigManager(ctx, mainCfg, freshState, ldapClient, nil)

		// Execute full loop with FirstRun=true
		_ = cm.LoadAllConfigs(context.Background())
		_ = cm.ParseMtaConfig(context.Background(), mainCfg.ConfigFile)
		_ = cm.CompareKeys(context.Background())
		cm.CompileActions(context.Background())
		_ = cm.DoConfigRewrites(context.Background())
	}
}
