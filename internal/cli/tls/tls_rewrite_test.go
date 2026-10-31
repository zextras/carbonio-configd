// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
)

const rewritePrefix = "Rewriting config files for cyrus-sasl, webxml, mailboxd, service, zimbraUI, and zimbraAdmin..."

func TestRun_RewriteOnly(t *testing.T) {
	tests := []struct {
		name       string
		standalone int
		wantCode   int
		wantStdout string
	}{
		{"success", 0, 0, rewritePrefix + "done.\n"},
		{"standalone failure", 3, 3, rewritePrefix},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			var gotConfigs []string

			opts := &Options{
				Host: "h",
				Standalone: func(_ context.Context, configs []string) int {
					gotConfigs = configs

					return tt.standalone
				},
			}

			code := run(context.Background(), &stdout, &stderr, opts, map[string]string{})

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}

			if len(gotConfigs) != len(rewriteConfigs) {
				t.Errorf("standalone configs = %v, want %v", gotConfigs, rewriteConfigs)
			}
		})
	}
}

func TestRun_LDAPUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	lc := map[string]string{
		"zimbra_server_hostname": "h",
		"ldap_master_url":        "ldap://" + addr,
	}

	for _, force := range []bool{false, true} {
		var stdout, stderr bytes.Buffer

		opts := &Options{Mode: "https", Force: force, Standalone: failStandalone(t)}

		if code := run(context.Background(), &stdout, &stderr, opts, lc); code != 1 {
			t.Errorf("force=%v: exit code = %d, want 1", force, code)
		}

		if want := "Attempting to set ldap config zimbraMailMode https on host h...failed.\n"; stdout.String() != want {
			t.Errorf("force=%v: stdout = %q, want %q", force, stdout.String(), want)
		}

		if !strings.Contains(stderr.String(), "Error: set mail mode:") {
			t.Errorf("force=%v: stderr = %q, want set-mail-mode error", force, stderr.String())
		}

		if skipped := strings.Contains(stderr.String(), "skipping proxy validation"); skipped == force {
			t.Errorf("force=%v: proxy validation skipped=%v", force, skipped)
		}
	}
}
