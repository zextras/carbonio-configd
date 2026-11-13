// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"math/rand/v2"
	"net"
	"strings"
	"time"
)

// The zmprov verbs the provisioning runs.
const (
	VerbCreateServer  = "cs"
	VerbCreateDomain  = "cd"
	VerbCreateAccount = "ca"
	VerbAddAlias      = "aaa"
	VerbModifyConfig  = "mcf"
	VerbModifyServer  = "ms"
	VerbModifyCOS     = "mc"
	// VerbModifyAccount and VerbModifyDomain are the zmprov ma and md.
	VerbModifyAccount = "ma"
	VerbModifyDomain  = "md"
)

// Options are what a Provisioner works with.
type Options struct {
	// Dir is the directory.
	Dir Directory
	// Schema is the attribute schema (LoadSchema of conf/attrs).
	Schema *Schema
	// LocalHostname is zimbra_server_hostname, the name of the local server.
	LocalHostname string
	// ReadFile reads the file named by the value of a binary attribute.
	ReadFile func(path string) ([]byte, error)
	// SetLocalConfig writes a key to localconfig.xml; the MailPort callback
	// keeps zimbra_mail_service_port in step with zimbraMailPort.
	SetLocalConfig func(key, value string) error
	// Warn receives what the Java code only logs (callbacks that fail after the
	// modification went through).
	Warn func(format string, args ...any)
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Resolve resolves a bind address (default net.LookupIP).
	Resolve func(host string) ([]net.IP, error)
	// Pick returns a random index below n for the mail host pool (default
	// rand.IntN).
	Pick func(n int) int
}

// Provisioner runs the provisioning commands against a directory.
type Provisioner struct {
	Options
}

// New returns a Provisioner; the options not set take their defaults.
func New(o Options) *Provisioner { //nolint:gocritic // the options are given by value, once
	if o.Now == nil {
		o.Now = time.Now
	}

	if o.Resolve == nil {
		o.Resolve = net.LookupIP
	}

	if o.Pick == nil {
		o.Pick = rand.IntN
	}

	if o.Warn == nil {
		o.Warn = func(string, ...any) {
			// No sink given: warnings are dropped.
		}
	}

	if o.SetLocalConfig == nil {
		o.SetLocalConfig = func(string, string) error { return nil }
	}

	if o.ReadFile == nil {
		o.ReadFile = func(string) ([]byte, error) { return nil, invalidRequest("no file access") }
	}

	return &Provisioner{o}
}

// Exec runs one zmprov command: args[0] is the verb, the rest are its
// arguments as on the zmprov command line. It returns the zimbraId of the entry
// a creation made (what zmprov prints), "" for the others.
//
//	cs  <server> [attr value ...]
//	cd  <domain> [attr value ...]
//	ca  <address> <password> [attr value ...]   ("" for no password)
//	aaa <account> <alias>
//	mcf [+|-]attr value ...
//	ms  <server> [+|-]attr value ...
//	mc  <cos> [+|-]attr value ...
//	ma  <account> [+|-]attr value ...
//	md  <domain> [+|-]attr value ...
func (p *Provisioner) Exec(args []string) (string, error) {
	if len(args) == 0 {
		return "", invalidRequest("no command")
	}

	verb, rest := args[0], args[1:]

	if need := argCount(verb); len(rest) < need {
		return "", invalidRequest("%s: expected at least %d arguments", verb, need)
	}

	switch verb {
	case VerbCreateServer:
		return p.createServerArgs(rest)
	case VerbCreateDomain:
		return p.createDomainArgs(rest)
	case VerbCreateAccount:
		return p.createAccountArgs(rest)
	case VerbAddAlias:
		return "", p.AddAlias(rest[0], rest[1])
	case VerbModifyConfig, VerbModifyServer, VerbModifyCOS, VerbModifyAccount, VerbModifyDomain:
		return "", p.modifyArgs(verb, rest)
	default:
		return "", invalidRequest("unknown command %q", verb)
	}
}

// argCount is the number of mandatory arguments of the verb.
func argCount(verb string) int {
	switch verb {
	case VerbCreateServer, VerbCreateDomain:
		return 1
	case VerbCreateAccount, VerbAddAlias, VerbModifyConfig:
		return 2 // ca: address and password; mcf: one attribute and its value
	case VerbModifyServer, VerbModifyCOS, VerbModifyAccount, VerbModifyDomain:
		return 3 // the entry, one attribute and its value
	default:
		return 0
	}
}

