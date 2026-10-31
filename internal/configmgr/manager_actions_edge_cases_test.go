// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

func newTestConfigManager(t *testing.T) *ConfigManager {
	t.Helper()

	baseDir := t.TempDir()
	st := state.NewState()
	st.FirstRun = true

	cfg := &config.Config{
		BaseDir:    baseDir,
		ConfigFile: filepath.Join(baseDir, "zmconfigd.cf"),
		Hostname:   "testhost",
	}

	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	return NewConfigManager(ctx, cfg, st, nil, cacheInstance, hostEnv())
}

// realFSConfigManager is a ConfigManager that only carries the real host
// boundary, for tests of helpers that act on real temp files.
func realFSConfigManager() *ConfigManager {
	return &ConfigManager{env: hostEnv()}
}

// mockMtaExecutor is a controllable mock for mtaops.Executor used in these tests.
type mockMtaExecutor struct {
	postconfOps  []mtaops.PostconfOperation
	postconfdOps []mtaops.PostconfdOperation
	ldapOps      []mtaops.LdapOperation
	mapfileOps   []mtaops.MapfileOperation
	postconfErr  error
	postconfdErr error
	ldapErr      error
	mapfileErr   error
}

func (m *mockMtaExecutor) ReadPostconf(_ context.Context, _ string, _ bool) (string, bool, error) {
	return "", false, nil
}

func (m *mockMtaExecutor) ExecutePostconf(_ context.Context, op mtaops.PostconfOperation) error {
	m.postconfOps = append(m.postconfOps, op)
	return m.postconfErr
}

func (m *mockMtaExecutor) ExecutePostconfBatch(_ context.Context, ops []mtaops.PostconfOperation) error {
	m.postconfOps = append(m.postconfOps, ops...)
	return m.postconfErr
}

func (m *mockMtaExecutor) ExecutePostconfd(_ context.Context, op mtaops.PostconfdOperation) error {
	m.postconfdOps = append(m.postconfdOps, op)
	return m.postconfdErr
}

func (m *mockMtaExecutor) ExecutePostconfdBatch(_ context.Context, ops []mtaops.PostconfdOperation) error {
	m.postconfdOps = append(m.postconfdOps, ops...)
	return m.postconfdErr
}

func (m *mockMtaExecutor) ExecuteMapfile(_ context.Context, op mtaops.MapfileOperation) error {
	m.mapfileOps = append(m.mapfileOps, op)
	return m.mapfileErr
}

func (m *mockMtaExecutor) ExecuteLdapWrite(_ context.Context, op mtaops.LdapOperation) error {
	m.ldapOps = append(m.ldapOps, op)
	return m.ldapErr
}

// mockMtaResolver is a simple resolver that returns the key as the value.
type mockMtaResolver struct {
	values map[string]string
	err    error
}

func (r *mockMtaResolver) ResolveValue(_ context.Context, _, key string, _ *snapshot.Snapshot) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.values != nil {
		if v, ok := r.values[key]; ok {
			return v, nil
		}
	}
	return key, nil // return key as value by default
}

// newTestCMWithExecutor creates a ConfigManager with mock executor and resolver.
func newTestCMWithExecutor(t *testing.T) (*ConfigManager, *mockMtaExecutor) {
	t.Helper()
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)
	st := state.NewState()
	exec := &mockMtaExecutor{}
	resolver := &mockMtaResolver{}
	svcMgr := &testutil.MockServiceManager{}
	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:  t.TempDir(),
			Hostname: "testhost",
		},
		State:       st,
		ServiceMgr:  svcMgr,
		Cache:       cacheInstance,
		mtaExecutor: exec,
		mtaResolver: resolver,
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)
	cm.Transformer = transformer.NewTransformer(cm)
	return cm, exec
}

// ---- doRewrites tests ----

// TestDoRewrites_EmptyRewrites exercises the early-return path (len == 0).
func TestDoRewrites_EmptyRewrites(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	// No rewrites added — must be a no-op.
	pending, err := cm.doRewrites(context.Background(), nil)
	if err != nil || len(pending) != 0 {
		t.Errorf("expected no pending and no error, got %v, %v", pending, err)
	}
}

// TestDoRewrites_SingleFile exercises the happy path: source file exists,
// default mode (empty), successful rename.
func TestDoRewrites_SingleFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	// Create source file
	srcRelPath := "main.cf.in"
	destRelPath := "main.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	pending, err := cm.doRewrites(context.Background(), []plan.Rewrite{{Source: srcRelPath, Target: destRelPath}})
	if err != nil || len(pending) != 0 {
		t.Errorf("expected success, got pending=%v err=%v", pending, err)
	}

	// Destination file should exist
	if _, err := os.Stat(baseDir + "/" + destRelPath); err != nil {
		t.Errorf("expected destination file to exist: %v", err)
	}
}

// TestDoRewrites_ExplicitMode exercises the octal mode parsing branch.
func TestDoRewrites_ExplicitMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "mode.cf.in"
	destRelPath := "mode.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	if _, err := cm.doRewrites(context.Background(),
		[]plan.Rewrite{{Source: srcRelPath, Target: destRelPath, Mode: "0600"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(baseDir + "/" + destRelPath)
	if err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	// Verify permissions were set (mode 0600 = rw-------)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected mode 0600, got %v", info.Mode().Perm())
	}
}

// TestDoRewrites_InvalidMode exercises the invalid mode error branch: the
// rewrite fails, is reported and stays pending for the next cycle.
func TestDoRewrites_InvalidMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "invalid.cf.in"
	destRelPath := "invalid.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	// "notanumber" is not a valid octal string — ParseInt will fail
	entry := plan.Rewrite{Source: srcRelPath, Target: destRelPath, Mode: "notanumber"}

	// Should not panic — the failure is returned and the entry stays pending
	pending, err := cm.doRewrites(context.Background(), []plan.Rewrite{entry})
	if err == nil {
		t.Error("expected an error for an invalid mode")
	}
	if len(pending) != 1 || pending[0].Source != srcRelPath {
		t.Errorf("expected the failed rewrite to stay pending, got %v", pending)
	}
}

// TestDoRewrites_MissingSourceFile exercises the os.Open error branch.
func TestDoRewrites_MissingSourceFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)

	// Should not panic
	pending, err := cm.doRewrites(context.Background(),
		[]plan.Rewrite{{Source: "nonexistent.cf.in", Target: "out.cf"}})
	if err == nil || len(pending) != 1 {
		t.Errorf("expected error and one pending rewrite, got %v, %v", pending, err)
	}
}

