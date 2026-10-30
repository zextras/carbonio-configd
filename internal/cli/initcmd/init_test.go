// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package initcmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
)

func loadFrom(lc map[string]string, err error) func() (map[string]string, error) {
	return func() (map[string]string, error) { return lc, err }
}

func TestInitComponents(t *testing.T) {
	for _, name := range []string{componentMTA, componentProxy} {
		if desc := initComponents[name]; desc == "" {
			t.Errorf("component %q missing or without description", name)
		}
	}
}

func TestConfigsExist(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	if configsExist(componentMTA) || configsExist(componentProxy) || configsExist("nope") {
		t.Fatal("nothing should exist in an empty base dir")
	}

	for _, rel := range []string{"common/conf/postfix/main.cf", "conf/nginx/nginx.conf"} {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if !configsExist(componentMTA) || !configsExist(componentProxy) || configsExist("nope") {
		t.Error("existing configs not detected")
	}
}

func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{Component: "mta"})

	if want := "Please run this program as the 'zextras' user.\n"; code != 1 || stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
}

func TestRun_Contracts(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	// A port nobody listens on: grab one and release it.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()

	tests := []struct {
		name       string
		opts       Options
		load       func() (map[string]string, error)
		wantStdout string
		wantStderr string
	}{
		{
			name: "unknown component", opts: Options{Component: "x"},
			wantStderr: "Unknown component: x\n",
		},
		{
			name: "load error", opts: Options{Component: "mta"},
			load:       loadFrom(nil, errors.New("bad xml")),
			wantStdout: "Initializing MTA (postfix, saslauthd, LDAP transport maps)...\n",
			wantStderr: "Error loading localconfig: bad xml\n",
		},
		{
			name: "daemon not running", opts: Options{Component: "proxy"},
			load:       loadFrom(map[string]string{"zmconfigd_listen_port": port}, nil),
			wantStdout: "Initializing Proxy (nginx reverse proxy configs)...\n",
			wantStderr: "Error: configd daemon is not running. Start it first:\n" +
				"  systemctl start carbonio-configd.service\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(context.Background(), &stdout, &stderr, &tt.opts, tt.load)

			if code != 1 || stdout.String() != tt.wantStdout || stderr.String() != tt.wantStderr {
				t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRun_ConfigsExistGuard(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	p := filepath.Join(base, "conf", "nginx", "nginx.conf")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	code := run(context.Background(), &stdout, &stderr, &Options{Component: "proxy"}, nil)

	want := "Configs for proxy already exist. Use --force to reinitialize.\n"
	if code != 1 || stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
