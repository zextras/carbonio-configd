// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// slapdArgs is the slapd command line (ServiceDef.ArgsFromConfig): no
// daemonizing (the launcher detaches it), syslog facility LOCAL0, the bind URL
// of localconfig plus the ldapi socket, and the cn=config directory. The bind
// URL matches the systemd envscript logic.
func (c *Controller) slapdArgs(lc map[string]string) []string {
	return []string{
		"-d", "0",
		"-l", "LOCAL0",
		"-h", buildLDAPBindURL(lc) + " ldapi:///",
		"-F", c.p.data + "/ldap/config",
	}
}

// ldapReadyTimeout bounds how long awaitLDAPReady waits for slapd to begin
// answering LDAP queries before declaring the start failed.
const ldapReadyTimeout = 30 * time.Second

// ldapProbeInterval is the poll cadence for the readiness probe.
const ldapProbeInterval = 250 * time.Millisecond

// ldapProbeDialTimeout and ldapProbeRequestTimeout bound one readiness probe.
const (
	ldapProbeDialTimeout    = 5 * time.Second
	ldapProbeRequestTimeout = 5 * time.Second
)

// ldapStopTimeout is ServiceDef.GracefulStop for slapd: the SIGTERM wait before
// escalating to SIGKILL. slapd must flush and cleanly close its LMDB backing
// store within this window; a hard SIGKILL skips the clean shutdown and can
// leave the next start doing recovery work that exceeds the readiness wait.
const ldapStopTimeout = 10 * time.Second

// buildLDAPBindURL constructs the LDAP bind URL from localconfig.
// Matches the old shell script logic: prefer ldap_bind_url, fall back to
// the first URL in ldap_url, then reconstruct from zimbra_server_hostname/ldap_port.
func buildLDAPBindURL(lc map[string]string) string {
	// Prefer explicit bind URL (may contain multiple space-separated URLs)
	if bindURL := lc["ldap_bind_url"]; bindURL != "" {
		return bindURL
	}

	// Fall back to first URL in ldap_url (matches ldap.sh lines 57-60)
	if urls := strings.Fields(lc["ldap_url"]); len(urls) > 0 {
		return urls[0]
	}

	// Last resort: reconstruct from individual keys
	port := lc["ldap_port"]
	if port == "" {
		port = "389"
	}

	host := lc["zimbra_server_hostname"]
	if host == "" {
		host = localhostName
	}

	return fmt.Sprintf("ldap://%s:%s", host, port)
}

// awaitLDAPReady waits until the LDAP server actually answers a query (or the
// child exits, ctx is cancelled, or ldapReadyTimeout elapses). It replaces
// sd_notify readiness for slapd, which is built without libsystemd and never
// sends READY=1: waiting on a notify datagram would always burn the full
// timeout (the daemon is up but silent), which made the first `zmcontrol
// start` report the directory server as Failed while a second start — seeing
// slapd already listening — succeeded. Probing the endpoint makes readiness
// mean "actually serving" and returns in ~1s. exited delivers the child's
// exit, so a slapd that dies during startup (cannot bind its listener) fails
// the start at once.
func (c *Controller) awaitLDAPReady(ctx context.Context, lc map[string]string, exited <-chan error) error {
	const service = "ldap"

	// The bind URL may contain multiple space-separated URLs (HA); probe all.
	urls := carboldap.ParseURLs(buildLDAPBindURL(lc))

	deadline := c.env.Clock.Now().Add(ldapReadyTimeout)
	ticker := c.env.Clock.NewTicker(ldapProbeInterval)

	defer ticker.Stop()

	for {
		if err := c.ldapProbe(ctx, urls); err == nil {
			logger.InfoContext(ctx, "LDAP server is answering queries", "service", service, "urls", urls)

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exited:
			return fmt.Errorf("%s exited during startup before becoming ready: %w", service, waitErr)
		case <-ticker.C():
			if c.env.Clock.Now().After(deadline) {
				return fmt.Errorf("%s did not become ready within %s", service, ldapReadyTimeout)
			}
		}
	}
}

// probeLDAP is the default Controller.ldapProbe: probeLDAPReady with the TLS
// settings the LDAP client itself runs with (carboldap.ResolveTLS): the
// legacy ssl_allow_untrusted_certs semantics, so with ssl_allow_untrusted_certs
// =false an ldaps:// bind URL is verified against the system roots and the
// Carbonio CA files.
func (c *Controller) probeLDAP(ctx context.Context, urls []string) error {
	lc, err := c.loadConfig()
	if err != nil {
		// No localconfig, no basis for stricter settings than the default.
		lc = nil
	}

	return probeLDAPReady(ctx, urls, carboldap.ResolveTLS(ctx, lc, c.env.FS).Config)
}

// probeLDAPReady dials the first reachable LDAP URL and performs an anonymous
// rootDSE base search. Success means slapd is accepting connections AND serving
// queries — a half-open listener that hasn't finished initializing fails the
// search, so this does not false-positive. No credentials are required.
//
// ldaps:// URLs are verified as tlsConfig says. Its default (ResolveTLS with
// ssl_allow_untrusted_certs unset) skips verification, like every legacy
// client of this directory server: the local certificate is often a
// self-signed chain, and a probe that only asks "does slapd serve queries?"
// must not make a healthy slapd look dead. Dial and request are bounded so a
// wedged listener cannot stall the start sequence.
func probeLDAPReady(ctx context.Context, urls []string, tlsConfig *tls.Config) error {
	conn, _, err := carboldap.DialFirstReachable(ctx, urls,
		ldap.DialWithDialer(&net.Dialer{Timeout: ldapProbeDialTimeout}),
		ldap.DialWithTLSConfig(tlsConfig),
	)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	conn.SetTimeout(ldapProbeRequestTimeout)

	req := ldap.NewSearchRequest(
		"", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false,
		"(objectClass=*)", []string{"namingContexts"}, nil,
	)

	if _, err := conn.Search(req); err != nil {
		return fmt.Errorf("rootDSE probe search failed: %w", err)
	}

	return nil
}

// ErrLDAPReplicaMisconfigured is returned by LDAPReplicaGuard.
var ErrLDAPReplicaMisconfigured = errors.New("ldap_url and ldap_master_url cannot be the same on an ldap replica")

// LDAPReplicaGuard reports a replica whose ldap_url equals ldap_master_url: it
// would replicate from itself. Legacy ldap.sh refuses every verb but stop on
// such a host (exit 1, this message); the CLI applies the same rule.
func (c *Controller) LDAPReplicaGuard() error {
	lc, err := c.loadConfig()
	if err != nil {
		return nil //nolint:nilerr // unreadable localconfig: no basis to refuse
	}

	if lc["ldap_is_master"] == "false" && lc["ldap_url"] == lc["ldap_master_url"] {
		return ErrLDAPReplicaMisconfigured
	}

	return nil
}
