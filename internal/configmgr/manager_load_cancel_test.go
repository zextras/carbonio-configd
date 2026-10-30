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
	"github.com/zextras/carbonio-configd/internal/snapshot"
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

// miscKeys returns the set values among keys of the snapshot's misc config.
func miscKeys(snap *snapshot.Snapshot, keys ...string) map[string]string {
	out := map[string]string{}

	for _, k := range keys {
		if v, ok := snap.Misc(k); ok {
			out[k] = v
		}
	}

	return out
}

// TestLoaders_CancelledMidLoadDoNotPublish: a loader whose context is cancelled
// mid-load must return an error and leave State untouched. A straggler of a
// timed-out cycle would otherwise overwrite the state of a later cycle.
func TestLoaders_CancelledMidLoadDoNotPublish(t *testing.T) {
	tests := []struct {
		name   string
		seed   func(cm *ConfigManager)
		target func(cm *ConfigManager) map[string]string
		setup  func(cm *ConfigManager, cancel context.CancelFunc)
		load   func(cm *ConfigManager, ctx context.Context) error
	}{
		{
			name:   "global",
			seed:   func(cm *ConfigManager) { setGlobal(cm, map[string]string{"old": "value"}) },
			target: func(cm *ConfigManager) map[string]string { return cm.State.Snapshot().GlobalMap() },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cm.CommandRegistry.GlobalAttrs = func(context.Context) (map[string]string, error) {
					cancel()

					return map[string]string{"zimbraFoo": "new"}, nil
				}
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.loadGlobalConfigWithRetry(ctx, 1) },
		},
		{
			name:   "server",
			seed:   func(cm *ConfigManager) { setServer(cm, map[string]string{"old": "value"}, nil) },
			target: func(cm *ConfigManager) map[string]string { return cm.State.Snapshot().ServerMap() },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cm.CommandRegistry.ServerAttrs = func(context.Context, string) (map[string]string, error) {
					cancel()

					return map[string]string{"zimbraFoo": "new", "zimbraServiceEnabled": "mta"}, nil
				}
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.loadServerConfigWithRetry(ctx, 1) },
		},
		{
			name:   "misc",
			seed:   func(cm *ConfigManager) { setMisc(cm, map[string]string{"old": "value"}) },
			target: func(cm *ConfigManager) map[string]string { return miscKeys(cm.State.Snapshot(), "old", "garpu") },
			setup: func(cm *ConfigManager, cancel context.CancelFunc) {
				cancelDuringCommand(cm, cancel, "garpu", "new-value")
			},
			load: func(cm *ConfigManager, ctx context.Context) error { return cm.LoadMiscConfig(ctx) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newTestConfigManager(t)
			tt.seed(cm)

			before := tt.target(cm)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tt.setup(cm, cancel)

			err := tt.load(cm, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("load error = %v, want context.Canceled", err)
			}

			if after := tt.target(cm); !maps.Equal(before, after) {
				t.Errorf("state published after cancellation: before=%v after=%v", before, after)
			}
		})
	}
}

// TestParseLocalConfigOutput_CancelledDoesNotPublish: a cancelled context yields
// an error and no data, and the published snapshot is left untouched.
func TestParseLocalConfigOutput_CancelledDoesNotPublish(t *testing.T) {
	cm := newTestConfigManager(t)
	setLocal(cm, map[string]string{"old": "value"})

	before := cm.State.Snapshot().LocalMap()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data, err := cm.parseLocalConfigOutput(ctx, "key1 = value1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parseLocalConfigOutput error = %v, want context.Canceled", err)
	}

	if data != nil {
		t.Errorf("data = %v, want nil after cancellation", data)
	}

	if after := cm.State.Snapshot().LocalMap(); !maps.Equal(before, after) {
		t.Errorf("local config published after cancellation: before=%v after=%v", before, after)
	}
}

