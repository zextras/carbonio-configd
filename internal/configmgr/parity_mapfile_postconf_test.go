// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/state"
)

const dhParamKey = "zimbraSSLDHParam"

// TestCompareKeys_MapfileSelfHeals verifies a deleted or edited mapped file is
// restored during compare without flagging the section (legacy lookUpConfig
// rewrites it on every lookup), and that steady-state cycles report no change.
func TestCompareKeys_MapfileSelfHeals(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	cm.mtaExecutor = mtaops.NewExecutor(cm.mainConfig.BaseDir, nil)
	cm.comparator = NewComparator(cm, cm.State, cm.ServiceMgr)

	content := []byte("dh params")
	cm.State.GlobalConfig.Data.Set(dhParamKey, base64.StdEncoding.EncodeToString(content))

	section := &config.MtaConfigSection{
		Name:         "mta",
		RequiredVars: map[string]string{dhParamKey: configTypeMAPFILE},
	}
	cm.State.MtaConfig.Sections["mta"] = section
	cm.State.FirstRun = true

	ctx := context.Background()
	path := filepath.Join(cm.mainConfig.BaseDir, "conf", "dhparam.pem")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	require.NoError(t, cm.comparator.CompareKeys(ctx))
	assert.True(t, section.Changed, "first cycle records the key")

	cm.State.FirstRun = false

	require.NoError(t, cm.comparator.CompareKeys(ctx))
	assert.False(t, section.Changed, "steady state must not report changes")

	require.NoError(t, os.Remove(path))
	require.NoError(t, cm.comparator.CompareKeys(ctx))
	assert.False(t, section.Changed, "restoring a deleted file must not restart")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	require.NoError(t, os.WriteFile(path, []byte("tampered"), 0o600))
	require.NoError(t, cm.comparator.CompareKeys(ctx))
	assert.False(t, section.Changed)

	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestDoMapfileSection_PassesConfiguredData verifies the MAPFILE operation
// carries the configured base64 value; without it the executor treats the key
// as unset and deletes the file.
func TestDoMapfileSection_PassesConfiguredData(t *testing.T) {
	cm, exec := newTestCMWithExecutor(t)
	cm.State.FirstRun = false
	cm.State.GlobalConfig.Data.Set(dhParamKey, "ZGF0YQ==")

	section := &config.MtaConfigSection{
		Name:         "mta",
		Changed:      true,
		RequiredVars: map[string]string{dhParamKey: configTypeMAPFILE},
	}

	require.Empty(t, cm.doMapfileSection(context.Background(), section))
	require.Len(t, exec.mapfileOps, 1)
	assert.Equal(t, "ZGF0YQ==", exec.mapfileOps[0].Base64Data)
}

// TestCompareKeys_EmptyValueSteadyState pins that a defined-but-empty value never
// reports a change once recorded as absent.
func TestCompareKeys_EmptyValueSteadyState(t *testing.T) {
	c, st := newComparatorWithLookup(map[string]map[string]string{"VAR": {"k": ""}}, nil)
	st.FirstRun = false
	section := &config.MtaConfigSection{Name: "s", RequiredVars: map[string]string{"k": "VAR"}}
	st.MtaConfig.Sections["s"] = section

	for range 3 {
		require.NoError(t, c.CompareKeys(context.Background()))
		assert.False(t, section.Changed)
	}
}

// failingBatchExecutor fails the batch call and any per-key call for badKey.
type failingBatchExecutor struct {
	mockMtaExecutor
	badKey string
}

func (f *failingBatchExecutor) ExecutePostconfBatch(_ context.Context, _ []mtaops.PostconfOperation) error {
	return errors.New("batch failed")
}

func (f *failingBatchExecutor) ExecutePostconf(_ context.Context, op mtaops.PostconfOperation) error {
	if op.Key == f.badKey {
		return errors.New("bad key")
	}

	f.postconfOps = append(f.postconfOps, op)

	return nil
}

// TestDoPostconf_BatchFailureFallsBackPerKey verifies that after a batch failure
// only the keys that still fail stay pending.
func TestDoPostconf_BatchFailureFallsBackPerKey(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	exec := &failingBatchExecutor{badKey: "bad"}
	cm.mtaExecutor = exec
	cm.State.CurrentActions.Postconf["good"] = "VAR:good"
	cm.State.CurrentActions.Postconf["bad"] = "VAR:bad"

	require.Error(t, cm.doPostconf(context.Background()))

	assert.Len(t, exec.postconfOps, 1)
	assert.NotContains(t, cm.State.CurrentActions.Postconf, "good")
	assert.Equal(t, "VAR:bad", cm.State.CurrentActions.Postconf["bad"])
}

// TestDoPostconf_ResolveFailureStaysPending verifies a directive that could not
// be resolved is not cleared by a successful batch for the others.
func TestDoPostconf_ResolveFailureStaysPending(t *testing.T) {
	cm, exec := newTestCMWithExecutor(t)
	cm.mtaResolver = &keyedFailResolver{failKey: "bad"}
	cm.State.CurrentActions.Postconf["good"] = "VAR:good"
	cm.State.CurrentActions.Postconf["bad"] = "VAR:bad"

	require.Error(t, cm.doPostconf(context.Background()))

	assert.Len(t, exec.postconfOps, 1)
	assert.NotContains(t, cm.State.CurrentActions.Postconf, "good")
	assert.Contains(t, cm.State.CurrentActions.Postconf, "bad")
}

// keyedFailResolver fails ResolveValue for failKey only.
type keyedFailResolver struct{ failKey string }

func (r *keyedFailResolver) ResolveValue(_ context.Context, _, key string, _ *state.State) (string, error) {
	if key == r.failKey {
		return "", errors.New("cannot resolve")
	}

	return key, nil
}
