// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tls implements Carbonio mail-mode (TLS) management.
//
// This is the Go port of the legacy zmtlsctl shell script. It:
//   - Reads proxy servers and their TLS/redirect constraints from LDAP.
//   - Validates a requested zimbraMailMode value against proxy constraints.
//   - Updates zimbraMailMode on the local server entry.
//
// The rewrite of webxml/mailbox/service/sasl/zextras/zextrasAdmin/zimlet
// configs is orchestrated by the caller (cmd/configd) using the shared
// handleForcedConfigs path, so it is not duplicated here.
package tls

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-ldap/ldap/v3"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
)

// Mode represents a valid zimbraMailMode value.
type Mode string

// zimbraBaseDN is the base DN for Zimbra LDAP entries.
const zimbraBaseDN = "cn=zimbra"

// Valid zimbraMailMode values.
const (
	ModeBoth     Mode = "both"
	ModeHTTP     Mode = "http"
	ModeHTTPS    Mode = "https"
	ModeMixed    Mode = "mixed"
	ModeRedirect Mode = "redirect"
)

// AllModes lists every accepted Mode value in canonical order.
var AllModes = []Mode{ModeBoth, ModeHTTP, ModeHTTPS, ModeMixed, ModeRedirect}

// RestrictedModes are the subset offered when the host participates in a
// proxy backend pool (legacy script logic: proxied hosts may only select
// both/http/https — mixed/redirect are admin-only overrides).
var RestrictedModes = []Mode{ModeBoth, ModeHTTP, ModeHTTPS}

// ParseMode validates and converts a string to a Mode.
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(AllModes, m) {
		return m, nil
	}

	return "", fmt.Errorf("invalid mail mode %q (valid: %s)", s, modeList(AllModes))
}

// modeList joins a slice of Modes with "|" for error messages.
func modeList(modes []Mode) string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		out = append(out, string(m))
	}

	return strings.Join(out, "|")
}

// ProxySettings captures the two LDAP attributes that constrain mail mode.
type ProxySettings struct {
	Proxy              string // proxy hostname (cn)
	MailMode           string // zimbraReverseProxyMailMode
	SSLToUpstreamTrue  bool   // zimbraReverseProxySSLToUpstreamEnabled == TRUE
	SSLToUpstreamValid bool   // whether the attribute was present on the entry
}

// ServerBackendDN returns the DN for a server entry.
func ServerBackendDN(hostname, baseDN string) string {
	if baseDN == "" {
		baseDN = zimbraBaseDN
	}

	return fmt.Sprintf("cn=%s,cn=servers,%s", ldap.EscapeDN(hostname), baseDN)
}

// IsReverseProxyBackend reports whether the local server is configured as a
// reverse-proxy lookup target (zimbraReverseProxyLookupTarget=TRUE). The legacy
// `zmprov garpb` enumerates servers with this attribute; we check the single
// local entry since that is all the validation flow needs.
func IsReverseProxyBackend(c *carboldap.Client, hostname string) (bool, error) {
	dn := ServerBackendDN(hostname, zimbraBaseDN)

	entry, err := c.GetEntry(dn, []string{"zimbraReverseProxyLookupTarget"})
	if err != nil {
		return false, fmt.Errorf("lookup backend flag: %w", err)
	}

	return strings.EqualFold(entry.GetAttributeValue("zimbraReverseProxyLookupTarget"), "TRUE"), nil
}

// EnumerateProxies returns the cn values of every server that has the proxy
// service enabled (zimbraServiceEnabled=proxy).
func EnumerateProxies(c *carboldap.Client) ([]string, error) {
	filter := "(&(objectClass=zimbraServer)(zimbraServiceEnabled=proxy))"

	res, err := c.Search("cn=servers,cn=zimbra", filter, []string{"cn"}, ldap.ScopeSingleLevel)
	if err != nil {
		return nil, fmt.Errorf("enumerate proxies: %w", err)
	}

	proxies := make([]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		if cn := e.GetAttributeValue("cn"); cn != "" {
			proxies = append(proxies, cn)
		}
	}

	return proxies, nil
}

