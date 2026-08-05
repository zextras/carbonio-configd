// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
)

// openWriteLDAPClient dials a write-capable LDAP client from a resolved
// localconfig map. It reads ldap_master_url (writes require the master),
// falling back to ldap_url when the master URL is unset. Both keys may carry
// space-separated multi-URL values; ParseURLs is used at both layers so
// connect-time failover works regardless of which key holds the list
// (CO-3565). Shared by proxycli and tlscli.
func openWriteLDAPClient(lc map[string]string) (*carboldap.Client, error) {
	urls := carboldap.ParseURLs(lc[lcKeyLDAPMasterURL])
	if len(urls) == 0 {
		urls = carboldap.ParseURLs(lc[lcKeyLDAPURL])
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("LDAP not configured (%s and %s are both empty)", lcKeyLDAPMasterURL, lcKeyLDAPURL)
	}

	client, err := carboldap.NewClient(&carboldap.ClientConfig{
		URLs:     urls,
		BindDN:   lc[lcKeyZimbraLDAPUserDN],
		Password: lc[lcKeyZimbraLDAPPassword],
		StartTLS: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to LDAP: %w", err)
	}

	return client, nil
}
