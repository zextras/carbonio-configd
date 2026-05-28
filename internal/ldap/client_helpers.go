// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// QueryDomains returns all domains with a non-empty zimbraVirtualHostname.
func (c *Client) QueryDomains(ctx context.Context) ([]Domain, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	t0 := time.Now()

	all, err := c.GetAllDomainsWithAttributes()
	if err != nil {
		return nil, fmt.Errorf("failed to get domains with attributes: %w", err)
	}

	domains := make([]Domain, 0, len(all))
	for name, a := range all {
		d := Domain{
			DomainName:       name,
			VirtualHostname:  a["zimbraVirtualHostname"],
			VirtualIPAddress: a["zimbraVirtualIPAddress"],
			ClientCertMode:   a["zimbraClientCertMode"],
			SSLCertificate:   a["zimbraSSLCertificate"],
			SSLPrivateKey:    a["zimbraSSLPrivateKey"],
		}
		if d.VirtualHostname != "" {
			domains = append(domains, d)
		}
	}

	logger.DebugContext(ctx, "QueryDomains completed",
		"duration_ms", time.Since(t0).Milliseconds(),
		"total_domains", len(all), "filtered_domains", len(domains))

	return domains, nil
}

// QueryServers returns servers with the named service enabled.
func (c *Client) QueryServers(ctx context.Context, serviceName string) ([]Server, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")

	all, err := c.GetAllServersWithAttributes()
	if err != nil {
		return nil, fmt.Errorf("failed to query servers: %w", err)
	}

	servers := make([]Server, 0, len(all))
	for _, a := range all {
		id := a["zimbraId"]

		host := a["zimbraServiceHostname"]
		if id == "" || host == "" {
			continue
		}

		if !strings.Contains(a[attrZimbraServiceEnabled], serviceName) {
			continue
		}

		servers = append(servers, Server{ServerID: id, ServiceHostname: host})
	}

	logger.DebugContext(ctx, "QueryServers completed",
		"service", serviceName, "count", len(servers))

	return servers, nil
}
