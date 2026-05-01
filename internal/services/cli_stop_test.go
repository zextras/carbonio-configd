// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestServiceStop_WithDisabledDependency exercises the "stop dependencies" path.
func TestServiceStop_WithDisabledDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stop dependency test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Dependencies = []string{"nonexistent-dep-xyz"}
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-99999"
	def.PidFile = ""
	def.SystemdUnits = nil
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop with disabled dependency returned error: %v", err)
	}
}

// TestServiceStop_WithEnabledDependency exercises stopping with an enabled dependency.
func TestServiceStop_WithEnabledDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stop dependency test on systemd-booted host")
	}

	const depName = "test-dep-xyzzy"
	Registry[depName] = &ServiceDef{
		Name:       depName,
		CustomStop: func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(Registry, depName)

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Dependencies = []string{depName}
	def.SystemdUnits = nil
	def.PidFile = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop with enabled dependency returned error: %v", err)
	}
}

// TestServiceStop_StopServiceError exercises the stopService error path.
func TestServiceStop_StopServiceError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stopService error test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		return errors.New("stop failed")
	}
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err == nil {
		t.Error("expected ServiceStop to return error when stopService fails")
	}
}

// TestServiceStop_PreStopHookError exercises the pre-stop hook warning path.
func TestServiceStop_PreStopHookError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: pre-stop hook test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	called := false
	def := *orig
	def.PreStop = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			called = true
			return errors.New("pre-stop failed")
		},
	}
	def.SystemdUnits = nil
	def.PidFile = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop should not return error for pre-stop hook failure, got: %v", err)
	}
	if !called {
		t.Error("pre-stop hook was not called")
	}
}

// TestServiceStop_WithPreStopHookFails verifies pre-stop hook does not cause panic.
func TestServiceStop_WithPreStopHookFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	orig, existed := Registry["memcached"]
	defer func() {
		if existed {
			Registry["memcached"] = orig
		}
	}()

	def := *Registry["memcached"]
	def.PreStop = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			return errors.New("pre-stop hook error")
		},
	}
	Registry["memcached"] = &def

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ServiceStop(ctx, "memcached")
}
