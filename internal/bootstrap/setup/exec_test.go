// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A step command must never get the controlling terminal: zmprov runs stty
// on /dev/tty and, from a background process group, is stopped by SIGTTOU,
// hanging a bootstrap started from a terminal.
func TestStepCommandsRunWithoutTerminal(t *testing.T) {
	e, fakes, _ := testEnv(t)

	require.NoError(t, e.Root("true"))
	require.NoError(t, e.Zextras("/opt/zextras/bin/zmprov", "-r", "-m", "-l"))

	calls := fakes.Exec.Calls()
	require.Len(t, calls, 2)

	for _, c := range calls {
		assert.True(t, c.Setsid, "%s runs in its own session", c)
	}
}
