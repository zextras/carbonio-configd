// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// TestUpstreamCacheInitialization verifies upstream cache is initialized
func TestUpstreamCacheInitialization(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		BaseDir:  t.TempDir(),
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), sys.Real(), cfg, Sources{})
	require.NoError(t, err)
	require.NotNil(t, gen.upstreamCache)
	assert.False(t, gen.upstreamCache.backendsPopulated, "cache must start unpopulated")
}

// TestUpstreamQueriesAreCached verifies that each upstream list is computed
// once from the server attribute index and then served from the cache, even
// when the underlying attributes change afterwards.
func TestUpstreamQueriesAreCached(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	attrs := map[string]string{
		zimbraServiceHostnameAttr:          "mail.example.com",
		zimbraReverseProxyLookupTargetAttr: "TRUE",
		zimbraMailModeAttr:                 "http",
		zimbraMailPortAttr:                 "8080",
		zimbraMailSSLPortAttr:              "8443",
		zimbraServiceEnabledAttr:           "memcached",
		zimbraMemcachedBindPortAttr:        "11211",
	}

	g := &Generator{
		env:           sys.Real(),
		upstreamCache: &upstreamQueryCache{serverAttrsByHost: map[string]map[string]string{"mail.example.com": attrs}},
	}

	backends, err := g.getAllReverseProxyBackends(ctx)
	require.NoError(t, err)

	backendsSSL, err := g.getAllReverseProxyBackendsSSL(ctx)
	require.NoError(t, err)

	memcached, err := g.getAllMemcachedServers(ctx)
	require.NoError(t, err)
	require.Len(t, memcached, 1)

	// Mutate the source: cached lists must not change.
	attrs[zimbraServiceHostnameAttr] = "changed.example.com"
	attrs[zimbraMemcachedBindPortAttr] = "1"

	backends2, err := g.getAllReverseProxyBackends(ctx)
	require.NoError(t, err)
	assert.Equal(t, backends, backends2)

	backendsSSL2, err := g.getAllReverseProxyBackendsSSL(ctx)
	require.NoError(t, err)
	assert.Equal(t, backendsSSL, backendsSSL2)

	memcached2, err := g.getAllMemcachedServers(ctx)
	require.NoError(t, err)
	assert.Equal(t, memcached, memcached2)
}
