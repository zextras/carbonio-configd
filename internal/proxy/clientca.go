// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"slices"
	"strings"
)

const clientCertCAAttr = "zimbraReverseProxyClientCertCA"

// errClientCAMissing is returned when client certificate verification is
// enabled but no client CA exists anywhere (legacy ProxyConfGen.java:2710-2713).
var errClientCAMissing = errors.New("client certificate verification is enabled but no client cert ca is provided")

// loadAllClientCertCA concatenates the distinct non-empty client CAs of the
// global/server config and of all domains (legacy loadAllClientCertCA,
// ProxyConfGen.java:362-390). Legacy joined a HashSet in iteration order; this
// keeps first-seen order (global first, then domains by name) so the output is
// deterministic. Entries are joined with a newline and the last separator
// trimmed.
func (g *Generator) loadAllClientCertCA(domains []DomainInfo) string {
	var cas []string

	add := func(ca string) {
		if strings.TrimSpace(ca) == "" {
			return
		}

		if !slices.Contains(cas, ca) {
			cas = append(cas, ca)
		}
	}

	if global, ok := g.getConfigValue(clientCertCAAttr, sourceServer, sourceGlobal); ok {
		add(global)
	}

	for i := range domains {
		add(domains[i].ClientCertCA)
	}

	return strings.Join(cas, "\n")
}

// clientCertVerifyEnabled mirrors isClientCertVerifyEnabled /
// isDomainClientCertVerifyEnabled (ProxyConfGen.java:2745-2765).
func (g *Generator) clientCertVerifyEnabled(domains []DomainInfo) bool {
	mode, ok := g.getConfigValue("zimbraReverseProxyClientCertMode", sourceServer, sourceGlobal)
	if !ok {
		mode = nginxOff
	}

	if clientCertVerifies(mode) {
		return true
	}

	for i := range domains {
		if clientCertVerifies(domains[i].ClientCertMode) {
			return true
		}
	}

	return false
}

// clientCertVerifies reports whether a client cert mode requires a client CA.
func clientCertVerifies(mode string) bool {
	return mode == "on" || mode == "optional"
}

// syncClientCA writes the combined client CA to nginx.client.ca.crt and sets
// ssl.clientcertca.enabled accordingly (legacy writeClientCAtoFile,
// ProxyConfGen.java:2701-2740, called at :2236-2237). The file is written
// only when its content differs. With no CA at all and verification enabled,
// generation fails as in legacy. Unlike legacy, dry-run does not write the
// file.
func (tp *TemplateProcessor) syncClientCA(ctx context.Context, domains []DomainInfo) error {
	g := tp.generator
	clientCA := g.loadAllClientCertCA(domains)

	if clientCA == "" {
		g.setClientCAEnabled(false)

		if g.clientCertVerifyEnabled(domains) {
			return errClientCAMissing
		}

		return nil
	}

	g.setClientCAEnabled(true)

	if g.DryRun {
		return nil
	}

	return writeIfChanged(ctx, g.clientCertCAPath(), clientCA)
}

// setClientCAEnabled records ssl.clientcertca.enabled (legacy mVars.put).
func (g *Generator) setClientCAEnabled(enabled bool) {
	if v, ok := g.Variables["ssl.clientcertca.enabled"]; ok && v != nil {
		v.Value = enabled
	}
}
