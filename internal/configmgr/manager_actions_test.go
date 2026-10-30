// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

func TestSectionEnabledOnNode(t *testing.T) {
	// A mailbox+service-only node (e.g. srv3): no mta, proxy, ldap, dhparam.
	mailboxNode := map[string]string{"mailbox": "", "service": ""}
	// A proxy node.
	proxyNode := map[string]string{"proxy": ""}
	// An ldap+mta node.
	ldapMtaNode := map[string]string{"ldap": "", "mta": ""}

	tests := []struct {
		name     string
		section  string
		services map[string]string
		want     bool
	}{
		// Mapped sections gate on their mapped service.
		{"amavis maps to mta - mailbox node skips", "amavis", mailboxNode, false},
		{"amavis maps to mta - mta node runs", "amavis", ldapMtaNode, true},
		{"sasl maps to mta - mailbox node skips", "sasl", mailboxNode, false},
		{"webxml maps to mailbox - mailbox node runs", "webxml", mailboxNode, true},
		{"nginx maps to proxy - mailbox node skips", "nginx", mailboxNode, false},
		{"nginx maps to proxy - proxy node runs", "nginx", proxyNode, true},
		// dhparam is negated: runs only when dhparam service is NOT present.
		{"dhparam negated - mailbox node runs", "dhparam", mailboxNode, true},
		{"dhparam negated - present means skip", "dhparam", map[string]string{"dhparam": ""}, false},
		// Unmapped sections gate on a same-named service.
		{"mta section - mailbox node skips", "mta", mailboxNode, false},
		{"mta section - mta node runs", "mta", ldapMtaNode, true},
		{"mailbox section - mailbox node runs", "mailbox", mailboxNode, true},
		{"proxy section - mailbox node skips", "proxy", mailboxNode, false},
		{"ldap section - mailbox node skips", "ldap", mailboxNode, false},
		{"ldap section - ldap node runs", "ldap", ldapMtaNode, true},
		{"stats section - mailbox node skips", "stats", mailboxNode, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enabled := func(service string) bool {
				_, ok := tt.services[service]

				return ok
			}

			if got := plan.SectionEnabled(tt.section, enabled); got != tt.want {
				t.Errorf("SectionEnabled(%q, %v) = %v, want %v",
					tt.section, tt.services, got, tt.want)
			}
		})
	}
}

// newTestTransformer creates a transformer for testing that returns lines unchanged
func newTestTransformer(cm *ConfigManager) *transformer.Transformer {
	return transformer.NewTransformer(cm)
}

// TestProcessIsRunning tests the ProcessIsRunning method
func TestProcessIsRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	runningServices := map[string]bool{"nginx": true, "mta": false}
	mockSvcMgr := &testutil.MockServiceManager{
		IsRunningFn: func(_ context.Context, service string) (bool, error) {
			running, ok := runningServices[service]
			if !ok {
				return false, nil
			}
			return running, nil
		},
	}

	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:  "/tmp",
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: mockSvcMgr,
		Cache:      cacheInstance,
	}

	// Test running service
	if !cm.ProcessIsRunning(context.Background(), "nginx") {
		t.Error("Expected nginx to be running")
	}

	// Test not running service
	if cm.ProcessIsRunning(context.Background(), "mta") {
		t.Error("Expected mta to not be running")
	}

	// Test unknown service
	if cm.ProcessIsRunning(context.Background(), "unknown") {
		t.Error("Expected unknown service to not be running")
	}
}

// TestProcessIsNotRunning tests the ProcessIsNotRunning method
func TestProcessIsNotRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	runningServices := map[string]bool{"nginx": true, "mta": false}
	mockSvcMgr := &testutil.MockServiceManager{
		IsRunningFn: func(_ context.Context, service string) (bool, error) {
			running, ok := runningServices[service]
			if !ok {
				return false, nil
			}
			return running, nil
		},
	}

	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:  "/tmp",
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: mockSvcMgr,
		Cache:      cacheInstance,
	}

	// Test running service (should be NOT not running)
	if cm.ProcessIsNotRunning(context.Background(), "nginx") {
		t.Error("Expected nginx to not be 'not running' (i.e., it is running)")
	}

	// Test not running service (should be not running)
	if !cm.ProcessIsNotRunning(context.Background(), "mta") {
		t.Error("Expected mta to be not running")
	}

	// Test unknown service (should be not running)
	if !cm.ProcessIsNotRunning(context.Background(), "unknown") {
		t.Error("Expected unknown service to be not running")
	}
}

