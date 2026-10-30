// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"bytes"
	"context"
	"os/user"
	"slices"
	"testing"

	"github.com/zextras/carbonio-configd/internal/security"
)

func TestRewriteConfigs_Contents(t *testing.T) {
	expected := []string{"sasl", "webxml", "mailbox", "service", "zextras", "zextrasAdmin", "zimlet"}
	if !slices.Equal(rewriteConfigs, expected) {
		t.Errorf("rewriteConfigs = %v, want %v", rewriteConfigs, expected)
	}
}

func failStandalone(t *testing.T) func(context.Context, []string) int {
	t.Helper()

	return func(context.Context, []string) int {
		t.Error("standalone must not run")

		return 0
	}
}

func TestRun_NotZextras(t *testing.T) {
	if current, err := user.Current(); err != nil || current.Username == security.RequiredUser {
		t.Skip("runs as the zextras user")
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{Mode: "https", Standalone: failStandalone(t)})

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

func TestRun_Errors(t *testing.T) {
	tests := []struct {
		name       string
		opts       Options
		lc         map[string]string
		wantStderr string
	}{
		{
			name:       "no hostname",
			opts:       Options{},
			lc:         map[string]string{},
			wantStderr: "Error: could not determine hostname (zimbra_server_hostname is empty, pass --host)\n",
		},
		{
			name:       "invalid mode",
			opts:       Options{Mode: "bogus", Host: "h"},
			lc:         map[string]string{},
			wantStderr: "Error: invalid mail mode \"bogus\" (valid: both|http|https|mixed|redirect)\n",
		},
		{
			name:       "ldap not configured",
			opts:       Options{Mode: "https"},
			lc:         map[string]string{"zimbra_server_hostname": "h"},
			wantStderr: "Error: LDAP not configured (ldap_master_url and ldap_url are both empty)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			opts := tt.opts
			opts.Standalone = failStandalone(t)

			code := run(context.Background(), &stdout, &stderr, &opts, tt.lc)

			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}

			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
