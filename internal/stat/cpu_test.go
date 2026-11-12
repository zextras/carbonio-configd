// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cpuStatBoot = "cpu  100 10 50 800 20 5 15 0 0 0\n" +
	"cpu0 60 5 30 400 10 3 7 0\n" +
	"cpu1 40 5 20 400 10 2 8 0\n" +
	"intr 1\n"

// stat-cpu.pl getHeading (lines 42-54) and getCpuStat (79-105): the first line
// is the share of each counter since boot, the next the share of the interval.
func TestCPUGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/stat": cpuStatBoot})
	c, err := newCPU(te.Env)
	require.NoError(t, err)

	assert.Equal(t, "timestamp, "+
		"cpu:user, cpu:nice, cpu:sys, cpu:idle, cpu:iowait, cpu:irq, cpu:softirq, "+
		"cpu0:user, cpu0:nice, cpu0:sys, cpu0:idle, cpu0:iowait, cpu0:irq, cpu0:softirq, "+
		"cpu1:user, cpu1:nice, cpu1:sys, cpu1:idle, cpu1:iowait, cpu1:irq, cpu1:softirq", c.Heading())

	assert.Equal(t, []string{stamp(0) + ", 10.0, 1.0, 5.0, 80.0, 2.0, 0.5, 1.5, " +
		"11.7, 1.0, 5.8, 77.7, 1.9, 0.6, 1.4, 8.2, 1.0, 4.1, 82.5, 2.1, 0.4, 1.6"}, te.sample(t, c, 0))

	te.setFiles(files{"proc/stat": "cpu  150 10 50 950 20 5 15 0 0 0\n" +
		"cpu0 85 5 30 475 10 3 7 0\n" +
		"cpu1 65 5 20 475 10 2 8 0\n"})

	assert.Equal(t, []string{stamp(1) + ", 25.0, 0.0, 0.0, 75.0, 0.0, 0.0, 0.0, " +
		"25.0, 0.0, 0.0, 75.0, 0.0, 0.0, 0.0, 25.0, 0.0, 0.0, 75.0, 0.0, 0.0, 0.0"}, te.sample(t, c, 1))
}

// percent (Stat.pm): 0.0 when the total did not move.
func TestCPUIdleSystemReportsZero(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/stat": "cpu  1 1 1 1 1 1 1 0\ncpu0 1 1 1 1 1 1 1 0\n"})
	c, err := newCPU(te.Env)
	require.NoError(t, err)

	te.sample(t, c, 0)

	assert.Equal(t, []string{stamp(1) + ", 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0"},
		te.sample(t, c, 1))
}

// stat-cpu.pl init (59): no /proc/stat is fatal.
func TestCPUMissingProcStat(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	_, err := newCPU(te.Env)

	require.ErrorContains(t, err, "can't open /proc/stat")
}

func TestCPUSampleFailsWhenProcStatVanishes(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/stat": cpuStatBoot})
	c, err := newCPU(te.Env)
	require.NoError(t, err)

	te.setFiles(files{})

	_, err = c.Sample(Tick{})
	require.Error(t, err)
}
