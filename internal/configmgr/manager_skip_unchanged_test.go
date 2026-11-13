// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

const skipBase = "/opt/zextras"

// newSkipRewriteCM returns a manager over the fake host with the template
// conf/x.cf.in holding "alpha = 1\n".
func newSkipRewriteCM(t *testing.T) (*ConfigManager, *systest.Fakes) {
	t.Helper()

	fakes := systest.New()
	fakes.FS.PutDir(skipBase+"/conf", 0o750)
	fakes.FS.Put(skipBase+"/conf/x.cf.in", []byte("alpha = 1\n"), 0o644)

	cm := &ConfigManager{
		env:        fakes.System(),
		mainConfig: &config.Config{BaseDir: skipBase},
		State:      state.NewState(),
	}
	cm.Transformer = transformer.NewTransformer(cm)

	return cm, fakes
}

func rewriteX(cm *ConfigManager) error {
	return cm.processRewrite(context.Background(), "conf/x.cf.in",
		config.RewriteEntry{Value: "conf/x.cf", Mode: "0640"})
}

func TestProcessRewrite_IdenticalTargetIsNotWritten(t *testing.T) {
	t.Parallel()

	cm, fakes := newSkipRewriteCM(t)
	fakes.FS.Put(skipBase+"/conf/x.cf", []byte("alpha = 1\n"), 0o640)
	fakes.Rec.Reset()

	require.NoError(t, rewriteX(cm))
	assert.Empty(t, fakes.Rec.Strings(), "no temp file, chmod or rename for an up-to-date target")
}

// An explicit REWRITE restores a hand-edited file: an edited target differs.
func TestProcessRewrite_EditedTargetIsRestored(t *testing.T) {
	t.Parallel()

	cm, fakes := newSkipRewriteCM(t)
	fakes.FS.Put(skipBase+"/conf/x.cf", []byte("alpha = hand edited\n"), 0o640)
	fakes.Rec.Reset()

	require.NoError(t, rewriteX(cm))

	data, _ := fakes.FS.Data(skipBase + "/conf/x.cf")
	assert.Equal(t, "alpha = 1\n", string(data))
	assert.NotEmpty(t, subsequence(fakes.Rec.Strings(), "fs.rename"))
}

func TestProcessRewrite_WrongModeIsFixed(t *testing.T) {
	t.Parallel()

	cm, fakes := newSkipRewriteCM(t)
	fakes.FS.Put(skipBase+"/conf/x.cf", []byte("alpha = 1\n"), 0o666)
	fakes.Rec.Reset()

	require.NoError(t, rewriteX(cm))

	mode, _ := fakes.FS.Mode(skipBase + "/conf/x.cf")
	assert.Equal(t, os.FileMode(0o640), mode.Perm())
	assert.NotEmpty(t, subsequence(fakes.Rec.Strings(), "fs.rename"))
}

func TestProcessRewrite_MissingTargetIsWritten(t *testing.T) {
	t.Parallel()

	cm, fakes := newSkipRewriteCM(t)

	require.NoError(t, rewriteX(cm))

	data, ok := fakes.FS.Data(skipBase + "/conf/x.cf")
	require.True(t, ok)
	assert.Equal(t, "alpha = 1\n", string(data))
}

func TestProcessRewrite_SymlinkTargetIsReplaced(t *testing.T) {
	t.Parallel()

	cm, fakes := newSkipRewriteCM(t)
	fakes.FS.Put(skipBase+"/conf/elsewhere", []byte("alpha = 1\n"), 0o640)
	fakes.FS.PutLink(skipBase+"/conf/x.cf", skipBase+"/conf/elsewhere")
	fakes.Rec.Reset()

	require.NoError(t, rewriteX(cm))

	mode, _ := fakes.FS.Mode(skipBase + "/conf/x.cf")
	assert.True(t, mode.IsRegular(), "a symlink target is replaced by a regular file")
}

