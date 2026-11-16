// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file that zextras swapped for a link is replaced, and what the link
// pointed to is neither written nor given away.
func TestWriteOwned_ReplacesALinkInsteadOfFollowingIt(t *testing.T) {
	e, fakes, _ := testEnv(t)
	fakes.FS.PutDir("/opt/zextras/conf", 0o755)
	fakes.FS.Put("/etc/shadow", []byte("root:x\n"), 0o600)
	fakes.FS.PutLink("/opt/zextras/conf/carbonio.ldif", "/etc/shadow")

	require.NoError(t, writeOwned(e, "/opt/zextras/conf/carbonio.ldif", []byte("new"), 0o640, 998, 997))

	data, ok := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
	require.True(t, ok)
	assert.Equal(t, "new", string(data))

	shadow, _ := fakes.FS.Data("/etc/shadow")
	assert.Equal(t, "root:x\n", string(shadow), "the target of the link is untouched")

	uid, gid, _ := fakes.FS.Owner("/etc/shadow")
	assert.NotEqual(t, [2]int{998, 997}, [2]int{uid, gid}, "and not given to zextras")

	uid, gid, _ = fakes.FS.Owner("/opt/zextras/conf/carbonio.ldif")
	assert.Equal(t, [2]int{998, 997}, [2]int{uid, gid})
	assert.Empty(t, fakes.Rec.Of("fs.chown"), "no chown by path")
}

func TestWriteOwned_AsZextrasLeavesTheOwnerAlone(t *testing.T) {
	e, fakes, _ := testEnv(t)
	e.IsZextras = true

	uid, gid, err := zextrasOwner(e)
	require.NoError(t, err)
	require.Equal(t, [2]int{-1, -1}, [2]int{uid, gid})

	fakes.FS.PutDir("/opt/zextras/conf", 0o755)
	require.NoError(t, writeOwned(e, "/opt/zextras/conf/f", []byte("x"), 0o600, uid, gid))
	assert.Empty(t, fakes.Rec.Of("fs.fchown"))
}

func TestOwnDir_RefusesALink(t *testing.T) {
	e, fakes, _ := testEnv(t)
	fakes.FS.PutDir("/etc", 0o755)
	fakes.FS.PutLink("/opt/zextras/.ssh", "/etc")

	err := ownDir(e, "/opt/zextras/.ssh", 0o700, 998, 997)
	require.ErrorIs(t, err, syscall.ELOOP)

	mode, _ := fakes.FS.Mode("/etc")
	assert.NotEqual(t, "drwx------", mode.String(), "the target of the link keeps its mode")
}
