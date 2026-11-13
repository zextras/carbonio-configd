// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func newFakeWriteGenerator() (*Generator, *systest.Fakes) {
	f := systest.New()

	return &Generator{env: f.System(), Config: &config.Config{BaseDir: "/opt/zextras"}}, f
}

func TestWriteFileSkipsUnchanged(t *testing.T) {
	t.Parallel()

	const path = "/inc/a.conf"

	g, f := newFakeWriteGenerator()
	f.FS.Put(path, []byte("same"), generatedFileMode)
	f.Rec.Reset()

	require.NoError(t, g.writeFile(context.Background(), path, []byte("same")))
	assert.Empty(t, f.Rec.Strings(), "identical file must not be rewritten")
}

func TestWriteFileRewritesDifferentContent(t *testing.T) {
	t.Parallel()

	const path = "/inc/a.conf"

	g, f := newFakeWriteGenerator()
	f.FS.Put(path, []byte("old"), generatedFileMode)
	f.Rec.Reset()

	require.NoError(t, g.writeFile(context.Background(), path, []byte("new")))
	assert.Equal(t, []string{"fs.write-atomic " + path + " mode=0640"}, f.Rec.Strings())

	data, _ := f.FS.Data(path)
	assert.Equal(t, "new", string(data))
}

func TestWriteFileFixesWrongMode(t *testing.T) {
	t.Parallel()

	const path = "/inc/a.conf"

	g, f := newFakeWriteGenerator()
	f.FS.Put(path, []byte("same"), 0o644)
	f.Rec.Reset()

	require.NoError(t, g.writeFile(context.Background(), path, []byte("same")))
	assert.Equal(t, []string{"fs.write-atomic " + path + " mode=0640"}, f.Rec.Strings())

	mode, _ := f.FS.Mode(path)
	assert.Equal(t, generatedFileMode, mode.Perm())
}

func TestWriteFileReplacesSymlink(t *testing.T) {
	t.Parallel()

	const path = "/inc/a.conf"

	g, f := newFakeWriteGenerator()
	f.FS.Put("/elsewhere", []byte("same"), generatedFileMode)
	f.FS.PutLink(path, "/elsewhere")
	f.Rec.Reset()

	require.NoError(t, g.writeFile(context.Background(), path, []byte("same")))
	assert.Equal(t, []string{"fs.write-atomic " + path + " mode=0640"}, f.Rec.Strings())

	mode, _ := f.FS.Mode(path)
	assert.True(t, mode.IsRegular(), "symlink must be replaced by a regular file")
}
