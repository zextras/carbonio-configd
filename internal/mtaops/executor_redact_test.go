// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
)

func debugCtx(buf *bytes.Buffer) context.Context {
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger.ContextWithLogger(context.Background(), l)
}

func TestExecuteLdapWrite_SecretValueNotLogged(t *testing.T) {
	const secret = "ldap-hunter2-secret"

	var buf bytes.Buffer

	exec := NewExecutor("/opt/zextras", &mockLdapManager{})
	require.NoError(t, exec.ExecuteLdapWrite(debugCtx(&buf),
		LdapOperation{Key: "ldap_root_password", Value: secret}))

	assert.NotContains(t, buf.String(), secret)
	assert.Contains(t, buf.String(), "ldap_root_password")
}

func TestExecutePostconfBatch_SecretValueNotLogged(t *testing.T) {
	const secret = "smtp-hunter2-secret"

	baseDir := t.TempDir()
	bin := filepath.Join(baseDir, "common", "sbin")
	require.NoError(t, os.MkdirAll(bin, 0o750))
	//nolint:gosec // test stub binary must be executable
	require.NoError(t, os.WriteFile(filepath.Join(bin, "postconf"), []byte("#!/bin/sh\nexit 0\n"), 0o700))

	var buf bytes.Buffer

	exec := NewExecutor(baseDir, nil)
	require.NoError(t, exec.ExecutePostconfBatch(debugCtx(&buf), []PostconfOperation{
		{Key: "smtp_sasl_password_maps", Value: "a\n" + secret},
		{Key: "myhostname", Value: "mx.example.com"},
	}))

	assert.NotContains(t, buf.String(), secret)
	assert.Contains(t, buf.String(), "mx.example.com")
}