// Targets under common/conf are installed as root:root, so a file owned by
// anybody else is not current even when content and mode match.
func TestTargetCurrent_RootOwnership(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("the test file would be root-owned")
	}

	path := filepath.Join(t.TempDir(), "master.cf")
	require.NoError(t, os.WriteFile(path, []byte("x\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o644))

	cm := &ConfigManager{env: hostEnv()}

	assert.True(t, cm.targetCurrent(path, []byte("x\n"), 0o644, false))
	assert.False(t, cm.targetCurrent(path, []byte("x\n"), 0o644, true), "owned by the test user, not root")
	assert.False(t, cm.targetCurrent(path, []byte("y\n"), 0o644, false), "content differs")
	assert.False(t, cm.targetCurrent(path, []byte("x\n"), 0o600, false), "mode differs")
	assert.False(t, cm.targetCurrent(path+".missing", []byte("x\n"), 0o644, false), "missing target")

	info, err := os.Stat(path)
	require.NoError(t, err)
	_, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
}

// putInstalled stores the files as the helper leaves them: mode 0644.
func putInstalled(fakes *systest.Fakes, masterContent string, names ...string) {
	for _, name := range names {
		content := "/pattern/ OK\n"
		if name == "master.cf" {
			content = masterContent
		}

		fakes.FS.Put(batchBase+"/common/conf/"+name, []byte(content), 0o644)
	}
}

// Installed files that already equal the rendered templates are not staged and
// the helper (a systemd oneshot or sudo run) is not started.
func TestDoRewrites_CommonConfUpToDateRunsNoHelper(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, validMaster)
	putInstalled(fakes, validMaster, batchFiles...)
	fakes.Rec.Reset()

	pending, err := cm.doRewrites(context.Background(), entries)
	require.NoError(t, err)
	assert.Empty(t, pending, "a skipped file counts as done")
	assert.Empty(t, fakes.Exec.Lines())

	for _, name := range batchFiles {
		_, staged := fakes.FS.Data(batchStage + name)
		assert.False(t, staged, "%s must not be staged", name)
	}
}

// One drifted file is staged alone and installed by one helper run.
func TestDoRewrites_CommonConfOnlyTheChangedFileIsStaged(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, validMaster)
	putInstalled(fakes, validMaster, batchFiles...)
	fakes.FS.Put(batchBase+"/common/conf/tag_as_foreign.re", []byte("/hand edited/ OK\n"), 0o644)

	pending, err := cm.doRewrites(context.Background(), entries)
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Equal(t, []string{batchHelper + " apply"}, fakes.Exec.Lines())

	_, ok := fakes.FS.Data(batchStage + "tag_as_foreign.re")
	assert.True(t, ok, "the edited file is staged")

	for _, name := range []string{"master.cf", "tag_as_originating.re"} {
		_, staged := fakes.FS.Data(batchStage + name)
		assert.False(t, staged, "%s is current and must not be staged", name)
	}
}

// An installed file with the right content but a mode other than the helper's
// 0644 is reinstalled.
func TestDoRewrites_CommonConfWrongModeIsReinstalled(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, validMaster)
	putInstalled(fakes, validMaster, batchFiles...)
	fakes.FS.Put(batchBase+"/common/conf/master.cf", []byte(validMaster), 0o600)

	_, err := cm.doRewrites(context.Background(), entries)
	require.NoError(t, err)

	_, ok := fakes.FS.Data(batchStage + "master.cf")
	assert.True(t, ok)
	assert.Equal(t, []string{batchHelper + " apply"}, fakes.Exec.Lines())
}

// ---- postconf and postconf -X ----

const postconfN = "exec " + skipBase + "/common/sbin/postconf -n"

// newPostconfCM returns a manager with the real MTA executor over the fake
// host, whose postconf -n prints mainCf.
func newPostconfCM(t *testing.T, mainCf string) (*ConfigManager, *systest.Fakes) {
	t.Helper()

	fakes := systest.New()
	fakes.Exec.OnCmdline(skipBase+"/common/sbin/postconf -n", systest.Reply(mainCf))

	env := fakes.System()
	cm := &ConfigManager{
		env:         env,
		mainConfig:  &config.Config{BaseDir: skipBase},
		State:       state.NewState(),
		mtaExecutor: mtaops.NewExecutor(skipBase, ldapRecorder{fakes.Rec}, env),
		mtaResolver: mtaops.NewResolver(skipBase, env),
	}
	cm.Transformer = transformer.NewTransformer(cm)

	return cm, fakes
}

