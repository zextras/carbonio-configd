// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/systemd"
)

// TestSystemctl_BootedHostReturnsError verifies Systemctl on a booted host.
func TestSystemctl_BootedHostReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if !systemd.IsBooted() {
		t.Skip("skipping: Systemctl success path requires systemd-booted host")
	}

	err := Systemctl(context.Background(), "status", "carbonio-configd-nonexistent-unit-xyzzy.service")
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}
}

// TestSystemctl_AnyHost_FakeUnit verifies behavior on any host type.
func TestSystemctl_AnyHost_FakeUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := Systemctl(context.Background(), "is-active", "carbonio-fake-unit-xyzzy-test.service")
	if err == nil {
		t.Error("expected error for fake unit")
	}
}

// TestIsSystemdMode_DoesNotPanic verifies IsSystemdMode() does not panic.
func TestIsSystemdMode_DoesNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	IsSystemdMode()
	IsSystemdMode()
}
