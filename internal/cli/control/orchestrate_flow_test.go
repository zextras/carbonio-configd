// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

var errBoom = errors.New("boom")

// flowEnv is an orchestrator whose services record starts/stops instead of
// launching processes. running is the probe answer per service.
type flowEnv struct {
	o       *orchestrator
	fakes   *systest.Fakes
	calls   []string
	running map[string]bool
	failOn  map[string]bool
}

// newFlowEnv builds ldap (local), configd and the given extra services; the
// cache enables enabled (LDAP is not configured, so discovery uses the cache).
func newFlowEnv(t *testing.T, extra []string, enabled []string) *flowEnv {
	t.Helper()

	fe := &flowEnv{running: map[string]bool{}, failOn: map[string]bool{}}
	registry := map[string]*services.ServiceDef{}

	for _, name := range append([]string{"ldap", "configd"}, extra...) {
		registry[name] = &services.ServiceDef{
			Name:        name,
			DisplayName: name,
			CustomStart: func(context.Context, *services.ServiceDef) error {
				fe.calls = append(fe.calls, "start "+name)

				if fe.failOn["start "+name] {
					return errBoom
				}

				fe.running[name] = true

				return nil
			},
			CustomStop: func(context.Context, *services.ServiceDef) error {
				fe.calls = append(fe.calls, "stop "+name)

				if fe.failOn["stop "+name] {
					return errBoom
				}

				return nil
			},
		}
	}

	o, f := testOrchestrator(registry, map[string]string{
		"zimbra_server_hostname": "h.example.com",
		"ldap_url":               "ldap://h.example.com:389",
	})
	o.svcStatus = func(_ context.Context, name string) (bool, error) { return fe.running[name], nil }

	f.FS.Put(testBase+"/log/.zmcontrol.cache", []byte(strings.Join(enabled, "\n")+"\n"), 0o600)

	fe.o, fe.fakes = o, f

	return fe
}

func TestControlStart(t *testing.T) {
	tests := []struct {
		name      string
		running   map[string]bool
		failOn    string
		enabled   []string
		wantRC    int
		wantCalls []string
	}{
		{
			name:      "starts ldap, configd then enabled services only",
			enabled:   []string{"mta"},
			wantCalls: []string{"start ldap", "start configd", "start mta"},
		},
		{
			name:      "running bootstrap services are not restarted",
			running:   map[string]bool{"ldap": true, "configd": true},
			enabled:   []string{"mta"},
			wantCalls: []string{"start mta"},
		},
		{
			name:      "ldap start failure aborts",
			failOn:    "start ldap",
			enabled:   []string{"mta"},
			wantRC:    1,
			wantCalls: []string{"start ldap"},
		},
		{
			name:      "configd start failure aborts",
			running:   map[string]bool{"ldap": true},
			failOn:    "start configd",
			enabled:   []string{"mta"},
			wantRC:    1,
			wantCalls: []string{"start configd"},
		},
		{
			name:      "enabled service failure yields rc 1 but others start",
			running:   map[string]bool{"ldap": true, "configd": true},
			failOn:    "start mta",
			enabled:   []string{"mta", "proxy"},
			wantRC:    1,
			wantCalls: []string{"start proxy", "start mta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := newFlowEnv(t, []string{"mta", "proxy", "unused"}, tt.enabled)
			fe.running = tt.running

			if fe.running == nil {
				fe.running = map[string]bool{}
			}

			fe.failOn[tt.failOn] = true

			c, _ := newConsole()

			if rc := fe.o.controlStart(context.Background(), c); rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}

			if !slices.Equal(fe.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", fe.calls, tt.wantCalls)
			}
		})
	}
}