func (p *Provisioner) mods(kv []string) (Mods, error) {
	return ParseMods(p.Schema, p.ReadFile, kv)
}

func (p *Provisioner) createServerArgs(rest []string) (string, error) {
	mods, err := p.mods(rest[1:])
	if err != nil {
		return "", err
	}

	return p.CreateServer(rest[0], mods)
}

func (p *Provisioner) createDomainArgs(rest []string) (string, error) {
	mods, err := p.mods(rest[1:])
	if err != nil {
		return "", err
	}

	return p.CreateDomain(rest[0], mods)
}

func (p *Provisioner) createAccountArgs(rest []string) (string, error) {
	mods, err := p.mods(rest[2:])
	if err != nil {
		return "", err
	}

	return p.CreateAccount(rest[0], rest[1], mods)
}

// modifyArgs runs mcf, ms, mc, ma or md.
func (p *Provisioner) modifyArgs(verb string, rest []string) error {
	var (
		target *Entry
		kind   entryKind
		err    error
	)

	switch verb {
	case VerbModifyConfig:
		kind = kindConfig
		target, err = p.config()
	case VerbModifyServer:
		kind = kindServer
		target, err = p.lookupServer(rest[0])
		rest = rest[1:]
	case VerbModifyAccount:
		kind = kindAccount
		target, err = p.lookupAccount(rest[0])
		rest = rest[1:]
	case VerbModifyDomain:
		kind = kindDomain
		target, err = p.lookupDomain(rest[0])
		rest = rest[1:]
	default:
		kind = kindCOS
		target, err = p.lookupCOS(rest[0])
		rest = rest[1:]
	}

	if err != nil {
		return err
	}

	mods, err := p.mods(rest)
	if err != nil {
		return err
	}

	if kind == kindAccount {
		if err := refuseUnhashedPassword(mods); err != nil {
			return err
		}
	}

	return p.modify(kind, target, mods)
}

// lookupServer is ProvUtil.lookupServer: by id or by name.
func (p *Provisioner) lookupServer(nameOrID string) (*Entry, error) {
	var (
		server *Entry
		err    error
	)

	if idPattern.MatchString(nameOrID) {
		server, err = p.serverBy(attrZimbraID, nameOrID)
	} else {
		server, err = p.entryAt(p.serverDN(nameOrID))
	}

	if err != nil {
		return nil, err
	}

	if server == nil {
		return nil, fail(CodeNoSuchServer, "no such server: %s", nameOrID)
	}

	return server, nil
}

// lookupCOS is ProvUtil.lookupCos: by id or by name.
func (p *Provisioner) lookupCOS(nameOrID string) (*Entry, error) {
	cos, err := p.cos(nameOrID)
	if err != nil {
		return nil, err
	}

	if cos == nil {
		return nil, fail(CodeNoSuchCOS, "no such cos: %s", nameOrID)
	}

	return cos, nil
}

// modify is LdapProvisioning.modifyAttrs with checkImmutable: the value
// checks and callbacks, the LDAP modification, the callbacks that follow it.
func (p *Provisioner) modify(kind entryKind, entry *Entry, mods Mods) error {
	ctx := &callbackContext{p: p, kind: kind, entry: entry, done: map[string]bool{}}

	if err := p.preModify(ctx, mods); err != nil {
		return err
	}

	change, err := mods.change()
	if err != nil {
		return err
	}

	if err := p.Dir.ModifyValues(entry.DN, change.add, change.del, change.replace); err != nil {
		return modifyFailed(err, entry.DN)
	}

	p.postModifyReload(ctx, mods)

	return nil
}

// postModifyReload runs the postModify callbacks on the entry as it is after the
// modification.
func (p *Provisioner) postModifyReload(ctx *callbackContext, mods Mods) {
	fresh, err := p.entryAt(ctx.entry.DN)
	if err != nil || fresh == nil {
		p.Warn("postModify: cannot reload %s: %v", ctx.entry.DN, err)

		return
	}

	ctx.entry = fresh

	p.postModify(ctx, mods)
}

// refuseUnhashedPassword refuses a userPassword modification: the value would
// be stored as given, not hashed by the directory as the Password Modify
// operation of ca does.
func refuseUnhashedPassword(mods Mods) error {
	for key := range mods {
		if _, name := split(key); strings.EqualFold(name, attrUserPassword) {
			return invalidRequest("%s cannot be modified here: the value would be stored unhashed", attrUserPassword)
		}
	}

	return nil
}
