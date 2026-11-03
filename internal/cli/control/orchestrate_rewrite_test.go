// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	rewriteScript = testBase + "/libexec/configrewrite"
	resolverctl   = testBase + "/bin/zmresolverctl"
)

// rewriteFlow is an orchestrator with proxy and mta registered with config
// rewrite sets; ldap and configd are already running.
type rewriteFlow struct {
	o      *orchestrator
	fakes  *systest.Fakes
	starts []string
	// resolverBeforeProxy records whether zmresolverctl had run when proxy started.
	resolverBeforeProxy bool
}

func newRewriteFlow(t *testing.T, lc map[string]string, enabled ...string) *rewriteFlow {
	t.Helper()

	rf := &rewriteFlow{}
	registry := map[string]*services.ServiceDef{}

	add := func(name string, rewrite []string) {
		registry[name] = &services.ServiceDef{
			Name:          name,
			DisplayName:   name,
			ConfigRewrite: rewrite,
			CustomStart: func(context.Context, *services.ServiceDef) error {
				rf.starts = append(rf.starts, name)

				if name == "proxy" {
					for _, l := range rf.fakes.Exec.Lines() {
						if strings.HasPrefix(l, resolverctl) {
							rf.resolverBeforeProxy = true
						}
					}
				}

				return nil
			},
		}
	}

	add("ldap", nil)
	add("configd", nil)
	add("proxy", []string{"proxy", "mta"})
	add("mta", []string{"mta", "zimbra"})

	base := map[string]string{"zimbra_server_hostname": "h.example.com", "ldap_url": "ldap://h.example.com:389"}
	for k, v := range lc {
		base[k] = v
	}

	rf.o, rf.fakes = testOrchestrator(registry, base)
	rf.o.svcStatus = func(_ context.Context, name string) (bool, error) {
		return name == "ldap" || name == "configd", nil
	}

	rf.fakes.FS.Put(testBase+"/log/.zmcontrol.cache", []byte(strings.Join(enabled, "\n")+"\n"), 0o600)
	rf.fakes.FS.Put(rewriteScript, []byte("#!/bin/sh\n"), 0o755)

	return rf
}

func (rf *rewriteFlow) rewriteCalls() [][]string {
	var calls [][]string

	for _, l := range rf.fakes.Exec.Lines() {
		if f := strings.Fields(l); len(f) > 0 && f[0] == rewriteScript {
			calls = append(calls, f[1:])
		}
	}

	return calls
}

// C-17: one rewrite of the union of the sections of every service to start,
// then each service starts norewrite — not one rewrite per service.
func TestControlStart_RewritesOnceUpFrontThenStartsNoRewrite(t *testing.T) {
	rf := newRewriteFlow(t, nil, "proxy", "mta")

	c, _ := newConsole()

	if rc := rf.o.controlStart(context.Background(), c); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	calls := rf.rewriteCalls()
	if len(calls) != 1 {
		t.Fatalf("configrewrite ran %d times (%v), want once", len(calls), calls)
	}

	got := slices.Clone(calls[0])
	slices.Sort(got)

	if want := []string{"mta", "proxy", "zimbra"}; !slices.Equal(got, want) {
		t.Errorf("rewrite sections = %v, want union %v", got, want)
	}

	if len(rf.starts) != 2 {
		t.Errorf("starts = %v, want proxy and mta", rf.starts)
	}
}

func TestControlStart_NothingToStartDoesNotRewrite(t *testing.T) {
	rf := newRewriteFlow(t, nil)

	c, _ := newConsole()

	if rc := rf.o.controlStart(context.Background(), c); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	if calls := rf.rewriteCalls(); len(calls) != 0 {
		t.Errorf("rewrite ran for nothing to start: %v", calls)
	}
}

// C-17: zmresolverctl regenerates resolvers.conf right before proxy starts.
func TestControlStart_RunsResolverctlBeforeProxy(t *testing.T) {
	rf := newRewriteFlow(t, nil, "proxy", "mta")
	rf.fakes.FS.Put(resolverctl, []byte("#!/bin/sh\n"), 0o755)

	c, out := newConsole()

	if rc := rf.o.controlStart(context.Background(), c); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	if !rf.resolverBeforeProxy {
		t.Error("zmresolverctl did not run before proxy started")
	}

	if strings.Contains(out.String(), "resolvers.conf file generation failed.") {
		t.Errorf("stdout = %q, unexpected failure message", out)
	}
}

// A failing zmresolverctl is reported and start goes on (legacy control.pl).
func TestControlStart_ResolverctlFailureIsReportedAndStartContinues(t *testing.T) {
	rf := newRewriteFlow(t, nil, "proxy")
	rf.fakes.FS.Put(resolverctl, []byte("#!/bin/sh\n"), 0o755)
	rf.fakes.Exec.OnPath(resolverctl, systest.Fail(1, "no dns"))

	c, out := newConsole()

	if rc := rf.o.controlStart(context.Background(), c); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}

	if !strings.Contains(out.String(), "resolvers.conf file generation failed.") {
		t.Errorf("stdout = %q, want the resolver failure line", out)
	}

	if !slices.Contains(rf.starts, "proxy") {
		t.Errorf("proxy was not started: %v", rf.starts)
	}
}

// C-15: LC zmcontrol_service_status_list picks the services whose stopped
// state fails `control status`; unset, everything but stats/logger counts.
// Here mta (enabled) and configd (always shown) are both stopped.
func TestControlStatus_ServiceStatusList(t *testing.T) {
	tests := []struct {
		name   string
		list   string
		wantRC int
	}{
		{"unset: a stopped service fails", "", 1},
		{"listed stopped service fails", "mta", 1},
		{"whitespace separated list", "  stats \t mta ", 1},
		{"listed service not shown does not count", "stats proxy", 0},
		{"only stats listed: stopped mta is tolerated", "stats", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := newRewriteFlow(t, map[string]string{"zmcontrol_service_status_list": tt.list}, "mta")

			c, _ := newConsole()

			if rc := rf.o.controlStatus(context.Background(), c); rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}
		})
	}
}