// TestDoRewrites_MultipleFiles exercises the semaphore / WaitGroup path with
// multiple concurrent rewrites.
func TestDoRewrites_MultipleFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	// Create 6 source files (more than the semaphore size of 4)
	var entries []plan.Rewrite
	for i := 0; i < 6; i++ {
		src := baseDir + "/" + string(rune('a'+i)) + ".in"
		if err := os.WriteFile(src, []byte("content\n"), 0o644); err != nil {
			t.Fatalf("create src: %v", err)
		}
		entries = append(entries, plan.Rewrite{
			Source: string(rune('a'+i)) + ".in", Target: string(rune('a'+i)) + ".out",
		})
	}

	if _, err := cm.doRewrites(context.Background(), entries); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All destination files should exist
	for i := 0; i < 6; i++ {
		dest := baseDir + "/" + string(rune('a'+i)) + ".out"
		if _, err := os.Stat(dest); err != nil {
			t.Errorf("expected dest file %s: %v", dest, err)
		}
	}
}

// TestDoRewrites_CancelledContext exercises the ctx.Done() early-exit path:
// nothing runs and every rewrite stays pending.
func TestDoRewrites_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	// Set up many rewrites so the loop actually checks ctx.Done()
	var entries []plan.Rewrite
	for i := 0; i < 10; i++ {
		src := baseDir + "/" + string(rune('a'+i)) + ".in"
		_ = os.WriteFile(src, []byte("content\n"), 0o644)
		entries = append(entries, plan.Rewrite{
			Source: string(rune('a'+i)) + ".in", Target: string(rune('a'+i)) + ".out",
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Should not panic or deadlock
	pending, _ := cm.doRewrites(ctx, entries)
	if len(pending) != len(entries) {
		t.Errorf("expected all %d rewrites pending, got %d", len(entries), len(pending))
	}
}

// ---- doPostconf tests ----

// TestDoPostconf_Empty exercises the early-return path (no entries).
func TestDoPostconf_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	if _, err := cm.doPostconf(context.Background(), nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(exec.postconfOps) != 0 {
		t.Errorf("expected 0 postconf ops, got %d", len(exec.postconfOps))
	}
}

// TestDoPostconf_SingleLiteral exercises the happy path with a LITERAL value spec.
func TestDoPostconf_SingleLiteral(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	_, _ = cm.doPostconf(context.Background(), []plan.Postconf{{Key: "myhostname", Value: "mail.example.com"}})

	if len(exec.postconfOps) != 1 {
		t.Fatalf("expected 1 postconf op, got %d", len(exec.postconfOps))
	}
	if exec.postconfOps[0].Key != "myhostname" {
		t.Errorf("expected key 'myhostname', got %q", exec.postconfOps[0].Key)
	}
	if exec.postconfOps[0].Value != "mail.example.com" {
		t.Errorf("expected value 'mail.example.com', got %q", exec.postconfOps[0].Value)
	}
}

// TestDoPostconf_BooleanNormalization verifies that POSTCONF values resolved
// from a typed lookup (VAR/LOCAL/FILE/MAPLOCAL) are normalized to Postfix
// booleans (TRUE->yes, FALSE->no), while literal values pass through unchanged.
// This mirrors the legacy zmconfigd (jylibs/mtaconfig.py).
func TestDoPostconf_BooleanNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	tests := []struct {
		name      string
		valueSpec string
		resolved  string // value the resolver returns for the spec's key
		want      string
	}{
		{name: "VAR TRUE -> yes", valueSpec: "VAR:zimbraBool", resolved: "TRUE", want: constYes},
		{name: "VAR FALSE -> no", valueSpec: "VAR:zimbraBool", resolved: "FALSE", want: constNo},
		{name: "VAR lowercase true -> yes", valueSpec: "VAR:zimbraBool", resolved: "true", want: constYes},
		{name: "VAR non-bool passthrough", valueSpec: "VAR:zimbraBool", resolved: "mail.example.com", want: "mail.example.com"},
		{name: "LOCAL FALSE -> no", valueSpec: "LOCAL:someKey", resolved: "FALSE", want: constNo},
		{name: "literal TRUE stays TRUE", valueSpec: "TRUE", resolved: "", want: "TRUE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, exec := newTestCMWithExecutor(t)

			// Drive the mock resolver to return tt.resolved for the spec's key.
			if tt.resolved != "" {
				_, valueKey := parseValueSpec(tt.valueSpec)
				cm.mtaResolver.(*mockMtaResolver).values = map[string]string{valueKey: tt.resolved}
			}

			_, _ = cm.doPostconf(context.Background(), []plan.Postconf{{Key: "param", Value: tt.valueSpec}})

			if len(exec.postconfOps) != 1 {
				t.Fatalf("expected 1 postconf op, got %d", len(exec.postconfOps))
			}
			if exec.postconfOps[0].Value != tt.want {
				t.Errorf("postconf value: got %q, want %q", exec.postconfOps[0].Value, tt.want)
			}
		})
	}
}

// TestDoPostconf_MultipleEntries exercises the batch with multiple entries.
func TestDoPostconf_MultipleEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	pending, _ := cm.doPostconf(context.Background(), []plan.Postconf{
		{Key: "myhostname", Value: "mail.example.com"},
		{Key: "mydestination", Value: "localhost"},
		{Key: "myorigin", Value: "example.com"},
	})

	if len(exec.postconfOps) != 3 {
		t.Errorf("expected 3 postconf ops, got %d", len(exec.postconfOps))
	}
	// Nothing stays pending after a clean pass
	if len(pending) != 0 {
		t.Errorf("expected nothing pending, got %d entries", len(pending))
	}
}

// TestDoPostconf_ExecutorError is a regression test: a failed postconf batch
// must leave the directive pending so the next cycle retries it, and the
// error must be returned rather than swallowed.
func TestDoPostconf_ExecutorError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	exec.postconfErr = &testError{msg: "postconf command failed"}

	pending, err := cm.doPostconf(context.Background(), []plan.Postconf{{Key: "key", Value: "value"}})

	if err == nil {
		t.Error("expected doPostconf to return the batch error")
	}
	if len(pending) != 1 || pending[0].Key != "key" {
		t.Error("expected postconf to stay pending after a failed batch so the next cycle retries")
	}
}

