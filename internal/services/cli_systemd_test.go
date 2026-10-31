// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// TestSystemctl_BootedHostReturnsError verifies Systemctl on a booted host
// whose systemctl exits non-zero.
func TestSystemctl_BootedHostReturnsError(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	ctl.pinMode(true)
	f.Exec.OnPath(systemctlBin, systest.Fail(4, "Unit could not be found."))

	err := ctl.Systemctl(context.Background(), "status", "carbonio-configd-nonexistent-unit-xyzzy.service")
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}
}

// TestSystemctl_NotBooted verifies Systemctl refuses to run on a host that is
// not systemd-booted.
func TestSystemctl_NotBooted(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	err := ctl.Systemctl(context.Background(), "is-active", "carbonio-fake-unit-xyzzy-test.service")
	if !errors.Is(err, ErrSystemdNotBooted) {
		t.Errorf("Systemctl error = %v, want ErrSystemdNotBooted", err)
	}

	if calls := f.Exec.Lines(); len(calls) != 0 {
		t.Errorf("systemctl must not be invoked on a non-booted host, got %q", calls)
	}
}

// TestIsSystemdMode_DoesNotPanic verifies ctl.IsSystemdMode() does not panic.
func TestIsSystemdMode_DoesNotPanic(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.IsSystemdMode()
	ctl.IsSystemdMode()
}
