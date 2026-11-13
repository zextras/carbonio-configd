// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// The zmprov verbs that read.
const (
	VerbGetServer              = "gs"
	VerbGetConfig              = "gcf"
	VerbGetAllConfig           = "gacf"
	VerbGetAccount             = "ga"
	VerbGetCOS                 = "gc"
	VerbGetDomain              = "gd"
	VerbGetAllServers          = "gas"
	VerbGetAllReverseProxyURLs = "garpu"
	VerbGetAllMemcachedServers = "gamcs"
)

const (
	// flagExpandOff is "-e": show the entry as stored, without the values it
	// inherits.
	flagExpandOff = "-e"
	// flagVerbose is "-v" of gas: dump every server.
	flagVerbose = "-v"

	serviceMailbox   = "mailbox"
	serviceMemcached = "memcached"

	// reverseProxyPath is ExtensionDispatcherServlet.EXTENSION_PATH + "/nginx-lookup".
	reverseProxyPath = "/service/extension/nginx-lookup"
	// defaultExtensionPort and defaultMemcachedPort are the defaults of
	// zimbraExtensionBindPort (ProvUtil) and zimbraMemcachedBindPort
	// (Config.D_MEMCACHED_BIND_PORT).
	defaultExtensionPort = 7072
	defaultMemcachedPort = 11211

	attrReverseProxyLookup = "zimbraReverseProxyLookupTarget"
	attrExtensionPort      = "zimbraExtensionBindPort"
	attrMemcachedPort      = "zimbraMemcachedBindPort"
)

// readVerbs are the zmprov commands that read, with the number of arguments
// they take: min and max (-1 for any).
var readVerbs = map[string][2]int{
	VerbGetServer:              {1, -1},
	VerbGetConfig:              {1, 1},
	VerbGetAllConfig:           {0, -1},
	VerbGetAccount:             {1, -1},
	VerbGetCOS:                 {1, -1},
	VerbGetDomain:              {1, -1},
	VerbGetAllServers:          {0, 3},
	VerbGetAllReverseProxyURLs: {0, 0},
	VerbGetAllMemcachedServers: {0, 0},
}

// IsRead reports whether the verb is one of the commands that read.
func IsRead(verb string) bool {
	_, ok := readVerbs[verb]

	return ok
}

// IsVerb reports whether the verb is a command of the provisioner.
func IsVerb(verb string) bool {
	switch verb {
	case VerbCreateServer, VerbCreateDomain, VerbCreateAccount, VerbAddAlias,
		VerbModifyConfig, VerbModifyServer, VerbModifyCOS, VerbModifyAccount, VerbModifyDomain:
		return true
	default:
		return IsRead(verb)
	}
}

// UsageError is a command line zmprov would answer with its usage (exit
// status 1) rather than with a service error: an unknown command or a wrong
// number of arguments.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Run executes one zmprov command, args[0] being the verb, and writes what
// zmprov prints to out: the id of a created entry, the dump of the entries a
// read command asks for.
func (p *Provisioner) Run(args []string, out io.Writer) error {
	if len(args) == 0 || !IsVerb(args[0]) {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}

		return &UsageError{Msg: fmt.Sprintf("unknown command: %q", name)}
	}

	if !IsRead(args[0]) {
		id, err := p.Exec(args)
		if err != nil {
			return err
		}

		if id != "" {
			_, err = fmt.Fprintln(out, id)
		}

		return err
	}

	limits := readVerbs[args[0]]
	if n := len(args) - 1; n < limits[0] || (limits[1] >= 0 && n > limits[1]) {
		return &UsageError{Msg: fmt.Sprintf("%s is expecting %s arguments but %d %s been provided",
			args[0], arity(limits), n, map[bool]string{true: "has", false: "have"}[n == 1])}
	}

	return p.read(args[0], args[1:], out)
}

// arity describes the argument count of a verb as ProvUtil does.
func arity(limits [2]int) string {
	switch {
	case limits[1] < 0:
		return "at least " + strconv.Itoa(limits[0])
	case limits[0] == limits[1]:
		return strconv.Itoa(limits[0])
	default:
		return fmt.Sprintf("between %d and %d", limits[0], limits[1])
	}
}