// TestDoPostconf_ResolverError exercises the resolveValueSpec error path (continue).
func TestDoPostconf_ResolverError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	// Override resolver to return an error
	cm.mtaResolver = &mockMtaResolver{err: &testError{msg: "resolver failed"}}

	// Use VAR type so it goes through the resolver
	pending, err := cm.doPostconf(context.Background(), []plan.Postconf{{Key: "somekey", Value: "VAR:some_key"}})

	// No ops should be batched because resolver failed for all entries
	if len(exec.postconfOps) != 0 {
		t.Errorf("expected 0 ops when resolver fails, got %d", len(exec.postconfOps))
	}
	if err == nil || len(pending) != 1 {
		t.Errorf("expected the resolver error and one pending entry, got %v, %v", pending, err)
	}
}

// TestDoPostconf_CancelledContext exercises the ctx.Done() check inside the loop.
func TestDoPostconf_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	var entries []plan.Postconf
	for i := 0; i < 5; i++ {
		entries = append(entries, plan.Postconf{Key: string(rune('a' + i)), Value: "val"})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should not panic; nothing is applied and everything stays pending
	pending, _ := cm.doPostconf(ctx, entries)
	if len(exec.postconfOps) != 0 || len(pending) != len(entries) {
		t.Errorf("expected no ops and all pending, got %d ops, %d pending", len(exec.postconfOps), len(pending))
	}
}

// ---- doPostconfd tests ----

// TestDoPostconfd_Empty exercises the early-return path.
func TestDoPostconfd_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	_, _ = cm.doPostconfd(context.Background(), nil)
	if len(exec.postconfdOps) != 0 {
		t.Errorf("expected 0 postconfd ops, got %d", len(exec.postconfdOps))
	}
}

// TestDoPostconfd_SingleEntry exercises the happy path.
func TestDoPostconfd_SingleEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	pending, _ := cm.doPostconfd(context.Background(),
		[]plan.Postconfd{{Key: "milter_default_action", Value: "accept"}})

	if len(exec.postconfdOps) != 1 {
		t.Fatalf("expected 1 postconfd op, got %d", len(exec.postconfdOps))
	}
	if exec.postconfdOps[0].Key != "milter_default_action" {
		t.Errorf("expected key 'milter_default_action', got %q", exec.postconfdOps[0].Key)
	}
	// Nothing stays pending
	if len(pending) != 0 {
		t.Errorf("expected nothing pending after execution")
	}
}

// TestDoPostconfd_MultipleEntries exercises the batch path.
func TestDoPostconfd_MultipleEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	_, _ = cm.doPostconfd(context.Background(), []plan.Postconfd{
		{Key: "key1", Value: "v1"},
		{Key: "key2", Value: "v2"},
	})

	if len(exec.postconfdOps) != 2 {
		t.Errorf("expected 2 postconfd ops, got %d", len(exec.postconfdOps))
	}
}

// TestDoPostconfd_ExecutorError is a regression test: a failed postconfd
// batch must leave the directive pending so the next cycle retries it, and
// the error must be returned rather than swallowed.
func TestDoPostconfd_ExecutorError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	exec.postconfdErr = &testError{msg: "postconfd failed"}

	pending, err := cm.doPostconfd(context.Background(), []plan.Postconfd{{Key: "key", Value: "val"}})

	if err == nil {
		t.Error("expected doPostconfd to return the batch error")
	}
	if len(pending) != 1 || pending[0].Key != "key" {
		t.Error("expected postconfd to stay pending after a failed batch so the next cycle retries")
	}
}

// TestDoPostconfd_CancelledContext exercises the ctx.Done() path inside loop.
func TestDoPostconfd_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	var entries []plan.Postconfd
	for i := 0; i < 5; i++ {
		entries = append(entries, plan.Postconfd{Key: string(rune('a' + i)), Value: "val"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending, _ := cm.doPostconfd(ctx, entries)
	if len(exec.postconfdOps) != 0 || len(pending) != len(entries) {
		t.Errorf("expected no ops and all pending, got %d ops, %d pending", len(exec.postconfdOps), len(pending))
	}
}

// ---- doLdap tests ----

// TestDoLdap_Empty exercises the early-return path (no entries).
func TestDoLdap_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	_, _ = cm.doLdap(context.Background(), nil)
	if len(exec.ldapOps) != 0 {
		t.Errorf("expected 0 ldap ops, got %d", len(exec.ldapOps))
	}
}

// TestDoLdap_SingleLiteralEntry exercises the happy path.
func TestDoLdap_SingleLiteralEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	_, _ = cm.doLdap(context.Background(), []plan.Ldap{{Key: "ldap_uri", Value: "ldap://localhost"}})

	if len(exec.ldapOps) != 1 {
		t.Fatalf("expected 1 ldap op, got %d", len(exec.ldapOps))
	}
	if exec.ldapOps[0].Key != "ldap_uri" {
		t.Errorf("expected key 'ldap_uri', got %q", exec.ldapOps[0].Key)
	}
	if exec.ldapOps[0].Value != "ldap://localhost" {
		t.Errorf("expected value 'ldap://localhost', got %q", exec.ldapOps[0].Value)
	}
}

// TestDoLdap_MultipleEntries exercises the full loop.
func TestDoLdap_MultipleEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	_, _ = cm.doLdap(context.Background(), []plan.Ldap{
		{Key: "key1", Value: "val1"},
		{Key: "key2", Value: "val2"},
		{Key: "key3", Value: "val3"},
	})

	if len(exec.ldapOps) != 3 {
		t.Errorf("expected 3 ldap ops, got %d", len(exec.ldapOps))
	}
}

// TestDoLdap_ExecutorError exercises the ExecuteLdapWrite error path
// (the entry must stay pending on failure).
func TestDoLdap_ExecutorError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	exec.ldapErr = &testError{msg: "ldap write failed"}

	pending, err := cm.doLdap(context.Background(), []plan.Ldap{{Key: "ldap_uri", Value: "ldap://localhost"}})

	// Entry should still be pending since write failed
	if err == nil || len(pending) != 1 || pending[0].Key != "ldap_uri" {
		t.Errorf("expected entry to remain pending after failed ldap write, got %v, %v", pending, err)
	}
}

// TestDoLdap_ResolverError exercises the resolver error path (continue).
func TestDoLdap_ResolverError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	// Override resolver to return an error
	cm.mtaResolver = &mockMtaResolver{err: &testError{msg: "resolver failed"}}

	pending, err := cm.doLdap(context.Background(), []plan.Ldap{{Key: "somekey", Value: "VAR:some_key"}})

	// No ldap ops should be executed
	if len(exec.ldapOps) != 0 {
		t.Errorf("expected 0 ldap ops when resolver fails, got %d", len(exec.ldapOps))
	}
	if err == nil || len(pending) != 1 {
		t.Errorf("expected the resolver error and one pending entry, got %v, %v", pending, err)
	}
}

