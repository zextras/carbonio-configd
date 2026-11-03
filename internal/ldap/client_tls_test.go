// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
)

// tlsTestClient builds a client against url with the given TLS settings and
// short timeouts, without retries so failures surface immediately.
func tlsTestClient(t *testing.T, url string, startTLS bool, cfg *tls.Config) *Client {
	t.Helper()

	c, err := NewClient(&ClientConfig{
		URLs:        []string{url},
		BindDN:      "uid=zimbra,cn=admins,cn=zimbra",
		Password:    "secret",
		StartTLS:    startTLS,
		TLSConfig:   cfg,
		MaxRetries:  1,
		RetryDelay:  time.Millisecond,
		DialTimeout: 2 * time.Second,
		BindTimeout: 2 * time.Second,
		OpTimeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// searchRoot runs a trivial base search so a test can prove the connection is
// usable end to end.
func searchRoot(c *Client) error {
	_, err := c.Search("cn=zimbra", ldapFilterAllObjects, []string{"cn"}, ldap.ScopeBaseObject)

	return err
}

func poolOf(t *testing.T, caPEM []byte) *x509.CertPool {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("CA PEM not parsed")
	}

	return pool
}

func TestClient_StartTLS_VerifiesAgainstRootCAs(t *testing.T) {
	t.Parallel()

	pk := pki.NewServer(t, "localhost", "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12}})

	// Both an IP and a DNS name URL must verify: ServerName comes from the URL host.
	for _, host := range []string{"127.0.0.1", "localhost"} {
		c := tlsTestClient(t, srv.URL("ldap", host), true,
			&tls.Config{RootCAs: poolOf(t, pk.CAPEM), MinVersion: tls.VersionTLS12})

		if err := searchRoot(c); err != nil {
			t.Fatalf("host %s: search over StartTLS: %v", host, err)
		}
	}

	if got := srv.BindsInTLS(); got != srv.Binds() || got == 0 {
		t.Errorf("binds in TLS = %d of %d, want all (credentials must never cross in clear text)", got, srv.Binds())
	}
}

func TestClient_StartTLS_RejectsUntrustedAndMismatchedCert(t *testing.T) {
	t.Parallel()

	good := pki.NewServer(t, "localhost", "127.0.0.1")
	other := pki.NewServer(t, "localhost", "127.0.0.1")
	wrongName := pki.NewServer(t, "ldap.other.example")

	tests := []struct {
		name   string
		server pki.Server
		trust  []byte
	}{
		{"unknown CA", other, good.CAPEM},
		{"certificate for another host", wrongName, wrongName.CAPEM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := ldapfake.New(t, ldapfake.Options{
				TLS: &tls.Config{Certificates: []tls.Certificate{tt.server.Certificate}, MinVersion: tls.VersionTLS12},
			})

			c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), true,
				&tls.Config{RootCAs: poolOf(t, tt.trust), MinVersion: tls.VersionTLS12})

			if err := searchRoot(c); err == nil {
				t.Fatal("search succeeded against a server whose certificate must be rejected")
			}

			if srv.Binds() != 0 {
				t.Errorf("bind reached the server %d time(s) after a failed handshake", srv.Binds())
			}
		})
	}
}

func TestClient_StartTLS_EmptyServerNameIsFilledFromURL(t *testing.T) {
	t.Parallel()

	// The CLI used to pass no TLS config at all; crypto/tls then refuses the
	// handshake ("either ServerName or InsecureSkipVerify must be specified").
	// With a nil base config the client must still handshake, and with a
	// trusting base config it needs no ServerName from the caller.
	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12}})

	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), true,
		&tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // exercising ServerName defaulting

	if err := searchRoot(c); err != nil {
		t.Fatalf("search: %v", err)
	}

	got := c.tlsConfigFor("ldap://ldap.example.com:389")
	if got.ServerName != "ldap.example.com" {
		t.Errorf("ServerName = %q, want ldap.example.com", got.ServerName)
	}

	if (&Client{}).tlsConfigFor("ldaps://[::1]:636").ServerName != "::1" {
		t.Error("IPv6 host must be unbracketed")
	}
}