func stagedSet(fakes *systest.Fakes) (string, bool) {
	data, ok := fakes.FS.Data(skipBase + "/data/postfix/conf-staging/postconf.set")

	return string(data), ok
}

func stagedUnset(fakes *systest.Fakes) (string, bool) {
	data, ok := fakes.FS.Data(skipBase + "/data/postfix/conf-staging/postconf.unset")

	return string(data), ok
}

func TestDoPostconf_AllValuesEqualRunsNothing(t *testing.T) {
	t.Parallel()

	// postconf -n wraps and re-indents: the comparison ignores whitespace.
	cm, fakes := newPostconfCM(t, "myhostname = mail.example.com\n"+
		"mynetworks = 127.0.0.0/8, 10.0.0.0/8,\n\t192.168.0.0/16\n"+
		"inet_protocols =\n")

	pending, err := cm.doPostconf(context.Background(), []plan.Postconf{
		{Key: "myhostname", Value: "mail.example.com"},
		{Key: "mynetworks", Value: "127.0.0.0/8, 10.0.0.0/8, 192.168.0.0/16"},
		{Key: "inet_protocols", Value: ""},
	})
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Equal(t, []string{postconfN}, subsequence(fakes.Rec.Strings(), "exec "), "only the main.cf read")

	_, staged := stagedSet(fakes)
	assert.False(t, staged, "nothing may be staged")
}

func TestDoPostconf_OnlyTheChangedKeyIsSent(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "myhostname = mail.example.com\nmynetworks = 127.0.0.0/8\n")

	pending, err := cm.doPostconf(context.Background(), []plan.Postconf{
		{Key: "myhostname", Value: "mail.example.com"},
		{Key: "mynetworks", Value: "127.0.0.0/8, 10.0.0.0/8"},
	})
	require.NoError(t, err)
	assert.Empty(t, pending)

	set, staged := stagedSet(fakes)
	require.True(t, staged)
	assert.Contains(t, set, "mynetworks")
	assert.NotContains(t, set, "myhostname", "an unchanged key must not be sent")
	assert.Len(t, subsequence(fakes.Rec.Strings(), "postfix-conf apply"), 1)
}

// A parameter that main.cf does not set explicitly (default, or master.cf
// only) is still set: main.cf then carries the value.
func TestDoPostconf_KeyNotInMainCfIsStillSet(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "myhostname = mail.example.com\n")

	_, err := cm.doPostconf(context.Background(), []plan.Postconf{
		{Key: "inet_protocols", Value: "all"},
		{Key: "myhostname", Value: "mail.example.com"},
	})
	require.NoError(t, err)

	set, staged := stagedSet(fakes)
	require.True(t, staged)
	assert.Contains(t, set, "inet_protocols")
	assert.NotContains(t, set, "myhostname")
}

// When main.cf cannot be read, every operation is applied.
func TestDoPostconf_UnreadableMainCfAppliesEverything(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "")
	fakes.Exec.OnCmdline(skipBase+"/common/sbin/postconf -n", systest.Fail(1, "postconf: fatal\n"))

	pending, err := cm.doPostconf(context.Background(), []plan.Postconf{
		{Key: "myhostname", Value: "mail.example.com"},
		{Key: "mynetworks", Value: "127.0.0.0/8"},
	})
	require.NoError(t, err)
	assert.Empty(t, pending)

	set, staged := stagedSet(fakes)
	require.True(t, staged)
	assert.Contains(t, set, "myhostname")
	assert.Contains(t, set, "mynetworks")
}

func TestDoPostconfd_AbsentKeyIsSkipped(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "myhostname = mail.example.com\n")

	pending, err := cm.doPostconfd(context.Background(), []plan.Postconfd{{Key: "smtpd_banner"}})
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Equal(t, []string{postconfN}, subsequence(fakes.Rec.Strings(), "exec "), "only the main.cf read")

	_, staged := stagedUnset(fakes)
	assert.False(t, staged)
}

