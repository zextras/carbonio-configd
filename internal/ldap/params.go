// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"errors"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Localconfig keys read to reach LDAP.
const (
	lcKeyURL       = "ldap_url"
	lcKeyMasterURL = "ldap_master_url"
	lcKeyUserDN    = "zimbra_ldap_userdn"
	lcKeyPassword  = "zimbra_ldap_password"
	lcKeyTemplates = "zmconfigd_template_dir"

	// readPoolSize is the connection pool of the cycle's data client.
	readPoolSize = 5
)

// Purpose says what an LDAP client is for. It selects which localconfig URL
// keys apply.
type Purpose int

const (
	// PurposeRead is the data client of the configuration cycle (cn=zimbra
	// queries): ldap_url, five pooled connections.
	PurposeRead Purpose = iota
	// PurposeWrite is a write-capable client: the master (ldap_master_url,
	// falling back to ldap_url).
	PurposeWrite
	// PurposeDiscovery is the service-discovery client: the master
	// (ldap_master_url) only, it never falls back to the replica.
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

// ParamsFrom extracts the connection parameters of an LDAP client for purpose
// from the effective localconfig lc. Transport security follows the legacy
// Java rule (ResolveTLS); fsys is where the Carbonio CA files are read from
// (nil: the real filesystem). The URL keys may carry space-separated
// multi-URL values (Carbonio HA convention, CO-3565); they are split into the
// failover list.
//
// The attrs schema directory follows zmconfigd_template_dir (<dir>/conf/attrs);
// unset, the client reads it under the install root.
func ParamsFrom(ctx context.Context, lc map[string]string, purpose Purpose, fsys sys.FS) (*ClientConfig, error) {
	tlsSetup := ResolveTLS(ctx, lc, fsys)

	cfg := &ClientConfig{
		BindDN:    lc[lcKeyUserDN],
		Password:  lc[lcKeyPassword],
		StartTLS:  tlsSetup.StartTLS,
		TLSConfig: tlsSetup.Config,
		FS:        fsys,
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
	case PurposeDiscovery:
		cfg.URLs = ParseURLs(lc[lcKeyMasterURL])
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