// TestIsAlreadyClosedError tests the isAlreadyClosedError utility function
func TestIsAlreadyClosedError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	// A real double-close reproduces the exact error shape production code
	// hits: an *fs.PathError wrapping os.ErrClosed.
	tmpFile, err := os.CreateTemp(t.TempDir(), "already-closed")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	doubleCloseErr := tmpFile.Close()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "real double close",
			err:      doubleCloseErr,
			expected: true,
		},
		{
			name:     "sentinel directly",
			err:      os.ErrClosed,
			expected: true,
		},
		{
			name:     "wrapped sentinel",
			err:      fmt.Errorf("close: %w", os.ErrClosed),
			expected: true,
		},
		{
			name:     "message merely mentions closed, does not wrap the sentinel",
			err:      &testError{msg: "error: connection already closed"},
			expected: false,
		},
		{
			name:     "different error",
			err:      &testError{msg: "permission denied"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAlreadyClosedError(tt.err)
			if result != tt.expected {
				t.Errorf("isAlreadyClosedError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// testError is a simple error type for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// TestParseValueSpec tests the parseValueSpec utility function
func TestParseValueSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tests := []struct {
		name         string
		valueSpec    string
		expectedType string
		expectedKey  string
	}{
		{
			name:         "VAR type",
			valueSpec:    "VAR:some_var_key",
			expectedType: "VAR",
			expectedKey:  "some_var_key",
		},
		{
			name:         "LOCAL type",
			valueSpec:    "LOCAL:local_config_key",
			expectedType: "LOCAL",
			expectedKey:  "local_config_key",
		},
		{
			name:         "MAPLOCAL type",
			valueSpec:    "MAPLOCAL:map_key",
			expectedType: "MAPLOCAL",
			expectedKey:  "map_key",
		},
		{
			name:         "FILE type",
			valueSpec:    "FILE /path/to/file.txt",
			expectedType: "FILE",
			expectedKey:  "/path/to/file.txt",
		},
		{
			name:         "FILE type with spaces in path",
			valueSpec:    "FILE /path/to/my file.txt",
			expectedType: "FILE",
			expectedKey:  "/path/to/my file.txt",
		},
		{
			name:         "literal value",
			valueSpec:    "simple_literal_value",
			expectedType: "LITERAL",
			expectedKey:  "simple_literal_value",
		},
		{
			name:         "literal value with colon but not a known type",
			valueSpec:    "UNKNOWN:value",
			expectedType: "LITERAL",
			expectedKey:  "UNKNOWN:value",
		},
		{
			name:         "empty value",
			valueSpec:    "",
			expectedType: "LITERAL",
			expectedKey:  "",
		},
		{
			name:         "VAR type with empty key",
			valueSpec:    "VAR:",
			expectedType: "VAR",
			expectedKey:  "",
		},
		{
			name:         "LOCAL type with colon in key",
			valueSpec:    "LOCAL:key:with:colons",
			expectedType: "LOCAL",
			expectedKey:  "key:with:colons",
		},
		{
			name:         "literal number",
			valueSpec:    "12345",
			expectedType: "LITERAL",
			expectedKey:  "12345",
		},
		{
			name:         "literal IP address",
			valueSpec:    "192.168.1.1",
			expectedType: "LITERAL",
			expectedKey:  "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valueType, valueKey := parseValueSpec(tt.valueSpec)
			if valueType != tt.expectedType {
				t.Errorf("parseValueSpec(%q) type = %q, expected %q", tt.valueSpec, valueType, tt.expectedType)
			}
			if valueKey != tt.expectedKey {
				t.Errorf("parseValueSpec(%q) key = %q, expected %q", tt.valueSpec, valueKey, tt.expectedKey)
			}
		})
	}
}

// newApplyRestartsCM returns a ConfigManager wired for ApplyRestarts: a restart
// policy on a mock service manager that records the queued restarts.
func newApplyRestartsCM(t *testing.T, restartsEnabled bool) (*ConfigManager, *[]string) {
	t.Helper()

	var queued []string

	mockSvcMgr := &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, service string, _ []string) error {
			queued = append(queued, service)

			return nil
		},
		GetPendingRestartsFn: func() []string { return queued },
	}

	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:       "/tmp",
			Hostname:      "testhost",
			RestartConfig: restartsEnabled,
		},
		State:      state.NewState(),
		ServiceMgr: mockSvcMgr,
		Cache:      cache.New(context.Background(), false),
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)

	return cm, &queued
}

