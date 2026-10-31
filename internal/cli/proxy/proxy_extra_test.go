// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestPrintNginxConfFile_MissingFileWithMarkers(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	opts := &nginxConfOpts{fs: systest.New().FS, markers: true, indent: true}
	printNginxConfFile(&buf, "/missing/nginx.conf", 2, opts)

	got := buf.String()

	for _, want := range []string{"    # begin:/missing/nginx.conf\n", "    # cannot open /missing/nginx.conf", "    # end:/missing/nginx.conf\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

func TestPrintNginxConf_FollowsIncludeDirective(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/cb/nginx.conf", []byte("working_directory /cb/inc;\ninclude sub.conf;\nlast;\n"), 0o600)
	f.FS.Put("/cb/inc/sub.conf", []byte("included;\n"), 0o600)

	var buf bytes.Buffer

	printNginxConf(&buf, "/cb/nginx.conf", 0, &nginxConfOpts{fs: f.FS})

	if want := "working_directory /cb/inc;\nincluded;\nlast;\n"; buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestContactDaemon_RewritesInProcessWithProxyConfigs(t *testing.T) {
	var got []string

	opts := &Options{
		Action:       ActionGen,
		ExtraConfigs: []string{"extra"},
		Standalone: func(_ context.Context, configs []string) int {
			got = configs

			return 7
		},
	}

	var stderr bytes.Buffer

	if code := contactDaemon(&stderr, opts); code != 7 {
		t.Errorf("exit code = %d, want the standalone status 7", code)
	}

	if want := []string{"proxy", "extra"}; !slices.Equal(got, want) {
		t.Errorf("configs = %v, want %v", got, want)
	}
}

func TestServerDN(t *testing.T) {
	t.Parallel()

	if got, want := serverDN("h.example.com"), "cn=h.example.com,cn=servers,cn=zimbra"; got != want {
		t.Errorf("serverDN = %q, want %q", got, want)
	}
}

// TestRun_LDAPActionsFailWithoutLDAP: with no reachable LDAP configuration the
// status and enable paths report an error on stderr and exit 1.
func TestRun_StatusWithoutLDAP(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{Action: ActionStatus})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.HasPrefix(stderr.String(), "Error") {
		t.Errorf("stderr = %q, want an Error line", stderr.String())
	}
}
