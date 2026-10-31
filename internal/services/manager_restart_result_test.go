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