// TestParseLocalConfigOutput_ReplacesAndDefaults: a live context returns the
// parsed data together with the post-processing defaults; it only contains
// what the output holds (stale keys of the published snapshot are not carried).
func TestParseLocalConfigOutput_ReplacesAndDefaults(t *testing.T) {
	cm := newTestConfigManager(t)
	setLocal(cm, map[string]string{"stale": "value"})

	data, err := cm.parseLocalConfigOutput(context.Background(), "key1 = value1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := data["stale"]; ok {
		t.Error("stale key leaked into parsed data")
	}

	if got := data["key1"]; got != "value1" {
		t.Errorf("key1 = %q, want value1", got)
	}

	if got := data["zmconfigd_listen_port"]; got != "7171" {
		t.Errorf("zmconfigd_listen_port = %q, want default 7171", got)
	}
}

// loadAllFixture wires the four loaders of a ConfigManager to fixed data; the
// server loader fails when serverErr is set.
func loadAllFixture(cm *ConfigManager, serverErr error) {
	cm.loadResolvedLocalConfig = func() (map[string]string, error) {
		return map[string]string{"local_key": "new"}, nil
	}
	cm.CommandRegistry.GlobalAttrs = func(context.Context) (map[string]string, error) {
		return map[string]string{"zimbraGlobalKey": "new"}, nil
	}
	cm.CommandRegistry.ServerAttrs = func(context.Context, string) (map[string]string, error) {
		if serverErr != nil {
			return nil, serverErr
		}

		return map[string]string{"zimbraServerKey": "new", "zimbraServiceEnabled": "mta"}, nil
	}
	cancelNothing := func(name, out string) {
		cm.CommandRegistry.Commands[name] = commands.NewCommand(name, name,
			func(context.Context, ...string) (string, error) { return out, nil })
	}
	cancelNothing("garpu", "new-misc")
}

// TestLoadAllConfigs_PublishesOneSnapshotOnSuccess: all four loaders succeed and
// their results become visible together.
func TestLoadAllConfigs_PublishesOneSnapshotOnSuccess(t *testing.T) {
	cm := newTestConfigManager(t)
	setLocal(cm, map[string]string{"old": "value"})
	loadAllFixture(cm, nil)

	if err := cm.LoadAllConfigs(context.Background()); err != nil {
		t.Fatalf("LoadAllConfigs: %v", err)
	}

	snap := cm.State.Snapshot()

	if got := valueOrEmpty(snap.Local("local_key")); got != "new" {
		t.Errorf("local_key = %q, want new", got)
	}

	if _, ok := snap.Local("old"); ok {
		t.Error("stale local key survived the full load")
	}

	if got := valueOrEmpty(snap.Global("zimbraGlobalKey")); got != "new" {
		t.Errorf("zimbraGlobalKey = %q, want new", got)
	}

	if got := valueOrEmpty(snap.Server("zimbraServerKey")); got != "new" {
		t.Errorf("zimbraServerKey = %q, want new", got)
	}

	if got := valueOrEmpty(snap.Misc("garpu")); got != "new-misc" {
		t.Errorf("garpu = %q, want new-misc", got)
	}

	if !snap.ServiceEnabled("mta") {
		t.Error("mta service not enabled after load")
	}
}

// TestLoadAllConfigs_PartialFailureKeepsOldSnapshot: when one loader fails, no
// part of the attempt is published (previously the partial data was); the
// error is returned and the old snapshot stays in place.
func TestLoadAllConfigs_PartialFailureKeepsOldSnapshot(t *testing.T) {
	cm := newTestConfigManager(t)
	setLocal(cm, map[string]string{"old": "value"})
	setGlobal(cm, map[string]string{"oldGlobal": "value"})
	loadAllFixture(cm, errors.New("ldap down"))

	before := cm.State.Snapshot()

	if err := cm.LoadAllConfigsWithRetry(context.Background(), 1); err == nil {
		t.Fatal("LoadAllConfigs error = nil, want the server loader failure")
	}

	after := cm.State.Snapshot()

	if after != before {
		t.Error("a snapshot was published although a loader failed")
	}

	if got := valueOrEmpty(after.Local("old")); got != "value" {
		t.Errorf("old local key = %q, want value", got)
	}

	if _, ok := after.Local("local_key"); ok {
		t.Error("local data of the failed attempt was published")
	}

	if _, ok := after.Global("zimbraGlobalKey"); ok {
		t.Error("global data of the failed attempt was published")
	}
}
