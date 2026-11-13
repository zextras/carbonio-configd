// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"errors"
	"slices"
	"strings"
)

// The attributes of accounts and aliases.
const (
	attrAccountStatus  = "zimbraAccountStatus"
	attrAccountExtraOC = "zimbraAccountExtraObjectClass"
	attrPwdModified    = "zimbraPasswordModifiedTime"
	attrUserPassword   = "userPassword"
	attrSN             = "sn"
	attrDisplayName    = "displayName"
	attrAliasTargetID  = "zimbraAliasTargetId"
	attrSystemResource = "zimbraIsSystemResource"
	classPerson        = "inetOrgPerson"
	classAccount       = "zimbraAccount"
	classAlias         = "zimbraAlias"
	attrDomainMaxAccts = "zimbraDomainMaxAccounts"
	attrDomainCOSMax   = "zimbraDomainCOSMaxAccounts"
	attrDomainFeatMax  = "zimbraDomainFeatureMaxAccounts"
)

// invalidCreateAttrs are the attributes CreateAccount derives itself.
var invalidCreateAttrs = []string{attrUID, attrMailAlias, attrMailDelivery}

// CreateAccount is createAccount (zmprov ca): the account entry under the
// people branch of its domain, with its default class of service, mail host and
// transport. The password is set after the entry exists, by the LDAP Password
// Modify operation: the server stores the hash. A password of "" creates the
// account without one. It returns the zimbraId.
func (p *Provisioner) CreateAccount(address, password string, mods Mods) (string, error) {
	local, domainName, err := splitEmail(address)
	if err != nil {
		return "", err
	}

	address = local + "@" + domainName
	if err := validEmail(address, false); err != nil {
		return "", err
	}

	c := p.createContext()
	if err := p.preModify(c, mods); err != nil {
		return "", err
	}

	domain, err := p.localDomain(domainName)
	if err != nil {
		return "", err
	}

	attrs, err := attrsOf(mods)
	if err != nil {
		return "", err
	}

	if err := rejectDerived(attrs); err != nil {
		return "", err
	}

	id, err := p.fillAccount(attrs, domain, local, address, password)
	if err != nil {
		return "", err
	}

	dn := emailDN(local, domainName)
	if err := p.add(dn, attrs, func() error {
		return fail(CodeAccountExists, "email address already exists: %s (%s)", address, dn)
	}); err != nil {
		return "", err
	}

	p.finishCreate(c, kindNone, dn, mods)

	if password != "" && !attrs.has(attrUserPassword) {
		if err := p.Dir.SetPassword(dn, password); err != nil {
			return id, failWith(err, "unable to set the password of %s", address)
		}
	}

	return id, nil
}

// rejectDerived refuses the attributes CreateAccount derives itself.
func rejectDerived(attrs attrSet) error {
	for _, a := range invalidCreateAttrs {
		if attrs.has(a) {
			return invalidRequest("invalid attribute for CreateAccount: %s", a)
		}
	}

	return nil
}

// localDomain finds the domain an account is created in; it must be a local
// domain without an account quota (the quota validators of the Java code count
// the accounts of the domain, which this tool does not).
func (p *Provisioner) localDomain(name string) (*Entry, error) {
	d, err := p.domain(name)
	if err != nil {
		return nil, err
	}

	if d == nil {
		return nil, fail(CodeNoSuchDomain, "no such domain: %s", name)
	}

	if t := d.First(attrDomainType); t != "" && t != domainTypeLocal {
		return nil, invalidRequest("domain type must be local")
	}

	for _, limit := range []string{attrDomainMaxAccts, attrDomainCOSMax, attrDomainFeatMax} {
		if d.Has(limit) {
			return nil, invalidRequest("domain %s has %s set: the native provisioning does not enforce account quotas",
				name, limit)
		}
	}

	return d, nil
}

