// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/security"
)

type fakeConn struct {
	modErr, getErr         error
	entry                  *ldap.Entry
	gotDN, gotAttr, gotVal string
	closed                 bool
}

func (f *fakeConn) ModifyAttribute(dn, attr, val string) error {
	f.gotDN, f.gotAttr, f.gotVal = dn, attr, val

	return f.modErr
}

func (f *fakeConn) GetEntry(string, []string) (*ldap.Entry, error) { return f.entry, f.getErr }

func (f *fakeConn) Close() error {
	f.closed = true

	return nil
}

func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	for _, action := range []Action{ActionGen, ActionEnable, ActionDisable, ActionRewrite} {
		t.Run(string(action), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Run(context.Background(), &stdout, &stderr, &Options{
				Action:   action,
				Protocol: "imap",
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
		})
	}
}

func TestRun_ConfNginxNotInstalled(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{Action: ActionConf})

	if code != 0 || stdout.String() != "Nginx not installed, exiting\n" {
		t.Errorf("code=%d stdout=%q, want 0 and the not-installed line", code, stdout.String())
	}
}

func TestRun_ConfPrintsConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	if err := os.MkdirAll(filepath.Join(base, "common", "sbin"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(base, "common", "sbin", "nginx"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	conf := filepath.Join(base, "nginx.conf")
	if err := os.WriteFile(conf, []byte("# c\n\nworker_processes 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{
		Action: ActionConf, ConfigFile: conf, NoComments: true, NoEmpty: true,
	})

	if code != 0 || stdout.String() != "worker_processes 1;\n" {
		t.Errorf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestSetProxyProtocol_UnknownProtocol(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := setProxyProtocol(&stdout, &stderr, "gopher", "TRUE")

	want := "Unknown protocol: gopher\nValid protocols: http, https, imap, imaps, pop3, pop3s\n"
	if code != 1 || stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
}

func TestModifyProtocol(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		modErr     error
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "enable", value: "TRUE", wantStdout: "Enabled IMAP proxy\n"},
		{name: "disable", value: "FALSE", wantStdout: "Disabled IMAP proxy\n"},
		{
			name: "ldap error", value: "TRUE", modErr: errors.New("boom"), wantCode: 1,
			wantStderr: "Error: failed to set zimbraReverseProxyMailImapEnabled=TRUE: boom\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			conn := &fakeConn{modErr: tt.modErr}

			code := modifyProtocol(&stdout, &stderr, conn, "cn=h,cn=servers,cn=zimbra",
				"IMAP", "zimbraReverseProxyMailImapEnabled", tt.value)

			if code != tt.wantCode || stdout.String() != tt.wantStdout || stderr.String() != tt.wantStderr {
				t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}

			if !conn.closed || conn.gotVal != tt.value || conn.gotDN != "cn=h,cn=servers,cn=zimbra" {
				t.Errorf("conn state: %+v", conn)
			}
		})
	}
}

func TestShowProxyStatus(t *testing.T) {
	entry := ldap.NewEntry("cn=h", map[string][]string{
		"zimbraReverseProxyMailImapEnabled": {"TRUE"},
		"zimbraReverseProxyHttpEnabled":     {"false"},
	})

	var stdout, stderr bytes.Buffer

	conn := &fakeConn{entry: entry}

	if code := showProxyStatus(&stdout, &stderr, conn, "cn=h"); code != 0 || stderr.Len() != 0 || !conn.closed {
		t.Fatalf("code=%d stderr=%q closed=%v", code, stderr.String(), conn.closed)
	}

	out := stdout.String()
	// Map iteration order is random: assert on lines, not their order.
	if !strings.HasPrefix(out, "Proxy Protocol Status:\n") {
		t.Errorf("stdout = %q", out)
	}

	for _, line := range []string{"  imap     enabled\n", "  http     disabled\n", "  pop3s    disabled\n"} {
		if !strings.Contains(out, line) {
			t.Errorf("stdout missing %q: %q", line, out)
		}
	}

	if n := strings.Count(out, "\n"); n != len(proxyProtocols)+1 {
		t.Errorf("got %d lines, want %d", n, len(proxyProtocols)+1)
	}
}

func TestShowProxyStatus_Error(t *testing.T) {
	var stdout, stderr bytes.Buffer

	conn := &fakeConn{getErr: errors.New("no entry")}

	code := showProxyStatus(&stdout, &stderr, conn, "cn=h")
	if code != 1 || stderr.String() != "Error: no entry\n" || stdout.Len() != 0 || !conn.closed {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRun_UnknownAction(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), &stdout, &stderr, &Options{Action: "x"}); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}
