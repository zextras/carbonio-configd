// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dfMountinfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 8:1 /var /var/bind rw - ext4 /dev/sda1 rw
24 22 8:2 / /opt/data rw - xfs /dev/sdb1 rw
25 22 0:20 / /run rw - tmpfs tmpfs rw
26 22 7:0 / /snap/core rw - squashfs /dev/loop0 ro
27 22 8:3 / /mnt/with\040space rw - ext4 /dev/sdc1 rw
28 22 8:4 / /mnt/empty rw - ext4 /dev/sdd1 rw
`

func dfStatfs(path string) (Statfs, error) {
	switch path {
	case "/", "/var/bind":
		return Statfs{Blocks: 1000, Bfree: 200, Bavail: 150, Bsize: 4096}, nil
	case "/opt/data":
		return Statfs{Blocks: 100, Bfree: 3, Bavail: 3, Bsize: 1024}, nil
	case "/mnt/empty":
		return Statfs{Blocks: 10, Bfree: 10, Bavail: 0, Bsize: 4096}, nil
	}

	return Statfs{}, errors.New("no such mount")
}

// stat-df.pl: "df -k -x squashfs -x udev -x devtmpfs -x overlay -x efivarfs"
// rows of the mounts that sit on a device (source starts with /): the size
// and use in KiB, the used percentage rounded up, one row per device.
func TestDFGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/self/mountinfo": dfMountinfo})
	te.Statfs = dfStatfs
	c := newDF(te.Env)

	assert.Equal(t, "timestamp, path, disk, disk_use, disk_space, disk_pct_used", c.Heading())
	assert.Equal(t, []string{
		stamp(0) + ", /, /dev/sda1, 3200, 4000, 85",
		stamp(0) + ", /opt/data, /dev/sdb1, 97, 100, 97",
	}, te.sample(t, c, 0))
}

// zmdisklog_warn_threshold (85) and zmdisklog_critical_threshold (95) turn a
// full file system into an err or crit message of the local0 facility; one in
// zmstat_df_excludes (by path or device) is only noted.
func TestDFWarnings(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		lc   map[string]string
		want []string
	}{
		"defaults": {want: []string{
			"err Disk warning: mx1: / on device /dev/sda1 at 85%",
			"crit Disk warning: mx1: /opt/data on device /dev/sdb1 at 97%",
		}},
		"excluded by path": {
			lc: map[string]string{"zmstat_df_excludes": "/opt/data:/other"},
			want: []string{
				"err Disk warning: mx1: / on device /dev/sda1 at 85%",
				"info Disk warning: mx1: /opt/data on device /dev/sdb1 at 97%",
			},
		},
		"excluded by device, custom thresholds": {
			lc: map[string]string{"zmstat_df_excludes": "/dev/sda1", "zmdisklog_warn_threshold": "90",
				"zmdisklog_critical_threshold": "99"},
			want: []string{"err Disk warning: mx1: /opt/data on device /dev/sdb1 at 97%"},
		},
	} {
		te := newTestEnv(t, files{"proc/self/mountinfo": dfMountinfo})
		te.Statfs = dfStatfs
		te.LC = map[string]string{"zimbra_server_hostname": "mx1"}

		for k, v := range tc.lc {
			te.LC[k] = v
		}

		rec := &recSyslog{}
		te.Syslog = rec

		te.sample(t, newDF(te.Env), 0)
		assert.Equal(t, tc.want, rec.log, name)
	}
}

func TestDFWithoutMountTable(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	_, err := newDF(te.Env).Sample(Tick{})

	require.ErrorContains(t, err, "mountinfo")
}

func TestUnescapeMount(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "/mnt/with space", unescapeMount(`/mnt/with\040space`))
	assert.Equal(t, `/a\b`, unescapeMount(`/a\b`))
	assert.Equal(t, "/tab\t", unescapeMount(`/tab\011`))
}
