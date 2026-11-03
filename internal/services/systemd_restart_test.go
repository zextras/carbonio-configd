// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func systemdTestRegistry() map[string]*ServiceDef {
	return map[string]*ServiceDef{
		"multi":    {Name: "multi", SystemdUnits: []string{"carbonio-a.service", "carbonio-b.service"}},
		"single":   {Name: "single", SystemdUnits: []string{"carbonio-single.service"}},
		"nounit":   {Name: "nounit"},
		"external": {Name: "external", SystemdUnits: []string{"ext.service"}, ExternallyManaged: true},
	}
}

func TestServiceTryReloadOrRestart_OneSystemctlCallWithAllUnits(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
	ctl.pinMode(true)

	require.NoError(t, ctl.ServiceTryReloadOrRestart(context.Background(), "multi"))

	assert.Equal(t,
		[]string{systemctlBin + " try-reload-or-restart carbonio-a.service carbonio-b.service"},
		callsWithPrefix(f, systemctlBin))
}

func TestServiceTryReloadOrRestart_Errors(t *testing.T) {
	t.Parallel()

	t.Run("unknown service", func(t *testing.T) {
		t.Parallel()

		ctl, _ := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)

		require.ErrorContains(t, ctl.ServiceTryReloadOrRestart(context.Background(), "nope"), "unknown service")
	})

	t.Run("no unit", func(t *testing.T) {
		t.Parallel()

		ctl, _ := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)

		require.ErrorContains(t, ctl.ServiceTryReloadOrRestart(context.Background(), "nounit"), "no systemd unit")
	})

	t.Run("not booted", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))

		require.ErrorIs(t, ctl.ServiceTryReloadOrRestart(context.Background(), "single"), ErrSystemdNotBooted)
		assert.Empty(t, callsWithPrefix(f, systemctlBin))
	})

	t.Run("systemctl failure", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)
		f.Exec.OnPath(systemctlBin, systest.Fail(1, "Job failed"))

		err := ctl.ServiceTryReloadOrRestart(context.Background(), "single")
		require.ErrorContains(t, err, "Job failed")
	})

	t.Run("externally managed is left alone", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)

		require.NoError(t, ctl.ServiceTryReloadOrRestart(context.Background(), "external"))
		assert.Empty(t, callsWithPrefix(f, systemctlBin))
	})
}

func TestApplySystemd(t *testing.T) {
	t.Parallel()

	t.Run("restart is try-reload-or-restart, never stop/start", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)
		sm := NewServiceManager(ctl)

		require.True(t, sm.IsSystemdMode())
		require.NoError(t, sm.ApplySystemd(context.Background(), "Single", plan.ActionRestart))

		assert.Equal(t, []string{systemctlBin + " try-reload-or-restart carbonio-single.service"},
			callsWithPrefix(f, systemctlBin))
	})

	t.Run("stop goes through systemctl stop", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)
		sm := NewServiceManager(ctl)

		require.NoError(t, sm.ApplySystemd(context.Background(), "single", plan.ActionStop))

		assert.Contains(t, callsWithPrefix(f, systemctlBin), systemctlBin+" stop carbonio-single.service")
	})

	t.Run("pending-restart is never executed", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)
		sm := NewServiceManager(ctl)

		require.Error(t, sm.ApplySystemd(context.Background(), "single", plan.ActionPendingRestart))
		assert.Empty(t, callsWithPrefix(f, systemctlBin))
	})

	t.Run("dry-run executes nothing", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithRegistry(systemdTestRegistry()))
		ctl.pinMode(true)
		sm := NewServiceManager(ctl)
		sm.DisableRestarts = true

		require.NoError(t, sm.ApplySystemd(context.Background(), "single", plan.ActionRestart))
		assert.Empty(t, callsWithPrefix(f, systemctlBin))
	})
}

// TestServiceManager_BeforeRestartRunsBeforeEachRestart pins the hook the
// watchdog uses to stop watching a service that is restarted on purpose.
func TestServiceManager_BeforeRestartRunsBeforeEachRestart(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t, WithRegistry(map[string]*ServiceDef{
		"svc": {Name: "svc", CustomStart: func(context.Context, *ServiceDef) error { return nil }},
	}))
	sm := NewServiceManager(ctl)

	var seen []string

	sm.BeforeRestart = func(service string) { seen = append(seen, service) }

	require.NoError(t, sm.AddRestart(context.Background(), "svc"))
	_ = sm.ProcessRestarts(context.Background(), nil)

	assert.Equal(t, []string{"svc"}, seen)
}

// TestServiceManager_ConcurrentUse drives the manager the way the daemon does
// (config restarts on one goroutine, watchdog restarts and the DEPENDS refresh
// on others); it only has to be clean under -race.
func TestServiceManager_ConcurrentUse(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t, WithRegistry(map[string]*ServiceDef{
		"svc": {Name: "svc", CustomStart: func(context.Context, *ServiceDef) error { return nil }},
	}))
	sm := NewServiceManager(ctl)

	lookup := func(string) string { return "enabled" }

	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 20 {
				_ = sm.AddRestart(context.Background(), "svc")
				_ = sm.ProcessRestarts(context.Background(), lookup)
				sm.SetDependencies(context.Background(), map[string][]string{"other": {"another"}})
				_ = sm.GetPendingRestarts()
			}
		}()
	}

	wg.Wait()
}

func callsWithPrefix(f *systest.Fakes, prefix string) []string {
	var out []string

	for _, line := range f.Exec.Lines() {
		if len(line) >= len(prefix) && line[:len(prefix)] == prefix && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}

	return out
}
