// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// faultTempFS hands out temp files whose Sync or Close fail.
type faultTempFS struct {
	sys.FS

	syncErr, closeErr error
}

func (f faultTempFS) CreateTemp(dir, pattern string) (sys.File, error) {
	file, err := f.FS.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err //nolint:wrapcheck // test double
	}

	return faultTempFile{File: file, syncErr: f.syncErr, closeErr: f.closeErr}, nil
}

type faultTempFile struct {
	sys.File

	syncErr, closeErr error
}

func (f faultTempFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}

	return f.File.Sync() //nolint:wrapcheck // test double
}

func (f faultTempFile) Close() error {
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}

	return err //nolint:wrapcheck // test double
}

// A temp file that fails to sync or close was not written completely: the
// rewrite fails and the old target stays, instead of publishing the file.
func TestProcessRewrite_SyncOrCloseFailureKeepsTheTarget(t *testing.T) {
	for name, fs := range map[string]faultTempFS{
		"sync":  {syncErr: errors.New("EIO")},
		"close": {closeErr: errors.New("EDQUOT")},
	} {
		t.Run(name, func(t *testing.T) {
			cm, _ := newTestCMWithExecutor(t)
			baseDir := cm.mainConfig.BaseDir

			require.NoError(t, os.WriteFile(filepath.Join(baseDir, "cfg.cf.in"), []byte("new\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(baseDir, "cfg.cf"), []byte("old\n"), 0o440))

			fs.FS = cm.env.FS
			cm.env.FS = fs

			err := cm.processRewrite(t.Context(), "cfg.cf.in", config.RewriteEntry{Value: "cfg.cf", Mode: "0440"})
			require.Error(t, err)
			assert.ErrorContains(t, err, name)

			got, readErr := os.ReadFile(filepath.Join(baseDir, "cfg.cf"))
			require.NoError(t, readErr)
			assert.Equal(t, "old\n", string(got), "the previous target is untouched")
		})
	}
}

// A REWRITE path that climbs out of the base directory is refused before
// anything is read or written.
func TestProcessRewrite_RefusesPathsOutsideBaseDir(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir
	outside := filepath.Join(filepath.Dir(baseDir), filepath.Base(baseDir)+"-outside")

	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "cfg.cf.in"), []byte("data\n"), 0o644))
	t.Cleanup(func() { _ = os.Remove(outside) })

	for name, tc := range map[string]struct {
		source string
		entry  config.RewriteEntry
	}{
		"target escapes":   {"cfg.cf.in", config.RewriteEntry{Value: "../" + filepath.Base(outside)}},
		"target absolute":  {"cfg.cf.in", config.RewriteEntry{Value: outside}},
		"source escapes":   {"../etc/passwd", config.RewriteEntry{Value: "cfg.cf"}},
		"source absolute":  {"/etc/passwd", config.RewriteEntry{Value: "cfg.cf"}},
		"target dot-dot":   {"cfg.cf.in", config.RewriteEntry{Value: "conf/../../x"}},
		"target empty":     {"cfg.cf.in", config.RewriteEntry{Value: ""}},
		"source dot-dot":   {"conf/../../x.in", config.RewriteEntry{Value: "cfg.cf"}},
		"target base only": {"cfg.cf.in", config.RewriteEntry{Value: ".."}},
	} {
		t.Run(name, func(t *testing.T) {
			err := cm.processRewrite(t.Context(), tc.source, tc.entry)
			require.ErrorIs(t, err, ErrPathEscapesBaseDir)
		})
	}

	_, err := os.Stat(outside)
	assert.True(t, os.IsNotExist(err), "nothing is written outside the base directory")
}