// read runs a read command.
func (p *Provisioner) read(verb string, args []string, out io.Writer) error {
	switch verb {
	case VerbGetServer:
		return p.getEntry(verb, args, out, entryKindOf{p.lookupServer, p.serverView, nameOf(attrCN)})
	case VerbGetConfig:
		return p.getConfig(args, out)
	case VerbGetAllConfig:
		return p.getConfig(nil, out, args...)
	case VerbGetAccount:
		return p.getEntry(verb, args, out, entryKindOf{p.lookupAccount, p.accountView, p.accountName})
	case VerbGetCOS:
		return p.getCOS(args, out)
	case VerbGetDomain:
		return p.getEntry(verb, args, out, entryKindOf{p.lookupDomain, p.domainView, nameOf(attrDomainName)})
	case VerbGetAllServers:
		return p.getAllServers(args, out)
	case VerbGetAllReverseProxyURLs:
		return p.getAllReverseProxyURLs(out)
	default:
		return p.getAllMemcachedServers(out)
	}
}

// expandFlags reads the leading "-e" arguments (gs, gd): expand is false when
// one was given. rest starts at the entry.
func expandFlags(args []string) (expand bool, rest []string) {
	i := 0
	for i < len(args) && args[i] == flagExpandOff {
		i++
	}

	return i == 0, args[i:]
}

// lookupDomain is ProvUtil.lookupDomain: by id or by name.
func (p *Provisioner) lookupDomain(nameOrID string) (*Entry, error) {
	var (
		domain *Entry
		err    error
	)

	if idPattern.MatchString(nameOrID) {
		domain, err = p.find("", filterAnd(object("zimbraDomain"), eq(attrZimbraID, nameOrID)))
	} else {
		domain, err = p.domain(strings.ToLower(nameOrID))
	}

	if err != nil {
		return nil, err
	}

	if domain == nil {
		return nil, fail(CodeNoSuchDomain, "no such domain: %s", nameOrID)
	}

	return domain, nil
}

// lookupAccount is ProvUtil.lookupAccount: by id or by address (the delivery
// address or an alias).
func (p *Provisioner) lookupAccount(addrOrID string) (*Entry, error) {
	var (
		acct *Entry
		err  error
	)

	if idPattern.MatchString(addrOrID) {
		acct, err = p.accountByID(addrOrID)
	} else {
		acct, err = p.account(strings.ToLower(strings.TrimSpace(addrOrID)))
	}

	if err != nil {
		return nil, err
	}

	if acct == nil {
		return nil, fail(CodeNoSuchAccount, "no such account: %s", addrOrID)
	}

	return acct, nil
}

// entryKindOf is what gs, gd and ga differ in: how the entry is looked up, the
// attributes it shows and its name.
type entryKindOf struct {
	lookup func(string) (*Entry, error)
	view   func(e *Entry, expand bool) (shownAttrs, error)
	name   func(*Entry) string
}

// nameOf is the name of an entry: the value of the attribute.
func nameOf(attr string) func(*Entry) string {
	return func(e *Entry) string { return e.First(attr) }
}

// getEntry is gs, gd and ga: "[-e] <entry> [attr...]".
func (p *Provisioner) getEntry(verb string, args []string, out io.Writer, k entryKindOf) error {
	expand, rest := expandFlags(args)
	if len(rest) == 0 {
		return &UsageError{Msg: verb + ": an entry is expected after -e"}
	}

	entry, err := k.lookup(rest[0])
	if err != nil {
		return err
	}

	filter, err := newDumpFilter(rest[1:])
	if err != nil {
		return err
	}

	view, err := k.view(entry, expand)
	if err != nil {
		return err
	}

	return p.dumpEntry(out, k.name(entry), view, filter)
}

// serverView is Server.getAttrs: the attributes of the server and, unless the
// view is not expanded, the global config values it inherits.
func (p *Provisioner) serverView(server *Entry, expand bool) (shownAttrs, error) {
	own := p.attrsOf(server)
	if !expand {
		return own, nil
	}

	cfg, err := p.config()
	if err != nil {
		return nil, err
	}

	return p.inherited(flagServerInherited, p.attrsOf(cfg)).over(own), nil
}

