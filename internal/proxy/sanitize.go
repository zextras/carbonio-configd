// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// maxHostnameLen is the longest host token accepted from LDAP (RFC 1035 limit).
const maxHostnameLen = 253

// validHostToken reports whether s is safe to splice into an nginx directive
// as a host name or IP literal (server_name, upstream "server host:port;",
// memcache "servers host:port;"). Only characters that can appear in DNS
// names, IPv4 and IPv6 literals are accepted, so a hostile LDAP value cannot
// terminate the directive (";"), open or close a block ("{", "}"), start a new
// line, or add extra parameters (whitespace).
func validHostToken(s string) bool {
	if s == "" || len(s) > maxHostnameLen {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}

	return true
}

// validIPLiteral reports whether s parses as an IPv4 or IPv6 address.
func validIPLiteral(s string) bool {
	return net.ParseIP(strings.Trim(s, "[]")) != nil
}

// validIPOrCIDR reports whether s is an IPv4/IPv6 address or a CIDR range, the
// forms nginx accepts in address lists such as mail_whitelist_ip.
func validIPOrCIDR(s string) bool {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}

	return net.ParseIP(s) != nil
}

// validNginxArg reports whether s can be spliced into a directive as one bare
// argument: no whitespace or control characters, and none of the characters
// that end the directive, open or close a block, or open or escape a quote.
func validNginxArg(s string) bool {
	if s == "" {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == 0x7f || strings.IndexByte(";{}\"'\\", c) >= 0 {
			return false
		}
	}

	return true
}

// validQuotedMessage reports whether s can sit inside a double-quoted nginx
// string: no quote, backslash or control character (a newline would otherwise
// end the directive).
func validQuotedMessage(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < ' ' || c == 0x7f || c == '"' || c == '\\' {
			return false
		}
	}

	return true
}

// validSizeToken reports whether s is an nginx size such as "10m" or "32k":
// digits with an optional k/m/g unit.
func validSizeToken(s string) bool {
	digits := strings.TrimRight(s, "kKmMgG")
	if digits == "" || len(s)-len(digits) > 1 {
		return false
	}

	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}

	return true
}

// validPort reports whether s is a TCP port number.
func validPort(s string) bool {
	n, err := strconv.Atoi(s)

	return err == nil && n > 0 && n <= 65535 && s == strconv.Itoa(n)
}

// validDomainName reports whether s is safe to use as a file name component of
// <conf>/domaincerts/<name>.crt|.key. It must be a plain host token without
// path separators, colons, brackets or ".." sequences, so an LDAP-provided
// domain name can never escape the domaincerts directory.
func validDomainName(s string) bool {
	return validHostToken(s) && !strings.ContainsAny(s, ":[]") && !strings.Contains(s, "..")
}

// validateDomainInfo returns an error describing the first LDAP-provided field
// of d that is unsafe to render into nginx configuration or to use in a file
// path. The SSL certificate and key hold PEM content, which is written to a
// file named after the domain and never spliced into nginx directives, so they
// are not validated here. Empty optional fields are valid.
func validateDomainInfo(d *DomainInfo) error {
	if !validDomainName(d.Name) {
		return fmt.Errorf("invalid domain name %q", truncateForLog(d.Name))
	}

	if d.VirtualHostname != "" && !validHostToken(d.VirtualHostname) {
		return fmt.Errorf("invalid virtual hostname %q", truncateForLog(d.VirtualHostname))
	}

	if d.VirtualIPAddress != "" && !validIPLiteral(d.VirtualIPAddress) {
		return fmt.Errorf("invalid virtual IP address %q", truncateForLog(d.VirtualIPAddress))
	}

	if !validClientCertMode(d.ClientCertMode) {
		return fmt.Errorf("invalid client cert mode %q", truncateForLog(d.ClientCertMode))
	}

	return nil
}

// validClientCertMode reports whether mode is a value nginx accepts for
// ssl_verify_client (or empty, meaning unset). It is spliced into the nginx
// configuration, so anything else is rejected.
func validClientCertMode(mode string) bool {
	switch mode {
	case "", "off", "on", "optional", "optional_no_ca":
		return true
	default:
		return false
	}
}

// truncateForLog bounds attacker-controlled values echoed into logs.
func truncateForLog(s string) string {
	const maxLogLen = 64

	if len(s) > maxLogLen {
		return s[:maxLogLen] + "..."
	}

	return s
}
