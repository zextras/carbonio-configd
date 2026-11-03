// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const hooksPath = "/etc/configd/hooks.json"

func localKeys(kv map[string]string) func() (map[string]string, error) {
	return func() (map[string]string, error) { return kv, nil }
}

// podRuntime builds a runtime on fakes with the given hooks file content.
func podRuntime(t *testing.T, hooks string, extra map[string]string) (*runtime, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	f.FS.Put(hooksPath, []byte(hooks), 0o644)

	keys := map[string]string{"zmconfigd_service_hooks": hooksPath}
	for k, v := range extra {
		keys[k] = v
	}

	rt, err := buildRuntime(context.Background(), f.System(), &runtimeOpts{loadLocal: localKeys(keys)})
	require.NoError(t, err)

	return rt, f
}

func mtaConfig(sections map[string][]string) *config.MtaConfig {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{}}

	for name, restarts := range sections {
		sec := &config.MtaConfigSection{Name: name, Restarts: map[string]bool{}}
		for _, svc := range restarts {
			sec.Restarts[svc] = true
		}

		conf.Sections[name] = sec
	}

	return conf
}

func TestBuildRuntime_HostByDefault(t *testing.T) {
	t.Parallel()

	rt, err := buildRuntime(context.Background(), systest.New().System(),
		&runtimeOpts{loadLocal: localKeys(map[string]string{}), disableRestarts: true})
	require.NoError(t, err)

	assert.Nil(t, rt.hooks)
	assert.True(t, rt.sm.DisableRestarts)
	assert.Empty(t, rt.cfg.Sections)
	assert.Equal(t, rt.cfg.BaseDir, rt.cfg.TemplateDir)
	assert.Equal(t, rt.cfg.BaseDir+"/conf/zmconfigd.cf", rt.cfg.ConfigFile)
	assert.Same(t, rt.sm, rt.cm.ServiceMgr, "the config manager shares the one service manager")
}

func TestBuildRuntime_AppliesScopeAndTemplateDir(t *testing.T) {
	t.Parallel()

	rt, _ := podRuntime(t, `{"*":{"restart":["skip"]}}`, map[string]string{
		"zmconfigd_sections":     "antivirus  amavis",
		"zmconfigd_template_dir": "/opt/zextras/share/configd",
	})

	assert.NotNil(t, rt.hooks)
	assert.Equal(t, []string{"antivirus", "amavis"}, rt.cfg.Sections)
	assert.Equal(t, "/opt/zextras/share/configd/conf/zmconfigd.cf", rt.cfg.ConfigFile)
	assert.Equal(t, "/opt/zextras/share/configd/conf/x.in", rt.cfg.TemplatePath("conf/x.in"))
}

func TestBuildRuntime_RejectsBadHooksFile(t *testing.T) {
	t.Parallel()

	f := systest.New()
	keys := localKeys(map[string]string{"zmconfigd_service_hooks": hooksPath})

	_, err := buildRuntime(context.Background(), f.System(), &runtimeOpts{loadLocal: keys})
	require.Error(t, err, "missing file")

	f.FS.Put(hooksPath, []byte(`{"amavis":{"restart":["bogus"]}}`), 0o644)

	_, err = buildRuntime(context.Background(), f.System(), &runtimeOpts{loadLocal: keys})
	require.ErrorIs(t, err, services.ErrHooks)
}

func TestValidateHooks_ListsMissingRestartHooksOfInScopeSections(t *testing.T) {
	t.Parallel()

	rt, _ := podRuntime(t, `{"amavis":{"restart":["signal:TERM"]}}`, map[string]string{"zmconfigd_sections": "amavis antivirus"})
	conf := mtaConfig(map[string][]string{
		"amavis":    {"amavis"},
		"antivirus": {"antivirus", "freshclam"},
		"mta":       {"mta"}, // out of scope: needs no hook
	})

	err := rt.validateHooks(conf, false)

	require.ErrorIs(t, err, services.ErrHooksMissing)
	assert.Equal(t, "service hooks missing: restart:antivirus, restart:freshclam", err.Error())

	rt.cfg.Sections = []string{"amavis"}
	require.NoError(t, rt.validateHooks(conf, false))
}

