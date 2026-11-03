// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func withStatusUnit(ctl *Controller) {
	ctl.pinMode(true)

	def := *ctl.registry["memcached"]
	def.SystemdUnits = []string{"carbonio-stub.service"}
	def.PidFile = ""
	def.ProcessName = ""
	ctl.registry["memcached"] = &def
}

// A non-zero systemctl exit (inactive/unknown unit) means "not running".
func TestServiceStatus_IsActiveNonZeroExit_NotRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	withStatusUnit(ctl)
	f.Exec.OnPath(systemctlBin, systest.Fail(3, ""))

	running, err := ctl.ServiceStatus(context.Background(), "memcached")
	if err != nil || running {
		t.Errorf("ServiceStatus = %v, %v; want false, nil", running, err)
	}
}

// An exec failure must surface as an error, not as "service down".
func TestServiceStatus_SystemctlExecFailure_ReturnsError(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	withStatusUnit(ctl)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == systemctlBin }, errors.New("fork/exec: no such file"))

	running, err := ctl.ServiceStatus(context.Background(), "memcached")
	if err == nil {
		t.Fatal("expected an error when systemctl cannot be executed")
	}

	if running {
		t.Error("running must be false alongside an error")
	}
}

// A cancelled context kills systemctl; that is an error, not "inactive".
func TestServiceStatus_ContextCancelled_ReturnsError(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	withStatusUnit(ctl)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ctl.ServiceStatus(ctx, "memcached"); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}
