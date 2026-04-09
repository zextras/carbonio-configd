// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"
)

// TestSSLUpstreamResolvers tests the SSL upstream resolver functions
// This test verifies that the resolvers work correctly when zmprov is available
func TestSSLUpstreamResolvers(t *testing.T) {
	// Create a test generator
	gen := &Generator{
		Variables: make(map[string]*Variable),
	}

	// Test the SSL upstream resolver via factory
	t.Log("Testing makeBackendResolver(true)")

	// This should fail gracefully without zmprov
	resolver := gen.makeBackendResolver(true)
	result, err := resolver(context.Background())
	if err != nil {
		t.Errorf("resolveWebSSLUpstreamServers failed: %v", err)
	}

	if result == "" {
		t.Log("SSL upstream resolver returned empty string (expected without zmprov)")
	} else {
		t.Logf("SSL upstream resolver returned: %s", result)
	}
}
