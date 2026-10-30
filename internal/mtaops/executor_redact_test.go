// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func debugCtx(buf *bytes.Buffer) context.Context {
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger.ContextWithLogger(context.Background(), l)
}

func TestExecuteLdapWrite_SecretValueNotLogged(t *testing.T) {
	const secret = "ldap-hunter2-secret"

	t.Parallel()

	var buf bytes.Buffer

	exec := NewExecutor("/opt/zextras", &mockLdapManager{}, systest.New().System())
	require.NoError(t, exec.ExecuteLdapWrite(debugCtx(&buf),
		LdapOperation{Key: "ldap_root_password", Value: secret}))

	assert.NotContains(t, buf.String(), secret)
	assert.Contains(t, buf.String(), "ldap_root_password")
}

func TestExecutePostconfBatch_SecretValueNotLogged(t *testing.T) {
	const secret = "smtp-hunter2-secret"

	t.Parallel()

	var buf bytes.Buffer

	exec := NewExecutor("/opt/zextras", nil, systest.New().System())
	require.NoError(t, exec.ExecutePostconfBatch(debugCtx(&buf), []PostconfOperation{
		{Key: "smtp_sasl_password_maps", Value: "a\n" + secret},
		{Key: "myhostname", Value: "mx.example.com"},
	}))

	assert.NotContains(t, buf.String(), secret)
	assert.Contains(t, buf.String(), "mx.example.com")
}