func TestClient_LDAPS_UsesTLSFromFirstByte(t *testing.T) {
	t.Parallel()

	pk := pki.NewServer(t, "localhost", "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		ImplicitTLS: true,
		TLS:         &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	// StartTLS must not be attempted on an ldaps:// URL even when enabled.
	c := tlsTestClient(t, srv.URL("ldaps", "localhost"), true,
		&tls.Config{RootCAs: poolOf(t, pk.CAPEM), MinVersion: tls.VersionTLS12})

	if err := searchRoot(c); err != nil {
		t.Fatalf("search over ldaps: %v", err)
	}

	if srv.StartTLSOps() != 0 {
		t.Error("StartTLS was issued on an ldaps:// connection")
	}

	bad := tlsTestClient(t, srv.URL("ldaps", "localhost"), false,
		&tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12})
	if err := searchRoot(bad); err == nil {
		t.Fatal("ldaps search succeeded without trusting the CA")
	}
}

func TestClient_StartTLSDisabled_StaysPlain(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{})
	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), false, nil)

	if err := searchRoot(c); err != nil {
		t.Fatalf("plain search: %v", err)
	}

	if srv.StartTLSOps() != 0 {
		t.Error("StartTLS issued although disabled")
	}
}

func TestClient_StartTLSRefused_FailsClosed(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{NoStartTLS: true})
	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), true, nil)

	if err := searchRoot(c); err == nil {
		t.Fatal("search succeeded although the server refused StartTLS")
	}

	if srv.Binds() != 0 {
		t.Error("client bound in clear text after StartTLS was refused (silent downgrade)")
	}
}

func TestClient_HungServer_BoundedByBindTimeout(t *testing.T) {
	t.Parallel()

	for _, hang := range []string{"starttls", "bind"} {
		t.Run(hang, func(t *testing.T) {
			t.Parallel()

			pk := pki.NewServer(t, "127.0.0.1")
			srv := ldapfake.New(t, ldapfake.Options{
				HangOn: hang,
				TLS:    &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
			})

			c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), hang == "starttls", nil)
			c.bindTimeout = 200 * time.Millisecond

			start := time.Now()
			_, err := c.connect()
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("connect to a hung server succeeded")
			}

			if !errors.Is(err, errConnectAborted) {
				t.Errorf("error %v does not wrap errConnectAborted", err)
			}

			if elapsed > 3*time.Second {
				t.Errorf("connect took %s, want ~200ms", elapsed)
			}
		})
	}
}

func TestClient_Close_AbortsInFlightConnect(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{HangOn: "bind"})
	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), false, nil)
	c.bindTimeout = time.Minute

	done := make(chan error, 1)

	go func() {
		_, err := c.connect()
		done <- err
	}()

	// While the dial+bind hangs the pool lock must be free: other callers
	// (and Close) may not queue behind a stalled network round-trip.
	deadline := time.After(2 * time.Second)

	for srv.Binds() == 0 {
		select {
		case <-deadline:
			t.Fatal("connect never reached the server")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if !c.poolMu.TryLock() {
		t.Fatal("pool mutex held across network I/O")
	}

	c.poolMu.Unlock()

	start := time.Now()
	_ = c.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("connect succeeded after Close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not abort the in-flight connect")
	}

	if time.Since(start) > 2*time.Second {
		t.Error("abort took too long")
	}
}

func TestClient_ReturnConnectionAfterClose_ClosesIt(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{})
	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), false, nil)

	conn, err := c.getConnection()
	if err != nil {
		t.Fatalf("getConnection: %v", err)
	}

	_ = c.Close()
	c.returnConnection(conn)

	if len(c.pool) != 0 {
		t.Error("connection returned to the pool of a closed client")
	}

	if !conn.IsClosing() {
		t.Error("connection returned to a closed client was not closed")
	}
}

func TestSleepCtx(t *testing.T) {
	t.Parallel()

	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleepCtx = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if err := sleepCtx(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepCtx cancelled = %v, want context.Canceled", err)
	}

	if time.Since(start) > time.Second {
		t.Error("sleepCtx ignored cancellation")
	}
}

func TestLdapWithRetry_ContextCancelAbortsBackoff(t *testing.T) {
	t.Parallel()

	l := NewLdap(context.Background(), nil)
	l.MaxRetries = 3
	l.RetryDelay = time.Minute
	l.MaxRetryDelay = time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := l.withRetry(ctx, "op", func() error { return errors.New("connection refused") })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("withRetry = %v, want context.DeadlineExceeded", err)
	}

	if time.Since(start) > 2*time.Second {
		t.Error("withRetry slept through the cancelled context")
	}
}

