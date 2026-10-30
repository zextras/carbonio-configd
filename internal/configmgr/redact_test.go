// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// captureLogCtx returns a context whose logger writes DEBUG and above to buf.
func captureLogCtx(buf *bytes.Buffer) context.Context {
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger.ContextWithLogger(context.Background(), l)
}

// configmgrLogLines keeps only the log lines emitted by the configmgr component.
// internal/state logs lastVal values at DEBUG on its own and is outside the
// scope of these assertions.
func configmgrLogLines(out string) string {
	var kept []string

	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "component=configmgr") {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n")
}

// TestComparator_CompareKeys_SecretValueNotLogged verifies a changed password key
// is reported by name only, while a normal key still logs both values.
func TestComparator_CompareKeys_SecretValueNotLogged(t *testing.T) {
	const (
		oldSecret = "old-hunter2-secret"
		newSecret = "new-hunter2-secret"
	)

	c, st := newComparatorWithLookup(
		map[string]map[string]string{
			"LOCAL": {"ldap_amavis_password": newSecret},
			"VAR":   {"zimbraImapBindPort": "7143"},
		},
		nil,
	)
	st.FirstRun = false
	st.MtaConfig.Sections["amavis"] = &config.MtaConfigSection{
		Name:         "amavis",
		RequiredVars: map[string]string{"ldap_amavis_password": "LOCAL", "zimbraImapBindPort": "VAR"},
	}

	var buf bytes.Buffer

	ctx := captureLogCtx(&buf)
	st.LastVal(ctx, "amavis", "LOCAL", "ldap_amavis_password", oldSecret)
	st.LastVal(ctx, "amavis", "VAR", "zimbraImapBindPort", "143")

	require.NoError(t, c.CompareKeys(ctx))

	out := configmgrLogLines(buf.String())
	assert.NotContains(t, out, oldSecret)
	assert.NotContains(t, out, newSecret)
	assert.Contains(t, out, "ldap_amavis_password", "key name must still be logged")
	assert.Contains(t, out, redact.Placeholder)
	assert.Contains(t, out, "previous_value=143", "non-secret values stay visible")
	assert.Contains(t, out, "current_value=7143", "non-secret values stay visible")
}

// TestComparator_CompareKeys_SecretBecameUndefinedNotLogged covers the
// "changed to undefined" INFO line.
func TestComparator_CompareKeys_SecretBecameUndefinedNotLogged(t *testing.T) {
	const oldSecret = "old-hunter2-secret"

	c, st := newComparatorWithLookup(map[string]map[string]string{}, nil)
	st.FirstRun = false
	st.MtaConfig.Sections["amavis"] = &config.MtaConfigSection{
		Name:         "amavis",
		RequiredVars: map[string]string{"ldap_amavis_password": "LOCAL"},
	}

	var buf bytes.Buffer

	ctx := captureLogCtx(&buf)
	st.LastVal(ctx, "amavis", "LOCAL", "ldap_amavis_password", oldSecret)

	require.NoError(t, c.CompareKeys(ctx))

	out := configmgrLogLines(buf.String())
	assert.Contains(t, out, "Variable changed to undefined")
	assert.NotContains(t, out, oldSecret)
}

// TestDoLdap_SecretValueNotLogged verifies doLdap redacts secret attribute values
// on both the success and the failure path.
func TestDoLdap_SecretValueNotLogged(t *testing.T) {
	const secret = "ldap-hunter2-secret"

	tests := []struct {
		name    string
		execErr error
	}{
		{name: "success"},
		{name: "failure", execErr: errors.New("ldap write failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, exec := newTestCMWithExecutor(t)
			exec.ldapErr = tt.execErr
			cm.State.CurrentActions.Ldap["ldap_root_password"] = secret
			cm.State.CurrentActions.Ldap["ldap_uri"] = "ldap://localhost"

			var buf bytes.Buffer

			ctx := captureLogCtx(&buf)
			_ = cm.doLdap(ctx)

			require.Len(t, exec.ldapOps, 2, "both writes must still be attempted")

			out := buf.String()
			assert.NotContains(t, out, secret)
			assert.Contains(t, out, "ldap_root_password")
			assert.Contains(t, out, "ldap://localhost", "non-secret values stay visible")
		})
	}
}

// TestLookUpConfig_SecretValueNotLogged verifies the DEBUG lookup log does not
// leak secret values.
func TestLookUpConfig_SecretValueNotLogged(t *testing.T) {
	const secret = "local-hunter2-secret"

	cm, _ := newTestCMWithExecutor(t)
	cm.State.LocalConfig.Data.Set("mailboxd_keystore_password", secret)
	cm.State.LocalConfig.Data.Set("ldap_master_url", "ldap://master:389")

	var buf bytes.Buffer

	ctx := captureLogCtx(&buf)

	got, err := cm.LookUpConfig(ctx, configTypeLOCAL, "mailboxd_keystore_password")
	require.NoError(t, err)
	assert.Equal(t, secret, got, "the returned value must not be redacted")

	_, err = cm.LookUpConfig(ctx, configTypeLOCAL, "ldap_master_url")
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, secret)
	assert.Contains(t, out, "value=ldap://master:389")
}
