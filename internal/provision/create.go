// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"errors"
	"regexp"
	"strings"
)

// The attributes and values the creation of entries sets.
const (
	attrCreateTimestamp = "zimbraCreateTimestamp"
	attrDomainType      = "zimbraDomainType"
	attrDomainStatus    = "zimbraDomainStatus"
	attrGentimeFraction = "zimbraLdapGentimeFractionalSecondsEnabled"
	attrAllowNonLDH     = "zimbraAllowNonLDHCharsInDomain"
	attrServerExtraOC   = "zimbraServerExtraObjectClass"
	attrDomainExtraOC   = "zimbraDomainExtraObjectClass"

	domainTypeLocal  = "local"
	statusActive     = "active"
	statusEnabled    = "enabled"
	statusDisabled   = "disabled"
	classServer      = "zimbraServer"
	classDomain      = "zimbraDomain"
	classRole        = "organizationalRole"
	classDC          = "dcObject"
	classOrg         = "organization"
	suffixDomainName = " domain"
)

// nonLDHPattern matches a character that is not a letter, digit, hyphen or dot
// (NameUtil.containsNonLDH, for ASCII names).
var nonLDHPattern = regexp.MustCompile(`[^a-zA-Z0-9.-]`)

// attrSet is the attributes of an entry being created, by lower-case name.
type attrSet map[string][]string

func (a attrSet) set(name string, values ...string) { a[strings.ToLower(name)] = values }

func (a attrSet) has(name string) bool { return len(a[strings.ToLower(name)]) > 0 }

func (a attrSet) first(name string) string {
	if v := a[strings.ToLower(name)]; len(v) > 0 {
		return v[0]
	}

	return ""
}

// setDefault gives the attribute a value unless it has one.
func (a attrSet) setDefault(name, value string) {
	if !a.has(name) {
		a.set(name, value)
	}
}

// attrsOf is ZMutableEntry.mapToAttrs: the attributes a creation is given.
// The +/- forms only make sense for a modification.
func attrsOf(mods Mods) (attrSet, error) {
	attrs := attrSet{}

	for _, key := range mods.keys() {
		op, name := split(key)
		if op != 0 {
			return nil, invalidRequest("%s: + and - are not valid when creating an entry", key)
		}

		for _, v := range mods[key] {
			if v != "" {
				attrs[strings.ToLower(name)] = append(attrs[strings.ToLower(name)], v)
			}
		}
	}

	return attrs, nil
}

// objectClasses is LdapObjectClass: the default classes of the entry and the
// extra ones the global config lists.
func objectClasses(cfg *Entry, extraAttr string, defaults ...string) []string {
	ocs := append([]string(nil), defaults...)

	for _, extra := range cfg.Values(extraAttr) {
		if !containsFold(ocs, extra) {
			ocs = append(ocs, extra)
		}
	}

	return ocs
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}

	return false
}

// timestamp is LdapDateUtil.toGeneralizedTime: with milliseconds when the local
// server enables them, in the legacy format when it does not or does not exist
// yet (the first server of a node).
func (p *Provisioner) timestamp(cfg *Entry) (string, error) {
	local, err := p.localServer()
	if err != nil {
		return "", err
	}

	millis := local != nil && p.serverValue(local, cfg, attrGentimeFraction) == boolTrue

	return gentime(p.Now(), millis), nil
}

// createFailed is the error of an entry the directory refused to create.
func createFailed(err error, dn string) error { return failWith(err, "unable to create %s", dn) }

// add creates the entry; a DN that is taken is reported as the error built by
// exists.
func (p *Provisioner) add(dn string, attrs map[string][]string, exists func() error) error {
	err := p.Dir.Add(dn, attrs)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrEntryExists):
		return exists()
	default:
		return createFailed(err, dn)
	}
}

// createContext is the context of the callbacks of a creation.
func (p *Provisioner) createContext() *callbackContext {
	return &callbackContext{p: p, create: true, done: map[string]bool{}}
}

// finishCreate runs the postModify callbacks on the entry as it was created.
func (p *Provisioner) finishCreate(c *callbackContext, kind entryKind, dn string, mods Mods) {
	c.kind = kind
	c.entry = &Entry{DN: dn}
	p.postModifyReload(c, mods)
}

// CreateServer is createServer (zmprov cs): the server entry, with the id and
// timestamp, named by its hostname. It returns the zimbraId.
func (p *Provisioner) CreateServer(name string, mods Mods) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))

	c := p.createContext()
	if err := p.preModify(c, mods); err != nil {
		return "", err
	}

	cfg, err := p.config()
	if err != nil {
		return "", err
	}

	attrs, err := attrsOf(mods)
	if err != nil {
		return "", err
	}

	stamp, err := p.timestamp(cfg)
	if err != nil {
		return "", err
	}

	id := newID()

	attrs.set(attrObjClass, objectClasses(cfg, attrServerExtraOC, classServer)...)
	attrs.set(attrZimbraID, id)
	attrs.set(attrCreateTimestamp, stamp)
	attrs.set(attrCN, name)
	attrs.setDefault(attrServiceHostname, name)

	dn := p.serverDN(name)

	exists := func() error { return fail(CodeServerExists, "server already exists: %s", name) }
	if err := p.add(dn, attrs, exists); err != nil {
		return "", err
	}

	p.finishCreate(c, kindServer, dn, mods)

	return id, nil
}