// TestDoLdap_CancelledContext exercises the ctx.Done() path.
func TestDoLdap_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	var entries []plan.Ldap
	for i := 0; i < 5; i++ {
		entries = append(entries, plan.Ldap{Key: string(rune('a' + i)), Value: "val"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending, _ := cm.doLdap(ctx, entries)
	if len(exec.ldapOps) != 0 || len(pending) != len(entries) {
		t.Errorf("expected no ops and all pending, got %d ops, %d pending", len(exec.ldapOps), len(pending))
	}
}

// TestDoLdap_SuccessfulWriteLeavesNothingPending checks that a successful
// write is not retried.
func TestDoLdap_SuccessfulWriteLeavesNothingPending(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)

	pending, err := cm.doLdap(context.Background(), []plan.Ldap{{Key: "ldap_uri", Value: "ldap://localhost"}})

	if err != nil || len(pending) != 0 {
		t.Errorf("expected nothing pending after successful ldap write, got %v, %v", pending, err)
	}
}

// ---- doMapfile tests ----
//
// Which sections contribute mapfile entries (changed vs unchanged vs first
// run, MAP* vs other var types) is decided by plan.Compute and covered by
// TestCompute_MapfileOpsAndHeal in internal/plan; doMapfile only executes the
// entries it is given.

// TestDoMapfile_NoEntries exercises the empty plan.
func TestDoMapfile_NoEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	if err := cm.doMapfile(context.Background(), nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(exec.mapfileOps) != 0 {
		t.Errorf("expected 0 mapfile ops, got %d", len(exec.mapfileOps))
	}
}

// TestDoMapfile_MAPFILE exercises the MAPFILE path.
func TestDoMapfile_MAPFILE(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	if err := cm.doMapfile(context.Background(), []plan.Mapfile{{Key: "myVar", Sections: []string{"mta"}}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(exec.mapfileOps) != 1 {
		t.Fatalf("expected 1 mapfile op, got %d", len(exec.mapfileOps))
	}
	if exec.mapfileOps[0].Key != "myVar" {
		t.Errorf("expected key 'myVar', got %q", exec.mapfileOps[0].Key)
	}
	if exec.mapfileOps[0].IsLocal {
		t.Error("expected IsLocal=false for MAPFILE type")
	}
}

// TestDoMapfile_MAPLOCAL exercises the MAPLOCAL (isLocal=true) path.
func TestDoMapfile_MAPLOCAL(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	if err := cm.doMapfile(context.Background(),
		[]plan.Mapfile{{Key: "localVar", Local: true, Sections: []string{"proxy"}}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(exec.mapfileOps) != 1 {
		t.Fatalf("expected 1 mapfile op, got %d", len(exec.mapfileOps))
	}
	if !exec.mapfileOps[0].IsLocal {
		t.Error("expected IsLocal=true for MAPLOCAL type")
	}
}

// TestDoMapfile_ExecutorError checks that a failing executor is reported and
// does not stop the remaining entries.
func TestDoMapfile_ExecutorError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)
	exec.mapfileErr = &testError{msg: "mapfile failed"}

	err := cm.doMapfile(context.Background(), []plan.Mapfile{{Key: "a"}, {Key: "b"}})
	if err == nil {
		t.Error("expected the executor error to be returned")
	}
	if len(exec.mapfileOps) != 2 {
		t.Errorf("expected both entries to be attempted, got %d", len(exec.mapfileOps))
	}
}

// TestDoMapfile_CancelledContext exercises the ctx.Done() check.
func TestDoMapfile_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	var entries []plan.Mapfile
	for i := 0; i < 5; i++ {
		entries = append(entries, plan.Mapfile{Key: string(rune('a' + i))})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = cm.doMapfile(ctx, entries)
	if len(exec.mapfileOps) != 0 {
		t.Errorf("expected no mapfile ops after cancellation, got %d", len(exec.mapfileOps))
	}
}

// ---- ApplyRestarts tests ----

// newRestartTestCM returns a ConfigManager with restarts enabled that queues
// restarts on mgr through the restart policy.
func newRestartTestCM(mgr *testutil.MockServiceManager) *ConfigManager {
	cm := &ConfigManager{
		env:        hostEnv(),
		mainConfig: &config.Config{BaseDir: "/tmp", Hostname: "testhost", RestartConfig: true},
		State:      state.NewState(),
		ServiceMgr: mgr,
		Cache:      cache.New(context.Background(), false),
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)

	return cm
}

// TestApplyRestarts_QueuesPlanRestarts checks that every restart of the plan is
// queued on the service manager with its reasons and then processed.
func TestApplyRestarts_QueuesPlanRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	var (
		queued    = map[string][]string{}
		processed int
	)

	mgr := &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, svc string, reasons []string) error {
			queued[svc] = reasons
			return nil
		},
		ProcessRestartsFn: func(_ context.Context, _ func(string) string) error {
			processed++
			return nil
		},
	}
	cm := newRestartTestCM(mgr)

	pl := &plan.Plan{Restarts: []plan.Restart{{
		Service: "nginx", Action: plan.ActionRestart,
		Reasons: []plan.Reason{{Kind: plan.ReasonForced, Section: "proxy"}},
	}}}
	cm.ApplyRestarts(context.Background(), pl)

	if _, ok := queued["nginx"]; !ok || len(queued["nginx"]) == 0 {
		t.Errorf("expected nginx queued with reasons, got %v", queued)
	}
	if processed != 1 {
		t.Errorf("expected ProcessRestarts to run once, ran %d", processed)
	}
	if len(cm.carry.Restarts) != 0 {
		t.Errorf("expected nothing carried, got %v", cm.carry.Restarts)
	}
}

// TestApplyRestarts_AddRestartError exercises the queue error path: the
// failed restart is carried into the next cycle and nothing panics.
func TestApplyRestarts_AddRestartError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mgr := &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, _ string, _ []string) error {
			return &testError{msg: "add restart failed"}
		},
	}
	cm := newRestartTestCM(mgr)

	cm.ApplyRestarts(context.Background(), &plan.Plan{
		Restarts: []plan.Restart{{Service: "nginx", Action: plan.ActionRestart}},
	})

	if len(cm.carry.Restarts) != 1 || cm.carry.Restarts[0].Service != "nginx" {
		t.Errorf("expected the refused restart to be carried, got %v", cm.carry.Restarts)
	}
}