func TestDoPostconfd_OnlyPresentKeysAreRemoved(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "smtpd_banner = hello\nmyhostname = mail.example.com\n")

	pending, err := cm.doPostconfd(context.Background(), []plan.Postconfd{{Key: "smtpd_banner"}, {Key: "mynetworks"}})
	require.NoError(t, err)
	assert.Empty(t, pending)

	unset, staged := stagedUnset(fakes)
	require.True(t, staged)
	assert.Contains(t, unset, "smtpd_banner")
	assert.NotContains(t, unset, "mynetworks")
}

func TestDoPostconfd_UnreadableMainCfRemovesEverything(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "")
	fakes.Exec.OnCmdline(skipBase+"/common/sbin/postconf -n", systest.Fail(1, "postconf: fatal\n"))

	_, err := cm.doPostconfd(context.Background(), []plan.Postconfd{{Key: "smtpd_banner"}, {Key: "mynetworks"}})
	require.NoError(t, err)

	unset, staged := stagedUnset(fakes)
	require.True(t, staged)
	assert.Contains(t, unset, "smtpd_banner")
	assert.Contains(t, unset, "mynetworks")
}

// Execute reads main.cf once for the postconf and the postconf -X step.
func TestExecute_ReadsMainCfOnce(t *testing.T) {
	t.Parallel()

	cm, fakes := newPostconfCM(t, "myhostname = mail.example.com\nsmtpd_banner = hello\n")

	require.NoError(t, cm.Execute(context.Background(), &plan.Plan{
		Postconf:  []plan.Postconf{{Key: "myhostname", Value: "mail.example.com"}},
		Postconfd: []plan.Postconfd{{Key: "smtpd_banner"}},
	}))
	assert.Equal(t, []string{postconfN}, subsequence(fakes.Rec.Strings(), "postconf -n"))
}

// ---- restarts ----

func inode(t *testing.T, path string) uint64 {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)

	st, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)

	return st.Ino
}

// Skipping an up-to-date file never changes what is restarted: a changed
// section restarts its services whether the rewrite was skipped or performed,
// and a cycle without change restarts nothing.
func TestCycle_SkippedRewriteDoesNotChangeRestarts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		template      string
		wantRewritten bool
	}{
		{"template independent of the changed value keeps its file", "static = 1\n", false},
		{"template following the changed value is rewritten", "value = %%VAR:zimbraMtaX%%\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
				"mta": {
					Name:         "mta",
					RequiredVars: map[string]string{"zimbraMtaX": configTypeVAR},
					Rewrites:     map[string]config.RewriteEntry{"conf/x.cf.in": {Value: "conf/x.cf", Mode: "0640"}},
					Restarts:     map[string]bool{"mta": true},
				},
			}}

			cm, _ := newPlanCM(t, conf)

			var added []string

			cm.ServiceMgr = &testutil.MockServiceManager{
				AddRestartWithReasonsFn: func(_ context.Context, service string, _ []string) error {
					added = append(added, service)

					return nil
				},
			}
			cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)
			cm.mainConfig.RestartConfig = true

			dir := filepath.Join(cm.mainConfig.BaseDir, "conf")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "x.cf.in"), []byte(tt.template), 0o644))

			target := filepath.Join(dir, "x.cf")

			setGlobal(cm, map[string]string{"zimbraMtaX": "1"})
			setServer(cm, nil, enabled("mta"))
			mustCycle(t, cm)
			cm.State.SetFirstRun(false)

			before := inode(t, target)
			added = nil

			setGlobal(cm, map[string]string{"zimbraMtaX": "2"})
			pl := mustCycle(t, cm)

			require.Equal(t, []string{"mta"}, sectionNames(pl))
			assert.Equal(t, []string{"mta"}, added, "the changed section restarts its service")
			assert.Equal(t, tt.wantRewritten, inode(t, target) != before, "was the target rewritten")

			added = nil

			mustCycle(t, cm)
			assert.Empty(t, added, "a cycle without change restarts nothing")
		})
	}
}