// A start that returned success but left the service stopped (the config
// daemon restarted it right after, or it died) must not end as "Done" with
// rc 0: `control status` would report it Stopped.
func TestControlStart_ServiceDownAfterStartFails(t *testing.T) {
	tests := []struct {
		name       string
		down       string
		probeErr   bool
		wantRC     int
		wantStderr string
	}{
		{name: "started service is down", down: "proxy", wantRC: 1, wantStderr: "proxy is not running after start\n"},
		{name: "stats is not counted by status", down: "stats"},
		{name: "probe error is not evidence", down: "proxy", probeErr: true},
		{name: "all up"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := newFlowEnv(t, []string{"proxy", "stats"}, []string{"proxy", "stats"})
			fe.running["ldap"], fe.running["configd"] = true, true

			fe.o.svcStatus = func(_ context.Context, name string) (bool, error) {
				switch {
				case name != tt.down:
					return slices.Contains(fe.calls, "start "+name) || fe.running[name], nil
				case tt.probeErr && slices.Contains(fe.calls, "start "+name):
					return false, errBoom
				default:
					return fe.running[name] && !slices.Contains(fe.calls, "start "+name), nil
				}
			}

			var out, errOut bytes.Buffer

			c := newConsoleTo(&out, &errOut)

			if rc := fe.o.controlStart(context.Background(), c); rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}

			if errOut.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", errOut.String(), tt.wantStderr)
			}

			if want := []string{"start proxy", "start stats"}; !slices.Equal(fe.calls, want) {
				t.Errorf("calls = %v, want %v: the check must not start or stop anything", fe.calls, want)
			}
		})
	}
}

func TestControlStart_DiskWarning(t *testing.T) {
	fe := newFlowEnv(t, []string{"mta"}, []string{"mta"})
	fe.running["ldap"], fe.running["configd"] = true, true
	fe.fakes.FS.Avail = 0

	c, out := newConsole()

	if rc := fe.o.controlStart(context.Background(), c); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	if !strings.Contains(out.String(), "Disk space below threshold for ") {
		t.Errorf("stdout = %q, want disk space warning", out)
	}
}

func TestControlStart_ProbeErrorAborts(t *testing.T) {
	fe := newFlowEnv(t, []string{"mta"}, []string{"mta"})
	fe.o.svcStatus = func(context.Context, string) (bool, error) { return false, errBoom }

	c, out := newConsole()

	if rc := fe.o.controlStart(context.Background(), c); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if len(fe.calls) != 0 {
		t.Errorf("no service may start after a probe error, got %v", fe.calls)
	}

	if !strings.Contains(out.String(), "Cannot determine status of ldap: boom") {
		t.Errorf("stdout = %q, want probe warning", out)
	}
}

func TestControlStart_DiscoveryFailure(t *testing.T) {
	o, _ := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	o.svcStatus = func(context.Context, string) (bool, error) { return true, nil }

	var out, errOut bytes.Buffer

	c := newConsoleTo(&out, &errOut)

	if rc := o.controlStart(context.Background(), c); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}

	if !strings.HasPrefix(errOut.String(), "Cannot determine services: ") {
		t.Errorf("stderr = %q, want discovery error", errOut.String())
	}
}

func TestControlStop(t *testing.T) {
	tests := []struct {
		name      string
		running   map[string]bool
		failOn    string
		wantRC    int
		wantCalls []string
	}{
		{
			name:      "reverse order with ldap then configd last",
			running:   map[string]bool{"ldap": true, "configd": true, "mta": true, "proxy": true},
			wantCalls: []string{"stop mta", "stop proxy", "stop ldap", "stop configd"},
		},
		{
			name:      "stopped services are skipped",
			running:   map[string]bool{"proxy": true},
			wantCalls: []string{"stop proxy"},
		},
		{
			name:      "service stop failure",
			running:   map[string]bool{"mta": true, "proxy": true},
			failOn:    "stop mta",
			wantRC:    1,
			wantCalls: []string{"stop mta", "stop proxy"},
		},
		{
			name:      "ldap stop failure",
			running:   map[string]bool{"ldap": true},
			failOn:    "stop ldap",
			wantRC:    1,
			wantCalls: []string{"stop ldap"},
		},
		{
			name:      "configd stop failure",
			running:   map[string]bool{"configd": true},
			failOn:    "stop configd",
			wantRC:    1,
			wantCalls: []string{"stop configd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := newFlowEnv(t, []string{"mta", "proxy"}, nil)
			fe.running = tt.running
			fe.failOn[tt.failOn] = true

			c, _ := newConsole()

			rc := fe.o.controlStop(context.Background(), c)
			if rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}

			got := fe.calls
			// mta and proxy order is registry-defined; compare as sets, but
			// ldap/configd must come last in that order.
			if len(got) != len(tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", got, tt.wantCalls)
			}

			if tail := got[max(0, len(got)-2):]; slices.Contains(got, "stop ldap") && slices.Contains(got, "stop configd") &&
				!slices.Equal(tail, []string{"stop ldap", "stop configd"}) {
				t.Errorf("ldap/configd must stop last: %v", got)
			}

			for _, w := range tt.wantCalls {
				if !slices.Contains(got, w) {
					t.Errorf("calls = %v, missing %q", got, w)
				}
			}
		})
	}
}

