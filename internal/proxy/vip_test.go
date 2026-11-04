// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestVirtualIPPrefix pins the listen address prefix to ProxyConfGen's
// fillVarsWithDomainAttrs: empty with SNI (the default), "<ip>:" or "[<ip>]:"
// when zimbraReverseProxySNIEnabled is FALSE, "[::]:" for IPv6 with SNI.
func TestVirtualIPPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sni    string // zimbraReverseProxySNIEnabled; "" leaves it unset
		domain DomainInfo
		want   string
	}{
		{"sni default, IPv4", "", DomainInfo{VirtualIPAddress: "192.0.2.50"}, ""},
		{"sni true, IPv4", "TRUE", DomainInfo{VirtualIPAddress: "192.0.2.50"}, ""},
		{"sni false, IPv4", "FALSE", DomainInfo{VirtualIPAddress: "192.0.2.50"}, "192.0.2.50:"},
		{"sni true, IPv6", "TRUE", DomainInfo{VirtualIPAddress: "2001:db8::1"}, "[::]:"},
		{"sni default, bracketed IPv6", "", DomainInfo{VirtualIPAddress: "[2001:db8::1]"}, "[::]:"},
		{"sni false, IPv6", "FALSE", DomainInfo{VirtualIPAddress: "2001:db8::1"}, "[2001:db8::1]:"},
		{"sni false, hostname literal address", "FALSE", DomainInfo{VirtualHostname: "198.51.100.7"}, "198.51.100.7:"},
		{"sni false, address preferred over hostname", "FALSE",
			DomainInfo{VirtualHostname: "198.51.100.7", VirtualIPAddress: "192.0.2.50"}, "192.0.2.50:"},
		{"sni false, unresolvable", "FALSE", DomainInfo{VirtualHostname: "no-such-host.invalid"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := config.NewConfigMap()
			if tt.sni != "" {
				srv.Set("zimbraReverseProxySNIEnabled", tt.sni)
			}

			tp := &TemplateProcessor{generator: &Generator{ServerConfig: &config.ServerConfig{Data: srv}}}

			assert.Equal(t, tt.want, tp.virtualIPPrefix(context.Background(), &tt.domain))
		})
	}
}