// domainView is Domain.getAttrs.
func (p *Provisioner) domainView(domain *Entry, expand bool) (shownAttrs, error) {
	own := p.attrsOf(domain)
	if !expand {
		return own, nil
	}

	cfg, err := p.config()
	if err != nil {
		return nil, err
	}

	return p.inherited(flagDomainInherited, p.attrsOf(cfg)).over(own), nil
}

// getCOS is gc cos [attr...].
func (p *Provisioner) getCOS(args []string, out io.Writer) error {
	cos, err := p.lookupCOS(args[0])
	if err != nil {
		return err
	}

	filter, err := newDumpFilter(args[1:])
	if err != nil {
		return err
	}

	return p.dumpEntry(out, cos.First(attrCN), p.attrsOf(cos), filter)
}

// getConfig is gcf attr (the attribute named in args) and gacf [attr...] (the
// names in names): the global config attributes, with no header.
func (p *Provisioner) getConfig(args []string, out io.Writer, names ...string) error {
	names = append(slices.Clone(args), names...)

	filter, err := newDumpFilter(names)
	if err != nil {
		return err
	}

	cfg, err := p.config()
	if err != nil {
		return err
	}

	return p.dump(out, p.attrsOf(cfg), filter)
}

// accountName is the name of an account: its delivery address, else the address
// its DN spells (LdapDIT.dnToEmail).
func (p *Provisioner) accountName(acct *Entry) string {
	if name := acct.First(attrMailDelivery); name != "" {
		return name
	}

	local, domain := acct.First(attrUID), []string{}

	for rdn := range strings.SplitSeq(acct.DN, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(rdn), attrDC+"="); ok {
			domain = append(domain, v)
		}
	}

	return local + "@" + strings.Join(domain, ".")
}

// accountView is Account.getAttrs: the attributes of the account over the ones
// of its class of service, over the ones of its domain.
func (p *Provisioner) accountView(acct *Entry, expand bool) (shownAttrs, error) {
	own := p.attrsOf(acct)
	if !expand {
		return own, nil
	}

	domain, err := p.accountDomain(acct)
	if err != nil {
		return nil, err
	}

	var fromDomain shownAttrs

	if domain != nil {
		dview, err := p.domainView(domain, true)
		if err != nil {
			return nil, err
		}

		fromDomain = p.inherited(flagAccountCosDomainInherited, dview)
	}

	cos, err := p.cosOfAccount(acct, domain)
	if err != nil {
		return nil, err
	}

	var fromCOS shownAttrs

	if cos != nil {
		fromCOS = p.inherited(flagAccountInherited, p.attrsOf(cos))
		if id := cos.First(attrZimbraID); id != "" && !acct.Has(attrCOSID) {
			// Account.setAccountDefaults: the account shows the class of service it uses.
			fromCOS[strings.ToLower(attrCOSID)] = shown{attrCOSID, []string{id}}
		}
	}

	return fromDomain.over(fromCOS).over(own), nil
}

// accountDomain is the domain of an account, nil when there is none.
func (p *Provisioner) accountDomain(acct *Entry) (*Entry, error) {
	name := p.accountName(acct)

	_, domain, ok := strings.Cut(name, "@")
	if !ok {
		return nil, nil //nolint:nilnil // no domain part, no domain
	}

	return p.domain(domain)
}

// cosOfAccount is Provisioning.getCOS(Account): the class of service the account
// names, else the default one of its domain, else the one called "default"
// ("defaultExternal" for an external virtual account).
func (p *Provisioner) cosOfAccount(acct, domain *Entry) (*Entry, error) {
	external := acct.First("zimbraIsExternalVirtualAccount") == boolTrue

	domainCOS, name := attrDomainCOSID, defaultCOS
	if external {
		domainCOS, name = "zimbraDomainDefaultExternalUserCOSId", "defaultExternal"
	}

	ids := []string{acct.First(attrCOSID)}
	if domain != nil {
		ids = append(ids, domain.First(domainCOS))
	}

	for _, id := range ids {
		if id == "" {
			continue
		}

		if cos, err := p.cos(id); err != nil || cos != nil {
			return cos, err
		}
	}

	return p.cos(name)
}

