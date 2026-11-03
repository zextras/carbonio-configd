// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestWatchdogServices_ConfiguredListOverridesDefault(t *testing.T) {
	t.Parallel()

	rt, _ := podRuntime(t, `{"*":{"restart":["skip"]}}`, nil)
	assert.Equal(t, []string{componentAntivirus}, rt.watchdogServices(), "default when zmconfigd_watchdog_services is unset")

	rt.cfg.WdList = []string{"amavis", "mta"}
	assert.Equal(t, []string{"amavis", "mta"}, rt.watchdogServices())
}

func TestBuildRuntime_UnreadableLocalconfigKeepsDefaults(t *testing.T) {
	t.Parallel()

	rt, err := buildRuntime(context.Background(), systest.New().System(), &runtimeOpts{
		loadLocal: func() (map[string]string, error) { return nil, errors.New("localconfig.xml is malformed") },
	})

	require.NoError(t, err, "the cycle's own load reports it later")
	assert.Empty(t, rt.cfg.ServiceHooks, "no hooks were configured from the failed load")
	assert.Nil(t, rt.hooks)
}

func TestBuildRuntime_ReadsInstalledLocalconfigByDefault(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	require.NoError(t, os.MkdirAll(filepath.Join(base, "conf"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(base, "conf", "localconfig.xml"),
		[]byte(`<localconfig><key name="zmconfigd_sections"><value>mta proxy</value></key></localconfig>`), 0o600))

	rt, err := buildRuntime(context.Background(), systest.New().System(), &runtimeOpts{})

	require.NoError(t, err)
	assert.Equal(t, []string{"mta", "proxy"}, rt.cfg.Sections)
}

func TestBuildRuntime_RejectsUnparsableHooksFile(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put(hooksPath, []byte(`{not json`), 0o644)

	_, err := buildRuntime(context.Background(), f.System(), &runtimeOpts{
		loadLocal: localKeys(map[string]string{"zmconfigd_service_hooks": hooksPath}),
	})

	require.ErrorIs(t, err, services.ErrHooks)
	assert.ErrorContains(t, err, hooksPath, "the message names the file")
}
