// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// registerRestartStub registers a ctl.registry entry whose start either succeeds
// or fails, and forces legacy (non-systemd) mode for the test.
func registerRestartStub(t *testing.T, ctl *Controller, name string, startErr error) {
	t.Helper()

	ctl.pinMode(false)

	ctl.registry[name] = &ServiceDef{
		Name:        name,
		CustomStart: func(_ context.Context, _ *ServiceDef) error { return startErr },
		CustomStop:  func(_ context.Context, _ *ServiceDef) error { return nil },
	}

	t.Cleanup(func() { delete(ctl.registry, name) })
}

// TestProcessRestarts_DroppedServicesReturnSortedError verifies that services
// dropped after max failed attempts are reported (sorted, wrapped) instead of
// ProcessRestarts claiming success, while healthy services are not listed.
func TestProcessRestarts_DroppedServicesReturnSortedError(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "zz-failing", errors.New("boom"))
	registerRestartStub(t, ctl, "aa-failing", errors.New("boom"))
	registerRestartStub(t, ctl, "mm-healthy", nil)

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 1

	for _, svc := range []string{"zz-failing", "mm-healthy", "aa-failing"} {
		sm.RestartQueue[svc] = true
	}

	err := sm.ProcessRestarts(context.Background(), nil)
	if !errors.Is(err, ErrRestartsFailed) {
		t.Fatalf("ProcessRestarts error = %v, want ErrRestartsFailed", err)
	}

	if !strings.HasSuffix(err.Error(), ": aa-failing, zz-failing") {
		t.Errorf("error %q must list failed services sorted and exclude healthy ones", err)
	}

	if len(sm.RestartQueue) != 0 {
		t.Errorf("queue = %v, want empty (failed services dropped after max attempts)", sm.RestartQueue)
	}
}

// TestProcessRestarts_SucceedingServiceReturnsNil guards against reporting
// failure for a clean restart.
func TestProcessRestarts_SucceedingServiceReturnsNil(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "ok-svc", nil)

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 1
	sm.RestartQueue["ok-svc"] = true

	if err := sm.ProcessRestarts(context.Background(), nil); err != nil {
		t.Fatalf("ProcessRestarts error = %v, want nil", err)
	}
}

// TestGetSortedServices_EqualPriorityIsStable verifies services sharing a start
// order (including every service absent from StartOrder) sort by name, so map
// iteration order never leaks into the restart order.
func TestGetSortedServices_EqualPriorityIsStable(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	sm := NewServiceManager(ctl)
	sm.StartOrder = map[string]int{"first": 0, "tie-b": 10, "tie-a": 10}

	names := []string{"zeta", "tie-b", "alpha", "first", "tie-a", "mid", "omega", "beta"}
	for _, n := range names {
		sm.RestartQueue[n] = true
	}

	want := []string{"first", "tie-a", "tie-b", "alpha", "beta", "mid", "omega", "zeta"}

	// Many iterations: Go randomizes map iteration, so an unstable tiebreak
	// fails with overwhelming probability.
	for range 200 {
		if got := sm.getSortedServices(); !slices.Equal(got, want) {
			t.Fatalf("getSortedServices() = %v, want %v", got, want)
		}
	}
}

// TestControlProcess_NeverRewritesConfig is the regression for the self-REWRITE
// stall: the daemon's main loop restarts a service through ControlProcess, and
// a start that rewrote its configuration would send a REWRITE to this daemon,
// which is busy running the restart and answers after the 60 s timeout. Legacy
// controlProcess always passed norewrite. The restart still starts the service
// (a failing start is reported) and the configuration is never touched.
func TestControlProcess_NeverRewritesConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		action   ServiceAction
		startErr error
	}{
		{name: "restart", action: ActionRestart},
		{name: "start", action: ActionStart},
		{name: "failed restart", action: ActionRestart, startErr: errors.New("nginx: operation not permitted")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			started := false
			reg := map[string]*ServiceDef{
				"proxy": {
					Name:          "proxy",
					ConfigRewrite: []string{"proxy"},
					CustomStart: func(_ context.Context, _ *ServiceDef) error {
						started = true

						return tc.startErr
					},
					CustomStop: func(_ context.Context, _ *ServiceDef) error { return nil },
				},
			}

			ctl, f := newFakeController(t, WithRegistry(reg))
			f.FS.Put(ctl.p.configrewrite, []byte("#!/bin/sh\n"), 0o755)

			err := NewServiceManager(ctl).ControlProcess(context.Background(), "proxy", tc.action)

			if !errors.Is(err, tc.startErr) {
				t.Errorf("ControlProcess error = %v, want %v", err, tc.startErr)
			}

			if !started {
				t.Error("the service was not started")
			}

			for _, line := range f.Exec.Lines() {
				if strings.HasPrefix(line, ctl.p.configrewrite) {
					t.Errorf("config rewritten by a daemon-initiated %s: %q", tc.name, line)
				}
			}
		})
	}
}
