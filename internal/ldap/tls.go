// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/fs"
	"net/url"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Localconfig keys that drive the LDAP transport security of the legacy Java
// client (carbonio-mailbox store LdapServerConfig.ZimbraLdapConfig and
// LdapSSLUtil). Their absent-key defaults are the LC.java KnownKey defaults.
const (
	lcStartTLSSupported           = "ldap_starttls_supported"
	lcStartTLSRequired            = "ldap_starttls_required"
	lcRequireInterprocessSecurity = "zimbra_require_interprocess_security"
	lcAllowUntrustedCerts         = "ssl_allow_untrusted_certs"
	lcZimbraHome                  = "zimbra_home"

	defaultZimbraHome = "/opt/zextras"
)

// caFiles are the CA bundles, relative to zimbra_home, that zmcertmgr adds to
// the Java trust store (mailboxd_truststore) and so are trusted by the legacy
// LDAP client: the deployed Carbonio CA and the commercial CA chain.
var caFiles = []string{
	"conf/ca/ca.pem",
	"ssl/carbonio/commercial/commercial_ca.crt",
}

// TLSSetup is the transport security a client must apply, derived from
// localconfig with the legacy semantics.
type TLSSetup struct {
	// StartTLS upgrades ldap:// connections. It never applies to ldaps://
	// (always TLS) or ldapi:// (local socket).
	StartTLS bool
	// Config is the base TLS configuration for StartTLS and ldaps://. The
	// client fills ServerName per URL.
	Config *tls.Config
}

// ResolveTLS derives the LDAP transport security from a localconfig map the
// way the legacy Java client does:
//
//   - StartTLS is wanted only when ldap_starttls_supported is "1" AND
//     ldap_starttls_required is true (default true) AND
//     zimbra_require_interprocess_security is "1" (LdapServerConfig.java:152-157).
//     When wanted, a StartTLS failure fails the connection: there is no silent
//     downgrade to clear text.
//   - ssl_allow_untrusted_certs (default true) disables certificate
//     verification, exactly like the Java dummy trust manager. When false the
//     server certificate is verified against the system roots plus the
//     Carbonio CA files under zimbra_home that zmcertmgr trusts.
//
// Booleans follow Java Boolean.valueOf: only "true" (case-insensitive) is true.
func ResolveTLS(ctx context.Context, lc map[string]string, fsys sys.FS) TLSSetup {
	startTLS := strings.TrimSpace(lc[lcStartTLSSupported]) == "1" &&
		lcBool(lc, lcStartTLSRequired, true) &&
		strings.TrimSpace(lc[lcRequireInterprocessSecurity]) == "1"

	var cfg *tls.Config

	if lcBool(lc, lcAllowUntrustedCerts, true) {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		// Legacy parity (LdapSSLUtil dummy trust manager). Operators opt in to
		// verification with ssl_allow_untrusted_certs=false.
		cfg.InsecureSkipVerify = true //nolint:gosec // G402: legacy ssl_allow_untrusted_certs semantics
	} else {
		cfg = VerifiedTLSConfig(ctx, lc, fsys)
	}

	return TLSSetup{StartTLS: startTLS, Config: cfg}
}

// VerifiedTLSConfig returns a TLS 1.2+ client config that always verifies the
// server certificate against the system roots plus the Carbonio CA files under
// zimbra_home, regardless of ssl_allow_untrusted_certs. Use it for calls that
// carry credentials over HTTPS.
func VerifiedTLSConfig(ctx context.Context, lc map[string]string, fsys sys.FS) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: caPool(ctx, lc, fsys)}
}

// lcBool reads a Java-style boolean localconfig value, using def when the key
// is absent or empty.
func lcBool(lc map[string]string, key string, def bool) bool {
	v := strings.TrimSpace(lc[key])
	if v == "" {
		return def
	}

	return strings.EqualFold(v, "true")
}

// caPool returns the system roots plus the Carbonio CA files that exist.
func caPool(ctx context.Context, lc map[string]string, fsys sys.FS) *x509.CertPool {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	home := strings.TrimSpace(lc[lcZimbraHome])
	if home == "" {
		home = defaultZimbraHome
	}

	if fsys == nil {
		fsys = sys.RealFS{}
	}

	for _, rel := range caFiles {
		path := home + "/" + rel

		pem, err := fsys.ReadFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				logger.WarnContext(ctx, "Cannot read LDAP CA file", "path", path, "error", err)
			}

			continue
		}

		if !pool.AppendCertsFromPEM(pem) {
			logger.WarnContext(ctx, "LDAP CA file holds no certificate", "path", path)
		}
	}

	return pool
}

// tlsConfigFor returns the TLS configuration for one URL: a clone of the base
// configuration with ServerName set from the URL host (crypto/tls refuses an
// empty ServerName on StartTLS, and ldaps:// needs it for hostname checks).
func (c *Client) tlsConfigFor(rawURL string) *tls.Config {
	var cfg *tls.Config
	if c.tlsConfig != nil {
		cfg = c.tlsConfig.Clone()
	} else {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	if cfg.ServerName == "" {
		cfg.ServerName = urlHostname(rawURL)
	}

	return cfg
}

// urlHostname returns the host of an LDAP URL without port or brackets, or ""
// when the URL does not parse.
func urlHostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

// urlScheme returns the lower-cased scheme of an LDAP URL ("ldap", "ldaps",
// "ldapi") or "" when it does not parse.
func urlScheme(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	return strings.ToLower(u.Scheme)
}
