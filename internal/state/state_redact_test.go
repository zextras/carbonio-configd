// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// captureDebugCtx returns a context whose logger writes DEBUG and above to buf.
func captureDebugCtx(buf *bytes.Buffer) context.Context {
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger.ContextWithLogger(context.Background(), l)
}

func TestLastVal_SecretValueNotLogged(t *testing.T) {
	const secret = "hunter2-secret"

	s := NewState()

	var buf bytes.Buffer

	ctx := captureDebugCtx(&buf)

	assert.Equal(t, secret, s.LastVal(ctx, "amavis", "LOCAL", "ldap_amavis_password", secret),
		"the returned value must not be redacted")
	assert.Equal(t, "7143", s.LastVal(ctx, "imap", "VAR", "zimbraImapBindPort", "7143"))

	out := buf.String()
	assert.NotContains(t, out, secret)
	assert.Contains(t, out, "ldap_amavis_password", "key name must still be logged")
	assert.Contains(t, out, "value=[REDACTED]")
	assert.Contains(t, out, "value=7143", "non-secret values stay visible")
}

func TestCurLdapAndPostconf_SecretValueNotLogged(t *testing.T) {
	const secret = "hunter2-secret"

	s := NewState()

	var buf bytes.Buffer

	ctx := captureDebugCtx(&buf)

	assert.Equal(t, secret, s.CurLdap(ctx, "ldap_root_password", secret))
	assert.Equal(t, "ldap://localhost", s.CurLdap(ctx, "ldap_uri", "ldap://localhost"))
	assert.Equal(t, secret, s.CurPostconf(ctx, "smtp_sasl_password", secret))
	assert.Equal(t, "yes", s.CurPostconf(ctx, "smtpd_use_tls", "yes"))

	out := buf.String()
	assert.NotContains(t, out, secret)
	assert.Contains(t, out, "ldap_root_password")
	assert.Contains(t, out, "smtp_sasl_password")
	assert.Contains(t, out, "value=ldap://localhost")
	assert.Contains(t, out, "value=yes")
}
