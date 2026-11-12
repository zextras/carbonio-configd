// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	vmMeminfo = "MemTotal:       16000000 kB\nMemFree:         4000000 kB\nBuffers:          100000 kB\n" +
		"Cached:          2000000 kB\nSwapTotal:       2000000 kB\nSwapFree:        1500000 kB\n" +
		"SReclaimable:     300000 kB\nHugePages_Total:       0\n"
	vmStat1 = "cpu  1000 0 500 8000 100 0 0 0 0 0\nintr 100000 1 2\nctxt 200000\n" +
		"procs_running 1\nprocs_blocked 0\n"
	vmStat2 = "cpu  1300 0 600 8900 200 10 20 0 0 0\nintr 103000 1 2\nctxt 206000\n" +
		"procs_running 3\nprocs_blocked 1\n"
	vmCounters1 = "pgpgin 1000\npgpgout 2000\npswpin 10\npswpout 20\nnr_free_pages 1\n"
	vmCounters2 = "pgpgin 1600\npgpgout 3500\npswpin 40\npswpout 80\nnr_free_pages 1\n"
)

// stat-vm.pl getHeading (47-65), getMeminfo (67-81), getLoadInfo (83-91):
// vmstat's columns, the /proc/meminfo keys and loadavg; the line of vmstat
// that covers the time since boot is dropped (runVMSTAT lines 103-105).
func TestVMGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{
		"proc/stat": vmStat1, "proc/vmstat": vmCounters1, "proc/meminfo": vmMeminfo, "proc/loadavg": "0.52 0.40 0.30 1/200 99\n",
	})
	c, err := newVM(te.Env, opts30())
	require.NoError(t, err)

	assert.Equal(t, "timestamp, r, b, swpd, free, buff, cache, si, so, bi, bo, in, cs, us, sy, id, wa, st, "+
		"MemTotal, MemFree, Buffers, Cached, SwapTotal, SwapFree, SReclaimable, HugePages_Total, loadavg", c.Heading())

	assert.Empty(t, te.sample(t, c, 0), "the first reading only sets the reference")

	te.setFiles(files{
		"proc/stat": vmStat2, "proc/vmstat": vmCounters2, "proc/meminfo": vmMeminfo, "proc/loadavg": "0.52 0.40 0.30 1/200 99\n",
	})

	assert.Equal(t, []string{stamp(1) + ", 2, 1, 500000, 4000000, 100000, 2300000, 4, 8, 20, 50, 100, 200, " +
		"21, 9, 63, 7, 0, 16000000, 4000000, 100000, 2000000, 2000000, 1500000, 300000, 0, 0.52"}, te.sample(t, c, 1))
}

// vmstat.c: a window without any tick reports 100% idle, and idle ticks that
// ran backwards are charged to the next interval.
func TestVMCPUShareEdgeCases(t *testing.T) {
	t.Parallel()

	tree := func(user, idle string) files {
		return files{
			"proc/stat":   "cpu  " + user + " 0 10 " + idle + " 0 0 0 0\nintr 1\nctxt 1\nprocs_running 1\nprocs_blocked 0\n",
			"proc/vmstat": "pgpgin 0\n", "proc/meminfo": "MemTotal: 1 kB\n", "proc/loadavg": "0.00 0 0 1/1 1\n",
		}
	}
	row := func(us, sy, id string) string {
		return ", 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, " + us + ", " + sy + ", " + id + ", 0, 0, 1, 0.00"
	}

	te := newTestEnv(t, tree("10", "100"))
	c, err := newVM(te.Env, opts30())
	require.NoError(t, err)

	te.sample(t, c, 0)

	te.setFiles(tree("10", "100"))
	assert.Equal(t, []string{stamp(1) + row("0", "0", "100")}, te.sample(t, c, 1), "nothing moved")

	te.setFiles(tree("10", "90"))
	assert.Equal(t, []string{stamp(2) + row("0", "0", "100")}, te.sample(t, c, 2), "idle went backwards by 10")

	te.setFiles(tree("20", "110"))
	assert.Equal(t, []string{stamp(3) + row("50", "0", "50")}, te.sample(t, c, 3), "10 user and 20-10 idle ticks")
}

func TestVMMissingFiles(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{"proc/meminfo", "proc/stat", "proc/vmstat", "proc/loadavg"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()

			tree := files{
				"proc/stat": vmStat1, "proc/vmstat": vmCounters1, "proc/meminfo": vmMeminfo, "proc/loadavg": "0.5 0 0 1/1 1\n",
			}
			te := newTestEnv(t, tree)
			c, err := newVM(te.Env, opts30())
			require.NoError(t, err)

			te.sample(t, c, 0)

			delete(tree, missing)
			te.setFiles(tree)

			_, err = c.Sample(Tick{})
			require.ErrorContains(t, err, "can't open")
		})
	}
}
