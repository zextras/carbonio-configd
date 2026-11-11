// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func hostTestHost(t *testing.T) (*systemHost, *systest.Fakes) {
	t.Helper()

	f := systest.New()

	return &systemHost{ctx: context.Background(), env: f.System()}, f
}

func hostTestAddrs(t *testing.T, cidrs ...string) {
	t.Helper()

	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })

	interfaceAddrs = func() ([]net.Addr, error) {
		var out []net.Addr

		for _, c := range cidrs {
			ip, n, err := net.ParseCIDR(c)
			if err != nil {
				return nil, err
			}

			out = append(out, &net.IPNet{IP: ip, Mask: n.Mask})
		}

		return out, nil
	}
}

func TestSystemHost_FQDN(t *testing.T) {
	h, f := hostTestHost(t)
	f.Exec.OnPath("hostname", systest.Reply("  Mail.Example.COM\n"))

	assert.Equal(t, "mail.example.com", h.FQDN(), "lowercased and trimmed")
	assert.Equal(t, []string{"hostname --fqdn"}, f.Exec.Lines())

	h, f = hostTestHost(t)
	f.Exec.OnPath("hostname", systest.Fail(1, "no name"))

	assert.Empty(t, h.FQDN(), "a failing hostname gives no default")
}

func TestSystemHost_InstalledRoles(t *testing.T) {
	h, f := hostTestHost(t)
	f.FS.Put("/etc/os-release", []byte("ID=rocky\n"), 0o644)
	f.Exec.OnPath("rpm", systest.Reply(
		"carbonio-core.x86_64 1-1\ncarbonio-mta.x86_64 1-1\ncarbonio-appserver.x86_64 1-1\ncarbonio-clamav.x86_64 1-1\n"))

	got := h.InstalledRoles()

	assert.ElementsMatch(t, []string{bootstrap.RoleMTA, bootstrap.RoleAppserver, bootstrap.RoleClamAV}, got)
	assert.NotContains(t, got, bootstrap.RoleDirectoryServer)
}

func TestSystemHost_IPMode(t *testing.T) {
	for name, tc := range map[string]struct {
		addrs []string
		want  string
	}{
		"ipv4 only":                    {[]string{"192.0.2.10/24"}, "ipv4"},
		"dual stack":                   {[]string{"192.0.2.10/24", "2001:db8::10/64"}, "ipv4"},
		"ipv6 only":                    {[]string{"2001:db8::10/64"}, "ipv6"},
		"loopback and link-local only": {[]string{"127.0.0.1/8", "::1/128", "fe80::1/64"}, "ipv4"},
		"ipv6 with loopback v4":        {[]string{"127.0.0.1/8", "2001:db8::10/64"}, "ipv6"},
	} {
		t.Run(name, func(t *testing.T) {
			hostTestAddrs(t, tc.addrs...)

			h, _ := hostTestHost(t)
			assert.Equal(t, tc.want, h.IPMode())
		})
	}
}

func TestSystemHost_Networks(t *testing.T) {
	hostTestAddrs(t, "127.0.0.1/8", "192.0.2.10/24", "192.0.2.77/24", "2001:db8:1:2::10/64", "fe80::1/64")

	h, _ := hostTestHost(t)

	assert.Equal(t, []string{"192.0.2.0/24", "[2001:db8:1:2::]/64"}, h.Networks(),
		"masked, deduplicated, IPv6 bracketed, no loopback or link-local")
}

func TestSystemHost_NoInterfaces(t *testing.T) {
	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })

	interfaceAddrs = func() ([]net.Addr, error) { return nil, assert.AnError }

	h, _ := hostTestHost(t)

	assert.Empty(t, h.Networks())
	assert.Equal(t, "ipv4", h.IPMode())
}

func TestSystemHost_Timezone(t *testing.T) {
	t.Run("localtime link", func(t *testing.T) {
		h, f := hostTestHost(t)
		f.FS.PutLink("/etc/localtime", "../usr/share/zoneinfo/Europe/Rome")
		f.FS.Put("/etc/timezone", []byte("UTC\n"), 0o644)

		assert.Equal(t, "Europe/Rome", h.Timezone(), "the link wins over /etc/timezone")
	})

	t.Run("link outside zoneinfo falls back to /etc/timezone", func(t *testing.T) {
		h, f := hostTestHost(t)
		f.FS.PutLink("/etc/localtime", "/etc/custom-tz")
		f.FS.Put("/etc/timezone", []byte(" America/New_York\n"), 0o644)

		assert.Equal(t, "America/New_York", h.Timezone())
	})

	t.Run("no information", func(t *testing.T) {
		h, _ := hostTestHost(t)

		assert.Empty(t, h.Timezone())
	})
}

func TestSystemHost_MemoryMB(t *testing.T) {
	for name, tc := range map[string]struct {
		meminfo string
		want    int
	}{
		"4 GiB":       {"MemFree:  100 kB\nMemTotal:       4194304 kB\nBuffers: 1 kB\n", 4096},
		"rounds down": {"MemTotal: 1500 kB\n", 1},
		"malformed":   {"MemTotal: lots kB\n", 0},
		"absent":      {"MemFree: 100 kB\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			h, f := hostTestHost(t)
			f.FS.Put("/proc/meminfo", []byte(tc.meminfo), 0o644)

			assert.Equal(t, tc.want, h.MemoryMB())
		})
	}

	h, _ := hostTestHost(t)
	assert.Zero(t, h.MemoryMB(), "unreadable /proc/meminfo")
}
