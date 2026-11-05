// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// A failed init-mode probe is no verdict: IsSystemdMode must not pin legacy
// mode for the daemon's lifetime, and must probe again on the next call.
func TestIsSystemdMode_ProbeFailureIsNotCached(t *testing.T) {
	t.Parallel()

	f := systest.New()

	var systemctlBroken atomic.Bool

	systemctlBroken.Store(true)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return systemctlBroken.Load() && c.Path == "systemctl" },
		errors.New("exec: systemctl: dbus not ready"))

	ctl := NewController(f.System(), WithConfigLoader(emptyConfig))

	assert.False(t, ctl.IsSystemdMode(), "probe failed: legacy for this call")

	systemctlBroken.Store(false) // every target now reports "enabled" (default success)

	assert.True(t, ctl.IsSystemdMode(), "the failure was not cached: the next call probes again")

	systemctlBroken.Store(true)

	assert.True(t, ctl.IsSystemdMode(), "a definite verdict is cached")
}

func TestIsSystemdMode_DefiniteNegativeIsCached(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnCmdline("systemctl is-enabled", systest.Fail(1, ""))

	ctl := NewController(f.System(), WithConfigLoader(emptyConfig))

	assert.False(t, ctl.IsSystemdMode())

	calls := len(f.Exec.Calls())

	assert.False(t, ctl.IsSystemdMode())
	assert.Len(t, f.Exec.Calls(), calls, "no new probe after a definite verdict")
}
