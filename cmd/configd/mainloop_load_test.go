// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const minimalMtaConfig = "SECTION mta\n"

// loadFixture is a config manager over fakes: LDAP lookups are canned by the
// command registry, so no network or Carbonio install is involved.
type loadFixture struct {
	cm    *configmgr.ConfigManager
	cfg   *config.Config
	state *state.State
}

func newLoadFixture(t *testing.T, mtaConfig string) *loadFixture {
	t.Helper()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "zmconfigd.cf")

	require.NoError(t, os.WriteFile(cfgFile, []byte(mtaConfig), 0o600))

	cfg := &config.Config{
		Progname:   "configd-test",
		BaseDir:    dir,
		ConfigFile: cfgFile,
		Hostname:   "testhost",
	}
	appState := state.NewState()
	env := systest.New().System()
	ctx := context.Background()
	sm := services.NewServiceManager(services.NewController(env))
	cm := configmgr.NewConfigManager(ctx, cfg, appState, nil, cache.New(ctx, false), env, sm)

	cm.CommandRegistry.GlobalAttrs = func(context.Context) (map[string]string, error) {
		return map[string]string{"zimbraFoo": "bar"}, nil
	}
	cm.CommandRegistry.ServerAttrs = func(context.Context, string) (map[string]string, error) {
		return map[string]string{"zimbraServiceEnabled": "mta"}, nil
	}

	return &loadFixture{cm: cm, cfg: cfg, state: appState}
}

func TestRunLoadAndParse(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		f := newLoadFixture(t, minimalMtaConfig)

		loadDur, parseDur, err := runLoadAndParse(t.Context(), f.cm, f.cfg)
		require.NoError(t, err)
		assert.Positive(t, loadDur)
		assert.Positive(t, parseDur)
		assert.Contains(t, f.state.MtaConfig.Sections, "mta")
	})

	t.Run("load failure", func(t *testing.T) {
		t.Parallel()

		f := newLoadFixture(t, minimalMtaConfig)
		// A server config load without a hostname fails at once (no retries).
		f.cfg.Hostname = ""

		_, parseDur, err := runLoadAndParse(t.Context(), f.cm, f.cfg)
		require.Error(t, err)
		assert.Zero(t, parseDur, "parse must not run after a failed load")
	})

	t.Run("parse failure", func(t *testing.T) {
		t.Parallel()

		f := newLoadFixture(t, minimalMtaConfig)
		f.cfg.ConfigFile = filepath.Join(t.TempDir(), "missing.cf")

		loadDur, _, err := runLoadAndParse(t.Context(), f.cm, f.cfg)
		require.Error(t, err)
		assert.Positive(t, loadDur, "load succeeded before the parse failed")
	})

	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()

		f := newLoadFixture(t, minimalMtaConfig)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _, err := runLoadAndParse(ctx, f.cm, f.cfg)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRunForcedMode(t *testing.T) {
	t.Parallel()

	t.Run("records forced configs and runs the plan", func(t *testing.T) {
		t.Parallel()

		f := newLoadFixture(t, minimalMtaConfig)
		rt := &runtime{
			state: f.state,
			cfg:   f.cfg,
			cm:    f.cm,
			sm:    f.cm.Services(),
		}

		code := runForcedMode(t.Context(), &Args{ForcedConfigs: []string{"mta"}}, rt)
		assert.Equal(t, 0, code)
		assert.Equal(t, 1, f.state.Forced)
		assert.Equal(t, "mta", f.state.ForcedConfig["mta"])
	})
}
