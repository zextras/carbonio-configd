// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// scopeFixture is a node with two sections whose templates live under tplDir
// and whose outputs go under outDir.
func scopeFixture(t *testing.T, sections []string) (*configmgr.ConfigManager, *systest.Fakes) {
	t.Helper()

	const (
		outDir = "/opt/zextras"
		tplDir = "/opt/zextras/share/configd"
	)

	host := systest.New()
	host.FS.Put(tplDir+"/conf/a.in", []byte("a-template\n"), 0o644)
	host.FS.Put(tplDir+"/conf/b.in", []byte("b-template\n"), 0o644)

	appState := state.NewState()
	for _, name := range []string{"amavis", "mta"} {
		file := map[string]string{"amavis": "a", "mta": "b"}[name]
		appState.MtaConfig.Sections[name] = &config.MtaConfigSection{
			Name:     name,
			Depends:  map[string]bool{},
			Restarts: map[string]bool{name: true},
			Rewrites: map[string]config.RewriteEntry{"conf/" + file + ".in": {Value: "conf/" + file, Mode: "0640"}},
		}
	}

	appState.Publish(snapshot.New(snapshot.Parts{
		Services: map[string]string{"amavis": "zimbraServiceEnabled", "mta": "zimbraServiceEnabled"},
	}))

	cfg := &config.Config{BaseDir: outDir, TemplateDir: tplDir, Sections: sections, Hostname: "h", RestartConfig: true}
	sm := services.NewServiceManager(services.NewController(host.System()))
	cm := configmgr.NewConfigManager(context.Background(), cfg, appState, nil,
		cache.New(context.Background(), true), host.System(), sm)

	return cm, host
}

func sectionNames(pl *plan.Plan) []string {
	var names []string
	for i := range pl.Sections {
		names = append(names, pl.Sections[i].Name)
	}

	return names
}

func TestScope_ConfiguredSectionsLimitTheCycle(t *testing.T) {
	t.Parallel()

	cm, _ := scopeFixture(t, nil)
	pl, err := cm.Plan(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"amavis", "mta"}, sectionNames(pl), "no zmconfigd_sections: every section")

	cm, _ = scopeFixture(t, []string{"amavis"})
	pl, err = cm.Plan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"amavis"}, sectionNames(pl))
	require.Len(t, pl.Rewrites, 1)
	assert.Equal(t, "conf/a", pl.Rewrites[0].Target)
}

func TestScope_PlanArgumentsIntersectTheConfiguredSections(t *testing.T) {
	t.Parallel()

	cm, _ := scopeFixture(t, []string{"amavis"})

	_, err := cm.PlanHost(context.Background(), []string{"mta"})
	require.ErrorIs(t, err, configmgr.ErrOutOfScope)

	pl, err := cm.PlanHost(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"amavis"}, sectionNames(pl), "no argument: the configured scope")

	pl, err = cm.PlanHost(context.Background(), []string{"amavis"})
	require.NoError(t, err)
	assert.Equal(t, []string{"amavis"}, sectionNames(pl))
}

func TestTemplateDir_RewriteReadsTemplatesAndWritesUnderBase(t *testing.T) {
	t.Parallel()

	cm, host := scopeFixture(t, []string{"amavis"})

	pl, err := cm.Plan(context.Background())
	require.NoError(t, err)
	require.NoError(t, cm.Execute(context.Background(), pl))

	out, ok := host.FS.Data("/opt/zextras/conf/a")
	require.True(t, ok, "the output is written under the base dir")
	assert.Equal(t, "a-template\n", string(out))
	assert.False(t, host.FS.Exists("/opt/zextras/share/configd/conf/a"), "nothing is written next to the templates")
}