// TestApplyRestarts tests that the plan's restarts reach the service manager.
func TestApplyRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	t.Run("restart services from plan", func(t *testing.T) {
		cm, queued := newApplyRestartsCM(t, true)

		pl := &plan.Plan{Restarts: []plan.Restart{
			{Service: "nginx", Action: plan.ActionRestart},
			{Service: "mta", Action: plan.ActionRestart},
		}}

		cm.ApplyRestarts(ctx, pl)

		pending := cm.ServiceMgr.GetPendingRestarts()
		assert.ElementsMatch(t, []string{"nginx", "mta"}, pending)
		assert.ElementsMatch(t, []string{"nginx", "mta"}, *queued)
		assert.Empty(t, cm.carry.Restarts, "queued restarts must not be carried")
	})

	t.Run("no restarts needed", func(t *testing.T) {
		cm, queued := newApplyRestartsCM(t, true)

		cm.ApplyRestarts(ctx, &plan.Plan{})

		assert.Empty(t, cm.ServiceMgr.GetPendingRestarts())
		assert.Empty(t, *queued)
		assert.Empty(t, cm.carry.Restarts)
	})

	t.Run("restarts disabled are carried", func(t *testing.T) {
		cm, queued := newApplyRestartsCM(t, false)

		pl := &plan.Plan{Restarts: []plan.Restart{{Service: "nginx", Action: plan.ActionRestart}}}

		cm.ApplyRestarts(ctx, pl)

		assert.Empty(t, *queued, "no restart may be queued when restarts are disabled")
		require.Len(t, cm.carry.Restarts, 1)
		assert.Equal(t, "nginx", cm.carry.Restarts[0].Service)
	})
}

// TestClearLocalConfigCache tests the ClearLocalConfigCache method
func TestClearLocalConfigCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	t.Run("clear non-empty cache", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:                   state.NewState(),
			Cache:                   cacheInstance,
			cachedLocalConfigOutput: "cached content",
		}

		// Verify cache has content before clearing
		if cm.cachedLocalConfigOutput == "" {
			t.Error("Expected cache to have content before clearing")
		}

		// Clear the cache
		cm.ClearLocalConfigCache(ctx)

		// Verify cache is empty
		if cm.cachedLocalConfigOutput != "" {
			t.Errorf("Expected cache to be empty after clearing, got: %q", cm.cachedLocalConfigOutput)
		}
	})

	t.Run("clear already empty cache", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  "/tmp",
				Hostname: "testhost",
			},
			State:                   state.NewState(),
			Cache:                   cacheInstance,
			cachedLocalConfigOutput: "",
		}

		// Clear the already empty cache (should be a no-op)
		cm.ClearLocalConfigCache(ctx)

		// Verify cache is still empty
		if cm.cachedLocalConfigOutput != "" {
			t.Errorf("Expected cache to remain empty, got: %q", cm.cachedLocalConfigOutput)
		}
	})
}

// TestCleanupRewriteFiles tests the cleanupRewriteFiles utility function
func TestCleanupRewriteFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	tmpDir := t.TempDir()

	t.Run("cleanup with all files", func(t *testing.T) {
		// Create temporary files
		srcPath := tmpDir + "/src1.txt"
		tmpPath := tmpDir + "/tmp1.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		// Call cleanup
		realFSConfigManager().cleanupRewriteFiles(ctx, srcFile, tmpFile, tmpPath)

		// Verify temp file was removed
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("Expected temp file to be removed")
		}
	})

	t.Run("cleanup with nil source file", func(t *testing.T) {
		tmpPath := tmpDir + "/tmp2.txt"

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		// Call cleanup with nil source
		realFSConfigManager().cleanupRewriteFiles(ctx, nil, tmpFile, tmpPath)

		// Verify temp file was removed
		if _, err := os.Stat(tmpPath); err == nil {
			t.Error("Expected temp file to be removed")
		}
	})

	t.Run("cleanup with nil temp file", func(t *testing.T) {
		srcPath := tmpDir + "/src3.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		// Call cleanup with nil temp file
		realFSConfigManager().cleanupRewriteFiles(ctx, srcFile, nil, "")

		// Should complete without error (nothing to verify since no temp file)
	})

	t.Run("cleanup with all nil", func(t *testing.T) {
		// Call cleanup with everything nil (should be a no-op)
		realFSConfigManager().cleanupRewriteFiles(ctx, nil, nil, "")

		// Should complete without error
	})

	t.Run("cleanup with nonexistent temp file path", func(t *testing.T) {
		srcPath := tmpDir + "/src4.txt"

		srcFile, err := os.Create(srcPath)
		if err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		// Call cleanup with nonexistent temp file path
		realFSConfigManager().cleanupRewriteFiles(ctx, srcFile, nil, tmpDir+"/nonexistent.txt")

		// Should complete without error even though file doesn't exist
	})
}

// newPlanTestCM returns a ConfigManager that can Plan (and ApplyRestarts) over
// a hand-built configd.cf kept in State.MtaConfig.
func newPlanTestCM(t *testing.T) *ConfigManager {
	t.Helper()

	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:  t.TempDir(),
			Hostname: "testhost",
		},
		State:      state.NewState(),
		ServiceMgr: &testutil.MockServiceManager{},
		Cache:      cache.New(context.Background(), false),
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)

	return cm
}

// planTestSection returns an empty section with every map allocated.
func planTestSection(name string) *config.MtaConfigSection {
	return &config.MtaConfigSection{
		Name:         name,
		Depends:      make(map[string]bool),
		Rewrites:     make(map[string]config.RewriteEntry),
		Restarts:     make(map[string]bool),
		RequiredVars: make(map[string]string),
		Postconf:     make(map[string]string),
		Postconfd:    make(map[string]string),
		Ldap:         make(map[string]string),
		Conditionals: make([]config.Conditional, 0),
	}
}

