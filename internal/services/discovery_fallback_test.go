// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discoveryController(t *testing.T, lc map[string]string, cached string) *Controller {
	t.Helper()

	ctl, f := newFakeController(t)
	ctl.loadConfig = func() (map[string]string, error) { return lc, nil }

	if cached != "" {
		f.FS.Put(ctl.p.cacheFile, []byte(cached), 0o600)
	}

	return ctl
}

func TestDiscoverEnabledServices_NoMasterURLFallsBackToCache(t *testing.T) {
	t.Parallel()

	ctl := discoveryController(t, map[string]string{"zimbra_server_hostname": "mbox.example"}, "mta\nproxy\n")

	got, err := ctl.DiscoverEnabledServices(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"mta", "proxy"}, got)
}

func TestDiscoverEnabledServices_NoMasterURLAndNoCacheFails(t *testing.T) {
	t.Parallel()

	ctl := discoveryController(t, map[string]string{"zimbra_server_hostname": "mbox.example"}, "")

	_, err := ctl.DiscoverEnabledServices(context.Background())

	assert.ErrorContains(t, err, "LDAP unreachable and cache missing")
}

func TestDiscoverEnabledServices_UnreachableLDAPFallsBackToCache(t *testing.T) {
	t.Parallel()

	// Nothing listens on port 1; StartTLS is advertised, so the client is
	// also given the CA of the installation (absent here: system roots).
	ctl := discoveryController(t, map[string]string{
		"zimbra_server_hostname":  "mbox.example",
		"ldap_master_url":         "ldap://127.0.0.1:1",
		"ldap_starttls_supported": "1",
	}, "mailbox\n")

	got, err := ctl.DiscoverEnabledServices(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"mailbox"}, got, "the cached list answers when the directory does not")
}