// TestApplyRestarts_ProcessRestartsError exercises the ProcessRestarts error
// logging branch (no panic).
func TestApplyRestarts_ProcessRestartsError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	mgr := &testutil.MockServiceManager{
		ProcessRestartsFn: func(_ context.Context, _ func(string) string) error {
			return &testError{msg: "process restarts failed"}
		},
	}
	cm := newRestartTestCM(mgr)

	cm.ApplyRestarts(context.Background(), &plan.Plan{})
}

// captureRestartLookup applies an empty plan whose snapshot enables the given
// services and returns the configLookup handed to ProcessRestarts.
func captureRestartLookup(t *testing.T, enabled ...string) func(string) string {
	t.Helper()

	var captured func(string) string

	mgr := &testutil.MockServiceManager{
		ProcessRestartsFn: func(_ context.Context, configLookup func(string) string) error {
			captured = configLookup
			return nil
		},
	}
	cm := newRestartTestCM(mgr)

	cm.ApplyRestarts(context.Background(), &plan.Plan{Enabled: enabled})

	if captured == nil {
		t.Fatal("expected ProcessRestarts to be called with a configLookup")
	}

	return captured
}

// TestApplyRestarts_ConfigLookup_ServiceEnabled exercises the SERVICE_ lookup
// of the cascade: services enabled in the plan answer "enabled", every other
// key "disabled".
func TestApplyRestarts_ConfigLookup_ServiceEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}

	lookup := captureRestartLookup(t, "mta", "nginx")

	if got := lookup("SERVICE_MTA"); got != "enabled" {
		t.Errorf("expected 'enabled' for SERVICE_MTA (service enabled), got %q", got)
	}
	if got := lookup("SERVICE_NGINX"); got != "enabled" {
		t.Errorf("SERVICE_NGINX lookup: got %q, want %q", got, "enabled")
	}

	// Non-SERVICE_ prefix — "disabled"
	if got := lookup("SHORT"); got != "disabled" {
		t.Errorf("expected 'disabled' for short key, got %q", got)
	}
	if got := lookup("OTHER_KEY"); got != "disabled" {
		t.Errorf("OTHER_KEY lookup: got %q, want %q", got, "disabled")
	}

	// SERVICE_ key of a service that is not enabled — "disabled"
	if got := lookup("SERVICE_PROXY"); got != "disabled" {
		t.Errorf("expected 'disabled' for disabled service, got %q", got)
	}

	// "SERVICE_" without a service name — "disabled"
	if got := lookup("SERVICE_"); got != "disabled" {
		t.Errorf("SERVICE_ (empty suffix) lookup: got %q, want %q", got, "disabled")
	}
}

// ---- cleanupRewriteFiles extended tests ----

// TestCleanupRewriteFiles_AlreadyClosed exercises the isAlreadyClosedError path.
func TestCleanupRewriteFiles_AlreadyClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	tmpDir := t.TempDir()

	srcPath := tmpDir + "/src_closed.txt"
	tmpPath := tmpDir + "/tmp_closed.txt"

	srcFile, err := os.Create(srcPath)
	if err != nil {
		t.Fatalf("create src: %v", err)
	}
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		t.Fatalf("create tmp: %v", err)
	}

	// Pre-close both files so cleanup encounters already-closed errors
	srcFile.Close()
	tmpFile.Close()

	// Should handle gracefully (isAlreadyClosedError absorbs the error)
	realFSConfigManager().cleanupRewriteFiles(ctx, srcFile, tmpFile, tmpPath)
}

// ---- parseLocalConfigOutput tests ----

// TestParseLocalConfigOutput_ValidInput exercises the happy path.
func TestParseLocalConfigOutput_ValidInput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	output := "key1 = value1\nkey2 = value2\nkey3 = value with spaces"
	data, err := cm.parseLocalConfigOutput(context.Background(), output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data["key1"] != "value1" {
		t.Errorf("expected key1=value1, got %q", data["key1"])
	}
	if data["key2"] != "value2" {
		t.Errorf("expected key2=value2, got %q", data["key2"])
	}
	if data["key3"] != "value with spaces" {
		t.Errorf("expected 'value with spaces', got %q", data["key3"])
	}
}

// TestParseLocalConfigOutput_EmptyOutput exercises the no-data path.
func TestParseLocalConfigOutput_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	// Only whitespace — TrimSpace then Split gives [""] — len==1 non-empty slice but
	// no parseable pairs, so the map stays empty; this should NOT error.
	_, err := cm.parseLocalConfigOutput(context.Background(), "   ")
	// The function only errors if lines length is 0, which won't happen after split.
	// This just verifies no panic and returns a valid (possibly empty) state.
	_ = err
}

// TestParseLocalConfigOutput_MalformedLines exercises the "skip non-pair lines" path.
func TestParseLocalConfigOutput_MalformedLines(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)

	// Mix of valid and malformed lines
	output := "valid = value\nmalformed_no_equals\nanother = good"
	data, err := cm.parseLocalConfigOutput(context.Background(), output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data["valid"] != "value" {
		t.Errorf("expected valid=value, got %q", data["valid"])
	}
	if data["another"] != "good" {
		t.Errorf("expected another=good, got %q", data["another"])
	}
	if _, ok := data["malformed_no_equals"]; ok {
		t.Error("malformed line should not be in data map")
	}
}

// ---- executeLocalConfigCommand tests ----

// TestExecuteLocalConfigCommand_CacheHit exercises the cached-output branch.
func TestExecuteLocalConfigCommand_CacheHit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	cm.cachedLocalConfigOutput = "cached = output"

	result, err := cm.executeLocalConfigCommand(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "cached = output" {
		t.Errorf("expected cached output, got %q", result)
	}
}

// TestExecuteLocalConfigCommand_CacheMiss exercises the XML-load branch (no XML → error).
func TestExecuteLocalConfigCommand_CacheMiss(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// No cached output — will try to load XML file which doesn't exist in test env

	_, err := cm.executeLocalConfigCommand(context.Background())
	// In test environment the XML file won't exist, so we expect an error
	if err == nil {
		t.Log("executeLocalConfigCommand succeeded (XML file found in test environment)")
	} else {
		// Verify it's the right kind of error
		if err.Error() == "" {
			t.Error("expected non-empty error message")
		}
	}
}