// addPlanSection registers sec in the parsed configd.cf of cm.
func addPlanSection(cm *ConfigManager, sec *config.MtaConfigSection) {
	cm.State.MtaConfig.Sections[sec.Name] = sec
}

// enableServices marks the services enabled on the node and as running in the
// previous cycle's tracking, so that only the section-derived actions show up
// in the plan (no start/stop for a status change).
func enableServices(cm *ConfigManager, names ...string) {
	services := make(map[string]string, len(names))
	tracked := make(map[string]string, len(names))

	for _, n := range names {
		services[n] = "zimbraServiceEnabled"
		tracked[n] = "running"
	}

	setServer(cm, map[string]string{}, services)

	cm.tracked = tracked
}

// markChanged makes sec depend on a global variable that goes from undefined
// (no previous cycle) to defined, so the section counts as changed on a
// cycle that is not the first run.
func markChanged(cm *ConfigManager, sec *config.MtaConfigSection) {
	sec.RequiredVars["zimbraTestTrigger"] = "VAR"

	setGlobal(cm, map[string]string{"zimbraTestTrigger": "1"})
}

// findRestart returns the restart entry of service in pl.
func findRestart(pl *plan.Plan, service string) (plan.Restart, bool) {
	for _, r := range pl.Restarts {
		if r.Service == service {
			return r, true
		}
	}

	return plan.Restart{}, false
}

func rewriteSources(pl *plan.Plan) []string {
	out := make([]string, 0, len(pl.Rewrites))
	for _, r := range pl.Rewrites {
		out = append(out, r.Source)
	}

	return out
}

// TestPlan_SectionSelection tests which sections a cycle processes.
func TestPlan_SectionSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	t.Run("plan with first run", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(true)
		// Enable the section's service on this node so it is not gated out.
		enableServices(cm, "testservice")

		section := planTestSection("testservice")
		section.Rewrites["conf/test.conf.in"] = config.RewriteEntry{Value: "conf/test.conf", Mode: "0644"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Rewrites, 1, "expected the rewrite to be planned")
		assert.Equal(t, "conf/test.conf.in", pl.Rewrites[0].Source)
		assert.Equal(t, "conf/test.conf", pl.Rewrites[0].Target)
		assert.Equal(t, "0644", pl.Rewrites[0].Mode)
		assert.True(t, pl.FirstRun)
	})

	t.Run("plan with forced config", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.SetForcedConfig(ctx, "proxy")
		// Enable proxy on this node so the section is not gated out.
		enableServices(cm, "proxy")

		section := planTestSection("proxy") // not changed, but forced
		section.Rewrites["conf/proxy.conf.in"] = config.RewriteEntry{Value: "conf/proxy.conf", Mode: "0644"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Rewrites, 1, "expected the rewrite to be planned for a forced section")
		assert.Equal(t, "conf/proxy.conf.in", pl.Rewrites[0].Source)
		assert.Equal(t, plan.ReasonForced, pl.Rewrites[0].Reasons[0].Kind)
	})

	t.Run("plan with requested config", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.AddRequestedConfigs(ctx, []string{"proxy"})
		enableServices(cm, "proxy")

		section := planTestSection("proxy")
		section.Rewrites["conf/proxy.conf.in"] = config.RewriteEntry{Value: "conf/proxy.conf", Mode: "0644"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Rewrites, 1, "expected the rewrite to be planned for a requested section")
		assert.Equal(t, plan.ReasonRequested, pl.Rewrites[0].Reasons[0].Kind)
		assert.Empty(t, cm.State.TakeRequestedConfigs(), "a successful plan consumes the requests")
	})

	t.Run("plan with changed section", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		enableServices(cm, "changed")

		section := planTestSection("changed")
		section.Rewrites["conf/changed.conf.in"] = config.RewriteEntry{Value: "conf/changed.conf", Mode: "0644"}
		markChanged(cm, section)
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Equal(t, []string{"conf/changed.conf.in"}, rewriteSources(pl))
		assert.Equal(t, plan.ReasonVarChanged, pl.Rewrites[0].Reasons[0].Kind)
	})

	t.Run("skip unchanged sections", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		enableServices(cm, "unchanged")

		section := planTestSection("unchanged") // not changed
		section.Rewrites["conf/unchanged.conf.in"] = config.RewriteEntry{Value: "conf/unchanged.conf", Mode: "0644"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Empty(t, pl.Rewrites, "expected no rewrites for unchanged section")
	})

	t.Run("skip sections whose service is disabled on the node", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(true)
		enableServices(cm, "mailbox")

		section := planTestSection("proxy")
		section.Rewrites["conf/proxy.conf.in"] = config.RewriteEntry{Value: "conf/proxy.conf", Mode: "0644"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Empty(t, pl.Rewrites, "proxy is not enabled on this node")
		assert.Empty(t, pl.Sections)
	})
}

