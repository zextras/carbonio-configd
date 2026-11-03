// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import "errors"

// Localconfig keys read to reach LDAP.
const (
	lcKeyURL        = "ldap_url"
	lcKeyMasterURL  = "ldap_master_url"
	lcKeyUserDN     = "zimbra_ldap_userdn"
	lcKeyPassword   = "zimbra_ldap_password"
	lcKeyStartTLSOK = "ldap_starttls_supported"
	lcKeyTemplates  = "zmconfigd_template_dir"

	// readPoolSize is the connection pool of the cycle's data client.
	readPoolSize = 5
)

// Purpose says what an LDAP client is for. It selects which localconfig URL
// key and which StartTLS rule apply.
type Purpose int

const (
	// PurposeRead is the data client of the configuration cycle (cn=zimbra
	// queries): ldap_url, five pooled connections. Its StartTLS is left false
	// here; the cycle sets it from the ldap_starttls_required it loads later.
	PurposeRead Purpose = iota
	// PurposeWrite is a write-capable client: the master (ldap_master_url,
	// falling back to ldap_url), always over StartTLS.
	PurposeWrite
	// PurposeDiscovery is the service-discovery client: the master
	// (ldap_master_url), StartTLS when ldap_starttls_supported is set and not 0.
	PurposeDiscovery
)

// Errors returned by ParamsFrom for a localconfig that cannot reach LDAP.
var (
	// ErrNoURL reports that the purpose's URL key(s) are empty.
	ErrNoURL = errors.New("LDAP URL not configured")
	// ErrNoBindDN reports an empty zimbra_ldap_userdn (PurposeRead only).
	ErrNoBindDN = errors.New("LDAP user DN not configured")
	// ErrNoPassword reports an empty zimbra_ldap_password (PurposeRead only).
	ErrNoPassword = errors.New("LDAP password not configured")
)

// StartTLSSupported reports whether localconfig lc advertises StartTLS on its
// LDAP servers (ldap_starttls_supported set and not "0").
func StartTLSSupported(lc map[string]string) bool {
	v := lc[lcKeyStartTLSOK]

	return v != "0" && v != ""
}

// ParamsFrom extracts the connection parameters of an LDAP client for purpose
// from the effective localconfig lc. The URL keys may carry space-separated
// multi-URL values (Carbonio HA convention, CO-3565); they are split into the
// failover list.
//
// The attrs schema directory follows zmconfigd_template_dir (<dir>/conf/attrs);
// unset, the client reads it under the install root.
//
// The caller adds what localconfig cannot say: the filesystem, a TLS config.
func ParamsFrom(lc map[string]string, purpose Purpose) (*ClientConfig, error) {
	cfg := &ClientConfig{
		BindDN:   lc[lcKeyUserDN],
		Password: lc[lcKeyPassword],
	}

	// The attrs schema lives under the template dir when one is set.
	if dir := lc[lcKeyTemplates]; dir != "" {
		cfg.AttrsDir = dir + "/conf/attrs"
	}

	switch purpose {
	case PurposeRead:
		cfg.URLs = ParseURLs(lc[lcKeyURL])
		cfg.BaseDN = defaultBaseDN
		cfg.PoolSize = readPoolSize
	case PurposeWrite:
		cfg.URLs = ParseURLs(lc[lcKeyMasterURL])
		if len(cfg.URLs) == 0 {
			cfg.URLs = ParseURLs(lc[lcKeyURL])
		}

		cfg.StartTLS = true
	case PurposeDiscovery:
		cfg.URLs = ParseURLs(lc[lcKeyMasterURL])
		cfg.StartTLS = StartTLSSupported(lc)
	}

	if len(cfg.URLs) == 0 {
		return nil, ErrNoURL
	}

	if purpose == PurposeRead {
		if cfg.BindDN == "" {
			return nil, ErrNoBindDN
		}

		if cfg.Password == "" {
			return nil, ErrNoPassword
		}
	}

	return cfg, nil
}
