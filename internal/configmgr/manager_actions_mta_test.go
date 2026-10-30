// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
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

func (m *mockMtaExecutorForMta) ReadPostconf(_ context.Context, _ string, _ bool) (string, bool, error) {
	return "", false, nil
}

func (m *mockMtaExecutorForMta) ExecuteLdapWrite(_ context.Context, op mtaops.LdapOperation) error {
	return nil
}

// newMapfileCM builds a ConfigManager with a mock mapfile executor and a
// configd.cf holding one section with the given required lookups.
func newMapfileCM(t *testing.T, firstRun bool, mockExec *mockMtaExecutorForMta,
	required map[string]string) *ConfigManager {
	t.Helper()

	tmpDir := t.TempDir()
	ctx := context.Background()

	st := state.NewState()
	st.SetFirstRun(firstRun)

	cfg := &config.Config{
		BaseDir:    tmpDir,
		ConfigFile: filepath.Join(tmpDir, "zmconfigd.cf"),
	}
	cm := NewConfigManager(ctx, cfg, st, nil, cache.New(ctx, false), hostEnv())
	cm.mtaExecutor = mockExec

	st.SetMtaConfig(&config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"test_section": {Name: "test_section", RequiredVars: required},
	}})

	setServer(cm, nil, map[string]string{"test_section": "zimbraServiceEnabled"})

	return cm
}

// mapfileOpsFor returns the executed mapfile operations for key.
func mapfileOpsFor(ops []mtaops.MapfileOperation, key string) []mtaops.MapfileOperation {
	var out []mtaops.MapfileOperation

	for _, op := range ops {
		if op.Key == key {
			out = append(out, op)
		}
	}

	return out
}

func planMapfileKeys(pl *plan.Plan) map[string]bool {
	out := map[string]bool{}

	for _, m := range pl.Mapfiles {
		out[m.Key] = m.Local
	}

	return out
}

// TestMapfilePlan_HappyPath: a changed section plans one mapped-file operation
// per MAPFILE/MAPLOCAL lookup (VAR lookups are skipped) and Execute runs them.
func TestMapfilePlan_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mockExec := &mockMtaExecutorForMta{}
	cm := newMapfileCM(t, true, mockExec, map[string]string{
		"zimbraSSLDHParam":    "MAPFILE",
		"zimbraSSLCertPath":   "MAPLOCAL",
		"zimbraMtaMyNetworks": "VAR", // Should be skipped
	})

	pl := mustCycle(t, cm)

	require.Equal(t, map[string]bool{"zimbraSSLDHParam": false, "zimbraSSLCertPath": true}, planMapfileKeys(pl))

	require.NotEmpty(t, mapfileOpsFor(mockExec.mapfileOps, "zimbraSSLDHParam"), "Expected MAPFILE operation")

	maplocal := mapfileOpsFor(mockExec.mapfileOps, "zimbraSSLCertPath")
	require.Len(t, maplocal, 1, "Expected MAPLOCAL operation")
	require.True(t, maplocal[0].IsLocal)
	require.Empty(t, mapfileOpsFor(mockExec.mapfileOps, "zimbraMtaMyNetworks"))
}

// TestMapfilePlan_ErrorPath: an executor failure is reported by Execute.
func TestMapfilePlan_ErrorPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mockExec := &mockMtaExecutorForMta{mapfileErr: errors.New("mapfile execution failed")}
	cm := newMapfileCM(t, true, mockExec, map[string]string{"zimbraSSLDHParam": "MAPFILE"})

	_, err := runCycle(t, cm)

	require.Error(t, err)
	require.Equal(t, 1, strings.Count(err.Error(), "mapfile execution failed"))
}

// TestMapfilePlan_NoChanges: when the section did not change and it is not the
// first run, no mapped-file write is planned. MAPLOCAL is never touched; the
// MAPFILE is only healed (re-synced from the snapshot), which is not a change.
func TestMapfilePlan_NoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mockExec := &mockMtaExecutorForMta{}
	cm := newMapfileCM(t, true, mockExec, map[string]string{
		"zimbraSSLDHParam":  "MAPFILE",
		"zimbraSSLCertPath": "MAPLOCAL",
	})

	mustCycle(t, cm) // first run: baseline

	cm.State.SetFirstRun(false)

	mockExec.mapfileOps = nil

	pl := mustCycle(t, cm)

	require.Empty(t, pl.Mapfiles)
	require.Empty(t, pl.Sections)
	require.Empty(t, mapfileOpsFor(mockExec.mapfileOps, "zimbraSSLCertPath"))
}

// TestMapfilePlan_FirstRunNoChanges: the first run plans the mapped files even
// if nothing changed.
func TestMapfilePlan_FirstRunNoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mockExec := &mockMtaExecutorForMta{}
	cm := newMapfileCM(t, true, mockExec, map[string]string{"zimbraSSLDHParam": "MAPFILE"})

	pl := mustCycle(t, cm)

	require.Equal(t, map[string]bool{"zimbraSSLDHParam": false}, planMapfileKeys(pl))
	require.NotEmpty(t, mapfileOpsFor(mockExec.mapfileOps, "zimbraSSLDHParam"))
}

// TestMapfilePlan_MultipleErrors: every failing mapped-file operation is
// reported.
func TestMapfilePlan_MultipleErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mockExec := &mockMtaExecutorForMta{mapfileErr: errors.New("mapfile execution failed")}
	cm := newMapfileCM(t, true, mockExec, map[string]string{
		"zimbraSSLDHParam":  "MAPFILE",
		"zimbraSSLCertPath": "MAPLOCAL",
	})

	_, err := runCycle(t, cm)

	require.Error(t, err)
	require.Equal(t, 2, strings.Count(err.Error(), "mapfile execution failed"))
}