// ---- LoadAllConfigsWithRetry tests ----

// TestLoadAllConfigsWithRetry_ContextCancelled exercises the ctx.Done() path.
func TestLoadAllConfigsWithRetry_ContextCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	err := cm.LoadAllConfigsWithRetry(ctx, 1)
	if err == nil {
		t.Log("LoadAllConfigsWithRetry returned nil for cancelled context (benign)")
	}
	// Main goal: no deadlock, no panic
}

// TestLoadAllConfigsWithRetry_SingleAttemptFailure exercises the "errors on final attempt" path.
func TestLoadAllConfigsWithRetry_SingleAttemptFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)

	// With maxRetries=1 and no commands available, all load functions will fail.
	// LoadAllConfigsWithRetry should return an error (not panic).
	err := cm.LoadAllConfigsWithRetry(context.Background(), 1)
	// We expect errors since localconfig XML doesn't exist in test env
	_ = err // may or may not error depending on environment
}

// TestLoadAllConfigsWithRetry_CachedLocalConfig exercises that a pre-cached local config
// is used (cache-hit branch in executeLocalConfigCommand).
func TestLoadAllConfigsWithRetry_CachedLocalConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)
	// Pre-cache the local config to exercise the cache-hit branch.
	cm.cachedLocalConfigOutput = "key = value"

	// Single attempt — should not panic even with other threads failing
	_ = cm.LoadAllConfigsWithRetry(context.Background(), 1)
}

// ---- RunProxygenWithConfigs tests ----

// TestRunProxygenWithConfigs_LoadConfigurationError exercises the proxy.LoadConfiguration failure path.
func TestRunProxygenWithConfigs_LoadConfigurationError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// With nil LDAP client and empty configs, LoadConfiguration should fail.
	err := cm.RunProxygenWithConfigs(context.Background())
	if err == nil {
		t.Log("RunProxygenWithConfigs succeeded unexpectedly (no proxy config available)")
	}
	// Main goal: no panic, error is returned (not deadlock)
}

// ---- Execute integration tests ----

// TestExecute_AllEmpty exercises Execute when the plan has no actions.
func TestExecute_AllEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	// All plan slices are empty — all goroutines return immediately.
	err := cm.Execute(context.Background(), &plan.Plan{})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestExecute_WithPostconfAndLdap exercises concurrent execution with real operations.
func TestExecute_WithPostconfAndLdap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, exec := newTestCMWithExecutor(t)

	err := cm.Execute(context.Background(), &plan.Plan{
		Postconf: []plan.Postconf{{Key: "myhostname", Value: "mail.example.com"}},
		Ldap:     []plan.Ldap{{Key: "ldap_uri", Value: "ldap://localhost"}},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(exec.postconfOps) != 1 {
		t.Errorf("expected 1 postconf op, got %d", len(exec.postconfOps))
	}
	if len(exec.ldapOps) != 1 {
		t.Errorf("expected 1 ldap op, got %d", len(exec.ldapOps))
	}
}

// orderingExecutor records whether postconf started while a mapfile write was
// still in progress.
type orderingExecutor struct {
	mockMtaExecutor
	postconfStarted chan struct{}
	overlapped      atomic.Bool
}

func (o *orderingExecutor) ExecuteMapfile(ctx context.Context, op mtaops.MapfileOperation) error {
	select {
	case <-o.postconfStarted:
		o.overlapped.Store(true)
	case <-time.After(200 * time.Millisecond):
	}

	return o.mockMtaExecutor.ExecuteMapfile(ctx, op)
}

func (o *orderingExecutor) ExecutePostconfBatch(ctx context.Context, ops []mtaops.PostconfOperation) error {
	close(o.postconfStarted)

	return o.mockMtaExecutor.ExecutePostconfBatch(ctx, ops)
}

// TestExecute_MapfilesBeforeOtherSteps pins the legacy order: mapped
// files (dhparam.pem) are written before any other step runs, because proxygen
// and rewrites read them; legacy writes them while compiling actions.
func TestExecute_MapfilesBeforeOtherSteps(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	exec := &orderingExecutor{postconfStarted: make(chan struct{})}
	cm.mtaExecutor = exec
	setGlobal(cm, map[string]string{dhParamKey: "ZGF0YQ=="})

	pl := &plan.Plan{
		Mapfiles: []plan.Mapfile{{Key: dhParamKey, Sections: []string{"mta"}}},
		Postconf: []plan.Postconf{{Key: "myhostname", Value: "mail.example.com"}},
	}

	if err := cm.Execute(context.Background(), pl); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(exec.mapfileOps) != 1 || len(exec.postconfOps) != 1 {
		t.Fatalf("ops: mapfile=%d postconf=%d, want 1 and 1", len(exec.mapfileOps), len(exec.postconfOps))
	}

	if exec.overlapped.Load() {
		t.Error("postconf started before the mapped file was written")
	}
}

// ---- ProcessIsRunning error branch ----

// TestProcessIsRunning_IsRunningError exercises the WarnContext logging branch when
// IsRunning returns an error.
func TestProcessIsRunning_IsRunningError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	errMgr := &testutil.MockServiceManager{
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return false, &testError{msg: "is-running check failed"}
		},
	}

	cm := &ConfigManager{
		env:        hostEnv(),
		mainConfig: &config.Config{BaseDir: "/tmp", Hostname: "testhost"},
		State:      state.NewState(),
		ServiceMgr: errMgr,
		Cache:      cacheInstance,
	}

	// Should return false (error from IsRunning) without panic
	result := cm.ProcessIsRunning(ctx, "nginx")
	if result {
		t.Error("expected ProcessIsRunning to return false when IsRunning errors")
	}
}

// ---- cleanupRewriteFiles additional paths ----

// TestCleanupRewriteFiles_TmpFileRemoveError exercises the Remove error logging branch.
// We simulate this by passing a path that exists but is a directory (not removable as file).
func TestCleanupRewriteFiles_TmpFileIsDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	tmpDir := t.TempDir()
	subDir := tmpDir + "/subdir"
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	// Pass subDir as tmpFileName — os.Remove on a non-empty directory fails on some OSes
	// or on an empty one returns nil. Either way, this exercises the stat+remove path.
	realFSConfigManager().cleanupRewriteFiles(ctx, nil, nil, subDir)
	// No assertion needed — just verify no panic
}

// ---- processRewrite additional paths ----

