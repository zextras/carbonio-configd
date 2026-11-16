// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net"
	"sync"
)

// HostIPCache remembers the last address each virtual hostname resolved to. A
// Generator lives for one generation run, so a cache that must survive a DNS
// outage has to be owned by the caller and handed to every run through
// Sources.HostIPs: when the lookup fails the last good address is kept and the
// domain's listen prefix does not flip. The zero value is not usable; use
// NewHostIPCache. A nil *HostIPCache remembers nothing.
type HostIPCache struct {
	mu  sync.Mutex
	ips map[string]net.IP
}

// NewHostIPCache returns an empty cache.
func NewHostIPCache() *HostIPCache {
	return &HostIPCache{ips: make(map[string]net.IP)}
}

// lastKnown returns the last address remembered for host, or nil.
func (c *HostIPCache) lastKnown(host string) net.IP {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.ips[host]
}

// forget drops the address remembered for host.
func (c *HostIPCache) forget(host string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.ips, host)
}

// remember records ip as the last good address of host.
func (c *HostIPCache) remember(host string, ip net.IP) {
	if c == nil || ip == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.ips[host] = ip
}
