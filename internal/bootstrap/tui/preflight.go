// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// CheckStatus is the verdict of a preflight check: they only inform, none
// stops the run.
type CheckStatus int

// The verdicts.
const (
	CheckOK CheckStatus = iota
	CheckWarn
)

// Check is one preflight line.
type Check struct {
	Status CheckStatus
	Text   string
}

// recommendedMemoryMB is the memory below which the preflight warns: the
// mailbox server wants a few GB, the other roles much less.
var recommendedMemoryMB = map[string]int{
	bootstrap.RoleAppserver: 8192,
	bootstrap.RoleMTA:       2048,
}

// PreflightInput is what the checks look at.
type PreflightInput struct {
	Cfg      *bootstrap.Config
	Host     bootstrap.Host
	Resolver bootstrap.Resolver
	Root     bool
	// PortFree reports whether nothing listens on the TCP port.
	PortFree func(port int) bool
}

// Preflight runs the checks of the apply screen.
func Preflight(ctx context.Context, in *PreflightInput) []Check {
	checks := []Check{rootCheck(in.Root)}

	if in.Resolver != nil {
		checks = append(checks, hostCheck(ctx, in))
		checks = append(checks, mxCheck(ctx, in)...)
	}

	checks = append(checks, portsCheck(in))

	return append(checks, memoryCheck(in))
}

func rootCheck(root bool) Check {
	if root {
		return Check{CheckOK, "running as root"}
	}

	return Check{CheckWarn, "not running as root"}
}

func hostCheck(ctx context.Context, in *PreflightInput) Check {
	name := in.Cfg.Host.Hostname
	r := lookupHostResult(ctx, in.Resolver, name)

	if r.ok {
		return Check{CheckOK, r.detail}
	}

	return Check{CheckWarn, r.detail}
}

func lookupHostResult(ctx context.Context, res bootstrap.Resolver, name string) dnsResult {
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()

	addrs, err := res.LookupHost(ctx, name)

	return hostResult(name, addrs, err)
}

func mxCheck(ctx context.Context, in *PreflightInput) []Check {
	if !in.Cfg.Domain.Create || in.Cfg.Domain.Name == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()

	mx, err := in.Resolver.LookupMX(ctx, in.Cfg.Domain.Name)
	r := mxResult(in.Cfg.Domain.Name, mx, err)

	if r.ok {
		return []Check{{CheckOK, r.detail}}
	}

	return []Check{{CheckWarn, r.detail}}
}

// neededPorts lists the TCP ports the roles of the node listen on.
func neededPorts(c *bootstrap.Config) []int {
	seen := map[int]bool{}

	add := func(ports ...int) {
		for _, p := range ports {
			if p > 0 {
				seen[p] = true
			}
		}
	}

	if c.HasRole(bootstrap.RoleDirectoryServer) {
		add(c.LDAP.Port)
	}

	if c.HasRole(bootstrap.RoleMTA) {
		add(25)
	}

	if c.HasRole(bootstrap.RoleAppserver) {
		p := c.Store.Ports
		add(p.IMAP, p.IMAPS, p.POP3, p.POP3S, p.HTTP, p.HTTPS)
	}

	if c.HasRole(bootstrap.RoleProxy) {
		p := c.Proxy.Ports
		add(p.IMAP, p.IMAPS, p.POP3, p.POP3S, p.HTTP, p.HTTPS)
	}

	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	sort.Ints(out)

	return out
}

func portsCheck(in *PreflightInput) Check {
	ports := neededPorts(in.Cfg)

	var busy []string

	for _, p := range ports {
		if in.PortFree != nil && !in.PortFree(p) {
			busy = append(busy, fmt.Sprint(p))
		}
	}

	if len(busy) > 0 {
		return Check{CheckWarn, "ports in use: " + strings.Join(busy, ", ") + " (fine if this node runs them already)"}
	}

	return Check{CheckOK, fmt.Sprintf("%d needed ports free", len(ports))}
}

func memoryCheck(in *PreflightInput) Check {
	want := 0

	for role, mb := range recommendedMemoryMB {
		if in.Cfg.HasRole(role) {
			want = max(want, mb)
		}
	}

	have := in.Host.MemoryMB()

	switch {
	case have == 0:
		return Check{CheckWarn, "memory unknown"}
	case have < want:
		return Check{CheckWarn, fmt.Sprintf("memory %d MB, %d MB recommended", have, want)}
	default:
		return Check{CheckOK, fmt.Sprintf("memory %d MB", have)}
	}
}
