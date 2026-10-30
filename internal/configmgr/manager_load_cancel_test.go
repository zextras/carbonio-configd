// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
)

// cancelDuringCommand registers name so that executing it cancels the load
// context (as a timeout or shutdown would mid-load) and then succeeds.
func cancelDuringCommand(cm *ConfigManager, cancel context.CancelFunc, name, output string) {
	cm.CommandRegistry.Commands[name] = commands.NewCommand(name, name,
		func(context.Context, ...string) (string, error) {
			cancel()

			return output, nil
		})
}

// TestLoaders_CancelledMidLoadDoNotPublish: a loader whose context is cancelled
// mid-load must return an error and leave State untouched. A straggler of a
// timed-out cycle would otherwise overwrite the state of a later cycle.
func TestLoaders_CancelledMidLoadDoNotPublish(t *testing.T) {
	tests := []struct {
		name   string
		target func(cm *ConfigManager) *config.ConfigMap
		setup  func(cm *ConfigManager, cancel context.CancelFunc)
		load   func(cm *ConfigManager, ctx context.Context) error
	}{
		{
			name:   "global",
			target: func(cm *ConfigManager) *config.ConfigMap { return cm.State.GlobalConfig.Data },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cancelDuringCommand(cm, cancel, "gacf", "zimbraFoo: new\n")
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.loadGlobalConfigWithRetry(ctx, 1) },
		},
		{
			name:   "server",
			target: func(cm *ConfigManager) *config.ConfigMap { return cm.State.ServerConfig.Data },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cancelDuringCommand(cm, cancel, "gs", "zimbraFoo: new\nzimbraServiceEnabled: mta\n")
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.loadServerConfigWithRetry(ctx, 1) },
		},
		{
			name:   "misc",
			target: func(cm *ConfigManager) *config.ConfigMap { return cm.State.MiscConfig.Data },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cancelDuringCommand(cm, cancel, "garpu", "new-value")
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.LoadMiscConfig(ctx) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newTestConfigManager(t)
			target := tt.target(cm)
			target.Set("old", "value")

			before := target.Snapshot()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tt.setup(cm, cancel)

			err := tt.load(cm, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("load error = %v, want context.Canceled", err)
			}

			if after := target.Snapshot(); !maps.Equal(before, after) {
				t.Errorf("state published after cancellation: before=%v after=%v", before, after)
			}
		})
	}
}

// TestParseLocalConfigOutput_CancelledDoesNotPublish: the local config is
// replaced in one step and only while the context is alive.
func TestParseLocalConfigOutput_CancelledDoesNotPublish(t *testing.T) {
	cm := newTestConfigManager(t)
	cm.State.LocalConfig.Data.Set("old", "value")

	before := cm.State.LocalConfig.Data.Snapshot()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cm.parseLocalConfigOutput(ctx, "key1 = value1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parseLocalConfigOutput error = %v, want context.Canceled", err)
	}

	if after := cm.State.LocalConfig.Data.Snapshot(); !maps.Equal(before, after) {
		t.Errorf("local config published after cancellation: before=%v after=%v", before, after)
	}
}

// TestParseLocalConfigOutput_ReplacesAndDefaults: a live context publishes the
// parsed data together with the post-processing defaults, dropping stale keys.
func TestParseLocalConfigOutput_ReplacesAndDefaults(t *testing.T) {
	cm := newTestConfigManager(t)
	cm.State.LocalConfig.Data.Set("stale", "value")

	if err := cm.parseLocalConfigOutput(context.Background(), "key1 = value1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := cm.State.LocalConfig.Data

	if _, ok := data.Get("stale"); ok {
		t.Error("stale key survived the replace")
	}

	if got := data.GetOr("key1", ""); got != "value1" {
		t.Errorf("key1 = %q, want value1", got)
	}

	if got := data.GetOr("zmconfigd_listen_port", ""); got != "7171" {
		t.Errorf("zmconfigd_listen_port = %q, want default 7171", got)
	}
}
