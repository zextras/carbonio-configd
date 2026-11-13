// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package statcmd

import (
	"bytes"
	"context"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/stat"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// "stat run" is the long-running process of all the collectors: its
// garbage collection target is the lower one of the collectors; a single
// collector keeps the runtime's.
func TestRunTunesTheMemoryOfTheProcessThatRunsCollectors(t *testing.T) { //nolint:paralleltest // the GC target is the process's
	orig := runStat

	defer func() { runStat = orig }()

	var got []string

	runStat = func(_ context.Context, _ *stat.Env, args []string) int {
		got = args

		return 0
	}

	prev := debug.SetGCPercent(100)

	defer debug.SetGCPercent(prev)

	run := func(args ...string) int {
		debug.SetGCPercent(100)
		assert.Zero(t, Run(context.Background(), systest.New().System(), &bytes.Buffer{}, &bytes.Buffer{}, args))

		return debug.SetGCPercent(100)
	}

	assert.Equal(t, 25, run("run", "cpu", "fd"))
	assert.Equal(t, []string{"run", "cpu", "fd"}, got)
	assert.Equal(t, 100, run("cpu"))
	assert.Equal(t, 100, run())
}