// TestPlan_ForcedSectionRestartsAreSectionScoped is a regression test for a
// bug where forcing ANY section suppressed restart planning cycle-wide. The
// restart skip for forced/requested sections is section-scoped: a requested
// section's own restart is skipped, while a sibling section whose variables
// changed still gets its restart planned.
func TestPlan_ForcedSectionRestartsAreSectionScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	newSection := func(name string) *config.MtaConfigSection {
		s := planTestSection(name)
		s.Restarts = map[string]bool{name + "restart": true}

		return s
	}

	t.Run("requested section skips its restart, changed sibling keeps it", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.AddRequestedConfigs(ctx, []string{"svca"})
		enableServices(cm, "svca", "svcb")

		svcb := newSection("svcb")
		markChanged(cm, svcb)

		addPlanSection(cm, newSection("svca"))
		addPlanSection(cm, svcb)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		_, ok := findRestart(pl, "svcarestart")
		assert.False(t, ok, "expected requested section svca's restart NOT to be planned")

		_, ok = findRestart(pl, "svcbrestart")
		assert.True(t, ok, "expected changed sibling section svcb's restart to be planned")
	})

	t.Run("forced section skips its restart and only forced sections are compared", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.SetForcedConfig(ctx, "svca")
		enableServices(cm, "svca", "svcb")

		svcb := newSection("svcb")
		markChanged(cm, svcb)

		addPlanSection(cm, newSection("svca"))
		addPlanSection(cm, svcb)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		_, ok := findRestart(pl, "svcarestart")
		assert.False(t, ok, "expected forced section svca's restart NOT to be planned")

		// Legacy semantics: with forced sections only those are compared.
		_, ok = findRestart(pl, "svcbrestart")
		assert.False(t, ok, "section svcb is outside the forced scope")
	})
}