// validNewDomainName is NameUtil.validNewDomainName.
func (p *Provisioner) validNewDomainName(domain string, cfg *Entry) error {
	if len(domain) > maxDomainNameLen {
		return invalidRequest("invalid domain name %s, domain name cannot exceed %d chars", domain, maxDomainNameLen)
	}

	if err := validEmail("test@"+domain, false); err != nil {
		return invalidRequest("invalid domain name %s", domain)
	}

	if cfg.First(attrAllowNonLDH) == boolFalse && nonLDHPattern.MatchString(domain) {
		return invalidRequest("invalid domain name %s: containing non-LDH characters and %s is false",
			domain, attrAllowNonLDH)
	}

	return nil
}

// CreateDomain is createDomain (zmprov cd): the parent entries of the name, the
// domain entry and its account and dynamic group bases. It returns the zimbraId.
func (p *Provisioner) CreateDomain(name string, mods Mods) (string, error) {
	name, err := asciiDomain(strings.ToLower(strings.TrimSpace(name)))
	if err != nil {
		return "", err
	}

	cfg, err := p.config()
	if err != nil {
		return "", err
	}

	if err := p.validNewDomainName(name, cfg); err != nil {
		return "", err
	}

	existing, err := p.domain(name)
	if err != nil {
		return "", err
	}

	if existing != nil {
		return "", fail(CodeDomainExists, "domain already exists: %s", name)
	}

	// The type and status may be given at creation though they are not
	// modifiable afterwards: they bypass the value checks.
	domainType, domainStatus := mods.first(attrDomainType), mods.first(attrDomainStatus)
	delete(mods, attrDomainType)
	delete(mods, attrDomainStatus)

	c := p.createContext()
	if err := p.preModify(c, mods); err != nil {
		return "", err
	}

	mods[attrDomainType] = []string{orDefault(domainType, domainTypeLocal)}
	mods[attrDomainStatus] = []string{orDefault(domainStatus, statusActive)}

	return p.writeDomain(c, name, cfg, mods)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// writeDomain adds the domain and what hangs under it.
func (p *Provisioner) writeDomain(c *callbackContext, name string, cfg *Entry, mods Mods) (string, error) {
	parts := strings.Split(name, ".")
	dns := domainDNS(parts)

	if err := p.createParentDomains(parts, dns); err != nil {
		return "", err
	}

	attrs, err := attrsOf(mods)
	if err != nil {
		return "", err
	}

	stamp, err := p.timestamp(cfg)
	if err != nil {
		return "", err
	}

	id := newID()

	attrs.set(attrObjClass, objectClasses(cfg, attrDomainExtraOC, classDC, classOrg, classDomain)...)
	attrs.set(attrZimbraID, id)
	attrs.set(attrCreateTimestamp, stamp)
	attrs.set(attrDomainName, name)
	attrs.setDefault(attrMailStatus, statusEnabled)
	attrs.set(attrOrg, name+suffixDomainName)
	attrs.set(attrDC, parts[0])

	if err := p.addOrReplace(dns[0], attrs); err != nil {
		return "", failWith(err, "unable to create domain: %s", name)
	}

	if err := p.addDomainBases(dns[0]); err != nil {
		return "", err
	}

	p.finishCreate(c, kindNone, dns[0], mods)

	return id, nil
}

// addOrReplace creates the entry, or replaces the attributes of the entry that
// is there (a parent entry created for another domain).
func (p *Provisioner) addOrReplace(dn string, attrs attrSet) error {
	err := p.Dir.Add(dn, attrs)
	if !errors.Is(err, ErrEntryExists) {
		return err
	}

	return p.Dir.ModifyValues(dn, nil, nil, attrs)
}

// createParentDomains is createParentDomains: the entries for the suffixes of
// the domain name, which are not domains.
func (p *Provisioner) createParentDomains(parts, dns []string) error {
	for i := len(dns) - 1; i > 0; i-- {
		existing, err := p.entryAt(dns[i])
		if err != nil {
			return err
		}

		if existing != nil {
			continue
		}

		attrs := attrSet{}
		attrs.set(attrObjClass, classDC, classOrg)
		attrs.set(attrOrg, parts[i]+suffixDomainName)
		attrs.set(attrDC, parts[i])

		if err := p.Dir.Add(dns[i], attrs); err != nil {
			return createFailed(err, dns[i])
		}
	}

	return nil
}

// addDomainBases creates the account base (ou=people) and the base of the
// dynamic groups of a domain.
func (p *Provisioner) addDomainBases(domainDN string) error {
	people := attrSet{}
	people.set(attrObjClass, classRole)
	people.set("ou", "people")
	people.set(attrCN, "people")

	groups := attrSet{}
	groups.set(attrObjClass, classRole)
	groups.set(attrCN, "groups")
	groups.set("description", "dynamic groups base")

	bases := []struct {
		dn    string
		attrs attrSet
	}{{ouPeople + "," + domainDN, people}, {cnGroups + "," + domainDN, groups}}

	for _, b := range bases {
		err := p.Dir.Add(b.dn, b.attrs)
		if errors.Is(err, ErrEntryExists) {
			return fail(CodeDomainExists, "domain already exists: %s", domainDN)
		}

		if err != nil {
			return createFailed(err, b.dn)
		}
	}

	return nil
}
