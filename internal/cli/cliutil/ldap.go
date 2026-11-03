// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"errors"
	"fmt"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
)

// LCKeyZimbraServerHostname is the localconfig key of the local server entry's
// hostname.
const LCKeyZimbraServerHostname = "zimbra_server_hostname"

// OpenWriteLDAPClient dials a write-capable LDAP client from the effective
// localconfig lc (ldap.ParamsFrom with PurposeWrite): ldap_master_url, falling
// back to ldap_url, over StartTLS. Both keys may carry space-separated
// multi-URL values so connect-time failover works regardless of which key
// holds the list (CO-3565). Shared by the proxy and tls commands.
func OpenWriteLDAPClient(lc map[string]string) (*carboldap.Client, error) {
	params, err := carboldap.ParamsFrom(lc, carboldap.PurposeWrite)
	if errors.Is(err, carboldap.ErrNoURL) {
		return nil, errors.New("LDAP not configured (ldap_master_url and ldap_url are both empty)")
	}

	if err != nil {
		return nil, fmt.Errorf("connecting to LDAP: %w", err)
	}

	client, err := carboldap.NewClient(params)
	if err != nil {
		return nil, fmt.Errorf("connecting to LDAP: %w", err)
	}

	return client, nil
}
