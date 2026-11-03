// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestNewRuntime_HostnameOverlay(t *testing.T) {
	t.Setenv("CONFIGD_LC_zimbra_server_hostname", "mailbox-0.pod")

	rt, err := buildRuntime(context.Background(), systest.New().System(), &runtimeOpts{})
	require.NoError(t, err)

	assert.Equal(t, "mailbox-0.pod", rt.cfg.Hostname)
}

func TestNewRuntime_EmptyHostnameOverlayIsIgnored(t *testing.T) {
	t.Setenv("CONFIGD_LC_zimbra_server_hostname", "")

	rt, err := buildRuntime(context.Background(), systest.New().System(), &runtimeOpts{})
	require.NoError(t, err)

	assert.NotEmpty(t, rt.cfg.Hostname)
}