// fillAccount sets the attributes createAccount derives: classes, id, times,
// status, class of service, mail host and transport, names and the password
// time. password is checked against the policy of the class of service.
func (p *Provisioner) fillAccount(attrs attrSet, domain *Entry, local, address, password string) (string, error) {
	cfg, err := p.config()
	if err != nil {
		return "", err
	}

	stamp, err := p.timestamp(cfg)
	if err != nil {
		return "", err
	}

	id := newID()

	attrs.set(attrObjClass, objectClasses(cfg, attrAccountExtraOC, classPerson, classAccount)...)
	attrs.set(attrZimbraID, id)
	attrs.set(attrCreateTimestamp, stamp)
	attrs.setDefault(attrAccountStatus, statusActive)

	cos, err := p.accountCOS(attrs, domain)
	if err != nil {
		return "", err
	}

	if err := p.setMailHosts(attrs, cos, cfg, address); err != nil {
		return "", err
	}

	attrs.set(attrMail, address)

	if !attrs.has(attrCN) {
		attrs.set(attrCN, orDefault(attrs.first(attrDisplayName), local))
	}

	attrs.setDefault(attrSN, local)
	attrs.set(attrUID, local)

	if password != "" && !attrs.has(attrUserPassword) {
		if err := checkPasswordStrength(password, policy{cos: cos, attrs: attrs}); err != nil {
			return "", err
		}
	}

	attrs.set(attrPwdModified, stamp)

	return id, nil
}

// accountCOS is the class of service of the new account: the one it names, its
// domain's default, else the class called default. The id of a named one is
// stored. nil when there is none.
func (p *Provisioner) accountCOS(attrs attrSet, domain *Entry) (*Entry, error) {
	if ref := attrs.first(attrCOSID); ref != "" {
		cos, err := p.lookupCOS(ref)
		if err != nil {
			return nil, err
		}

		attrs.set(attrCOSID, cos.First(attrZimbraID))

		return cos, nil
	}

	if id := domain.First(attrDomainCOSID); id != "" {
		cos, err := p.cos(id)
		if err != nil || cos != nil {
			return cos, err
		}
	}

	return p.cos(defaultCOS)
}

// setMailHosts gives the account a mail host and transport unless it comes with
// a transport, and the mail attributes that depend on them.
func (p *Provisioner) setMailHosts(attrs attrSet, cos, cfg *Entry, address string) error {
	if !attrs.has(attrMailTransport) {
		if err := p.addMailHost(attrs, cos, cfg); err != nil {
			return err
		}
	}

	if !attrs.has(attrMailHost) && !attrs.has(attrMailTransport) {
		return invalidRequest("missing %s or %s for CreateAccount: %s", attrMailHost, attrMailTransport, address)
	}

	attrs.setDefault(attrMailStatus, statusEnabled)
	attrs.setDefault(attrMailDelivery, address)

	return nil
}

// setMailHost sets the mail host of the account to the server, and its
// transport.
func (p *Provisioner) setMailHost(attrs attrSet, server, cfg *Entry) {
	attrs.set(attrMailHost, server.First(attrServiceHostname))
	attrs.set(attrMailTransport, p.mailTransport(server, cfg))
}

// usable reports whether the server can host mailboxes: it runs the mail client
// service and has a service hostname.
func usable(server *Entry) bool {
	return hasService(server, serviceWebapp) && server.Has(attrServiceHostname)
}

// addMailHost is addMailHost of LdapProvisioning: a server of the class of
// service's pool, picked at random; else the local server, else the first
// server that can host mailboxes.
func (p *Provisioner) addMailHost(attrs attrSet, cos, cfg *Entry) error {
	if cos != nil && !attrs.has(attrMailHost) {
		if err := p.pickFromPool(attrs, cos, cfg); err != nil {
			return err
		}
	}

	if attrs.has(attrMailHost) {
		return nil
	}

	local, err := p.localServer()
	if err != nil {
		return err
	}

	if local != nil && usable(local) {
		p.setMailHost(attrs, local, cfg)

		return nil
	}

	servers, err := p.servers()
	if err != nil {
		return err
	}

	if i := slices.IndexFunc(servers, func(s Entry) bool { return usable(&s) }); i >= 0 {
		p.setMailHost(attrs, &servers[i], cfg)
	}

	return nil
}

