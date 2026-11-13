// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"fmt"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// The attributes the provisioning reads.
const (
	attrServiceEnabled  = "zimbraServiceEnabled"
	attrServiceHostname = "zimbraServiceHostname"
	attrDomainName      = "zimbraDomainName"
	attrMailDelivery    = "zimbraMailDeliveryAddress"
	attrMailHost        = "zimbraMailHost"
	attrMailTransport   = "zimbraMailTransport"
	attrMailStatus      = "zimbraMailStatus"
	attrMailHostPool    = "zimbraMailHostPool"
	attrLmtpPort        = "zimbraLmtpBindPort"
	attrCOSID           = "zimbraCOSId"
	attrDomainCOSID     = "zimbraDomainDefaultCOSId"

	// serviceWebapp is Provisioning.SERVICE_MAILCLIENT: the service a server
	// must run to host mailboxes.
	serviceWebapp = "service"
	// defaultLmtpPort is Config.D_LMTP_BIND_PORT.
	defaultLmtpPort = "7025"
)

// filterAnd is (&a b ...), each term escaped by the caller.
func filterAnd(terms ...string) string { return "(&" + strings.Join(terms, "") + ")" }

// eq is the filter (attr=value) with the value escaped.
func eq(attr, value string) string { return "(" + attr + "=" + goldap.EscapeFilter(value) + ")" }

// object is the filter term of an object class.
func object(class string) string { return eq(attrObjClass, class) }

// entryAt reads the entry at dn; nil when there is none.
func (p *Provisioner) entryAt(dn string) (*Entry, error) {
	entries, err := p.Dir.Search(dn, ScopeBase, "(objectClass=*)")
	if err != nil {
		return nil, failWith(err, "unable to read %s", dn)
	}

	if len(entries) == 0 {
		return nil, nil //nolint:nilnil // an absent entry is not an error
	}

	return &entries[0], nil
}

// find returns the one entry that matches the filter under base; nil when
// there is none.
func (p *Provisioner) find(base, filter string) (*Entry, error) {
	entries, err := p.Dir.Search(base, ScopeSub, filter)
	if err != nil {
		return nil, failWith(err, "unable to look up %s", filter)
	}

	switch len(entries) {
	case 0:
		return nil, nil //nolint:nilnil // not found is not an error
	case 1:
		return &entries[0], nil
	default:
		return nil, invalidRequest("multiple entries matched %s", filter)
	}
}

func (p *Provisioner) configDN() string { return cnConfig + "," + p.Dir.BaseDN() }

func (p *Provisioner) serversDN() string { return cnServers + "," + p.Dir.BaseDN() }

func (p *Provisioner) serverDN(name string) string {
	return "cn=" + goldap.EscapeDN(name) + "," + p.serversDN()
}

func (p *Provisioner) cosDN(name string) string {
	return "cn=" + goldap.EscapeDN(name) + "," + cnCOS + "," + p.Dir.BaseDN()
}

// config reads the global config.
func (p *Provisioner) config() (*Entry, error) {
	cfg, err := p.entryAt(p.configDN())
	if err != nil {
		return nil, err
	}

	if cfg == nil {
		return nil, fail(CodeFailure, "the global config %s does not exist", p.configDN())
	}

	return cfg, nil
}

// servers reads every server entry, by DN.
func (p *Provisioner) servers() ([]Entry, error) {
	entries, err := p.Dir.Search(p.serversDN(), ScopeOne, filterAnd(object("zimbraServer")))
	if err != nil {
		return nil, failWith(err, "unable to list the servers")
	}

	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.DN, b.DN) })

	return entries, nil
}

// serverBy finds the server whose attribute is value.
func (p *Provisioner) serverBy(attr, value string) (*Entry, error) {
	return p.find(p.serversDN(), filterAnd(object("zimbraServer"), eq(attr, value)))
}

// localServer is the server of this node (the zimbra_server_hostname of the
// local config), nil when it is not created yet.
func (p *Provisioner) localServer() (*Entry, error) {
	if p.LocalHostname == "" {
		return nil, nil //nolint:nilnil // no local hostname, no local server
	}

	return p.entryAt(p.serverDN(strings.ToLower(p.LocalHostname)))
}

// cos finds a class of service by id or by name.
func (p *Provisioner) cos(nameOrID string) (*Entry, error) {
	if idPattern.MatchString(nameOrID) {
		return p.find(cnCOS+","+p.Dir.BaseDN(), filterAnd(object("zimbraCOS"), eq(attrZimbraID, nameOrID)))
	}

	return p.entryAt(p.cosDN(nameOrID))
}

// domain finds the domain (a zimbraDomain entry) of the ASCII name in the mail
// branch.
func (p *Provisioner) domain(name string) (*Entry, error) {
	return p.find("", filterAnd(object("zimbraDomain"), eq(attrDomainName, name)))
}

// account finds the account that has the address as its delivery address or as
// an alias.
func (p *Provisioner) account(addr string) (*Entry, error) {
	f := filterAnd("(|"+eq(attrMailDelivery, addr)+eq(attrMailAlias, addr)+")", object("zimbraAccount"))

	return p.find("", f)
}

// AccountID is the zimbraId of the account named name, a delivery address or an
// alias (Provisioning.getAccountByName). An unknown name is account.NO_SUCH_ACCOUNT.
func (p *Provisioner) AccountID(name string) (string, error) {
	acct, err := p.account(strings.ToLower(strings.TrimSpace(name)))
	if err != nil {
		return "", err
	}

	if acct == nil {
		return "", fail(CodeNoSuchAccount, "no such account: %s", name)
	}

	return acct.First(attrZimbraID), nil
}

// entryByID finds the account or server entry of a zimbraId, for the alias
// target check.
func (p *Provisioner) accountByID(id string) (*Entry, error) {
	return p.find("", filterAnd(object("zimbraAccount"), eq(attrZimbraID, id)))
}

// serverValue is the value of a server attribute: the server's own, else the
// global config value when the attribute is inherited by servers
// (Server.getAttr with inheritance).
func (p *Provisioner) serverValue(server, cfg *Entry, name string) string {
	if server.Has(name) {
		return server.First(name)
	}

	if p.Schema.ServerInherited(name) {
		return cfg.First(name)
	}

	return ""
}

// serverDefaults are the global config values a server inherits
// (Config.getServerDefaults).
func (p *Provisioner) serverDefaults(cfg *Entry) map[string]string {
	defaults := map[string]string{}

	for name, values := range cfg.Attrs {
		if len(values) > 0 && p.Schema.ServerInherited(name) {
			defaults[name] = values[0]
		}
	}

	return defaults
}

// hasService reports whether the server runs the service.
func hasService(server *Entry, service string) bool {
	return slices.Contains(server.Values(attrServiceEnabled), service)
}

// lmtpPort is the LMTP port of a server: its own, the inherited default, else
// Config.D_LMTP_BIND_PORT.
func (p *Provisioner) lmtpPort(server, cfg *Entry) string {
	if v := p.serverValue(server, cfg, attrLmtpPort); v != "" {
		return v
	}

	return defaultLmtpPort
}

// mailTransport is the LMTP transport of the server, "lmtp:host:port"
// (MailHost.mailTransport).
func (p *Provisioner) mailTransport(server, cfg *Entry) string {
	return fmt.Sprintf("lmtp:%s:%s", server.First(attrServiceHostname), p.lmtpPort(server, cfg))
}
