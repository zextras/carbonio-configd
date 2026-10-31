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
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

const dhParamKey = "zimbraSSLDHParam"

// TestCycle_MapfileSelfHeals verifies a deleted or edited mapped file is
// restored during the cycle without making the section changed (legacy
// lookUpConfig rewrites it on every lookup), and that steady-state cycles plan
// no change.
func TestCycle_MapfileSelfHeals(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	cm.mtaExecutor = mtaops.NewExecutor(cm.mainConfig.BaseDir, nil, cm.env)

	content := []byte("dh params")
	setGlobal(cm, map[string]string{dhParamKey: base64.StdEncoding.EncodeToString(content)})

	cm.State.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Name:         "mta",
		RequiredVars: map[string]string{dhParamKey: configTypeMAPFILE},
	}
	setServer(cm, nil, map[string]string{"mta": "zimbraServiceEnabled"})
	cm.State.SetFirstRun(true)

	path := filepath.Join(cm.mainConfig.BaseDir, "conf", "dhparam.pem")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	pl := mustCycle(t, cm)
	assert.Len(t, pl.Sections, 1, "first cycle records the key")

	cm.State.SetFirstRun(false)

	pl = mustCycle(t, cm)
	assert.Empty(t, pl.Sections, "steady state must not report changes")

	require.NoError(t, os.Remove(path))

	pl = mustCycle(t, cm)
	assert.Empty(t, pl.Sections, "restoring a deleted file must not restart")
	assert.Empty(t, pl.Restarts)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	require.NoError(t, os.WriteFile(path, []byte("tampered"), 0o600))

	pl = mustCycle(t, cm)
	assert.Empty(t, pl.Sections)

	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestDoMapfile_PassesConfiguredData verifies the MAPFILE operation
// carries the configured base64 value; without it the executor treats the key
// as unset and deletes the file.
func TestDoMapfile_PassesConfiguredData(t *testing.T) {
	cm, exec := newTestCMWithExecutor(t)
	setGlobal(cm, map[string]string{dhParamKey: "ZGF0YQ=="})

	require.NoError(t, cm.doMapfile(context.Background(), []plan.Mapfile{{Key: dhParamKey}}))
	require.Len(t, exec.mapfileOps, 1)
	assert.Equal(t, "ZGF0YQ==", exec.mapfileOps[0].Base64Data)
}

// TestDoMapfile_EncodesRawNativeValue covers the native LDAP client,
// which returns the raw attribute bytes (the DH PEM) instead of zmprov's
// "attr:: <base64>" payload: the operation must still carry valid base64 of
// exactly those bytes, or the executor fails and the rewrite cycle aborts.
func TestDoMapfile_EncodesRawNativeValue(t *testing.T) {
	const pem = "-----BEGIN DH PARAMETERS-----\nMIIBCAKCAQEA\n-----END DH PARAMETERS-----"

	cm, exec := newTestCMWithExecutor(t)
	setGlobal(cm, map[string]string{dhParamKey: pem})

	require.NoError(t, cm.doMapfile(context.Background(), []plan.Mapfile{{Key: dhParamKey}}))
	require.Len(t, exec.mapfileOps, 1)

	decoded, err := base64.StdEncoding.DecodeString(exec.mapfileOps[0].Base64Data)
	require.NoError(t, err)
	assert.Equal(t, pem, string(decoded))
}

// TestCycle_EmptyValueSteadyState pins that a defined-but-empty value never
// reports a change once recorded as absent.
func TestCycle_EmptyValueSteadyState(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	setGlobal(cm, map[string]string{"k": ""})
	setServer(cm, nil, map[string]string{"s": "zimbraServiceEnabled"})
	cm.State.SetFirstRun(false)
	cm.State.MtaConfig.Sections["s"] = &config.MtaConfigSection{
		Name: "s", RequiredVars: map[string]string{"k": "VAR"},
	}

	for range 3 {
		pl := mustCycle(t, cm)
		assert.Empty(t, pl.Sections)
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
	entries := []plan.Postconf{{Key: "good", Value: "VAR:good"}, {Key: "bad", Value: "VAR:bad"}}

	pending, err := cm.doPostconf(context.Background(), entries)
	require.Error(t, err)

	assert.Len(t, exec.postconfOps, 1)
	assert.Equal(t, []plan.Postconf{{Key: "bad", Value: "VAR:bad"}}, pending)
}

// TestDoPostconf_ResolveFailureStaysPending verifies a directive that could not
// be resolved is not cleared by a successful batch for the others.
func TestDoPostconf_ResolveFailureStaysPending(t *testing.T) {
	cm, exec := newTestCMWithExecutor(t)
	cm.mtaResolver = &keyedFailResolver{failKey: "bad"}
	entries := []plan.Postconf{{Key: "good", Value: "VAR:good"}, {Key: "bad", Value: "VAR:bad"}}

	pending, err := cm.doPostconf(context.Background(), entries)
	require.Error(t, err)

	assert.Len(t, exec.postconfOps, 1)
	require.Len(t, pending, 1)
	assert.Equal(t, "bad", pending[0].Key)
}

// keyedFailResolver fails ResolveValue for failKey only.
type keyedFailResolver struct{ failKey string }

func (r *keyedFailResolver) ResolveValue(_ context.Context, _, key string, _ *snapshot.Snapshot) (string, error) {
	if key == r.failKey {
		return "", errors.New("cannot resolve")
	}

	return key, nil
}
