// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"

	carbonioldap "github.com/zextras/carbonio-configd/internal/ldap"
)

const (
	// ldapSocketURL is the ldapi:// URL of the local slapd (the legacy tools
	// use ldapi://%2frun%2fcarbonio%2frun%2fldapi/).
	ldapSocketURL = "ldapi://%2Frun%2Fcarbonio%2Frun%2Fldapi/"
	// ldapConfigDN is the rootdn of the cn=config database.
	ldapConfigDN = "cn=config"

	ldapDialTimeout = 10 * time.Second
	ldapOpTimeout   = 30 * time.Second
)

// ldapTLS says how a connection to an ldap:// URL is secured.
type ldapTLS int

const (
	// ldapTLSNone opens the connection as it is.
	ldapTLSNone ldapTLS = iota
	// ldapTLSInsecure upgrades with StartTLS without verifying the server
	// (ldapinit.pm postLdapStart: verify => 'none').
	ldapTLSInsecure
	// ldapTLSVerified upgrades with StartTLS and checks the server
	// certificate against the Carbonio CA (zmldappasswd: verify => 'require').
	ldapTLSVerified
)

// ldapEntry is one search result.
type ldapEntry struct {
	DN    string
	Attrs map[string][]string
}

// ldapConn is the LDAP connection the steps use; the production one wraps
// go-ldap, tests substitute a recording fake.
type ldapConn interface {
	Bind(dn, password string) error
	// Add adds the entry dn; the attributes keep the order given.
	Add(dn string, attrs []ldapLDIFAttr) error
	// Replace replaces the values of attr on dn.
	Replace(dn, attr string, values []string) error
	// SetPassword is the LDAP password modify extended operation, as the
	// directory manager (no old password).
	SetPassword(userDN, newPassword string) error
	// Search runs a base- or subtree search and returns the entries.
	Search(base, filter string, subtree bool, attrs []string) ([]ldapEntry, error)
	Close()
}

// ldapDial describes one connection request.
type ldapDial struct {
	URLs []string
	TLS  ldapTLS
}

// ldapIsAlreadyExists reports whether err is LDAP result 68.
func ldapIsAlreadyExists(err error) bool {
	return goldap.IsErrorWithCode(err, goldap.LDAPResultEntryAlreadyExists)
}

// ldapNativeConn is ldapConn over a go-ldap connection.
type ldapNativeConn struct{ c *goldap.Conn }

// ldapNativeDial opens a go-ldap connection to the first reachable URL.
func ldapNativeDial(e *Env, d ldapDial) (ldapConn, error) {
	var tlsCfg *tls.Config

	switch d.TLS {
	case ldapTLSNone:
	case ldapTLSInsecure:
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec // legacy verify => 'none'
	case ldapTLSVerified:
		lc, err := e.LC()
		if err != nil {
			return nil, err
		}

		tlsCfg = carbonioldap.VerifiedTLSConfig(e.Ctx, maps.Clone(lc), e.Sys.FS)
	}

	opts := []goldap.DialOpt{goldap.DialWithDialer(&net.Dialer{Timeout: ldapDialTimeout})}
	if tlsCfg != nil {
		opts = append(opts, goldap.DialWithTLSConfig(tlsCfg))
	}

	conn, used, err := carbonioldap.DialFirstReachable(e.Ctx, d.URLs, opts...)
	if err != nil {
		return nil, err
	}

	conn.SetTimeout(ldapOpTimeout)

	if tlsCfg != nil && !strings.HasPrefix(used, "ldaps:") && !strings.HasPrefix(used, "ldapi:") {
		cfg := tlsCfg.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = ldapURLHost(used)
		}

		if err := conn.StartTLS(cfg); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("StartTLS to %s: %w", used, err)
		}
	}

	return &ldapNativeConn{c: conn}, nil
}

// ldapURLHost is the host name of an LDAP URL.
func ldapURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

func (n *ldapNativeConn) Bind(dn, password string) error {
	if password == "" {
		// An empty password would be an unauthenticated bind, which
		// servers accept: never let it count as a successful check.
		return errors.New("empty password")
	}

	return n.c.Bind(dn, password)
}

func (n *ldapNativeConn) Add(dn string, attrs []ldapLDIFAttr) error {
	req := goldap.NewAddRequest(dn, nil)
	for _, a := range attrs {
		req.Attribute(a.Name, a.Values)
	}

	return n.c.Add(req)
}

func (n *ldapNativeConn) Replace(dn, attr string, values []string) error {
	req := goldap.NewModifyRequest(dn, nil)
	req.Replace(attr, values)

	return n.c.Modify(req)
}

func (n *ldapNativeConn) SetPassword(userDN, newPassword string) error {
	_, err := n.c.PasswordModify(&goldap.PasswordModifyRequest{UserIdentity: userDN, NewPassword: newPassword})

	return err
}

func (n *ldapNativeConn) Search(base, filter string, subtree bool, attrs []string) ([]ldapEntry, error) {
	scope := goldap.ScopeBaseObject
	if subtree {
		scope = goldap.ScopeWholeSubtree
	}

	res, err := n.c.Search(goldap.NewSearchRequest(
		base, scope, goldap.NeverDerefAliases, 0, 0, false, filter, attrs, nil))
	if err != nil {
		return nil, err
	}

	out := make([]ldapEntry, 0, len(res.Entries))

	for _, en := range res.Entries {
		m := map[string][]string{}
		for _, a := range en.Attributes {
			m[a.Name] = a.Values
		}

		out = append(out, ldapEntry{DN: en.DN, Attrs: m})
	}

	return out, nil
}

func (n *ldapNativeConn) Close() { _ = n.c.Close() }
