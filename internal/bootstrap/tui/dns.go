// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"context"
	"fmt"
	"net"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// dnsTimeout bounds one lookup: a slow resolver never holds the screen.
const dnsTimeout = 3 * time.Second

// The DNS checks.
const (
	dnsHost = "host"
	dnsMX   = "mx"
)

// dnsResult is the answer of one check; the screen only informs, it never
// blocks on it.
type dnsResult struct {
	name   string
	ok     bool
	detail string
}

type dnsMsg struct {
	kind   string
	result dnsResult
}

// dnsCmds starts the lookups of the names that changed since the last ones.
func (m *Model) dnsCmds() tea.Cmd {
	if m.opts.Resolver == nil {
		return nil
	}

	var cmds []tea.Cmd

	for kind, name := range map[string]string{dnsHost: m.r.Config.Host.Hostname, dnsMX: m.domainName()} {
		if name == "" || m.dnsAsk[kind] == name {
			continue
		}

		m.dnsAsk[kind] = name
		delete(m.dns, kind)
		cmds = append(cmds, lookup(m.opts.Resolver, kind, name))
	}

	return tea.Batch(cmds...)
}

func (m *Model) domainName() string {
	if !m.r.Config.Domain.Create {
		return ""
	}

	return m.r.Config.Domain.Name
}

func lookup(res bootstrap.Resolver, kind, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
		defer cancel()

		if kind == dnsMX {
			mx, err := res.LookupMX(ctx, name)

			return dnsMsg{kind, mxResult(name, mx, err)}
		}

		addrs, err := res.LookupHost(ctx, name)

		return dnsMsg{kind, hostResult(name, addrs, err)}
	}
}

func hostResult(name string, addrs []string, err error) dnsResult {
	if err != nil || len(addrs) == 0 {
		return dnsResult{name: name, detail: name + " does not resolve"}
	}

	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && !ip.IsLoopback() {
			return dnsResult{name: name, ok: true, detail: name + " → " + a}
		}
	}

	return dnsResult{name: name, detail: name + " resolves only to loopback"}
}

func mxResult(name string, mx []*net.MX, err error) dnsResult {
	if err != nil || len(mx) == 0 {
		return dnsResult{name: name, detail: name + " has no MX record"}
	}

	return dnsResult{name: name, ok: true, detail: fmt.Sprintf("%s MX %s", name, mx[0].Host)}
}

// dnsLine renders the DNS checks in one line.
func (m *Model) dnsLine() string {
	if m.opts.Resolver == nil {
		return ""
	}

	var parts []string

	for _, kind := range []string{dnsHost, dnsMX} {
		name := m.dnsAsk[kind]
		if name == "" {
			continue
		}

		r, done := m.dns[kind]

		switch {
		case !done:
			parts = append(parts, m.th.muted.Render("… "+name))
		case r.ok:
			parts = append(parts, m.th.ok.Render("✓ "+r.detail))
		default:
			parts = append(parts, m.th.warn.Render("! "+r.detail))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return m.th.muted.Render("DNS ") + joinWith(parts, "  ")
}
