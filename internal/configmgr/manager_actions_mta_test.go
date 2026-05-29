// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/state"
)

// mockMtaExecutorForMta is a controllable mock for mtaops.Executor used in mapfile tests
type mockMtaExecutorForMta struct {
	mapfileOps []mtaops.MapfileOperation
	mapfileErr error
}

func (m *mockMtaExecutorForMta) ExecutePostconf(_ context.Context, op mtaops.PostconfOperation) error {
	return nil
}

func (m *mockMtaExecutorForMta) ExecutePostconfBatch(_ context.Context, ops []mtaops.PostconfOperation) error {
	return nil
}

func (m *mockMtaExecutorForMta) ExecutePostconfd(_ context.Context, op mtaops.PostconfdOperation) error {
	return nil
}

func (m *mockMtaExecutorForMta) ExecutePostconfdBatch(_ context.Context, ops []mtaops.PostconfdOperation) error {
	return nil
}

func (m *mockMtaExecutorForMta) ExecuteMapfile(_ context.Context, op mtaops.MapfileOperation) error {
	m.mapfileOps = append(m.mapfileOps, op)
	return m.mapfileErr
}

func (m *mockMtaExecutorForMta) ExecuteLdapWrite(_ context.Context, op mtaops.LdapOperation) error {
	return nil
}

// TestDoMapfileSection_HappyPath tests doMapfileSection with successful mapfile operations
func TestDoMapfileSection_HappyPath(t *testing.T) {
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

	// Setup mock executor
	mockExec := &mockMtaExecutorForMta{}
	cm.mtaExecutor = mockExec

	// Create a section with MAPFILE and MAPLOCAL variables
	section := &config.MtaConfigSection{
		Name:    "test_section",
		Changed: true,
		RequiredVars: map[string]string{
			"zimbraSSLDHParam":    "MAPFILE",
			"zimbraSSLCertPath":   "MAPLOCAL",
			"zimbraMtaMyNetworks": "VAR", // Should be skipped
		},
	}

	// Execute doMapfileSection
	errs := cm.doMapfileSection(ctx, section)

	// Verify no errors
	require.Empty(t, errs)

	// Verify mapfile operations were executed
	require.Len(t, mockExec.mapfileOps, 2)

	// Check that both MAPFILE and MAPLOCAL operations were recorded
	// Note: map iteration order is not guaranteed, so check both operations exist
	var foundMapfile, foundMaplocal bool
	for _, op := range mockExec.mapfileOps {
		if op.Key == "zimbraSSLDHParam" && !op.IsLocal {
			foundMapfile = true
		}
		if op.Key == "zimbraSSLCertPath" && op.IsLocal {
			foundMaplocal = true
		}
	}
	require.True(t, foundMapfile, "Expected MAPFILE operation for zimbraSSLDHParam")
	require.True(t, foundMaplocal, "Expected MAPLOCAL operation for zimbraSSLCertPath")
}

// TestDoMapfileSection_ErrorPath tests doMapfileSection when executor returns errors
func TestDoMapfileSection_ErrorPath(t *testing.T) {
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

	// Setup mock executor that returns an error
	mockExec := &mockMtaExecutorForMta{
		mapfileErr: errors.New("mapfile execution failed"),
	}
	cm.mtaExecutor = mockExec

	// Create a section with MAPFILE variables
	section := &config.MtaConfigSection{
		Name:    "test_section",
		Changed: true,
		RequiredVars: map[string]string{
			"zimbraSSLDHParam": "MAPFILE",
		},
	}

	// Execute doMapfileSection
	errs := cm.doMapfileSection(ctx, section)

	// Verify error was collected
	require.Len(t, errs, 1)
	require.Equal(t, "mapfile execution failed", errs[0].Error())
}

// TestDoMapfileSection_NoChanges tests doMapfileSection when section hasn't changed and not first run
func TestDoMapfileSection_NoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = false // Not first run
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Setup mock executor
	mockExec := &mockMtaExecutorForMta{}
	cm.mtaExecutor = mockExec

	// Create a section that hasn't changed
	section := &config.MtaConfigSection{
		Name:    "test_section",
		Changed: false, // Not changed
		RequiredVars: map[string]string{
			"zimbraSSLDHParam": "MAPFILE",
		},
	}

	// Execute doMapfileSection
	errs := cm.doMapfileSection(ctx, section)

	// Verify no operations were executed
	require.Empty(t, errs)
	require.Empty(t, mockExec.mapfileOps)
}

// TestDoMapfileSection_FirstRunNoChanges tests doMapfileSection on first run even if section hasn't changed
func TestDoMapfileSection_FirstRunNoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup ConfigManager
	st := state.NewState()
	st.FirstRun = true // First run
	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cacheInstance := cache.New(ctx, false)
	cm := NewConfigManager(ctx, cfg, st, nil, cacheInstance)

	// Setup mock executor
	mockExec := &mockMtaExecutorForMta{}
	cm.mtaExecutor = mockExec

	// Create a section that hasn't changed but it's first run
	section := &config.MtaConfigSection{
		Name:    "test_section",
		Changed: false, // Not changed
		RequiredVars: map[string]string{
			"zimbraSSLDHParam": "MAPFILE",
		},
	}

	// Execute doMapfileSection
	errs := cm.doMapfileSection(ctx, section)

	// Verify operations were executed (because it's first run)
	require.Empty(t, errs)
	require.Len(t, mockExec.mapfileOps, 1)
}

// TestDoMapfileSection_MultipleErrors tests doMapfileSection collecting multiple errors
func TestDoMapfileSection_MultipleErrors(t *testing.T) {
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

	// Setup mock executor that always fails
	mockExec := &mockMtaExecutorForMta{
		mapfileErr: errors.New("mapfile execution failed"),
	}
	cm.mtaExecutor = mockExec

	// Create a section with multiple MAPFILE variables
	section := &config.MtaConfigSection{
		Name:    "test_section",
		Changed: true,
		RequiredVars: map[string]string{
			"zimbraSSLDHParam":  "MAPFILE",
			"zimbraSSLCertPath": "MAPLOCAL",
		},
	}

	// Execute doMapfileSection
	errs := cm.doMapfileSection(ctx, section)

	// Verify both errors were collected
	require.Len(t, errs, 2)
	require.Equal(t, "mapfile execution failed", errs[0].Error())
	require.Equal(t, "mapfile execution failed", errs[1].Error())
}