func TestResolveTLS_StartTLSRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lc   map[string]string
		want bool
	}{
		{"empty localconfig (all Java defaults)", map[string]string{}, false},
		{"supported only", map[string]string{"ldap_starttls_supported": "1"}, false},
		{"supported+required, no interprocess security", map[string]string{"ldap_starttls_supported": "1", "ldap_starttls_required": "true"}, false},
		{"all three", map[string]string{"ldap_starttls_supported": "1", "zimbra_require_interprocess_security": "1"}, true},
		{"required defaults to true", map[string]string{"ldap_starttls_supported": "1", "ldap_starttls_required": "", "zimbra_require_interprocess_security": "1"}, true},
		{"required=false", map[string]string{"ldap_starttls_supported": "1", "ldap_starttls_required": "false", "zimbra_require_interprocess_security": "1"}, false},
		{"required=TRUE case-insensitive", map[string]string{"ldap_starttls_supported": "1", "ldap_starttls_required": "TRUE", "zimbra_require_interprocess_security": "1"}, true},
		{"required=1 is not a Java boolean", map[string]string{"ldap_starttls_supported": "1", "ldap_starttls_required": "1", "zimbra_require_interprocess_security": "1"}, false},
		{"supported=true is not '1'", map[string]string{"ldap_starttls_supported": "true", "zimbra_require_interprocess_security": "1"}, false},
		{"supported=0", map[string]string{"ldap_starttls_supported": "0", "zimbra_require_interprocess_security": "1"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ResolveTLS(context.Background(), tt.lc, sys.RealFS{})
			if got.StartTLS != tt.want {
				t.Errorf("StartTLS = %v, want %v", got.StartTLS, tt.want)
			}

			if got.Config == nil || got.Config.MinVersion < tls.VersionTLS12 {
				t.Error("config missing or allows TLS < 1.2")
			}
		})
	}
}

func TestResolveTLS_UntrustedCertsFlag(t *testing.T) {
	t.Parallel()

	dflt := ResolveTLS(context.Background(), map[string]string{}, sys.RealFS{})
	if !dflt.Config.InsecureSkipVerify {
		t.Error("ssl_allow_untrusted_certs defaults to true in legacy: verification must be skipped")
	}

	strict := ResolveTLS(context.Background(), map[string]string{"ssl_allow_untrusted_certs": "false"}, sys.RealFS{})
	if strict.Config.InsecureSkipVerify || strict.Config.RootCAs == nil {
		t.Error("ssl_allow_untrusted_certs=false must verify against a root pool")
	}
}

func TestResolveTLS_TrustsCarbonioCA(t *testing.T) {
	t.Parallel()

	pk := pki.NewServer(t, "127.0.0.1")
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), pk.CAPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := ldapfake.New(t, ldapfake.Options{
		TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	lc := map[string]string{
		"ldap_starttls_supported":              "1",
		"zimbra_require_interprocess_security": "1",
		"ssl_allow_untrusted_certs":            "false",
		"zimbra_home":                          home,
	}

	setup := ResolveTLS(context.Background(), lc, sys.RealFS{})
	if !setup.StartTLS {
		t.Fatal("StartTLS not enabled by localconfig")
	}

	c := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), setup.StartTLS, setup.Config)
	if err := searchRoot(c); err != nil {
		t.Fatalf("search with the Carbonio CA from conf/ca/ca.pem: %v", err)
	}

	// Without the CA file the same self-signed server must be rejected.
	lc["zimbra_home"] = t.TempDir()

	setup = ResolveTLS(context.Background(), lc, sys.RealFS{})

	c2 := tlsTestClient(t, srv.URL("ldap", "127.0.0.1"), setup.StartTLS, setup.Config)
	if err := searchRoot(c2); err == nil {
		t.Fatal("self-signed server accepted without its CA being trusted")
	}
}

func TestResolveTLS_BadCAFileIsIgnored(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), []byte("not a pem\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ResolveTLS(context.Background(),
		map[string]string{"ssl_allow_untrusted_certs": "false", "zimbra_home": home}, sys.RealFS{})
	if got.Config.RootCAs == nil {
		t.Error("RootCAs must still hold the system pool")
	}
}

func TestURLHelpers(t *testing.T) {
	t.Parallel()

	if urlScheme("LDAPS://h:636") != "ldaps" || urlScheme("ldap://h") != "ldap" {
		t.Error("urlScheme")
	}

	if urlHostname("ldap://mail.example.com:389") != "mail.example.com" || urlHostname("::bad::") != "" {
		t.Error("urlHostname")
	}
}
