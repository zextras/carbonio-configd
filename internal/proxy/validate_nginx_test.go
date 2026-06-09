// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// installFakeNginx writes an executable `nginx` script into a temp dir and
// prepends it to PATH so ValidateNginxConfig picks it up deterministically.
func installFakeNginx(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nginx"), []byte(script), 0o700))
	t.Setenv("PATH", dir)
}

func skipIfSystemNginx(t *testing.T) {
	t.Helper()

	for _, p := range []string{"/opt/zextras/common/sbin/nginx", "/usr/bin/nginx", "/usr/sbin/nginx"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("system nginx at %s would shadow the PATH stub", p)
		}
	}
}

func TestValidateNginxConfig_NoBinaryAvailable(t *testing.T) {
	skipIfSystemNginx(t)
	t.Setenv("PATH", t.TempDir()) // nothing on PATH

	tp := NewTemplateProcessor(nil, t.TempDir(), t.TempDir())
	assert.NoError(t, tp.ValidateNginxConfig(context.Background(), "/tmp/nginx.conf"),
		"missing nginx must not fail validation")
}

func TestValidateNginxConfig_SyntaxOK(t *testing.T) {
	skipIfSystemNginx(t)
	installFakeNginx(t, "#!/bin/sh\necho 'nginx: configuration file syntax is ok' >&2\nexit 0\n")

	tp := NewTemplateProcessor(nil, t.TempDir(), t.TempDir())
	assert.NoError(t, tp.ValidateNginxConfig(context.Background(), "/tmp/nginx.conf"))
}

func TestValidateNginxConfig_SyntaxOKDespiteRuntimeError(t *testing.T) {
	skipIfSystemNginx(t)
	// nginx -t returns non-zero for PID/log permission issues even when the
	// syntax is fine; the validator must treat "syntax is ok" as success.
	installFakeNginx(t, "#!/bin/sh\necho 'nginx: configuration file syntax is ok' >&2\nexit 1\n")

	tp := NewTemplateProcessor(nil, t.TempDir(), t.TempDir())
	assert.NoError(t, tp.ValidateNginxConfig(context.Background(), "/tmp/nginx.conf"))
}

func TestValidateNginxConfig_RealFailure(t *testing.T) {
	skipIfSystemNginx(t)
	installFakeNginx(t, "#!/bin/sh\necho 'nginx: [emerg] unknown directive' >&2\nexit 1\n")

	tp := NewTemplateProcessor(nil, t.TempDir(), t.TempDir())
	err := tp.ValidateNginxConfig(context.Background(), "/tmp/nginx.conf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nginx validation failed")
}

func TestQueryDomains_NilClientFallsBack(t *testing.T) {
	ctx := context.Background()

	t.Run("nil LdapClient", func(t *testing.T) {
		g := &Generator{}
		domains, ok := g.queryDomains(ctx, "fallback")
		assert.False(t, ok)
		assert.Nil(t, domains)
	})

	t.Run("nil NativeClient", func(t *testing.T) {
		g := &Generator{LdapClient: &ldap.Ldap{}}
		domains, ok := g.queryDomains(ctx, "fallback")
		assert.False(t, ok)
		assert.Nil(t, domains)
	})
}
