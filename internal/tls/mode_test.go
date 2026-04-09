// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"both", ModeBoth, false},
		{"HTTP", ModeHTTP, false},
		{" https ", ModeHTTPS, false},
		{"Mixed", ModeMixed, false},
		{"redirect", ModeRedirect, false},
		{"", "", true},
		{"sftp", "", true},
		{"http ", ModeHTTP, false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}

			if got != tc.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateMode_Nil(t *testing.T) {
	if err := ValidateMode(ModeBoth, nil); err == nil {
		t.Fatal("expected error for nil proxy settings")
	}
}

func TestValidateMode_MissingSSLAttr(t *testing.T) {
	s := &ProxySettings{Proxy: "p1", MailMode: "https", SSLToUpstreamValid: false}
	if err := ValidateMode(ModeHTTPS, s); err == nil {
		t.Fatal("expected error when SSLToUpstreamValid is false")
	} else if !strings.Contains(err.Error(), "zimbraReverseProxySSLToUpstreamEnabled") {
		t.Fatalf("expected sslup-related error, got %v", err)
	}
}

func TestValidateMode_MissingMailMode(t *testing.T) {
	s := &ProxySettings{Proxy: "p1", MailMode: "", SSLToUpstreamValid: true, SSLToUpstreamTrue: true}
	if err := ValidateMode(ModeHTTPS, s); err == nil {
		t.Fatal("expected error when MailMode is empty")
	} else if !strings.Contains(err.Error(), "zimbraReverseProxyMailMode") {
		t.Fatalf("expected mailmode-related error, got %v", err)
	}
}

func TestValidateMode_CrossConstraints(t *testing.T) {
	cases := []struct {
		name      string
		requested Mode
		sslupTrue bool
		proxyMode string
		wantErr   bool
		errSubstr string
	}{
		// sslup=TRUE and proxy mode not in {https,redirect} -> error
		{"sslup_true_proxy_http_rejected", ModeBoth, true, "http", true, "SSLToUpstreamEnabled=TRUE"},
		{"sslup_true_proxy_both_rejected", ModeBoth, true, "both", true, "SSLToUpstreamEnabled=TRUE"},
		// sslup=TRUE, proxy=https + requested=both -> ok
		{"sslup_true_proxy_https_req_both", ModeBoth, true, "https", false, ""},
		// sslup=TRUE, proxy=https + requested=https -> ok
		{"sslup_true_proxy_https_req_https", ModeHTTPS, true, "https", false, ""},
		// sslup=TRUE, proxy=https + requested=http -> rejected by req-mode check
		{"sslup_true_proxy_https_req_http", ModeHTTP, true, "https", true, "requested mode in {both, https}"},
		// sslup=TRUE, proxy=redirect + requested=both -> ok
		{"sslup_true_proxy_redirect_req_both", ModeBoth, true, "redirect", false, ""},
		// sslup=TRUE, proxy=redirect + requested=mixed -> rejected
		{"sslup_true_proxy_redirect_req_mixed", ModeMixed, true, "redirect", true, "requested mode in {both, https}"},
		// sslup=FALSE and proxy mode not in {both,http} -> error
		{"sslup_false_proxy_https_rejected", ModeHTTPS, false, "https", true, "SSLToUpstreamEnabled=FALSE"},
		// sslup=FALSE, proxy=both -> any requested mode passes the req-mode check
		{"sslup_false_proxy_both_req_mixed", ModeMixed, false, "both", false, ""},
		{"sslup_false_proxy_http_req_https", ModeHTTPS, false, "http", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &ProxySettings{
				Proxy:              "p1",
				MailMode:           tc.proxyMode,
				SSLToUpstreamValid: true,
				SSLToUpstreamTrue:  tc.sslupTrue,
			}

			err := ValidateMode(tc.requested, s)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (case %+v)", tc)
				}

				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tc.errSubstr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestServerBackendDN(t *testing.T) {
	cases := []struct {
		host, base, want string
	}{
		{"host1.example.com", "cn=zimbra", "cn=host1.example.com,cn=servers,cn=zimbra"},
		{"host1.example.com", "", "cn=host1.example.com,cn=servers,cn=zimbra"},
		// DN-escape is applied; a plain hostname round-trips unchanged.
		{"simple", "cn=zimbra", "cn=simple,cn=servers,cn=zimbra"},
	}

	for _, tc := range cases {
		if got := ServerBackendDN(tc.host, tc.base); got != tc.want {
			t.Errorf("ServerBackendDN(%q, %q) = %q, want %q", tc.host, tc.base, got, tc.want)
		}
	}
}
