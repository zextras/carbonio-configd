// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rewrite

import (
	"bytes"
	"context"
	"net"
	"os/user"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
)

// TestListenerAddress pins the legacy listener defaults: port 7171 over IPv4
// unless localconfig sets zmconfigd_listen_port / zimbraIPMode.
func TestListenerAddress(t *testing.T) {
	tests := []struct {
		name     string
		lc       map[string]string
		wantPort int
		wantMode string
	}{
		{"defaults", map[string]string{}, 7171, IPModeIPv4},
		{"unparsable port", map[string]string{"zmconfigd_listen_port": "x"}, 7171, IPModeIPv4},
		{"configured", map[string]string{"zmconfigd_listen_port": "7172", "zimbraIPMode": "ipv6"}, 7172, "ipv6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, mode := ListenerAddress(tt.lc)
			if port != tt.wantPort || mode != tt.wantMode {
				t.Errorf("ListenerAddress = (%d, %q), want (%d, %q)", port, mode, tt.wantPort, tt.wantMode)
			}
		})
	}
}

// TestViaDaemon covers the legacy configrewrite contract: a reachable daemon
// gets the REWRITE request and nothing runs in-process; an unreachable or
// failing daemon falls back to a standalone rewrite whose exit code is
// returned, instead of failing the caller (zmtlsctl, zmproxyctl, rewrite).
func TestViaDaemon(t *testing.T) {
	tests := []struct {
		name           string
		reply          string // "" = no listener on the port
		wantStandalone bool
		wantCode       int
	}{
		{name: "daemon accepts", reply: "SUCCESS REWRITE\n", wantStandalone: false, wantCode: 0},
		{name: "daemon not running", reply: "", wantStandalone: true, wantCode: 3},
		{name: "daemon answers error", reply: "ERROR busy\n", wantStandalone: true, wantCode: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := fakeDaemon(t, tt.reply)

			var gotConfigs []string

			ran := false
			standalone := func(_ context.Context, configs []string) int {
				ran = true
				gotConfigs = configs

				return 3
			}

			code := viaDaemon(context.Background(), []string{"proxy", "mta"}, port, IPModeIPv4, standalone)

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

// fakeDaemon returns a port where one connection is answered with reply, or a
// port with nothing listening when reply is empty.
func fakeDaemon(t *testing.T, reply string) int {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	if reply == "" {
		_ = ln.Close()

		return port
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(reply))
	}()

	return port
}
