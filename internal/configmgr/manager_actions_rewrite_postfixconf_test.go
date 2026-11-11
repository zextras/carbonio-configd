// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/postfixconf"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

// A REWRITE target under the root-owned common/conf is staged for the helper;
// configd never creates, renames or writes anything there (CO-4621).
func TestProcessRewrite_CommonConfGoesThroughHelper(t *testing.T) {
	t.Parallel()

	const base = "/opt/zextras"

	for _, target := range []string{"master.cf", "tag_as_foreign.re", "tag_as_originating.re"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			fakes := systest.New()
			fakes.Proc.UserID = 0

			content := "line one\nline two\n"
			if target == "master.cf" {
				content = "# header\nsmtp inet n - n - - smtpd\n"
			}

			fakes.FS.Put(base+"/common/conf/"+target+".in", []byte(content), 0o644)

			cm := &ConfigManager{
				env:        fakes.System(),
				mainConfig: &config.Config{BaseDir: base},
				State:      state.NewState(),
			}
			cm.Transformer = transformer.NewTransformer(cm)

			err := cm.processRewrite(context.Background(), "common/conf/"+target+".in",
				config.RewriteEntry{Value: "common/conf/" + target, Mode: "0644"})
			require.NoError(t, err)

			staged, ok := fakes.FS.Data(base + "/data/postfix/conf-staging/" + target)
			require.True(t, ok, "rendered content must be staged")
			assert.Equal(t, content, string(staged))
			assert.Equal(t, []string{"exec " + base + "/libexec/postfix-conf apply"},
				subsequence(fakes.Rec.Strings(), "exec "))

			for _, op := range fakes.Rec.Strings() {
				if strings.HasPrefix(op, "fs.read") || strings.HasPrefix(op, "fs.open ") {
					continue // the template is read from there
				}

				assert.NotContains(t, op, "common/conf/", "no write, temp or rename under common/conf: %s", op)
			}
		})
	}
}

func TestProcessRewrite_CommonConfRejectsUnknownTarget(t *testing.T) {
	t.Parallel()

	fakes := systest.New()
	fakes.FS.Put("/opt/zextras/common/conf/main.cf.in", []byte("x\n"), 0o644)

	cm := &ConfigManager{env: fakes.System(), mainConfig: &config.Config{BaseDir: "/opt/zextras"}, State: state.NewState()}
	cm.Transformer = transformer.NewTransformer(cm)

	err := cm.processRewrite(context.Background(), "common/conf/main.cf.in",
		config.RewriteEntry{Value: "common/conf/main.cf"})
	require.Error(t, err)
	assert.Empty(t, fakes.Exec.Calls())
}

const (
	batchBase   = "/opt/zextras"
	batchHelper = batchBase + "/libexec/postfix-conf"
	batchStage  = batchBase + "/data/postfix/conf-staging/"
)

var batchFiles = []string{"master.cf", "tag_as_foreign.re", "tag_as_originating.re"}

// newBatchCM returns a manager running as root (the helper is exec'd directly)
// with the three common/conf templates in the fake filesystem.
func newBatchCM(t *testing.T, masterContent string) (*ConfigManager, *systest.Fakes, []plan.Rewrite) {
	t.Helper()

	fakes := systest.New()
	fakes.Proc.UserID = 0

	var entries []plan.Rewrite

	for _, name := range batchFiles {
		content := "/pattern/ OK\n"
		if name == "master.cf" {
			content = masterContent
		}

		fakes.FS.Put(batchBase+"/common/conf/"+name+".in", []byte(content), 0o644)

		entries = append(entries, plan.Rewrite{Source: "common/conf/" + name + ".in", Target: "common/conf/" + name})
	}

	cm := &ConfigManager{
		env:        fakes.System(),
		mainConfig: &config.Config{BaseDir: batchBase},
		State:      state.NewState(),
	}
	cm.Transformer = transformer.NewTransformer(cm)

	return cm, fakes, entries
}

const validMaster = "smtp inet n - n - - smtpd\n"

// The three root-owned files of one rewrite pass are staged together and
// installed by a single helper run.
func TestDoRewrites_CommonConfBatchIsOneHelperRun(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, validMaster)

	pending, err := cm.doRewrites(context.Background(), entries)
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Equal(t, []string{batchHelper + " apply"}, fakes.Exec.Lines(), "one helper run for the whole batch")

	for _, name := range batchFiles {
		_, ok := fakes.FS.Data(batchStage + name)
		assert.True(t, ok, "%s must be staged", name)
	}
}

// A file the helper's validator rejects fails alone: the others are installed
// by their own run and only the invalid one stays pending.
func TestDoRewrites_CommonConfValidationFailureIsolatesTheFile(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, "smtp inet n - n - - /tmp/evil\n")

	pending, err := cm.doRewrites(context.Background(), entries)
	require.Error(t, err)
	assert.ErrorIs(t, err, postfixconf.ErrValidation)

	require.Len(t, pending, 1)
	assert.Equal(t, "common/conf/master.cf.in", pending[0].Source)
	assert.Len(t, fakes.Exec.Lines(), 2, "the two valid files are installed by one run each")
}

// A helper that cannot be run fails every file of the batch; they all stay
// pending and nothing is retried file by file.
func TestDoRewrites_CommonConfTriggerFailureKeepsAllPending(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, validMaster)
	fakes.Exec.OnPath(batchHelper, systest.Fail(1, "boom"))

	pending, err := cm.doRewrites(context.Background(), entries)
	require.Error(t, err)
	assert.NotErrorIs(t, err, postfixconf.ErrValidation)
	assert.Len(t, pending, len(batchFiles))
	assert.Len(t, fakes.Exec.Lines(), 1, "a trigger failure is not retried per file")
}

// A single invalid file is not retried a second time.
func TestDoRewrites_CommonConfSingleInvalidFile(t *testing.T) {
	t.Parallel()

	cm, fakes, entries := newBatchCM(t, "smtp inet n - n - - /tmp/evil\n")

	pending, err := cm.doRewrites(context.Background(), entries[:1])
	require.Error(t, err)
	assert.Len(t, pending, 1)
	assert.Empty(t, fakes.Exec.Lines())
}

func TestProcessRewrite_CommonConfRenderFailure(t *testing.T) {
	t.Parallel()

	cm, fakes, _ := newBatchCM(t, validMaster)

	err := cm.processRewrite(context.Background(), "common/conf/missing.in",
		config.RewriteEntry{Value: "common/conf/master.cf"})
	require.Error(t, err)
	assert.Empty(t, fakes.Exec.Lines())
}

func TestProcessRewrite_CommonConfTriggerFailure(t *testing.T) {
	t.Parallel()

	cm, fakes, _ := newBatchCM(t, validMaster)
	fakes.Exec.OnPath(batchHelper, systest.Fail(1, "boom"))

	err := cm.processRewrite(context.Background(), "common/conf/master.cf.in",
		config.RewriteEntry{Value: "common/conf/master.cf"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit rewrite")
}
