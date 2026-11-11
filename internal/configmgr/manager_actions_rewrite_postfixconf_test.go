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
			assert.Equal(t, []string{"exec " + base + "/libexec/carbonio-postfix-conf apply"},
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
