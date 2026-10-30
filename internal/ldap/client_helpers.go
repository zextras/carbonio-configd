// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// QueryDomains returns one Domain per zimbraVirtualHostname value of every
// domain that has a virtual hostname and at least one of SSL certificate, SSL
// private key, client cert mode or client CA, mirroring legacy
// ProxyConfGen.loadDomainReverseProxyAttrs (ProxyConfGen.java:253-352).
// When zimbraVirtualIPAddress is set, the i-th hostname gets the i-th address
// if both lists have the same length, otherwise the first address (:326-337).
// The result is ordered by domain name for deterministic output.
func (c *Client) QueryDomains(ctx context.Context) ([]Domain, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	t0 := time.Now()

	all, err := c.GetAllDomainsWithAttributes()
	if err != nil {
		return nil, fmt.Errorf("failed to get domains with attributes: %w", err)
	}

	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}

	slices.Sort(names)

	domains := make([]Domain, 0, len(all))

	for _, name := range names {
		a := all[name]

		vhns := splitMultiValue(a["zimbraVirtualHostname"])
		vips := splitMultiValue(a["zimbraVirtualIPAddress"])

		// Legacy skips domains without a virtual hostname or without any of
		// cert, key, client cert mode and client CA: they use the global
		// configuration (ProxyConfGen.java:303-310).
		if len(vhns) == 0 || (a["zimbraSSLCertificate"] == "" && a["zimbraSSLPrivateKey"] == "" &&
			a[AttrZimbraReverseProxyClientCertMode] == "" && a[AttrZimbraReverseProxyClientCertCA] == "") {
			continue
		}

		for i, vhn := range vhns {
			domains = append(domains, Domain{
				DomainName:       name,
				VirtualHostname:  vhn,
				VirtualIPAddress: pickVirtualIP(vips, len(vhns), i),
				ClientCertMode:   a[AttrZimbraReverseProxyClientCertMode],
				ClientCertCA:     a[AttrZimbraReverseProxyClientCertCA],
				SSLCertificate:   a["zimbraSSLCertificate"],
				SSLPrivateKey:    a["zimbraSSLPrivateKey"],
			})
		}
	}

	logger.DebugContext(ctx, "QueryDomains completed",
		"duration_ms", time.Since(t0).Milliseconds(),
		"total_domains", len(all), "filtered_domains", len(domains))

	return domains, nil
}

// splitMultiValue splits a newline-joined multi-valued attribute (see
// entryToMap), dropping empty values.
func splitMultiValue(v string) []string {
	var out []string

	for part := range strings.SplitSeq(v, "\n") {
		if part != "" {
			out = append(out, part)
		}
	}

	return out
}

// pickVirtualIP returns the virtual IP for the i-th of n hostnames.
func pickVirtualIP(vips []string, n, i int) string {
	switch {
	case len(vips) == 0:
		return ""
	case len(vips) == n:
		return vips[i]
	default:
		return vips[0]
	}
}

// SetDomainSSLCertKey replaces zimbraSSLCertificate and zimbraSSLPrivateKey on
// the named domain in a single LDAP modify (legacy mProv.modifyAttrs,
// ProxyConfGen.java:2590-2597).
func (c *Client) SetDomainSSLCertKey(domainName, certificate, privateKey string) error {
	filter := fmt.Sprintf("(&(objectClass=zimbraDomain)(zimbraDomainName=%s))", ldap.EscapeFilter(domainName))

	result, err := c.search("", filter, []string{attrZimbraDomainName}, ldap.ScopeWholeSubtree)
	if err != nil {
		return fmt.Errorf("failed to look up domain %s: %w", domainName, err)
	}

	if len(result.Entries) == 0 {
		return fmt.Errorf("no such domain: %s", domainName)
	}

	dn := result.Entries[0].DN

	return c.executeWithRetry(func(conn *ldap.Conn) error {
		req := ldap.NewModifyRequest(dn, nil)
		req.Replace("zimbraSSLCertificate", []string{certificate})
		req.Replace("zimbraSSLPrivateKey", []string{privateKey})

		return conn.Modify(req)
	})
}

// serverHasService reports whether the (possibly multi-valued, newline-joined)
// zimbraServiceEnabled value contains serviceName as an exact value. This mirrors
// the Java LDAP equality filter (zimbraServiceEnabled=<service>) and avoids the
// substring bug where "zimbra" would match "zimbraAdmin".
func serverHasService(serviceEnabled, serviceName string) bool {
	for v := range strings.SplitSeq(serviceEnabled, "\n") {
		if strings.TrimSpace(v) == serviceName {
			return true
		}
	}

	return false
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

		if !serverHasService(a[AttrZimbraServiceEnabled], serviceName) {
			continue
		}

		servers = append(servers, Server{ServerID: id, ServiceHostname: host, Attributes: a})
	}

	logger.DebugContext(ctx, "QueryServers completed",
		"service", serviceName, "count", len(servers))

	return servers, nil
}
