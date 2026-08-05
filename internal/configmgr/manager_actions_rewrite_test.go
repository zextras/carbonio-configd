// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

// TestRewriteWorker_HappyPath tests rewriteWorker with a successful rewrite
func TestRewriteWorker_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	err := os.WriteFile(srcPath, []byte("line1\nline2\nline3\n"), 0644)
	require.NoError(t, err)

	// Create a destination directory
	destDir := filepath.Join(tmpDir, "dest")
	err = os.MkdirAll(destDir, 0755)
	require.NoError(t, err)

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Create a rewrite entry
	entry := config.RewriteEntry{
		Value: "dest/output.txt",
		Mode:  "0644",
	}

	// Execute rewriteWorker
	err = cm.rewriteWorker(ctx, "source.txt", entry, 1, 1)
	require.NoError(t, err)

	// Verify destination file exists
	destPath := filepath.Join(tmpDir, "dest/output.txt")
	_, err = os.Stat(destPath)
	require.NoError(t, err)
}

// TestRewriteWorker_ErrorPath tests rewriteWorker when source file doesn't exist
func TestRewriteWorker_ErrorPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Create a rewrite entry pointing to non-existent source
	entry := config.RewriteEntry{
		Value: "dest/output.txt",
		Mode:  "0644",
	}

	// Execute rewriteWorker with non-existent source
	err := cm.rewriteWorker(ctx, "nonexistent.txt", entry, 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "open source file")
}

// TestRewriteWorker_DebugLogging tests rewriteWorker with debug logging enabled
func TestRewriteWorker_DebugLogging(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()

	// Create a source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	err := os.WriteFile(srcPath, []byte("test content\n"), 0644)
	require.NoError(t, err)

	// Create a destination directory
	destDir := filepath.Join(tmpDir, "dest")
	err = os.MkdirAll(destDir, 0755)
	require.NoError(t, err)

	// Setup logger with debug level
	buf := &bytes.Buffer{}
	logCfg := &logger.Config{
		Format:    logger.FormatText,
		Level:     slog.LevelDebug,
		Output:    buf,
		AddSource: false,
	}
	logger.InitStructuredLogging(logCfg)

	// Create context with debug logger
	debugLogger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := logger.ContextWithLogger(context.Background(), debugLogger)

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Create a rewrite entry
	entry := config.RewriteEntry{
		Value: "dest/output.txt",
		Mode:  "0644",
	}

	// Execute rewriteWorker
	err = cm.rewriteWorker(ctx, "source.txt", entry, 1, 1)
	require.NoError(t, err)

	// Verify debug logs were written
	output := buf.String()
	require.Contains(t, output, "Rewriting file")
	require.Contains(t, output, "Completed file rewrite")
}

// TestRewriteAtomicCommit_HappyPath tests successful atomic commit with default mode
func TestRewriteAtomicCommit_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a temp file
	tmpFile, err := os.CreateTemp(tmpDir, "tmp-")
	require.NoError(t, err)
	tmpFile.WriteString("test content\n")
	tmpFile.Close()

	destPath := filepath.Join(tmpDir, "dest.txt")

	// Execute rewriteAtomicCommit with empty mode (should default to 0644)
	err = rewriteAtomicCommit(ctx, tmpFile.Name(), destPath, "")
	require.NoError(t, err)

	// Verify destination file exists
	stat, err := os.Stat(destPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), stat.Mode().Perm())

	// Verify temp file is gone
	_, err = os.Stat(tmpFile.Name())
	require.True(t, os.IsNotExist(err))
}

// TestRewriteAtomicCommit_InvalidMode tests rewriteAtomicCommit with invalid mode string
func TestRewriteAtomicCommit_InvalidMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a temp file
	tmpFile, err := os.CreateTemp(tmpDir, "tmp-")
	require.NoError(t, err)
	tmpFile.WriteString("test content\n")
	tmpFile.Close()

	destPath := filepath.Join(tmpDir, "dest.txt")

	// Execute rewriteAtomicCommit with invalid mode
	err = rewriteAtomicCommit(ctx, tmpFile.Name(), destPath, "not-octal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid file mode")
}

// TestRewriteAtomicCommit_CustomMode tests rewriteAtomicCommit with custom octal mode
func TestRewriteAtomicCommit_CustomMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a temp file
	tmpFile, err := os.CreateTemp(tmpDir, "tmp-")
	require.NoError(t, err)
	tmpFile.WriteString("test content\n")
	tmpFile.Close()

	destPath := filepath.Join(tmpDir, "dest.txt")

	// Execute rewriteAtomicCommit with custom mode
	err = rewriteAtomicCommit(ctx, tmpFile.Name(), destPath, "0755")
	require.NoError(t, err)

	// Verify destination file has correct mode
	stat, err := os.Stat(destPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), stat.Mode().Perm())
}