func TestStopLDAPIfLocal_LocalRunning(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		failOn  bool
		wantRC  int
		want    []string
	}{
		{"running stops", true, false, 0, []string{"stop ldap"}},
		{"stop failure", true, true, 1, []string{"stop ldap"}},
		{"already stopped", false, false, 0, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := newFlowEnv(t, nil, nil)
			fe.running["ldap"] = tt.running
			fe.failOn["stop ldap"] = tt.failOn

			c, _ := newConsole()

			if rc := fe.o.stopLDAPIfLocal(context.Background(), c, map[string]bool{"ldap": true}); rc != tt.wantRC {
				t.Errorf("rc = %d, want %d", rc, tt.wantRC)
			}

			if !slices.Equal(fe.calls, tt.want) {
				t.Errorf("calls = %v, want %v", fe.calls, tt.want)
			}
		})
	}
}

func TestRun_ActionsLocal(t *testing.T) {
	tests := []struct {
		name       string
		action     Action
		running    map[string]bool
		failOn     string
		wantCode   int
		wantStderr string
		wantCalls  []string
	}{
		{
			name: "start", action: ActionStart,
			running:   map[string]bool{"ldap": true, "configd": true},
			wantCalls: []string{"start mta"},
		},
		{
			name: "start failure", action: ActionStart,
			running: map[string]bool{"ldap": true, "configd": true}, failOn: "start mta",
			wantCode: 1, wantStderr: "Error: some services failed to start\n",
			wantCalls: []string{"start mta"},
		},
		{
			name: "stop", action: ActionStop,
			running:   map[string]bool{"mta": true},
			wantCalls: []string{"stop mta"},
		},
		{
			name: "stop failure", action: ActionStop,
			running: map[string]bool{"mta": true}, failOn: "stop mta",
			wantCode: 1, wantStderr: "Error: some services failed to stop\n",
			wantCalls: []string{"stop mta"},
		},
		{
			name: "restart ignores stop failure", action: ActionRestart,
			running: map[string]bool{"mta": true, "ldap": true, "configd": true}, failOn: "stop mta",
			wantCalls: []string{"stop mta", "stop ldap", "stop configd", "start ldap", "start configd", "start mta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe := newFlowEnv(t, []string{"mta"}, []string{"mta"})
			fe.failOn[tt.failOn] = true

			// The probe follows the last action taken on a service: a stop
			// leaves it down (so restart sees services down), a start up.
			fe.o.svcStatus = func(_ context.Context, name string) (bool, error) {
				for i := len(fe.calls) - 1; i >= 0; i-- {
					switch fe.calls[i] {
					case "stop " + name:
						return false, nil
					case "start " + name:
						return true, nil
					}
				}

				return tt.running[name], nil
			}

			var stdout, stderr bytes.Buffer

			code := fe.o.run(context.Background(), &stdout, &stderr, &Options{Action: tt.action})

			if code != tt.wantCode || stderr.String() != tt.wantStderr {
				t.Errorf("code = %d stderr = %q, want %d %q", code, stderr.String(), tt.wantCode, tt.wantStderr)
			}

			if !slices.Equal(fe.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", fe.calls, tt.wantCalls)
			}
		})
	}
}

func TestControlStatus_DiscoveryFailureShowsRegistryAndFails(t *testing.T) {
	o, _ := testOrchestrator(
		map[string]*services.ServiceDef{
			"configd": {Name: "configd", DisplayName: "configd daemon", ProcessName: "/usr/bin/fake-configd"},
		},
		map[string]string{},
	)

	c, out := newConsole()

	if rc := o.controlStatus(context.Background(), c); rc != 1 {
		t.Errorf("rc = %d, want 1 when discovery fails", rc)
	}

	if !strings.Contains(out.String(), "configd daemon") {
		t.Errorf("stdout = %q, want full-registry fallback row", out)
	}
}