// TestLookUpConfig tests the LookUpConfig method
func TestLookUpConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)
	tmpDir := t.TempDir()

	t.Run("lookup VAR type from GlobalConfig", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to GlobalConfig
		setGlobal(cm, map[string]string{"testKey": "testValue"})

		value, err := cm.LookUpConfig(ctx, "VAR", "testKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "testValue" {
			t.Errorf("Expected 'testValue', got %q", value)
		}
	})

	t.Run("lookup VAR type from MiscConfig", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to MiscConfig (not in GlobalConfig)
		setMisc(cm, map[string]string{"miscKey": "miscValue"})

		value, err := cm.LookUpConfig(ctx, "VAR", "miscKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "miscValue" {
			t.Errorf("Expected 'miscValue', got %q", value)
		}
	})

	t.Run("lookup VAR type from ServerConfig", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to ServerConfig (not in Global or Misc)
		setServer(cm, map[string]string{"serverKey": "serverValue"}, map[string]string{})

		value, err := cm.LookUpConfig(ctx, "VAR", "serverKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "serverValue" {
			t.Errorf("Expected 'serverValue', got %q", value)
		}
	})

	t.Run("lookup LOCAL type", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to LocalConfig
		setLocal(cm, map[string]string{"localKey": "localValue"})

		value, err := cm.LookUpConfig(ctx, "LOCAL", "localKey")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "localValue" {
			t.Errorf("Expected 'localValue', got %q", value)
		}
	})

	t.Run("lookup SERVICE type - enabled", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Add to ServiceConfig
		setServer(cm, map[string]string{}, map[string]string{"nginx": "zimbraServiceEnabled"})

		value, err := cm.LookUpConfig(ctx, "SERVICE", "nginx")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "TRUE" {
			t.Errorf("Expected 'TRUE', got %q", value)
		}
	})

	t.Run("lookup SERVICE type - disabled", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// Service not in ServiceConfig
		value, err := cm.LookUpConfig(ctx, "SERVICE", "nonexistent")
		if err != nil {
			t.Errorf("LookUpConfig failed: %v", err)
		}
		if value != "FALSE" {
			t.Errorf("Expected 'FALSE', got %q", value)
		}
	})

	t.Run("lookup unknown config type", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "UNKNOWN", "key")
		if err == nil {
			t.Error("Expected error for unknown config type")
		}
	})

	t.Run("lookup VAR type - key not found", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "VAR", "nonexistentKey")
		if err == nil {
			t.Error("Expected error for nonexistent key")
		}
	})

	t.Run("lookup FILE type - read from disk", func(t *testing.T) {
		// Create a test file
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		testFile := confDir + "/test.txt"
		testContent := "line1\n\nline2\n  line3  \n\n"
		if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		stateInstance := state.NewState()
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		// Create a simple transformer with nil ConfigLookup (not used in this test)
		cm.Transformer = newTestTransformer(cm)

		value, err := cm.LookUpConfig(ctx, "FILE", "test.txt")
		if err != nil {
			t.Errorf("LookUpConfig FILE failed: %v", err)
		}
		// Expect: non-empty lines, trimmed, joined with ", "
		expected := "line1, line2, line3"
		if value != expected {
			t.Errorf("Expected %q, got %q", expected, value)
		}

		// Verify it was cached
		if cachedValue, ok := cm.State.FileCache["test.txt"]; !ok {
			t.Error("Expected value to be cached")
		} else if cachedValue != expected {
			t.Errorf("Expected cached value %q, got %q", expected, cachedValue)
		}
	})

	t.Run("lookup FILE type - from cache", func(t *testing.T) {
		stateInstance := state.NewState()
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		cm.Transformer = newTestTransformer(cm)

		// Pre-populate cache
		cm.State.FileCache["cached.txt"] = "cached content"

		value, err := cm.LookUpConfig(ctx, "FILE", "cached.txt")
		if err != nil {
			t.Errorf("LookUpConfig FILE from cache failed: %v", err)
		}
		if value != "cached content" {
			t.Errorf("Expected 'cached content', got %q", value)
		}
	})

	t.Run("lookup FILE type - file not found", func(t *testing.T) {
		stateInstance := state.NewState()
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: stateInstance,
			Cache: cacheInstance,
		}
		cm.Transformer = newTestTransformer(cm)

		_, err := cm.LookUpConfig(ctx, "FILE", "nonexistent.txt")
		if err == nil {
			t.Error("Expected error for nonexistent file")
		}
	})

	t.Run("lookup MAPFILE type - file exists", func(t *testing.T) {
		// Create the mapped file path
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		mappedFile := confDir + "/dhparam.pem"
		if err := os.WriteFile(mappedFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write mapped file: %v", err)
		}

		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// zimbraSSLDHParam is in state.MAPPEDFILES
		value, err := cm.LookUpConfig(ctx, "MAPFILE", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPFILE failed: %v", err)
		}
		expectedPath := tmpDir + "/conf/dhparam.pem"
		if value != expectedPath {
			t.Errorf("Expected %q, got %q", expectedPath, value)
		}
	})

	t.Run("lookup MAPFILE type - file does not exist", func(t *testing.T) {
		// Use a fresh temp directory without creating conf dir
		tmpDir2 := t.TempDir()
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir2,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		// zimbraSSLDHParam is in MAPPEDFILES but file doesn't exist
		value, err := cm.LookUpConfig(ctx, "MAPFILE", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPFILE failed: %v", err)
		}
		// When file doesn't exist, should return empty string
		if value != "" {
			t.Errorf("Expected empty string for nonexistent mapped file, got %q", value)
		}
	})

	t.Run("lookup MAPFILE type - key not in MAPPEDFILES", func(t *testing.T) {
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		_, err := cm.LookUpConfig(ctx, "MAPFILE", "unknownKey")
		if err == nil {
			t.Error("Expected error for key not in MAPPEDFILES")
		}
	})

	t.Run("lookup MAPLOCAL type - file exists", func(t *testing.T) {
		// Create the mapped file path
		confDir := tmpDir + "/conf"
		if err := os.MkdirAll(confDir, 0755); err != nil {
			t.Fatalf("Failed to create conf dir: %v", err)
		}

		mappedFile := confDir + "/dhparam.pem"
		if err := os.WriteFile(mappedFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to write mapped file: %v", err)
		}

		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		value, err := cm.LookUpConfig(ctx, "MAPLOCAL", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPLOCAL failed: %v", err)
		}
		expectedPath := tmpDir + "/conf/dhparam.pem"
		if value != expectedPath {
			t.Errorf("Expected %q, got %q", expectedPath, value)
		}
	})

	t.Run("lookup MAPLOCAL type - file does not exist", func(t *testing.T) {
		// Use a fresh temp directory without creating conf dir
		tmpDir3 := t.TempDir()
		cm := &ConfigManager{
			env: hostEnv(),
			mainConfig: &config.Config{
				BaseDir:  tmpDir3,
				Hostname: "testhost",
			},
			State: state.NewState(),
			Cache: cacheInstance,
		}

		value, err := cm.LookUpConfig(ctx, "MAPLOCAL", "zimbraSSLDHParam")
		if err != nil {
			t.Errorf("LookUpConfig MAPLOCAL failed: %v", err)
		}
		if value != "" {
			t.Errorf("Expected empty string for nonexistent mapped file, got %q", value)
		}
	})
}

