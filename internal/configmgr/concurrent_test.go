// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/state"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConfigManager_ConcurrentLoading tests concurrent configuration loading.
// This test validates thread safety and ensures no race conditions exist
// when multiple goroutines attempt to load configurations simultaneously.
func TestConfigManager_ConcurrentLoading(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("Skipping concurrent loading test in short mode")
	}

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-concurrent-test"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Try initial load to check if environment is available
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Skipf("Skipping test - requires live Carbonio environment: %v", err)
	}

	const numGoroutines = 20
	const numIterations = 10

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32
	errors := make(chan error, numGoroutines*numIterations)

	// Launch multiple goroutines that load configurations concurrently
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := range numIterations {
				// Each goroutine gets its own ConfigManager instance
				localCM := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

				err := localCM.LoadAllConfigs(context.Background())
				if err != nil {
					errorCount.Add(1)
					errors <- err
					t.Logf("Goroutine %d iteration %d: LoadAllConfigs failed: %v", id, j, err)
					continue
				}

				successCount.Add(1)

				// Verify we can lookup values
				_, err = localCM.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
				if err != nil {
					t.Logf("Goroutine %d iteration %d: LookUpConfig warning: %v", id, j, err)
				}
			}
		}(i)
	}

	// Wait for all goroutines with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		close(done)
		close(errors)
	}()

	select {
	case <-done:
		t.Logf("All goroutines completed. Success: %d, Errors: %d",
			successCount.Load(), errorCount.Load())
	case <-time.After(2 * time.Minute):
		t.Fatal("Test timed out after 2 minutes")
	}

	// Assert zero errors - concurrent loading must be fully safe
	if errorCount.Load() > 0 {
		t.Errorf("Expected zero errors, got %d out of %d operations", errorCount.Load(), numGoroutines*numIterations)
		// Report first few errors
		count := 0
		for err := range errors {
			t.Errorf("Sample error: %v", err)
			count++
			if count >= 5 {
				break
			}
		}
	}

	// Ensure at least some operations succeeded
	if successCount.Load() == 0 {
		t.Fatal("No successful configuration loads")
	}
}

// TestConfigManager_ConcurrentLoadAndLookup tests concurrent loading and lookups.
// This validates that configuration reads are safe while loads are happening.
func TestConfigManager_ConcurrentLoadAndLookup(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("Skipping concurrent load and lookup test in short mode")
	}

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-concurrent-test"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Initial load
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Skipf("Skipping test - requires live Carbonio environment: %v", err)
	}

	const duration = 10 * time.Second
	const numReaders = 10
	const numLoaders = 2

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readOps atomic.Int64
	var loadOps atomic.Int64
	var readErrors atomic.Int32
	var loadErrors atomic.Int32

	// Start reader goroutines
	for i := range numReaders {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					_, err := cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port")
					if err == nil {
						readOps.Add(1)
					} else {
						readErrors.Add(1)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	// Start loader goroutines
	for i := range numLoaders {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					err := cm.LoadAllConfigs(context.Background())
					if err == nil {
						loadOps.Add(1)
					} else {
						loadErrors.Add(1)
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Logf("Operations completed:")
	t.Logf("  Read ops: %d (errors: %d)", readOps.Load(), readErrors.Load())
	t.Logf("  Load ops: %d (errors: %d)", loadOps.Load(), loadErrors.Load())

	// Validate results
	if readOps.Load() == 0 {
		t.Error("No successful read operations")
	}

	if loadOps.Load() == 0 {
		t.Error("No successful load operations")
	}

	// Check error rates
	if readOps.Load() > 0 {
		readErrorRate := float64(readErrors.Load()) / float64(readOps.Load()+int64(readErrors.Load())) * 100
		if readErrorRate > 10.0 {
			t.Errorf("Read error rate too high: %.2f%%", readErrorRate)
		}
	}

	if loadOps.Load() > 0 {
		loadErrorRate := float64(loadErrors.Load()) / float64(loadOps.Load()+int64(loadErrors.Load())) * 100
		if loadErrorRate > 10.0 {
			t.Errorf("Load error rate too high: %.2f%%", loadErrorRate)
		}
	}
}

// TestConfigManager_ConcurrentCompareKeys tests concurrent change detection.
// This validates that CompareKeys is thread-safe.
func TestConfigManager_ConcurrentCompareKeys(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("Skipping concurrent compare keys test in short mode")
	}

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-concurrent-test"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Initial load
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Skipf("Skipping test - requires live Carbonio environment: %v", err)
	}

	const numGoroutines = 10
	const numIterations = 50

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := range numIterations {
				err := cm.CompareKeys(context.Background())
				if err != nil {
					errorCount.Add(1)
					t.Logf("Goroutine %d iteration %d: CompareKeys failed: %v", id, j, err)
				} else {
					successCount.Add(1)
				}
			}
		}(i)
	}

	done := make(chan bool)
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("CompareKeys operations completed. Success: %d, Errors: %d",
			successCount.Load(), errorCount.Load())
	case <-time.After(30 * time.Second):
		t.Fatal("Test timed out after 30 seconds")
	}

	// All operations should succeed
	if successCount.Load() < int32(numGoroutines*numIterations*0.95) {
		t.Errorf("Too many failures: expected ~%d successes, got %d",
			numGoroutines*numIterations, successCount.Load())
	}
}

// TestConfigManager_RaceConditions uses Go's race detector to find data races.
// Run with: go test -race -run=TestConfigManager_RaceConditions
func TestConfigManager_RaceConditions(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("Skipping race condition test in short mode")
	}

	mainCfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	mainCfg.Progname = "configd-race-test"
	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	cm := NewConfigManager(ctx, mainCfg, appState, ldapClient, nil)

	// Try initial load
	err = cm.LoadAllConfigs(context.Background())
	if err != nil {
		t.Skipf("Skipping test - requires live Carbonio environment: %v", err)
	}

	const numWorkers = 5
	const duration = 3 * time.Second

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Mix of different operations
	operations := []func(){
		func() { cm.LoadAllConfigs(context.Background()) },
		func() { cm.LoadLocalConfig(context.Background()) },
		func() { cm.CompareKeys(context.Background()) },
		func() { cm.LookUpConfig(ctx, "LOCAL", "zmconfigd_listen_port") },
		func() { cm.LookUpConfig(ctx, "GLOBAL", "zimbra_server_hostname") },
	}

	for i := range numWorkers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					// Randomly execute different operations
					op := operations[id%len(operations)]
					op()
					time.Sleep(50 * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Log("Race detection test completed successfully")
}
