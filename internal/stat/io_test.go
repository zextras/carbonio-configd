// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	idle0    = "0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
	sdaBoot  = "1000 100 20000 5000 500 50 10000 2500 0 3000 8000 10 1 200 20 5 50"
	sdaLater = "1090 110 21800 5450 530 50 10600 2650 0 3600 9500 10 1 200 20 8 80"
)

func ioTree(sda, uptime string) files {
	return files{
		"sys/block/sda/stat": sda + "\n", "sys/block/sdb/stat": idle0 + "\n", "proc/uptime": uptime + " 99.00\n",
	}
}

// stat-io.pl getHeading (50-91) with "iostat -d -k": one group of columns per
// device iostat lists; devices that never did I/O are not listed.
func TestIOBasicGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, ioTree(sdaBoot, "1000.00"))
	c, err := newIO(te.Env, false)
	require.NoError(t, err)

	assert.Equal(t, "timestamp, sda:tps, sda:kB_read/s, sda:kB_wrtn/s, sda:kB_dscd/s, sda:kB_read, sda:kB_wrtn, sda:kB_dscd",
		c.Heading())

	// The first iostat report covers the time since boot (itv = uptime).
	assert.Equal(t, []string{stamp(0) + ", 1.51, 10.00, 5.00, 0.10, 10000, 5000, 100"}, te.sample(t, c, 0))

	te.setFiles(ioTree(sdaLater, "1030.00"))
	assert.Equal(t, []string{stamp(1) + ", 4.00, 30.00, 10.00, 0.00, 900, 300, 0"}, te.sample(t, c, 1))
}

func TestIOExtendedGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, ioTree(sdaBoot, "1000.00"))
	c, err := newIO(te.Env, true)
	require.NoError(t, err)

	cols := strings.Split(c.Heading(), ", ")
	require.Len(t, cols, 1+22)
	assert.Equal(t, []string{"timestamp", "sda:r/s", "sda:rkB/s", "sda:rrqm/s", "sda:%rrqm", "sda:r_await", "sda:rareq-sz"},
		cols[:7])
	assert.Equal(t, "sda:%util", cols[22])

	te.sample(t, c, 0)

	te.setFiles(ioTree(sdaLater, "1030.00"))
	assert.Equal(t, []string{stamp(1) + ", 3.00, 30.00, 0.33, 10.00, 5.00, 10.00, " +
		"1.00, 10.00, 0.00, 0.00, 5.00, 10.00, " +
		"0.00, 0.00, 0.00, 0.00, 0.00, 0.00, " +
		"0.10, 10.00, 0.05, 2.00"}, te.sample(t, c, 1))
}

func TestIOCannotListDevices(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	_, err := newIO(te.Env, false)

	require.ErrorContains(t, err, "unable to list /sys/block")
}

func TestIOSampleNeedsUptime(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, ioTree(sdaBoot, "1000.00"))
	c, err := newIO(te.Env, false)
	require.NoError(t, err)

	te.setFiles(files{"sys/block/sda/stat": sdaBoot})

	_, err = c.Sample(Tick{})
	require.ErrorContains(t, err, "/proc/uptime")

	te.setFiles(files{"sys/block/sda/stat": sdaBoot, "proc/uptime": "soon\n"})

	_, err = c.Sample(Tick{})
	require.ErrorContains(t, err, "bad /proc/uptime")
}

// iostat keeps its tick counters in 32 bits and repairs a sector counter that
// wrapped.
func TestSectorDelta(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ cur, prev, want uint64 }{
		"plain":       {cur: 30, prev: 10, want: 20},
		"32-bit wrap": {cur: 5, prev: 0xfffffffe, want: 7},
	} {
		assert.Equal(t, tc.want, sectorDelta(tc.cur, tc.prev), name)
	}
}

// A device that vanished and returned restarts from zero instead of
// reporting a huge negative delta.
func TestIOReturnedDeviceStartsFromZero(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, ioTree(sdaLater, "1030.00"))
	c, err := newIO(te.Env, false)
	require.NoError(t, err)

	te.sample(t, c, 0)

	te.setFiles(ioTree("10 0 100 0 0 0 0 0 0 0 0 0 0 0 0 0 0", "1060.00"))
	assert.Equal(t, []string{stamp(1) + ", 0.33, 1.67, 0.00, 0.00, 50, 0, 0"}, te.sample(t, c, 1))
}
