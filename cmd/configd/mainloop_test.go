// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

func TestMainLoopActionTrigger_TriggerRewrite(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan: reloadChan,
		State:      appState,
	}

	configs := []string{"proxy", "mta"}
	trigger.TriggerRewrite(ctx, configs)

	if trigger.EventCounter.Load() != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter.Load())
	}

	select {
	case <-reloadChan:
		// Expected: reload signal received
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reload signal on channel")
	}
}

func TestMainLoopActionTrigger_TriggerRewrite_MultipleEvents(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan: reloadChan,
		State:      appState,
	}

	trigger.TriggerRewrite(ctx, []string{"proxy"})
	trigger.TriggerRewrite(ctx, []string{"mta"})
	trigger.TriggerRewrite(ctx, []string{"ldap"})

	if trigger.EventCounter.Load() != 3 {
		t.Errorf("expected EventCounter 3, got %d", trigger.EventCounter.Load())
	}
}

func TestMainLoopActionTrigger_TriggerRewrite_EmptyConfigs(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan: reloadChan,
		State:      appState,
	}

	trigger.TriggerRewrite(ctx, []string{})

	if trigger.EventCounter.Load() != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter.Load())
	}

	select {
	case <-reloadChan:
		// Expected: reload signal received even with empty configs
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reload signal on channel")
	}
}

// newFakeServiceManager builds a ServiceManager on fake host fakes: the tests
// using it only exercise bookkeeping and never reach a service.
func newFakeServiceManager() *services.ServiceManager {
	return services.NewServiceManager(services.NewController(systest.New().System()))
}

// depsRecorder records the dependencies handed to SetDependencies; the
// embedded nil Manager makes any other call panic.
type depsRecorder struct {
	services.Manager
	calls int
	deps  map[string][]string
}

func (r *depsRecorder) SetDependencies(_ context.Context, deps map[string][]string) {
	r.calls++
	r.deps = deps
}

func TestBuildServiceDependencies_NilSections(t *testing.T) {
	ctx := context.Background()
	rec := &depsRecorder{}
	appState := state.NewState()
	// MtaConfig.Sections is initialized to empty map by NewState, set nil to test guard
	appState.MtaConfig.Sections = nil

	buildServiceDependencies(ctx, rec, appState)

	if rec.calls != 0 {
		t.Errorf("SetDependencies called %d times for nil sections, want 0", rec.calls)
	}
}

func TestBuildServiceDependencies_WithDeps(t *testing.T) {
	ctx := context.Background()
	rec := &depsRecorder{}
	appState := state.NewState()

	appState.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Depends: map[string]bool{"ldap": true, "configd": true},
	}
	appState.MtaConfig.Sections["proxy"] = &config.MtaConfigSection{
		Depends: map[string]bool{},
	}

	buildServiceDependencies(ctx, rec, appState)

	want := map[string][]string{"mta": {"configd", "ldap"}}
	if !reflect.DeepEqual(rec.deps, want) {
		t.Errorf("SetDependencies got %v, want %v", rec.deps, want)
	}
}

func TestBuildServiceDependencies_EmptySections(t *testing.T) {
	ctx := context.Background()
	rec := &depsRecorder{}
	appState := state.NewState()
	// Empty sections — no deps to set
	buildServiceDependencies(ctx, rec, appState)

	if len(rec.deps) != 0 {
		t.Errorf("SetDependencies got %v, want none", rec.deps)
	}
}

func TestUpdateWatchdogServices_EmptyList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	tracked := map[string]string{"antivirus": "running", "mta": "running"}
	mainCfg, _ := config.NewConfigWithHostname("")
	mainCfg.WdList = []string{}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: newFakeServiceManager(),
		State:          appState,
		Clock:          systest.New().Clock,
		ConfigLookup:   func(_ string) string { return "" },
	})

	// An empty list defaults to ["antivirus"]
	updateWatchdogServices(ctx, wd, tracked, mainCfg)

	if !wd.IsServiceTracked("antivirus") {
		t.Error("expected the default antivirus entry to be tracked")
	}

	if wd.IsServiceTracked("mta") {
		t.Error("mta is not in the default watchdog list and must not be tracked")
	}
}

func TestUpdateWatchdogServices_WithList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	tracked := map[string]string{"mta": "running", "proxy": "running"}
	mainCfg, _ := config.NewConfigWithHostname("")
	mainCfg.WdList = []string{"antivirus", "mta"}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: newFakeServiceManager(),
		State:          appState,
		Clock:          systest.New().Clock,
		ConfigLookup:   func(_ string) string { return "" },
	})

	updateWatchdogServices(ctx, wd, tracked, mainCfg)

	if !wd.IsServiceTracked("mta") {
		t.Error("expected mta, tracked and listed, to be tracked")
	}

	if wd.IsServiceTracked("proxy") {
		t.Error("proxy is tracked by the plan but not listed; it must not be watched")
	}
}

func TestUpdateWatchdogServices_ServiceInWatchdogList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	mainCfg, _ := config.NewConfigWithHostname("")
	mainCfg.WdList = []string{"antivirus"}

	// Add a service to the plan's tracked set so we exercise the slices.Contains path
	tracked := map[string]string{"antivirus": "running"}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: newFakeServiceManager(),
		State:          appState,
		Clock:          systest.New().Clock,
		ConfigLookup:   func(_ string) string { return "" },
	})

	updateWatchdogServices(ctx, wd, tracked, mainCfg)

	if !wd.IsServiceTracked("antivirus") {
		t.Error("expected antivirus, present in plan.Tracked and the watchdog list, to be tracked")
	}
}

func TestMainLoopActionTrigger_TriggerRewrite_ChannelBlocked(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan: reloadChan,
		State:      appState,
	}

	// Fill the channel
	reloadChan <- struct{}{}

	// This should not block even though channel is full
	trigger.TriggerRewrite(ctx, []string{"proxy"})

	if trigger.EventCounter.Load() != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter.Load())
	}

	// Channel should still have only one item
	select {
	case <-reloadChan:
		// Expected: original signal
	case <-time.After(100 * time.Millisecond):
		t.Error("expected signal on channel")
	}

	// Channel should now be empty
	select {
	case <-reloadChan:
		t.Error("expected channel to be empty after first read")
	case <-time.After(10 * time.Millisecond):
		// Expected: channel is empty
	}
}