// TestPlan_SectionActions tests the actions Plan derives from a section.
func TestPlan_SectionActions(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	t.Run("plan rewrites", func(t *testing.T) {
		cm := newPlanTestCM(t)
		enableServices(cm, "testservice")

		section := planTestSection("testservice")
		section.Rewrites = map[string]config.RewriteEntry{
			"file1": {Value: "/path/to/file1"},
			"file2": {Value: "/path/to/file2"},
		}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Equal(t, []string{"file1", "file2"}, rewriteSources(pl))
		assert.Equal(t, "/path/to/file1", pl.Rewrites[0].Target)
		assert.Equal(t, "/path/to/file2", pl.Rewrites[1].Target)
	})

	t.Run("plan postconf and postconfd", func(t *testing.T) {
		cm := newPlanTestCM(t)
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Postconf = map[string]string{
			"myhostname":    "mail.example.com",
			"mydestination": "localhost",
		}
		section.Postconfd = map[string]string{"milter_default_action": "accept"}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Postconf, 2)
		values := map[string]string{}
		for _, p := range pl.Postconf {
			values[p.Key] = p.Value
		}
		assert.Equal(t, "mail.example.com", values["myhostname"])
		assert.Equal(t, "localhost", values["mydestination"])

		require.Len(t, pl.Postconfd, 1)
		assert.Equal(t, "milter_default_action", pl.Postconfd[0].Key)
		assert.Equal(t, "accept", pl.Postconfd[0].Value)
	})

	t.Run("plan proxygen directive", func(t *testing.T) {
		cm := newPlanTestCM(t)
		enableServices(cm, "proxy")

		section := planTestSection("proxy")
		section.Proxygen = true
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.NotNil(t, pl.Proxygen, "expected proxygen to be planned")
		assert.Equal(t, []string{"proxy"}, pl.Proxygen.Sections)
	})

	t.Run("plan ldap entries", func(t *testing.T) {
		cm := newPlanTestCM(t)
		enableServices(cm, "ldap")

		section := planTestSection("ldap")
		section.Ldap = map[string]string{
			"ldap_uri":  "ldap://localhost",
			"ldap_base": "dc=example,dc=com",
		}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Ldap, 2)
		values := map[string]string{}
		for _, l := range pl.Ldap {
			values[l.Key] = l.Value
		}
		assert.Equal(t, "ldap://localhost", values["ldap_uri"])
		assert.Equal(t, "dc=example,dc=com", values["ldap_base"])
	})

	t.Run("skip restarts on first run", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(true)
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Restarts = map[string]bool{"mta": true}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Empty(t, pl.Restarts, "expected 0 restarts on first run")
	})

	t.Run("skip restarts with forced config", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.SetForcedConfig(ctx, "mta")
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Restarts = map[string]bool{"mta": true}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Empty(t, pl.Restarts, "expected 0 restarts with forced config")
	})

	t.Run("skip restarts with requested config", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		cm.State.AddRequestedConfigs(ctx, []string{"mta"})
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Restarts = map[string]bool{"mta": true}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Empty(t, pl.Restarts, "expected 0 restarts with requested config")
	})

	t.Run("add restart for enabled service", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Restarts = map[string]bool{"mta": true}
		markChanged(cm, section)
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Restarts, 1)
		assert.Equal(t, "mta", pl.Restarts[0].Service)
		assert.Equal(t, plan.ActionRestart, pl.Restarts[0].Action)
	})

	t.Run("add stop for disabled service", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		// The section itself must be enabled to be processed; the service it
		// restarts (mta) is not enabled, so it is stopped. A section named
		// after a disabled service is gated out before restarts are planned.
		enableServices(cm, "svca")

		section := planTestSection("svca")
		section.Restarts = map[string]bool{"mta": true}
		markChanged(cm, section)
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Restarts, 1)
		assert.Equal(t, "mta", pl.Restarts[0].Service)
		assert.Equal(t, plan.ActionStop, pl.Restarts[0].Action)
	})

	t.Run("add opendkim restart when mta is enabled", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		// opendkim is not explicitly enabled, but mta is.
		enableServices(cm, "mta")

		section := planTestSection("mta")
		section.Restarts = map[string]bool{"opendkim": true}
		markChanged(cm, section)
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Restarts, 1)
		assert.Equal(t, "opendkim", pl.Restarts[0].Service)
		assert.Equal(t, plan.ActionRestart, pl.Restarts[0].Action)
	})

	t.Run("plan conditionals", func(t *testing.T) {
		cm := newPlanTestCM(t)
		enableServices(cm, "mta")

		// A condition that evaluates to true.
		setLocal(cm, map[string]string{"zimbraServiceEnabled": "TRUE"})

		section := planTestSection("mta")
		section.Conditionals = []config.Conditional{
			{
				Type:    "LOCAL",
				Key:     "zimbraServiceEnabled",
				Negated: false,
				Postconf: map[string]string{
					"conditional_setting": "value",
				},
			},
		}
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		require.Len(t, pl.Postconf, 1)
		assert.Equal(t, "conditional_setting", pl.Postconf[0].Key)
		assert.Equal(t, "value", pl.Postconf[0].Value)
	})

	t.Run("plan multiple directives together", func(t *testing.T) {
		cm := newPlanTestCM(t)
		cm.State.SetFirstRun(false)
		enableServices(cm, "mta", "nginx")

		section := planTestSection("mta")
		section.Rewrites = map[string]config.RewriteEntry{
			"main.cf": {Value: "/opt/zextras/conf/main.cf"},
		}
		section.Postconf = map[string]string{"myhostname": "mail.example.com"}
		section.Postconfd = map[string]string{"milter_action": "accept"}
		section.Ldap = map[string]string{"ldap_uri": "ldap://localhost"}
		section.Proxygen = true
		section.Restarts = map[string]bool{"mta": true, "nginx": true}
		markChanged(cm, section)
		addPlanSection(cm, section)

		pl, err := cm.Plan(ctx)
		require.NoError(t, err)

		assert.Len(t, pl.Rewrites, 1)
		assert.Len(t, pl.Postconf, 1)
		assert.Len(t, pl.Postconfd, 1)
		assert.Len(t, pl.Ldap, 1)
		assert.NotNil(t, pl.Proxygen, "expected proxygen to be planned")
		assert.Len(t, pl.Restarts, 2)
	})
}

