// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// Watched components, as reported to Options.Trigger.
const (
	// ComponentGlobal is the global config entry (cn=config,cn=zimbra).
	ComponentGlobal = "global"
	// ComponentServers is every server entry under cn=servers,cn=zimbra: this
	// server's own attributes and enabled services, the upstream servers the
	// proxy generates and the servers gamau, garpu and garpb enumerate.
	ComponentServers = "servers"
	// ComponentDomains is every zimbraDomain entry, read by the proxy
	// generator (virtual hostnames, per-domain certificates).
	ComponentDomains = "domains"
)

// watchedAttrs are the operational attributes that change whenever an entry
// does. entryCSN has a microsecond resolution and a replica-unique suffix;
// modifyTimestamp is the fallback for a directory that does not return it.
var watchedAttrs = []string{"entryCSN", "modifyTimestamp"}

// Searcher is the LDAP search the source needs; *ldap.Client of
// internal/ldap satisfies it.
type Searcher interface {
	Search(baseDN, filter string, attributes []string, scope int) (*goldap.SearchResult, error)
}

type query struct {
	component string
	baseDN    string
	filter    string
	scope     int
}

// LDAPSource fingerprints the entries a configd cycle reads: the global
// config, the server entries and the domains. It issues one search per
// component, asking only for operational attributes, so a poll moves a few
// hundred bytes per entry.
type LDAPSource struct {
	searcher Searcher
	queries  []query
}

// NewLDAPSource returns a source over the configd directory tree rooted at
// baseDN (cn=zimbra). Domains live in their own DC subtrees outside baseDN,
// so they are searched from the root, as the cycle does.
func NewLDAPSource(searcher Searcher, baseDN string) *LDAPSource {
	return &LDAPSource{
		searcher: searcher,
		queries: []query{
			{ComponentGlobal, "cn=config," + baseDN, "(objectClass=*)", goldap.ScopeBaseObject},
			{ComponentServers, "cn=servers," + baseDN, "(objectClass=zimbraServer)", goldap.ScopeSingleLevel},
			{ComponentDomains, "", "(objectClass=zimbraDomain)", goldap.ScopeWholeSubtree},
		},
	}
}

type fingerprintResult struct {
	fp  Fingerprint
	err error
}

// Fingerprint implements Source. The LDAP client API takes no context, so the
// searches run in their own goroutine and the call returns as soon as ctx is
// done; the goroutine ends with the in-flight search, which the client bounds
// with its operation timeout.
func (s *LDAPSource) Fingerprint(ctx context.Context) (Fingerprint, error) {
	done := make(chan fingerprintResult, 1)

	go func() {
		fp, err := s.collect(ctx)
		done <- fingerprintResult{fp, err}
	}()

	select {
	case r := <-done:
		return r.fp, r.err
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // callers want the bare context error
	}
}

// collect runs every query, stopping early once ctx is done.
func (s *LDAPSource) collect(ctx context.Context) (Fingerprint, error) {
	fp := make(Fingerprint, len(s.queries))

	for _, q := range s.queries {
		if err := ctx.Err(); err != nil {
			return nil, err //nolint:wrapcheck // callers want the bare context error
		}

		res, err := s.searcher.Search(q.baseDN, q.filter, watchedAttrs, q.scope)
		if err != nil {
			return nil, fmt.Errorf("fingerprint %s: %w", q.component, err)
		}

		fp[q.component] = digest(res)
	}

	return fp, nil
}

// digest hashes the DN and the returned attributes of every entry, in a
// canonical order, so the result changes when an entry is added, removed or
// modified and is stable otherwise.
func digest(res *goldap.SearchResult) string {
	var entries []*goldap.Entry
	if res != nil {
		entries = res.Entries
	}

	lines := make([]string, 0, len(entries))

	for _, e := range entries {
		attrs := make([]string, 0, len(e.Attributes))

		for _, a := range e.Attributes {
			values := slices.Clone(a.Values)
			slices.Sort(values)

			attrs = append(attrs, strings.ToLower(a.Name)+"="+strings.Join(values, ","))
		}

		slices.Sort(attrs)

		lines = append(lines, strings.ToLower(e.DN)+"\x00"+strings.Join(attrs, "\x00"))
	}

	slices.Sort(lines)

	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))

	return hex.EncodeToString(sum[:])
}
