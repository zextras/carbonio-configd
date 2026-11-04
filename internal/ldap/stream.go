// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"

	"github.com/go-ldap/ldap/v3"
)

// NewStreamConn opens a connection for a long-lived operation, such as an
// RFC 4533 content-sync search that stays open and streams changes. It uses the
// same URL failover, StartTLS/ldaps, CA, bind and dial/bind time bounds as the
// pooled connections, but the connection is never pooled and carries no request
// timeout, because a persistent search is idle for as long as nothing changes.
//
// The caller owns the connection and must Close it. Retries are the caller's
// job: this is a single attempt over the URL list.
func (c *Client) NewStreamConn(ctx context.Context) (*ldap.Conn, error) {
	return c.connect(ctx)
}
