// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// srvEntry is an LDAP entry as the server steps read it: attribute names
// lower-cased, every value kept (zmprov prints multi-valued attributes as
// repeated lines, the native client joins them: the steps need the list).
type srvEntry map[string][]string

// first is the first value of attr ("" when absent).
func (m srvEntry) first(attr string) string {
	if v := m[strings.ToLower(attr)]; len(v) > 0 {
		return v[0]
	}

	return ""
}

// values are the values of attr.
func (m srvEntry) values(attr string) []string { return m[strings.ToLower(attr)] }

// srvDirectory is the read side of LDAP the server steps use (setup.pl
// getLdap*Value, getAllServers, and the Net::LDAP searches). Writes go
// through zmprov, which owns the attribute logic.
type srvDirectory interface {
	Close() error
	// Global is the global config entry.
	Global() (srvEntry, error)
	// Server is the entry of one server as stored (no inherited values);
	// found is false when there is none.
	Server(host string) (entry srvEntry, found bool, err error)
	// COS is the entry of one class of service.
	COS(name string) (entry srvEntry, found bool, err error)
	// ServerNames lists the servers matching an LDAP filter fragment such as
	// "(zimbraServiceEnabled=mta)" ("" for all).
	ServerNames(filter string) ([]string, error)
	// VIPDomains lists the domains that set zimbraVirtualIPAddress.
	VIPDomains() ([]srvEntry, error)
}

// srvOpenDir opens the directory; tests replace it.
var srvOpenDir = func(e *Env) (srvDirectory, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	return &srvLDAP{c: c}, nil
}

// srvRead opens the directory for fn and closes it.
func srvRead(e *Env, fn func(d srvDirectory) error) error {
	d, err := srvOpenDir(e)
	if err != nil {
		return fmt.Errorf("opening LDAP: %w", err)
	}

	defer func() { _ = d.Close() }()

	return fn(d)
}

// srvLDAP is srvDirectory over the native client.
type srvLDAP struct{ c *ldap.Client }

func (d *srvLDAP) Close() error { return d.c.Close() }

func (d *srvLDAP) search(base, filter string, scope int) ([]srvEntry, error) {
	res, err := d.c.Search(base, filter, []string{"*"}, scope)
	if err != nil {
		return nil, err
	}

	out := make([]srvEntry, 0, len(res.Entries))

	for _, ent := range res.Entries {
		m := srvEntry{}

		for _, a := range ent.Attributes {
			m[strings.ToLower(a.Name)] = slices.Clone(a.Values)
		}

		out = append(out, m)
	}

	return out, nil
}

func (d *srvLDAP) Global() (srvEntry, error) {
	list, err := d.search("cn=config,"+d.c.BaseDN(), "(objectClass=*)", goldap.ScopeBaseObject)
	if err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("global config not found")
	}

	return list[0], nil
}

func (d *srvLDAP) one(base, class, name string) (srvEntry, bool, error) {
	filter := fmt.Sprintf("(&(objectClass=%s)(cn=%s))", class, goldap.EscapeFilter(name))

	list, err := d.search(base, filter, goldap.ScopeSingleLevel)
	if err != nil {
		return nil, false, fmt.Errorf("%s %s: %w", class, name, err)
	}

	if len(list) == 0 {
		return nil, false, nil
	}

	return list[0], true, nil
}

func (d *srvLDAP) Server(host string) (srvEntry, bool, error) {
	return d.one("cn=servers,"+d.c.BaseDN(), "zimbraServer", host)
}

func (d *srvLDAP) COS(name string) (srvEntry, bool, error) {
	return d.one("cn=cos,"+d.c.BaseDN(), "zimbraCOS", name)
}

func (d *srvLDAP) ServerNames(filter string) ([]string, error) {
	list, err := d.search("cn=servers,"+d.c.BaseDN(), "(&(objectClass=zimbraServer)"+filter+")",
		goldap.ScopeSingleLevel)
	if err != nil {
		return nil, fmt.Errorf("servers: %w", err)
	}

	names := make([]string, 0, len(list))
	for _, ent := range list {
		names = append(names, ent.first("cn"))
	}

	return names, nil
}

func (d *srvLDAP) VIPDomains() ([]srvEntry, error) {
	return d.search("", "(&(objectClass=zimbraDomain)(zimbraVirtualIPAddress=*))", goldap.ScopeWholeSubtree)
}