// GetProxiesForHost returns the proxies specifically designated for `host`
// (via zimbraReverseProxyAvailableLookupTargets). If none are designated,
// it falls back to every proxy in the cluster — mirroring the legacy script.
func GetProxiesForHost(c *carboldap.Client, host string) ([]string, error) {
	filter := fmt.Sprintf(
		"(&(objectClass=zimbraServer)(zimbraReverseProxyAvailableLookupTargets=*%s*))",
		ldap.EscapeFilter(host),
	)

	res, err := c.Search("cn=servers,cn=zimbra", filter, []string{"cn"}, ldap.ScopeSingleLevel)
	if err != nil {
		return nil, fmt.Errorf("lookup designated proxies: %w", err)
	}

	designated := make([]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		if cn := e.GetAttributeValue("cn"); cn != "" {
			designated = append(designated, cn)
		}
	}

	if len(designated) > 0 {
		return designated, nil
	}

	return EnumerateProxies(c)
}

// ReadProxySettings loads zimbraReverseProxyMailMode and
// zimbraReverseProxySSLToUpstreamEnabled from a proxy server entry.
func ReadProxySettings(c *carboldap.Client, proxy string) (*ProxySettings, error) {
	dn := ServerBackendDN(proxy, zimbraBaseDN)

	entry, err := c.GetEntry(dn, []string{
		"zimbraReverseProxyMailMode",
		"zimbraReverseProxySSLToUpstreamEnabled",
	})
	if err != nil {
		return nil, fmt.Errorf("read proxy settings for %s: %w", proxy, err)
	}

	s := &ProxySettings{
		Proxy:    proxy,
		MailMode: strings.ToLower(entry.GetAttributeValue("zimbraReverseProxyMailMode")),
	}

	sslup := entry.GetAttributeValue("zimbraReverseProxySSLToUpstreamEnabled")
	if sslup != "" {
		s.SSLToUpstreamValid = true
		s.SSLToUpstreamTrue = strings.EqualFold(sslup, "TRUE")
	}

	return s, nil
}

// ValidateMode enforces the cross-constraints the legacy script applied
// between the proxy's SSL-to-upstream / MailMode and the requested mode.
func ValidateMode(requested Mode, s *ProxySettings) error {
	if s == nil {
		return fmt.Errorf("proxy settings are nil")
	}

	if !s.SSLToUpstreamValid {
		return fmt.Errorf("unable to determine zimbraReverseProxySSLToUpstreamEnabled on proxy %q", s.Proxy)
	}

	if s.MailMode == "" {
		return fmt.Errorf("unable to determine zimbraReverseProxyMailMode on proxy %q", s.Proxy)
	}

	// Proxy cross-constraint #1: when SSLToUpstream=TRUE, the proxy's
	// MailMode must already be https or redirect.
	if s.SSLToUpstreamTrue && s.MailMode != string(ModeHTTPS) && s.MailMode != string(ModeRedirect) {
		return fmt.Errorf(
			"on proxy %s: zimbraReverseProxySSLToUpstreamEnabled=TRUE requires "+
				"zimbraReverseProxyMailMode in {https, redirect} (found %q)",
			s.Proxy, s.MailMode,
		)
	}

	// Proxy cross-constraint #2: when SSLToUpstream=FALSE, MailMode must be both|http.
	if !s.SSLToUpstreamTrue && s.MailMode != string(ModeBoth) && s.MailMode != string(ModeHTTP) {
		return fmt.Errorf(
			"on proxy %s: zimbraReverseProxySSLToUpstreamEnabled=FALSE requires "+
				"zimbraReverseProxyMailMode in {both, http} (found %q)",
			s.Proxy, s.MailMode,
		)
	}

	// Requested-mode constraints vs. proxy's MailMode.
	switch s.MailMode {
	case string(ModeHTTPS), string(ModeRedirect):
		if requested != ModeBoth && requested != ModeHTTPS {
			return fmt.Errorf(
				"on proxy %s: zimbraReverseProxyMailMode=%s requires requested mode in {both, https} (got %q)",
				s.Proxy, s.MailMode, requested,
			)
		}
	}

	return nil
}

// SetMailMode writes zimbraMailMode on the server entry for hostname.
func SetMailMode(c *carboldap.Client, hostname string, mode Mode) error {
	dn := ServerBackendDN(hostname, zimbraBaseDN)

	if err := c.ModifyAttribute(dn, "zimbraMailMode", string(mode)); err != nil {
		return fmt.Errorf("set zimbraMailMode=%s on %s: %w", mode, hostname, err)
	}

	return nil
}