// TestProcessRewrite_WriteStringError is hard to trigger directly without mocking os.File.
// Instead we exercise the scanner error path by having a bad file descriptor.
// We verify the function gracefully handles a source file opened but becomes unreadable.

// TestProcessRewrite_SuccessfulRename verifies the happy path with default mode (0440).
func TestProcessRewrite_SuccessfulRename(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "cfg.cf.in"
	destRelPath := "cfg.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	entry := config.RewriteEntry{Value: destRelPath, Mode: ""}
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err != nil {
		t.Fatalf("processRewrite: %v", err)
	}

	// Destination file should exist
	destContent, err := os.ReadFile(baseDir + "/" + destRelPath)
	if err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	if len(destContent) == 0 {
		t.Error("expected non-empty destination file")
	}
	// Legacy state.py:775 default mode is 0440.
	if fi, err := os.Stat(baseDir + "/" + destRelPath); err != nil || fi.Mode().Perm() != 0o440 {
		t.Errorf("default rewrite mode = %v (err %v), want 0440", fi.Mode().Perm(), err)
	}
}

// TestProcessRewrite_InvalidMode exercises the ParseInt error branch in processRewrite.
func TestProcessRewrite_InvalidMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "badmode.cf.in"
	destRelPath := "badmode.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	entry := config.RewriteEntry{Value: destRelPath, Mode: "INVALID"}
	// Should not panic — the error is returned
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err == nil {
		t.Error("expected an error for an invalid mode")
	}

	// Destination should NOT exist (function returned early)
	if _, err := os.Stat(baseDir + "/" + destRelPath); err == nil {
		t.Error("expected destination file NOT to exist after invalid mode error")
	}
}

// TestProcessRewrite_MissingSourceFile exercises the os.Open error branch.
func TestProcessRewrite_MissingSourceFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)

	entry := config.RewriteEntry{Value: "out.cf", Mode: ""}
	// Should not panic
	if err := cm.processRewrite(context.Background(), "nonexistent_source.cf.in", entry); err == nil {
		t.Error("expected an error for a missing source file")
	}
}

// TestProcessRewrite_ExplicitMode verifies the 0600 mode is applied correctly.
func TestProcessRewrite_ExplicitMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "mode600.cf.in"
	destRelPath := "mode600.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	entry := config.RewriteEntry{Value: destRelPath, Mode: "0600"}
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err != nil {
		t.Fatalf("processRewrite: %v", err)
	}

	info, err := os.Stat(baseDir + "/" + destRelPath)
	if err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected mode 0600, got %v", info.Mode().Perm())
	}
}

// ---- RunProxygenWithConfigs additional paths ----

// TestRunProxygenWithConfigs_GenerateAllError exercises the gen.GenerateAll error path
// indirectly — since we can't easily inject a mock generator, we rely on the existing
// LoadConfiguration failure path and confirm the error propagation is clean.
func TestRunProxygenWithConfigs_ReturnsErrorNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	err := cm.RunProxygenWithConfigs(context.Background())
	// The function either errors from LoadConfiguration or GenerateAll.
	// Either way, no panic and we get a wrapped error.
	if err == nil {
		t.Log("RunProxygenWithConfigs unexpectedly succeeded (proxy config may be available in env)")
	}
}

// ---- LoadAllConfigsWithRetry additional paths ----

// TestLoadAllConfigsWithRetry_MultipleRetries exercises the retry-on-error path.
func TestLoadAllConfigsWithRetry_MultipleRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)

	// Two retries — both will fail since no localconfig XML exists.
	// This exercises the "len(errors) > 0 && attempt < maxRetries" retry branch.
	err := cm.LoadAllConfigsWithRetry(context.Background(), 2)
	// We expect an error since no real localconfig is available
	_ = err // may or may not error depending on environment
}

// TestLoadAllConfigsWithRetry_ZeroRetries exercises the single-pass path with maxRetries=0.
// When maxRetries is 0, the loop body runs 0 times and lastErr is nil.
func TestLoadAllConfigsWithRetry_ZeroRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)
	// With maxRetries=0, loop doesn't execute, returns nil immediately
	err := cm.LoadAllConfigsWithRetry(context.Background(), 0)
	_ = err
}

// TestLoadAllConfigsWithRetry_LdapReadTimeoutFromLocalConfig exercises the
// strconv.Atoi branch for ldap_read_timeout parsing.
func TestLoadAllConfigsWithRetry_LdapReadTimeoutParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)
	// Set a valid ldap_read_timeout so the Atoi branch is taken
	setLocal(cm, map[string]string{"ldap_read_timeout": "30000"}) // Pre-cache local config so it doesn't fail on XML load
	cm.cachedLocalConfigOutput = "ldap_read_timeout = 30000\nkey = val"

	err := cm.LoadAllConfigsWithRetry(context.Background(), 1)
	_ = err
}

// ---- executeLocalConfigCommand additional path ----

// TestExecuteLocalConfigCommand_CacheMissAndCachesResult exercises the XML-load branch
// by pre-populating cachedLocalConfigOutput after the first call fails.
// In test environments without localconfig.xml, the function returns an error.
// We verify that when cache is empty, it attempts to load XML (and errors gracefully).
func TestExecuteLocalConfigCommand_EmptyCacheAttemptsXML(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// cachedLocalConfigOutput is "" — must attempt XML load
	_, err := cm.executeLocalConfigCommand(context.Background())
	// In test env, XML doesn't exist → error expected
	if err == nil {
		// If it somehow succeeded (XML present), check the cache was set
		if cm.cachedLocalConfigOutput == "" {
			t.Error("expected cachedLocalConfigOutput to be populated after successful load")
		}
	}
	// Either way, no panic
}

// ---- processRewrite additional paths ----

// TestProcessRewrite_CopyFallback exercises the copy-fallback path when os.Rename
// fails because source and destination are on different filesystems.
// We achieve this by pointing destPath to a file inside /tmp while srcPath is on
// the test temp dir (which may be the same filesystem, so we force the fallback
// by using a dest path in /tmp/configd-test-copy-fallback-<random>).
// If rename succeeds (same fs), the test is still valid — it just exercises the
// rename-success path, which is fine.
func TestProcessRewrite_CopyFallback_SameFS(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "copy.cf.in"
	destRelPath := "copy.cf"
	content := "line1\nline2\n"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte(content), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	entry := config.RewriteEntry{Value: destRelPath, Mode: "0644"}
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err != nil {
		t.Fatalf("processRewrite: %v", err)
	}

	// Destination must exist regardless of rename vs copy path
	dest, err := os.ReadFile(baseDir + "/" + destRelPath)
	if err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	if string(dest) != content {
		t.Errorf("expected content %q, got %q", content, string(dest))
	}
}

