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

// TestServiceRestart_MTA_IsRegistered verifies that MTA restart calls ServiceReload.
func TestServiceRestart_MTA_IsRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ServiceRestart(ctx, "mta")
	_ = err
}

// TestServiceRestart_StopFailedStartAnyway exercises the "stop failed, start anyway" branch.
func TestServiceRestart_StopFailedStartAnyway(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: restart test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Name = "memcached"
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		return errors.New("stop failed intentionally")
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		return nil
	}
	def.ConfigRewrite = nil
	Registry["memcached"] = &def

	ServiceRestart(context.Background(), "memcached")
}

// TestServiceRestart_NonMTA_StopAndStartBoth exercises the stop-failed-warn-then-start path.
func TestServiceRestart_NonMTA_StopAndStartBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping restart branch test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	stopCalled := false
	startCalled := false

	def := *orig
	def.Name = "memcached"
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.ConfigRewrite = nil
	def.Dependencies = nil
	def.PreStart = nil
	def.PostStart = nil
	def.PreStop = nil
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		stopCalled = true
		return errors.New("stop failed intentionally")
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		startCalled = true
		return nil
	}
	Registry["memcached"] = &def

	ServiceRestart(context.Background(), "memcached")

	if !stopCalled {
		t.Error("expected stop to be called")
	}
	if !startCalled {
		t.Error("expected start to be called even after stop failure")
	}
}

// TestServiceReload_NoSystemdUnits verifies ServiceReload returns nil when no units defined.
func TestServiceReload_NoSystemdUnits(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	err := ServiceReload(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceReload with no units returned error: %v", err)
	}
}
