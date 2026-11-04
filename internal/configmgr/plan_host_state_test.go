// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFileLookup makes the "mta" section of the fixture depend on a FILE
// lookup whose file exists on the fake host.
func (f *planHostFixture) withFileLookup(key, content string) {
	f.cm.State.MtaConfig.Sections["mta"].RequiredVars[key] = configTypeFILE
	f.fakes.FS.Put(planHostBase+"/conf/"+key, []byte(content), 0o644)
}

// TestPlanHost_LeavesTheFileCacheAlone pins that planning is side-effect free
// for the manager too: the FILE lookups it makes are not left in
// State.FileCache, where a later daemon cycle would take them for its own.
func TestPlanHost_LeavesTheFileCacheAlone(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.syncHost()
	f.withFileLookup("hosts.txt", "a\nb\n")

	_, err := f.cm.PlanHost(context.Background(), nil)
	require.NoError(t, err)

	_, cached := f.cm.State.LookupFile("hosts.txt")
	assert.False(t, cached, "PlanHost must not populate State.FileCache")
}

// TestPlanHost_ReadsFilesFreshEveryCall pins the other half: because the
// cache of a planning call dies with it, a file changed between two calls is
// seen by the second one, whatever the first one cached.
func TestPlanHost_ReadsFilesFreshEveryCall(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.withFileLookup("hosts.txt", "a\n")
	f.cm.State.FileCache["hosts.txt"] = "stale cycle value"

	ctx := withFileScratch(context.Background())

	got, err := f.cm.lookupFileKey(ctx, "hosts.txt")
	require.NoError(t, err)
	assert.Equal(t, "a", got, "the private cache never reads the cycle's cache")

	f.fakes.FS.Put(planHostBase+"/conf/hosts.txt", []byte("b\n"), 0o644)

	got, err = f.cm.lookupFileKey(withFileScratch(context.Background()), "hosts.txt")
	require.NoError(t, err)
	assert.Equal(t, "b", got)
}

// TestPlan_StillCachesFileLookupsForTheCycle pins that the daemon cycle keeps
// its FILE cache: Execute renders from what Plan observed.
func TestPlan_StillCachesFileLookupsForTheCycle(t *testing.T) {
	t.Parallel()

	f := newPlanHostFixture(t)
	f.withFileLookup("hosts.txt", "a\nb\n")

	_, err := f.cm.Plan(context.Background())
	require.NoError(t, err)

	got, cached := f.cm.State.LookupFile("hosts.txt")
	assert.True(t, cached)
	assert.Equal(t, "a, b", got)
}
