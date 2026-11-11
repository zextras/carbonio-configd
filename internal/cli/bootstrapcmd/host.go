// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/cli/release"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// systemHost is bootstrap.Host on the running node.
type systemHost struct {
	ctx context.Context
	env sys.System
}

func (h *systemHost) FQDN() string {
	out, err := sys.Output(h.ctx, h.env.Exec, "hostname", "--fqdn")
	if err != nil {
		return ""
	}

	return strings.ToLower(strings.TrimSpace(string(out)))
}

func (h *systemHost) InstalledRoles() []string {
	installed := release.InstalledPackageNames(h.ctx, h.env)

	var roles []string

	for _, role := range bootstrap.Roles {
		if slices.Contains(installed, bootstrap.RolePackage(role)) {
			roles = append(roles, role)
		}
	}

	return roles
}

func (h *systemHost) IPMode() string {
	var v4, v6 bool

	for _, n := range interfaceNets() {
		if n.IP.To4() != nil {
			v4 = true
		} else {
			v6 = true
		}
	}

	if v6 && !v4 {
		return "ipv6"
	}

	return "ipv4"
}

// Timezone reads the zone name from the /etc/localtime link
// (.../zoneinfo/Europe/Rome), else from /etc/timezone.
func (h *systemHost) Timezone() string {
	if target, err := h.env.FS.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(target, "zoneinfo/"); ok {
			return zone
		}
	}

	if data, err := h.env.FS.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(data))
	}

	return ""
}

// MemoryMB reads MemTotal from /proc/meminfo.
func (h *systemHost) MemoryMB() int {
	data, err := h.env.FS.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(rest), " kB"))
			if err != nil {
				return 0
			}

			return kb / 1024
		}
	}

	return 0
}

func (h *systemHost) Networks() []string {
	var out []string

	for _, n := range interfaceNets() {
		masked := &net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask}

		s := masked.String()
		if n.IP.To4() == nil {
			ip, bits, _ := strings.Cut(s, "/")
			s = "[" + ip + "]/" + bits
		}

		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	return out
}

// interfaceAddrs lists the interface addresses (a test seam).
var interfaceAddrs = net.InterfaceAddrs

// interfaceNets returns the global unicast addresses of the node's
// interfaces (no loopback, no link-local).
func interfaceNets() []*net.IPNet {
	addrs, err := interfaceAddrs()
	if err != nil {
		return nil
	}

	var out []*net.IPNet

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
			out = append(out, n)
		}
	}

	return out
}
