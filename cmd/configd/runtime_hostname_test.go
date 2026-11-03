// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRuntime_HostnameOverlay(t *testing.T) {
	t.Setenv("CONFIGD_LC_zimbra_server_hostname", "mailbox-0.pod")

	cfg, _, _, err := newRuntime(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "mailbox-0.pod", cfg.Hostname)
}

func TestNewRuntime_EmptyHostnameOverlayIsIgnored(t *testing.T) {
	t.Setenv("CONFIGD_LC_zimbra_server_hostname", "")

	cfg, _, _, err := newRuntime(context.Background())
	require.NoError(t, err)

	assert.NotEmpty(t, cfg.Hostname)
}