// TestRewriteTransform tests rewriteTransform with a source file
func TestRewriteTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a source file with multiple lines
	srcPath := filepath.Join(tmpDir, "source.txt")
	srcContent := "line1\nline2\nline3\n"
	err := os.WriteFile(srcPath, []byte(srcContent), 0644)
	require.NoError(t, err)

	// Create a temp destination file
	tmpFile, err := os.CreateTemp(tmpDir, "tmp-")
	require.NoError(t, err)
	defer tmpFile.Close()

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Execute rewriteTransform
	lineCount, err := cm.rewriteTransform(ctx, srcPath, tmpFile)
	require.NoError(t, err)
	require.Equal(t, 3, lineCount)

	// Verify temp file content
	tmpFile.Seek(0, 0)
	content := make([]byte, 1024)
	n, err := tmpFile.Read(content)
	require.NoError(t, err)
	require.Equal(t, srcContent, string(content[:n]))
}

// TestRewriteTransform_NonexistentSource tests rewriteTransform with non-existent source
func TestRewriteTransform_NonexistentSource(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a temp destination file
	tmpFile, err := os.CreateTemp(tmpDir, "tmp-")
	require.NoError(t, err)
	defer tmpFile.Close()

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Execute rewriteTransform with non-existent source
	_, err = cm.rewriteTransform(ctx, filepath.Join(tmpDir, "nonexistent.txt"), tmpFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "open source file")
}


// TestResolveValueSpec_FILE is the CO-4096 regression suite: FILE fragments must be
// transformer-expanded before reaching postconf. Before the fix, FILE was delegated
// to mtaops.ResolveValue which returned the raw path as a literal, so %%...%% directives
// appeared verbatim in main.cf and postfix refused to start.
func TestResolveValueSpec_FILE(t *testing.T) {
	ctx := context.Background()

	// smtpd_sender_login_maps.cf fixture content from CO-4096 batch context
	const senderLoginMapsContent = "" +
		"%%contains VAR:zimbraMtaSmtpdSenderLoginMaps lmdb:/opt/zextras/conf/slm-exceptions-db%%\n" +
		"%%contains VAR:zimbraMtaSmtpdSenderLoginMaps proxy:ldap:/opt/zextras/conf/ldap-slm.cf%%\n"

	setup := func(t *testing.T, fragName, fragContent string) (*ConfigManager, func(string, string) (string, error)) {
		t.Helper()
		tmpDir := t.TempDir()
		fragDir := filepath.Join(tmpDir, "conf", "zmconfigd")
		require.NoError(t, os.MkdirAll(fragDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(fragDir, fragName), []byte(fragContent), 0644))
		st := state.NewState()
		cfg := &config.Config{BaseDir: tmpDir}
		cm := NewConfigManager(ctx, cfg, st, nil, cache.New(ctx, false))
		resolve := func(key, valueSpec string) (string, error) {
			return cm.resolveValueSpec(ctx, key, valueSpec)
		}
		return cm, resolve
	}

	t.Run("contains directives fully expanded - regression guard no %%", func(t *testing.T) {
		cm, resolve := setup(t, "smtpd_sender_login_maps.cf", senderLoginMapsContent)
		// Seed the lookup value so both %%contains%% lines hit.
		cm.State.GlobalConfig.Data.Set("zimbraMtaSmtpdSenderLoginMaps",
			"lmdb:/opt/zextras/conf/slm-exceptions-db proxy:ldap:/opt/zextras/conf/ldap-slm.cf")

		result, err := resolve("smtpd_sender_login_maps", "FILE zmconfigd/smtpd_sender_login_maps.cf")
		require.NoError(t, err)

		// Primary regression guard: no unexpanded directive markers must survive.
		require.NotContains(t, result, "%%",
			"FILE fragment must be fully expanded before reaching postconf (CO-4096 regression)")
		// Both lines hit → comma-space-joined concrete values.
		require.Equal(t,
			"lmdb:/opt/zextras/conf/slm-exceptions-db, proxy:ldap:/opt/zextras/conf/ldap-slm.cf",
			result)
	})

	t.Run("bare variable expanded", func(t *testing.T) {
		const contentFilterContent = "smtp-amavis:[%%zimbraLocalBindAddress%%]:10024\n"
		cm, resolve := setup(t, "postfix_content_filter.cf", contentFilterContent)
		cm.State.GlobalConfig.Data.Set("zimbraLocalBindAddress", "127.0.0.1")

		result, err := resolve("content_filter", "FILE zmconfigd/postfix_content_filter.cf")
		require.NoError(t, err)
		require.Equal(t, "smtp-amavis:[127.0.0.1]:10024", result)
		require.NotContains(t, result, "%%")
	})

	t.Run("contains miss - empty lines dropped from join", func(t *testing.T) {
		cm, resolve := setup(t, "smtpd_sender_login_maps.cf", senderLoginMapsContent)
		// VAR value does not contain either search string → both lines resolve to "".
		cm.State.GlobalConfig.Data.Set("zimbraMtaSmtpdSenderLoginMaps", "ldap:/some/other/map")

		result, err := resolve("smtpd_sender_login_maps", "FILE zmconfigd/smtpd_sender_login_maps.cf")
		require.NoError(t, err)
		// Legacy behaviour: empty expanded lines are dropped; joined result must be empty.
		require.Empty(t, result, "contains-miss lines must be dropped, not joined as empty entries")
	})
}