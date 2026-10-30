// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// reloadLocalConfigEnvelope is the admin SOAP ReloadLocalConfigRequest
// (AdminConstants.RELOAD_LOCAL_CONFIG_REQUEST in carbonio-mailbox).
const reloadLocalConfigEnvelope = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">` +
	`<soap:Body><ReloadLocalConfigRequest xmlns="urn:zimbraAdmin"/></soap:Body>` +
	`</soap:Envelope>`

// ReloadLocalConfig asks mailboxd to re-read its localconfig.xml, like the
// legacy "zmlocalconfig -l" (LocalConfigCLI.reload): admin authentication with
// the LDAP master credentials followed by ReloadLocalConfigRequest.
func (c *Client) ReloadLocalConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureToken(ctx); err != nil {
		return err
	}

	token := c.token

	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+soapPath,
			strings.NewReader(reloadLocalConfigEnvelope))
		if err != nil {
			return nil, fmt.Errorf("zxadmin: build reload request: %w", err)
		}

		req.Header.Set("Content-Type", "text/xml; charset=UTF-8")
		req.AddCookie(authCookie(token))

		return req, nil
	})
	if err != nil {
		return fmt.Errorf("zxadmin: reload request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("zxadmin: read reload response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.token = ""
		c.expiresAt = time.Time{}

		return fmt.Errorf("zxadmin: reload auth rejected: HTTP %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zxadmin: reload failed: HTTP %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	return nil
}
