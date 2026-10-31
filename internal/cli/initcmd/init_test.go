// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package initcmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/user"
	"strconv"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const testBase = "/zx"

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
	t.Setenv("CARBONIO_BASE_DIR", testBase)

	f := systest.New()
	env := f.System()

	if configsExist(env, componentMTA) || configsExist(env, componentProxy) || configsExist(env, "nope") {
		t.Fatal("nothing should exist in an empty base dir")
	}

	f.FS.Put(testBase+"/common/conf/postfix/main.cf", nil, 0o600)
	f.FS.Put(testBase+"/conf/nginx/nginx.conf", nil, 0o600)

	if !configsExist(env, componentMTA) || !configsExist(env, componentProxy) || configsExist(env, "nope") {
		t.Error("existing configs not detected")
	}
}

func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{Component: "mta"})

	if want := "Please run this program as the 'zextras' user.\n"; code != 1 || stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
}

func TestRun_Contracts(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", testBase)

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

			env := systest.New().System()
			code := run(context.Background(), env, services.NewController(env), &stdout, &stderr, &tt.opts, tt.load)

			if code != 1 || stdout.String() != tt.wantStdout || stderr.String() != tt.wantStderr {
				t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRun_ConfigsExistGuard(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", testBase)

	f := systest.New()
	f.FS.Put(testBase+"/conf/nginx/nginx.conf", nil, 0o600)

	var stdout, stderr bytes.Buffer

	code := run(context.Background(), f.System(), services.NewController(f.System()), &stdout, &stderr,
		&Options{Component: "proxy"}, nil)

	want := "Configs for proxy already exist. Use --force to reinitialize.\n"
	if code != 1 || stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
