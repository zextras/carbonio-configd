// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestReadOnly_RefusesEveryMutationBeforeItReachesTheHost(t *testing.T) {
	t.Parallel()

	fakes := systest.New()
	fakes.FS.Put("/etc/live.conf", []byte("live"), 0o644)
	fakes.Proc.Add(sys.ProcEntry{PID: 42, Argv: []string{"nginx"}})

	ro := sys.ReadOnly(fakes.System())
	ctx := t.Context()

	_, execErr := ro.Exec.Run(ctx, &sys.Cmd{Path: "/opt/zextras/common/sbin/postconf", Args: []string{"-e", "k=secret"}})
	_, startErr := ro.Exec.Start(ctx, &sys.Cmd{Path: "/bin/true"})
	_, openErr := ro.FS.OpenFile("/etc/live.conf", os.O_WRONLY, 0)
	_, createErr := ro.FS.OpenFile("/etc/new.conf", os.O_RDONLY|os.O_CREATE, 0o644)
	_, tmpErr := ro.FS.CreateTemp("", "x-")

	errs := map[string]error{
		"exec run":    execErr,
		"exec start":  startErr,
		"open write":  openErr,
		"open create": createErr,
		"create temp": tmpErr,
		"atomic":      ro.FS.WriteFileAtomic("/etc/live.conf", []byte("x"), 0o644),
		"copy":        ro.FS.CopyFile(ctx, "/etc/live.conf", "/etc/copy.conf"),
		"rename":      ro.FS.Rename("/etc/live.conf", "/etc/moved.conf"),
		"remove":      ro.FS.Remove("/etc/live.conf"),
		"remove all":  ro.FS.RemoveAll("/etc"),
		"mkdir":       ro.FS.MkdirAll("/etc/d", 0o755),
		"chmod":       ro.FS.Chmod("/etc/live.conf", 0o600),
		"chown":       ro.FS.Chown("/etc/live.conf", 1, 1),
		"symlink":     ro.FS.Symlink("/etc/live.conf", "/etc/link"),
		"signal":      ro.Proc.Signal(42, syscall.SIGHUP),
		"signal grp":  ro.Proc.SignalGroup(42, syscall.SIGHUP),
	}

	for name, err := range errs {
		require.ErrorIs(t, err, sys.ErrReadOnly, name)
	}

	assert.Empty(t, fakes.Rec.Ops(), "nothing may reach the host")

	data, ok := fakes.FS.Data("/etc/live.conf")
	require.True(t, ok)
	assert.Equal(t, "live", string(data))
}

func TestReadOnly_ExecErrorDoesNotLeakArguments(t *testing.T) {
	t.Parallel()

	ro := sys.ReadOnly(systest.New().System())

	_, err := ro.Exec.Run(t.Context(), &sys.Cmd{Path: "/usr/bin/ldapmodify", Args: []string{"-w", "hunter2"}})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")
	assert.Contains(t, err.Error(), "/usr/bin/ldapmodify")
}

func TestReadOnly_ReadsPassThrough(t *testing.T) {
	t.Parallel()

	fakes := systest.New()
	fakes.FS.PutDir("/etc", 0o755)
	fakes.FS.Put("/etc/live.conf", []byte("live"), 0o644)
	fakes.Proc.Add(sys.ProcEntry{PID: 42, Argv: []string{"nginx"}})

	ro := sys.ReadOnly(fakes.System())

	data, err := ro.FS.ReadFile("/etc/live.conf")
	require.NoError(t, err)
	assert.Equal(t, "live", string(data))

	f, err := ro.FS.Open("/etc/live.conf")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	f, err = ro.FS.OpenFile("/etc/live.conf", os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	info, err := ro.FS.Stat("/etc/live.conf")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	entries, err := ro.FS.ReadDir("/etc")
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	_, ok := ro.Proc.Entry(42)
	assert.True(t, ok)
	assert.True(t, ro.Proc.Alive(42))

	assert.Equal(t, fakes.Clock.Now(), ro.Clock.Now())
}