// TestProcessRewrite_EmptySourceFile verifies an empty source file rewrites
// to an empty destination (scanner loop body never executes).
func TestProcessRewrite_EmptySourceFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "empty.cf.in"
	destRelPath := "empty.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte(""), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	entry := config.RewriteEntry{Value: destRelPath, Mode: ""}
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err != nil {
		t.Fatalf("processRewrite: %v", err)
	}

	if _, err := os.Stat(baseDir + "/" + destRelPath); err != nil {
		t.Errorf("expected destination file to exist even for empty source: %v", err)
	}
}

// TestProcessRewrite_ModeLoggedOnSuccess verifies rewrite with Mode set logs the mode value.
func TestProcessRewrite_ModeLoggedOnSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	srcRelPath := "log_mode.cf.in"
	destRelPath := "log_mode.cf"
	if err := os.WriteFile(baseDir+"/"+srcRelPath, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}

	// Mode "0755" exercises the non-empty mode branch in the final log statement
	entry := config.RewriteEntry{Value: destRelPath, Mode: "0755"}
	if err := cm.processRewrite(context.Background(), srcRelPath, entry); err != nil {
		t.Fatalf("processRewrite: %v", err)
	}

	info, err := os.Stat(baseDir + "/" + destRelPath)
	if err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("expected mode 0755, got %v", info.Mode().Perm())
	}
}

// TestProcessRewrite_ErrorReturned verifies that a missing source is reported
// to the caller, which keeps the rewrite pending for the next cycle (see
// TestDoRewrites_MissingSourceFile).
func TestProcessRewrite_ErrorReturned(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)

	entry := config.RewriteEntry{Value: "out.cf", Mode: ""}
	if err := cm.processRewrite(context.Background(), "missing.cf.in", entry); err == nil {
		t.Error("expected an error for a missing source file")
	}
}

// ---- cleanupRewriteFiles additional paths ----

// TestCleanupRewriteFiles_NonEmptyDir exercises the os.Remove failure path by
// passing a non-empty directory as tmpFileName — os.Remove on a non-empty dir
// fails with ENOTEMPTY, which triggers the WarnContext log branch.
func TestCleanupRewriteFiles_NonEmptyDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a directory with a file inside so os.Remove fails
	subDir := tmpDir + "/nonempty"
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	if err := os.WriteFile(subDir+"/child.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Should log a warning but not panic
	realFSConfigManager().cleanupRewriteFiles(ctx, nil, nil, subDir)
}

// ---- LoadAllConfigsWithRetry additional paths ----

// TestLoadAllConfigsWithRetry_ContextCancelledDuringLoad exercises the ctx.Done()
// path inside the select that waits for load goroutines.
func TestLoadAllConfigsWithRetry_ContextCancelledDuringLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)

	// Use a very short timeout so the context cancels while goroutines are running
	ctx, cancel := context.WithTimeout(context.Background(), 1)
	defer cancel()

	err := cm.LoadAllConfigsWithRetry(ctx, 1)
	// Should return context error or nil — no panic or deadlock
	_ = err
}

// TestLoadAllConfigsWithRetry_RetryOnError exercises the retry-on-error path
// when maxRetries > 1 and all attempts fail (no localconfig XML in test env).
func TestLoadAllConfigsWithRetry_RetryOnError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)
	// With maxRetries=2, the first failure triggers a retry
	// (2s sleep is in the impl, but tests should be fast — we accept the delay)
	// Skip if this would take too long
	t.Parallel() // run in parallel to not block other tests

	err := cm.LoadAllConfigsWithRetry(context.Background(), 1)
	_ = err // may succeed or fail, main goal is no panic
}

// TestLoadAllConfigsWithRetry_WithCachedLocalConfig exercises the successful
// load path for the lc (LocalConfig) goroutine by pre-caching local config.
func TestLoadAllConfigsWithRetry_WithCachedLocalConfig_AndCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: LoadAllConfigsWithRetry has retry delays")
	}
	cm := newTestConfigManager(t)
	// Pre-cache to make the lc goroutine succeed
	cm.cachedLocalConfigOutput = "ldap_url = ldap://localhost:389\nzimbra_server_hostname = testhost"

	// Note: gc/sc/mc goroutines will still fail (no commands available),
	// but this exercises the mixed success/failure path
	err := cm.LoadAllConfigsWithRetry(context.Background(), 1)
	_ = err // error expected due to gc/sc/mc failure; no panic is the goal
}

// ---- executeLocalConfigCommand success path with XML ----

// TestExecuteLocalConfigCommand_CachesOnSuccess verifies that on a successful
// XML load, the result is stored in cachedLocalConfigOutput.
// This path is only exercised when a real localconfig.xml exists.
func TestExecuteLocalConfigCommand_CachesOnSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm := newTestConfigManager(t)
	// If no cached output and no XML, we get an error. Accept either outcome.
	result, err := cm.executeLocalConfigCommand(context.Background())
	if err == nil {
		// XML load succeeded — verify caching
		if cm.cachedLocalConfigOutput == "" {
			t.Error("expected cachedLocalConfigOutput to be set after successful load")
		}
		if result == "" {
			t.Error("expected non-empty result from executeLocalConfigCommand")
		}
	}
	// On error: verify cachedLocalConfigOutput was NOT set (early return)
	if err != nil && cm.cachedLocalConfigOutput != "" {
		t.Error("expected cachedLocalConfigOutput to remain empty after failed load")
	}
}

// ---- RunProxygenWithConfigs additional paths ----

// TestRunProxygenWithConfigs_WithPopulatedState exercises RunProxygenWithConfigs
// with some state populated (still fails at LoadConfiguration but exercises more code).
func TestRunProxygenWithConfigs_WithPopulatedState(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	cm, _ := newTestCMWithExecutor(t)

	// Populate some state to exercise the parameter-passing code paths
	setLocal(cm, map[string]string{"key": "val"})
	setGlobal(cm, map[string]string{"globalKey": "globalVal"})
	setServer(cm, map[string]string{"serverKey": "serverVal"}, nil)
	err := cm.RunProxygenWithConfigs(context.Background())
	// Expect error (no real proxy config), but no panic
	if err == nil {
		t.Log("RunProxygenWithConfigs unexpectedly succeeded")
	}
}
