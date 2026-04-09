// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

func TestMainLoopActionTrigger_TriggerRewrite(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	configs := []string{"proxy", "mta"}
	trigger.TriggerRewrite(configs)

	if trigger.EventCounter != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter)
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
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	trigger.TriggerRewrite([]string{"proxy"})
	trigger.TriggerRewrite([]string{"mta"})
	trigger.TriggerRewrite([]string{"ldap"})

	if trigger.EventCounter != 3 {
		t.Errorf("expected EventCounter 3, got %d", trigger.EventCounter)
	}
}

func TestMainLoopActionTrigger_TriggerRewrite_EmptyConfigs(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	trigger.TriggerRewrite([]string{})

	if trigger.EventCounter != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter)
	}

	select {
	case <-reloadChan:
		// Expected: reload signal received even with empty configs
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reload signal on channel")
	}
}

func TestBuildServiceDependencies_NilSections(t *testing.T) {
	ctx := context.Background()
	sm := services.NewServiceManager()
	appState := state.NewState()
	// MtaConfig.Sections is initialized to empty map by NewState, set nil to test guard
	appState.MtaConfig.Sections = nil
	// Must not panic
	buildServiceDependencies(ctx, sm, appState)
}

func TestBuildServiceDependencies_WithDeps(t *testing.T) {
	ctx := context.Background()
	sm := services.NewServiceManager()
	appState := state.NewState()

	appState.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Depends: map[string]bool{"ldap": true, "configd": true},
	}
	appState.MtaConfig.Sections["proxy"] = &config.MtaConfigSection{
		Depends: map[string]bool{},
	}

	// Must not panic and should call SetDependencies
	buildServiceDependencies(ctx, sm, appState)
}

func TestBuildServiceDependencies_EmptySections(t *testing.T) {
	ctx := context.Background()
	sm := services.NewServiceManager()
	appState := state.NewState()
	// Empty sections — no deps to set
	buildServiceDependencies(ctx, sm, appState)
}

func TestUpdateWatchdogServices_EmptyList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	mainCfg, _ := config.NewConfig()
	mainCfg.WdList = []string{}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: services.NewServiceManager(),
		State:          appState,
		ConfigLookup:   func(_ string) string { return "" },
	})

	// Must not panic; defaults to ["antivirus"]
	updateWatchdogServices(ctx, wd, appState, mainCfg)
}

func TestUpdateWatchdogServices_WithList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	mainCfg, _ := config.NewConfig()
	mainCfg.WdList = []string{"antivirus", "mta"}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: services.NewServiceManager(),
		State:          appState,
		ConfigLookup:   func(_ string) string { return "" },
	})

	updateWatchdogServices(ctx, wd, appState, mainCfg)
}

func TestUpdateWatchdogServices_ServiceInWatchdogList(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	mainCfg, _ := config.NewConfig()
	mainCfg.WdList = []string{"antivirus"}

	// Add a service to CurrentActions so we exercise the slices.Contains path
	appState.CurrentActions.Services["antivirus"] = "running"

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  0,
		ServiceManager: services.NewServiceManager(),
		State:          appState,
		ConfigLookup:   func(_ string) string { return "" },
	})

	updateWatchdogServices(ctx, wd, appState, mainCfg)
}

func TestMainLoopActionTrigger_TriggerRewrite_ChannelBlocked(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	reloadChan := make(chan struct{}, 1)

	trigger := &MainLoopActionTrigger{
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	// Fill the channel
	reloadChan <- struct{}{}

	// This should not block even though channel is full
	trigger.TriggerRewrite([]string{"proxy"})

	if trigger.EventCounter != 1 {
		t.Errorf("expected EventCounter 1, got %d", trigger.EventCounter)
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