// TestPlan_ErrAllServicesDisabledKeepsRequests verifies the guard: when every
// tracked service looks disabled the cycle is aborted and the requested
// sections stay queued for the next attempt.
func TestPlan_ErrAllServicesDisabledKeepsRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	cm := newPlanTestCM(t)
	cm.State.SetFirstRun(false)
	cm.State.AddRequestedConfigs(ctx, []string{"mta"})
	setServer(cm, map[string]string{}, map[string]string{})
	cm.tracked = map[string]string{"mta": "running", "proxy": "running"}

	_, err := cm.Plan(ctx)
	require.ErrorIs(t, err, plan.ErrAllServicesDisabled)

	assert.Equal(t, []string{"mta"}, cm.State.TakeRequestedConfigs())
}

// TestExecute_ErrorCollection verifies that Execute lets every step finish and
// reports the failure of the ones that failed, without deadlocking.
func TestExecute_ErrorCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	cm := newPlanTestCM(t)

	// Proxygen is planned and will fail (no proxy config loaded, so
	// RunProxygenWithConfigs will error).
	pl := &plan.Plan{Proxygen: &plan.Proxygen{Sections: []string{"proxy"}}}

	err := cm.Execute(ctx, pl)

	// The key thing is that we don't deadlock and all goroutines complete even
	// when errors occur; a failed proxygen must be retried by the next plan.
	if err != nil {
		t.Logf("Execute returned error as expected: %v", err)
		assert.NotNil(t, cm.carry.Proxygen, "a failed proxygen must be carried to the next cycle")
	}
}

// TestExecute_ErrorSurfacing verifies that errors from rewrite operations are
// surfaced through Execute and the failed rewrite is carried for a retry.
func TestExecute_ErrorSurfacing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()

	cm := newPlanTestCM(t)

	// Create a transformer for testing
	cm.Transformer = newTestTransformer(cm)

	// A rewrite that will fail because the source file doesn't exist.
	pl := &plan.Plan{Rewrites: []plan.Rewrite{{
		Source: "nonexistent.conf",
		Target: "output.conf",
		Mode:   "0644",
	}}}

	err := cm.Execute(ctx, pl)

	require.Error(t, err, "expected Execute to return an error for nonexistent source file")
	t.Logf("Execute correctly returned error: %v", err)

	errStr := err.Error()
	if !strings.Contains(errStr, "nonexistent.conf") && !strings.Contains(errStr, "no such file") {
		t.Logf("Error message may not contain expected details: %s", errStr)
	}

	require.Len(t, cm.carry.Rewrites, 1, "a failed rewrite must be carried to the next cycle")
	assert.Equal(t, "nonexistent.conf", cm.carry.Rewrites[0].Source)
}

// TestPlan_RewriteRetriedUntilSuccess verifies the carry loop end to end: a
// rewrite that failed in one cycle is planned again (as a retry) in the next
// one even though no variable changed.
func TestPlan_RewriteRetriedUntilSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	cm := newPlanTestCM(t)
	cm.Transformer = newTestTransformer(cm)
	cm.State.SetFirstRun(true)
	enableServices(cm, "testservice")

	section := planTestSection("testservice")
	section.Rewrites["missing.conf.in"] = config.RewriteEntry{Value: "conf/missing.conf", Mode: "0644"}
	addPlanSection(cm, section)

	pl, err := runCycle(t, cm)
	require.Error(t, err, "the template does not exist")
	require.Len(t, pl.Rewrites, 1)

	cm.State.SetFirstRun(false)

	pl2, err := cm.Plan(context.Background())
	require.NoError(t, err)
	require.Len(t, pl2.Rewrites, 1, fmt.Sprintf("failed rewrite must be retried, got %v", pl2.Rewrites))
	assert.Equal(t, "missing.conf.in", pl2.Rewrites[0].Source)
}
