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

const (
	testConfDir  = "/conf"
	testConfPath = testConfDir + "/nginx.conf"
	testConfBody = "worker_processes 4;\n"
)

// newFakeConfGenerator returns a generator whose rendered nginx.conf is body,
// as ProcessTemplateWithProcessor records it, over the fake host.
func newFakeConfGenerator(body string) (*Generator, *systest.Fakes) {
	g, f := newFakeWriteGenerator()
	g.ConfDir = testConfDir
	g.confBody = []byte(body)

	return g, f
}

// writeConfAndMarker is what one GenerateAll run does to nginx.conf: write the
// rendered content (skipped when unchanged), then record the result marker.
func writeConfAndMarker(t *testing.T, g *Generator, body, marker string) {
	t.Helper()

	require.NoError(t, g.writeFile(context.Background(), testConfPath, []byte(body)))
	g.appendConfGenResult(context.Background(), marker)
}

func TestConfUnchangedWithSameMarkerIsNotTouched(t *testing.T) {
	t.Parallel()

	g, f := newFakeConfGenerator(testConfBody)
	f.FS.Put(testConfPath, []byte(testConfBody+"\n#"+confGenSuccessMarker+"\n"), generatedFileMode)
	f.Rec.Reset()

	writeConfAndMarker(t, g, testConfBody, confGenSuccessMarker)

	assert.Empty(t, f.Rec.Strings(), "neither the write nor the marker append may happen")

	data, _ := f.FS.Data(testConfPath)
	assert.Equal(t, testConfBody+"\n#"+confGenSuccessMarker+"\n", string(data))
}

func TestConfUnchangedWithOtherMarkerGetsTheNewMarker(t *testing.T) {
	t.Parallel()

	g, f := newFakeConfGenerator(testConfBody)
	f.FS.Put(testConfPath, []byte(testConfBody+"\n#"+confGenErrorMarker+"boom\n"), generatedFileMode)
	f.Rec.Reset()

	writeConfAndMarker(t, g, testConfBody, confGenSuccessMarker)

	data, _ := f.FS.Data(testConfPath)
	assert.Equal(t, testConfBody+"\n#"+confGenSuccessMarker+"\n", string(data),
		"the file must end with the current result marker only")
	assert.Equal(t, []string{"fs.write-atomic " + testConfPath + " mode=0640"}, f.Rec.Strings())
}

func TestConfChangedIsRewrittenWithNewMarker(t *testing.T) {
	t.Parallel()

	const newBody = "worker_processes 8;\n"

	g, f := newFakeConfGenerator(newBody)
	f.FS.Put(testConfPath, []byte(testConfBody+"\n#"+confGenSuccessMarker+"\n"), generatedFileMode)
	f.Rec.Reset()

	writeConfAndMarker(t, g, newBody, confGenSuccessMarker)

	data, _ := f.FS.Data(testConfPath)
	assert.Equal(t, newBody+"\n#"+confGenSuccessMarker+"\n", string(data))
	assert.Contains(t, f.Rec.Strings(), "fs.write-atomic "+testConfPath+" mode=0640")
}

func TestConfMissingIsWrittenWithMarker(t *testing.T) {
	t.Parallel()

	g, f := newFakeConfGenerator(testConfBody)

	require.NoError(t, g.writeFile(context.Background(), testConfPath, []byte(testConfBody)))
	g.appendConfGenResult(context.Background(), confGenSuccessMarker)

	data, ok := f.FS.Data(testConfPath)
	require.True(t, ok)
	assert.Equal(t, testConfBody+"\n#"+confGenSuccessMarker+"\n", string(data))
}

// Only the main nginx.conf carries a marker: an include with trailing marker
// text is a different file and must be rewritten.
func TestIncludeWithMarkerLookalikeIsRewritten(t *testing.T) {
	t.Parallel()

	const path = "/conf/includes/a.conf"

	g, f := newFakeConfGenerator(testConfBody)
	f.FS.Put(path, []byte("same\n#"+confGenSuccessMarker+"\n"), generatedFileMode)
	f.Rec.Reset()

	require.NoError(t, g.writeFile(context.Background(), path, []byte("same")))
	assert.Equal(t, []string{"fs.write-atomic " + path + " mode=0640"}, f.Rec.Strings())
}