// gasArgs are the arguments of gas.
type gasArgs struct {
	verbose, noDefaults bool
	service             string
}

// parseGasArgs reads "[-v] [-e] [service]".
func parseGasArgs(args []string) (gasArgs, error) {
	var a gasArgs

	for _, arg := range args {
		switch {
		case arg == flagVerbose:
			a.verbose = true
		case arg == flagExpandOff:
			a.noDefaults = true
		case a.service == "":
			a.service = arg
		default:
			return a, &UsageError{Msg: fmt.Sprintf("invalid arg: %s, already specified service: %s", arg, a.service)}
		}
	}

	if a.noDefaults && !a.verbose {
		return a, &UsageError{Msg: "-e is only valid with -v"}
	}

	return a, nil
}

// getAllServers is gas [-v] [-e] [service]: the names of the servers, sorted,
// of those that run the service when one is given; -v dumps them.
func (p *Provisioner) getAllServers(args []string, out io.Writer) error {
	a, err := parseGasArgs(args)
	if err != nil {
		return err
	}

	servers, err := p.serversOf(a.service)
	if err != nil {
		return err
	}

	for i := range servers {
		name := servers[i].First(attrCN)

		if !a.verbose {
			if _, err := fmt.Fprintln(out, name); err != nil {
				return err
			}

			continue
		}

		view, err := p.serverView(&servers[i], !a.noDefaults)
		if err != nil {
			return err
		}

		if err := p.dumpEntry(out, name, view, nil); err != nil {
			return err
		}
	}

	return nil
}

// serversOf is Provisioning.getAllServers(service): the servers that run the
// service ("" for all), sorted by name.
func (p *Provisioner) serversOf(service string) ([]Entry, error) {
	terms := []string{object("zimbraServer")}
	if service != "" {
		terms = append(terms, eq(attrServiceEnabled, service))
	}

	servers, err := p.Dir.Search(p.serversDN(), ScopeSub, filterAnd(terms...))
	if err != nil {
		return nil, failWith(err, "unable to list all servers")
	}

	slices.SortFunc(servers, func(a, b Entry) int { return strings.Compare(a.First(attrCN), b.First(attrCN)) })

	return servers, nil
}

// serverInt is Entry.getIntAttr of a server.
func (p *Provisioner) serverInt(server, cfg *Entry, name string, def int) int {
	v := p.serverValue(server, cfg, name)
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}

	return n
}

// getAllReverseProxyURLs is garpu: the nginx lookup URL of every mail client
// server that is a lookup target, each followed by a blank, and a newline.
func (p *Provisioner) getAllReverseProxyURLs(out io.Writer) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}

	// Provisioning.getAllMailClientServers: the servers of the service
	// webapp, then the mailbox ones.
	var servers []Entry

	for _, service := range []string{serviceWebapp, serviceMailbox} {
		part, err := p.serversOf(service)
		if err != nil {
			return err
		}

		servers = append(servers, part...)
	}

	var line strings.Builder

	for i := range servers {
		s := &servers[i]
		if p.serverValue(s, cfg, attrReverseProxyLookup) != boolTrue {
			continue
		}

		fmt.Fprintf(&line, "%s:%d%s ", p.serverValue(s, cfg, attrServiceHostname),
			p.serverInt(s, cfg, attrExtensionPort, defaultExtensionPort), reverseProxyPath)
	}

	_, err = fmt.Fprintln(out, line.String())

	return err
}

// getAllMemcachedServers is gamcs: host:port of every memcached server, each
// followed by a blank, and a newline.
func (p *Provisioner) getAllMemcachedServers(out io.Writer) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}

	servers, err := p.serversOf(serviceMemcached)
	if err != nil {
		return err
	}

	var line strings.Builder

	for i := range servers {
		s := &servers[i]
		fmt.Fprintf(&line, "%s:%d ", p.serverValue(s, cfg, attrServiceHostname),
			p.serverInt(s, cfg, attrMemcachedPort, defaultMemcachedPort))
	}

	_, err = fmt.Fprintln(out, line.String())

	return err
}