// pickFromPool deals the servers of the pool of the class of service in random
// order and takes the first that can host mailboxes.
func (p *Provisioner) pickFromPool(attrs attrSet, cos, cfg *Entry) error {
	pool := slices.Clone(cos.Values(attrMailHostPool))

	for len(pool) > 0 {
		i := p.Pick(len(pool))
		id := pool[i]
		pool = slices.Delete(pool, i, i+1)

		server, err := p.serverBy(attrZimbraID, id)
		if err != nil {
			return err
		}

		if server == nil || !usable(server) {
			p.Warn("cos(%s) mailHostPool server(%s) is not a mailclient server with service webapp enabled",
				cos.First(attrCN), id)

			continue
		}

		p.setMailHost(attrs, server, cfg)

		return nil
	}

	return nil
}

// AddAlias is addAlias for an account (zmprov aaa): the alias entry in the people
// branch of the alias' domain, pointing at the account, and the alias in the
// mail and zimbraMailAlias attributes of the account.
func (p *Provisioner) AddAlias(account, alias string) error {
	target, err := p.account(strings.ToLower(strings.TrimSpace(account)))
	if err != nil {
		return err
	}

	if target == nil {
		return fail(CodeNoSuchAccount, "no such account: %s", account)
	}

	local, domainName, err := splitEmail(alias)
	if err != nil {
		return err
	}

	alias = local + "@" + domainName
	if err := validEmail(alias, false); err != nil {
		return err
	}

	domain, err := p.domain(domainName)
	if err != nil {
		return err
	}

	if domain == nil {
		return fail(CodeNoSuchDomain, "no such domain: %s", domainName)
	}

	if err := p.addAliasEntry(emailDN(local, domainName), local, alias, target); err != nil {
		return err
	}

	return p.Dir.ModifyValues(target.DN, map[string][]string{attrMailAlias: {alias}, attrMail: {alias}}, nil, nil)
}

// addAliasEntry creates the alias entry, handling an entry already at its DN as
// addAliasInternal does.
func (p *Provisioner) addAliasEntry(dn, local, alias string, target *Entry) error {
	exists := func() error { return fail(CodeAccountExists, "email address already exists: %s (%s)", alias, dn) }

	err := p.createAlias(dn, local, target)
	if !errors.Is(err, ErrEntryExists) {
		return p.failure(err, dn)
	}

	existing, err := p.entryAt(dn)
	if err != nil {
		return err
	}

	if existing == nil || !slices.Contains(existing.Values(attrObjClass), classAlias) {
		return exists()
	}

	owner, err := p.accountByID(existing.First(attrAliasTargetID))
	if err != nil {
		return err
	}

	if owner == nil {
		// A dangling alias: replace it.
		if err := p.Dir.Delete(dn); err != nil {
			return failWith(err, "unable to remove the dangling alias %s", dn)
		}

		return p.failure(p.createAlias(dn, local, target), dn)
	}

	listed := slices.Contains(target.Values(attrMailAlias), alias) && slices.Contains(target.Values(attrMail), alias)
	if owner.DN != target.DN || listed {
		return exists()
	}

	p.Warn("alias entry exists at %s, but either mail or zimbraMailAlias of the target does not contain %s, adding it",
		dn, alias)

	return nil
}

// failure maps the error of an entry creation.
func (p *Provisioner) failure(err error, dn string) error {
	if err == nil {
		return nil
	}

	return failWith(err, "unable to create alias %s", dn)
}

// createAlias adds the alias entry.
func (p *Provisioner) createAlias(dn, local string, target *Entry) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}

	stamp, err := p.timestamp(cfg)
	if err != nil {
		return err
	}

	attrs := attrSet{}
	attrs.set(attrObjClass, classAlias)
	attrs.set(attrUID, local)
	attrs.set(attrZimbraID, newID())
	attrs.set(attrCreateTimestamp, stamp)
	attrs.set(attrAliasTargetID, target.First(attrZimbraID))

	return p.Dir.Add(dn, attrs)
}
