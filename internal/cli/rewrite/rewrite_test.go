// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rewrite

import (
	"bytes"
	"context"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/security"
)

// TestListenerAddress pins the legacy listener defaults: port 7171 unless
// localconfig sets zmconfigd_listen_port, and the legacy configrewrite timeout.
func TestListenerAddress(t *testing.T) {
	tests := []struct {
		name string
		lc   map[string]string
		want Endpoint
	}{
		{"defaults", map[string]string{}, Endpoint{Port: 7171}},
		{"unparsable port", map[string]string{"zmconfigd_listen_port": "x"}, Endpoint{Port: 7171}},
		{
			"configured",
			map[string]string{
				"zmconfigd_listen_port":        "7172",
				"zimbraIPMode":                 "ipv6",
				"zimbra_configrewrite_timeout": "30",
			},
			Endpoint{Port: 7172, IPMode: "ipv6", Timeout: 30 * time.Second},
		},
		{"bad timeout", map[string]string{"zimbra_configrewrite_timeout": "-1"}, Endpoint{Port: 7171}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ListenerAddress(tt.lc); got != tt.want {
				t.Errorf("ListenerAddress = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestViaDaemon covers the REWRITE contract of "configd rewrite": a daemon
// that processed the request means nothing runs in-process; a daemon that
// answers ERROR fails the command without a competing standalone rewrite; only
// when nothing listens does a standalone rewrite run and set the exit code.
func TestViaDaemon(t *testing.T) {
	tests := []struct {
		name           string
		network        string
		reply          string // "" = no listener
		wantStandalone bool
		wantCode       int
	}{
		{name: "daemon accepts", network: networkTCP4, reply: "SUCCESS REWRITES COMPLETE", wantCode: 0},
		{name: "daemon on the other family", network: networkTCP6, reply: "SUCCESS REWRITES COMPLETE", wantCode: 0},
		{name: "daemon not running", wantStandalone: true, wantCode: 3},
		{name: "daemon answers error", network: networkTCP4, reply: "ERROR REWRITE TIMEOUT", wantCode: 1},
		{name: "daemon on the other family answers error", network: networkTCP6, reply: "ERROR REWRITE TIMEOUT", wantCode: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := closedPort(t)
			if tt.reply != "" {
				port, _ = listenLoopback(t, tt.network, tt.reply, 0)
			}

			var gotConfigs []string

			ran := false
			standalone := func(_ context.Context, configs []string) int {
				ran = true
				gotConfigs = configs

				return 3
			}

			code := viaDaemon(context.Background(), []string{"proxy", "mta"}, Endpoint{Port: port}, standalone)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if ran != tt.wantStandalone {
				t.Fatalf("standalone ran = %v, want %v", ran, tt.wantStandalone)
			}

			if ran && strings.Join(gotConfigs, " ") != "proxy mta" {
				t.Errorf("standalone configs = %v, want [proxy mta]", gotConfigs)
			}
		})
	}
}

// TestRun_NotZextras pins the permission contract: any user but zextras gets
// the usage line on stderr, status 1, and nothing on stdout.
func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{
		Configs: []string{"mta"},
		Standalone: func(context.Context, []string) int {
			t.Error("standalone must not run")

			return 0
		},
	})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if want := "Please run this program as the 'zextras' user.\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}