func TestValidateHooks_ListsCascadedDependencyRestarts(t *testing.T) {
	t.Parallel()

	rt, _ := podRuntime(t, `{"antivirus":{"restart":["skip"]}}`, map[string]string{"zmconfigd_sections": "antivirus"})
	conf := mtaConfig(map[string][]string{"antivirus": {"antivirus"}})
	conf.Sections["antivirus"].Depends = map[string]bool{"amavis": true}
	conf.Sections["amavis"] = &config.MtaConfigSection{Name: "amavis", Depends: map[string]bool{"mta": true}}

	err := rt.validateHooks(conf, false)

	require.ErrorIs(t, err, services.ErrHooksMissing)
	assert.Equal(t, "service hooks missing: restart:amavis, restart:mta", err.Error())
}

func TestValidateHooks_WatchdogNeedsStatusHooks(t *testing.T) {
	t.Parallel()

	rt, _ := podRuntime(t, `{"amavis":{"restart":["skip"]}}`, nil)
	conf := mtaConfig(map[string][]string{"amavis": {"amavis"}})

	require.NoError(t, rt.validateHooks(conf, false))

	err := rt.validateHooks(conf, true)
	require.ErrorIs(t, err, services.ErrHooksMissing)
	assert.Equal(t, "service hooks missing: status:antivirus", err.Error(), "default watchdog list")

	rt.cfg.WdList = []string{"amavis"}
	require.Error(t, rt.validateHooks(conf, true))
}

func TestValidateHooks_NoopOnAHost(t *testing.T) {
	t.Parallel()

	rt, err := buildRuntime(context.Background(), systest.New().System(),
		&runtimeOpts{loadLocal: localKeys(map[string]string{})})
	require.NoError(t, err)

	require.NoError(t, rt.validateHooks(mtaConfig(map[string][]string{"mta": {"mta"}}), true))
}

func TestCheckHooks_FailureSetsExitStatusOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	deps := &daemonLoopDeps{cfg: &config.Config{}, appState: state.NewState()}
	st := &loopIterState{}

	assert.True(t, checkHooks(context.Background(), deps, st), "no validator")

	deps.validate = func(*config.MtaConfig, bool) error {
		calls++

		return services.ErrHooksMissing
	}

	assert.False(t, checkHooks(context.Background(), deps, st))
	assert.Equal(t, 1, st.exitCode)

	deps.validate = func(*config.MtaConfig, bool) error { calls++; return nil }

	assert.True(t, checkHooks(context.Background(), deps, st))
	assert.True(t, checkHooks(context.Background(), deps, st))
	assert.Equal(t, 2, calls, "a successful validation is not repeated")
}

func TestRuntime_RestartsGoThroughTheHooks(t *testing.T) {
	t.Parallel()

	rt, f := podRuntime(t, `{"amavis":{"restart":["signal:TERM"]},"mta":{"restart":["skip"]}}`, nil)

	def := rt.ctl.LookupService("amavis")
	f.FS.Put(def.PidFile, []byte("4100\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4100, Argv: []string{def.ProcessName}, Exe: def.ProcessName})

	ctx := context.Background()
	require.NoError(t, rt.sm.AddRestart(ctx, "amavis"))
	require.NoError(t, rt.sm.AddRestart(ctx, "mta"))
	require.NoError(t, rt.sm.ProcessRestarts(ctx, nil))

	assert.Equal(t, []systest.Signal{{PID: 4100, Sig: syscall.SIGTERM}}, f.Proc.Signals())
}

func TestRuntime_ReadOnlyShadowRecordsNothing(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put(hooksPath, []byte(`{"amavis":{"restart":["signal:TERM","exec:/bin/x"]}}`), 0o644)

	rt, err := buildRuntime(context.Background(), f.System(), &runtimeOpts{
		readOnly:  true,
		loadLocal: localKeys(map[string]string{"zmconfigd_service_hooks": hooksPath}),
	})
	require.NoError(t, err)

	def := rt.ctl.LookupService("amavis")
	f.FS.Put(def.PidFile, []byte("4100\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4100, Argv: []string{def.ProcessName}, Exe: def.ProcessName})
	f.Rec.Reset()

	require.Error(t, rt.sm.ControlProcess(context.Background(), "amavis", services.ActionRestart))

	assert.Empty(t, f.Proc.Signals())
	assert.Empty(t, f.Exec.Lines())
	assert.Empty(t, f.Rec.Ops(), "nothing reached the host below sys.ReadOnly")
}
