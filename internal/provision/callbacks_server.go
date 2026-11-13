// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"net"
	"slices"
	"strings"
)

// The attributes of the port, mail mode and bind address callbacks.
const (
	attrAdminBindAddress = "zimbraAdminBindAddress"
	attrAdminLocalBind   = "zimbraAdminLocalBind"
	attrMailMode         = "zimbraMailMode"
	attrMailBindAddress  = "zimbraMailBindAddress"
	attrMailSSLBind      = "zimbraMailSSLBindAddress"
	attrMailLocalBind    = "zimbraMailLocalBind"
	attrMailPort         = "zimbraMailPort"

	boolTrue  = "TRUE"
	boolFalse = "FALSE"

	// lcMailServicePort is the localconfig key the MailPort callback keeps in
	// step with zimbraMailPort of the local server.
	lcMailServicePort = "zimbra_mail_service_port"

	modeHTTPS = "https"

	callbackCheckPort = "CheckPortConflict"
)

// proxyPortAttrs and nonProxyPortAttrs are the port attributes of
// CheckPortConflict; a proxy attribute does not conflict with a non-proxy one
// on a server that does not run both services.
var (
	proxyPortAttrs = []string{
		"zimbraAdminProxyPort", "zimbraImapProxyBindPort", "zimbraImapSSLProxyBindPort",
		"zimbraPop3ProxyBindPort", "zimbraPop3SSLProxyBindPort", "zimbraMailProxyPort",
		"zimbraMailSSLProxyPort", "zimbraMailSSLProxyClientCertPort", "carbonioAdminProxyPort",
	}
	nonProxyPortAttrs = []string{
		"zimbraAdminPort", "zimbraImapBindPort", "zimbraImapSSLBindPort", "zimbraPop3BindPort",
		"zimbraPop3SSLBindPort", attrMailPort, "zimbraMailSSLPort", "zimbraMailSSLClientCertPort",
		attrLmtpPort, "zimbraSmtpPort", "zimbraRemoteManagementPort", "zimbraMessageChannelPort",
		"zimbraExtensionBindPort", "zimbraMtaAuthPort",
	}
	portAttrs = slices.Sorted(slices.Values(slices.Concat(proxyPortAttrs, nonProxyPortAttrs)))
)

// portConflict is CheckPortConflict.conflict: whether the port is already used
// by another attribute of the server. A server that runs the mailbox service
// but not the proxy (or the reverse) may bind a proxy and a non-proxy port to
// the same number.
func portConflict(server *Entry, ports map[string]string, port, attr string) bool {
	if port == "" || port == "0" {
		return false
	}

	other, used := ports[port]
	if !used {
		return false
	}

	if server != nil && (!hasService(server, "mailbox") || !hasService(server, "proxy")) &&
		slices.Contains(proxyPortAttrs, attr) != slices.Contains(proxyPortAttrs, other) {
		return false
	}

	return true
}

func portConflictError(server *Entry, port, attr, other string) error {
	name := ""
	if server != nil {
		name = server.First(attrCN)
	}

	return invalidRequest("port %s conflict between %s and %s on server %s", port, attr, other, name)
}

// checkPortConflict is the preModify of CheckPortConflict and MailPort: no two
// port attributes of a server may have the same number. It runs once per
// request, for the server, the global config or a server being created.
func checkPortConflict(c *callbackContext, _ string, _ []string, mods Mods) error {
	if c.once(callbackCheckPort) {
		return nil
	}

	switch {
	case c.create:
		return c.p.checkServerPorts(nil, mods)
	case c.kind == kindServer:
		return c.p.checkServerPorts(c.entry, mods)
	case c.kind == kindConfig:
		return c.p.checkConfigPorts(mods)
	default:
		return nil
	}
}

// portMap is the BiMap of the Java code, port to attribute; a port bound again
// drops the attribute it had.
type portMap map[string]string

func (m portMap) bind(port, attr string) {
	for k, v := range m {
		if v == attr {
			delete(m, k)
		}
	}

	m[port] = attr
}

// portOf is the port bound to the attribute, "" for none.
func (m portMap) portOf(attr string) string {
	for port, a := range m {
		if a == attr {
			return port
		}
	}

	return ""
}

// splitPortAttrs divides the port attributes into those the modification
// touches and those it does not.
func splitPortAttrs(mods Mods) (modified, untouched []string) {
	for _, attr := range portAttrs {
		if _, ok := mods[attr]; ok {
			modified = append(modified, attr)
		} else {
			untouched = append(untouched, attr)
		}
	}

	return modified, untouched
}

// checkServerPorts is CheckPortConflict.checkServer: the ports the modification
// sets against the ports the server has; server is nil for a server being
// created.
func (p *Provisioner) checkServerPorts(server *Entry, mods Mods) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}

	defaults := p.serverDefaults(cfg)
	ports := map[string]string{}
	modified, untouched := splitPortAttrs(mods)

	if server != nil {
		for _, attr := range untouched {
			ports[p.serverValue(server, cfg, attr)] = attr
		}
	}

	for _, attr := range modified {
		value, set, err := mods.singleValue(attr)
		if err != nil {
			return err
		}

		if !set {
			value = defaults[strings.ToLower(attr)]
		}

		if portConflict(server, ports, value, attr) {
			return portConflictError(server, value, attr, ports[value])
		}

		ports[value] = attr
	}

	return nil
}

// checkConfigPorts is CheckPortConflict.checkConfig: the ports the global
// config gets by default are checked against each other, then against the
// ports of every server that inherits them.
func (p *Provisioner) checkConfigPorts(mods Mods) error {
	cfg, err := p.config()
	if err != nil {
		return err
	}

	newDefaults := portMap{}
	modified, untouched := splitPortAttrs(mods)

	for _, attr := range untouched {
		newDefaults.bind(cfg.First(attr), attr)
	}

	for _, attr := range modified {
		value, set, err := mods.singleValue(attr)
		if err != nil {
			return err
		}

		if !set {
			value = ""
		}

		if portConflict(nil, newDefaults, value, attr) {
			return portConflictError(nil, value, attr, newDefaults[value])
		}

		newDefaults.bind(value, attr)
	}

	return p.checkServersWithNewDefaults(newDefaults, mods)
}

// checkServersWithNewDefaults checks every server with the ports the global
// config is going to give it; a conflict only counts when the modification
// touches one of the two attributes.
func (p *Provisioner) checkServersWithNewDefaults(newDefaults portMap, mods Mods) error {
	servers, err := p.servers()
	if err != nil {
		return err
	}

	for i := range servers {
		if err := checkServerWithDefaults(&servers[i], newDefaults, mods); err != nil {
			return err
		}
	}

	return nil
}

// checkServerWithDefaults checks the ports of one server, those it has and
// those it would inherit from newDefaults.
func checkServerWithDefaults(server *Entry, newDefaults portMap, mods Mods) error {
	ports := map[string]string{}

	for _, attr := range portAttrs {
		value := server.First(attr)
		if !server.Has(attr) {
			value = newDefaults.portOf(attr)
		}

		if !portConflict(server, ports, value, attr) {
			ports[value] = attr

			continue
		}

		other := ports[value]
		if mods.has(attr) || mods.has(other) {
			return portConflictError(server, value, attr, other)
		}
	}

	return nil
}

// mailPortPost is the postModify of MailPort: the local server's mail port is
// the one the service-control scripts read from localconfig.
func mailPortPost(c *callbackContext, attr string) error {
	local, err := c.p.localServer()
	if err != nil || local == nil || c.kind != kindServer || local.DN != c.entry.DN {
		return err
	}

	cfg, err := c.p.config()
	if err != nil {
		return err
	}

	if port := c.p.serverValue(local, cfg, attr); port != "" {
		return c.p.SetLocalConfig(lcMailServicePort, port)
	}

	return nil
}

// mailHostPoolPre is the preModify of MailHostPool: the pool holds the ids of
// servers that run the mail client service.
func mailHostPoolPre(c *callbackContext, attr string, _ []string, mods Mods) error {
	kind, pool := mods.multiValue(attr)
	if kind != multiAdding && kind != multiReplacing {
		return nil
	}

	for _, id := range pool {
		if id == "" {
			continue
		}

		server, err := c.p.serverBy(attrZimbraID, id)
		if err != nil {
			return err
		}

		if server == nil {
			return invalidRequest("specified %s does not correspond to a valid server: %s", attr, id)
		}

		if !hasService(server, serviceWebapp) {
			return invalidRequest("specified %s is not a mailclient server with service webapp enabled: %s", attrMailHost, id)
		}
	}

	return nil
}

// mailHostPre is the preModify of MailHost: the host is the service hostname of
// a mail client server, and the mail transport follows it.
func mailHostPre(c *callbackContext, attr string, values []string, mods Mods) error {
	if len(values) != 1 || values[0] == "" || len(mods["-"+attr]) > 0 {
		return nil // unsetting
	}

	host := values[0]

	if t := mods.first(attrMailTransport); t != "" {
		return invalidRequest("setting both %s and %s in the same request is not allowed", attrMailHost, attrMailTransport)
	}

	server, err := c.p.serverBy(attrServiceHostname, host)
	if err != nil {
		return err
	}

	if server == nil {
		return invalidRequest("specified %s does not correspond to a valid server service hostname: %s", attrMailHost, host)
	}

	if !hasService(server, serviceWebapp) {
		return invalidRequest("specified %s does not correspond to a mailclient server with service webapp enabled: %s",
			attrMailHost, host)
	}

	cfg, err := c.p.config()
	if err != nil {
		return err
	}

	if !c.create && c.entry != nil {
		if err := c.p.checkOldMailTransport(c.entry, cfg); err != nil {
			return err
		}
	}

	mods[attrMailTransport] = []string{c.p.mailTransport(server, cfg)}

	return nil
}

// checkOldMailTransport refuses to move a mailbox whose current transport is not
// the one its current host implies (bug 18419).
func (p *Provisioner) checkOldMailTransport(entry, cfg *Entry) error {
	oldHost := entry.First(attrMailHost)
	if oldHost == "" {
		return nil
	}

	oldServer, err := p.serverBy(attrServiceHostname, oldHost)
	if err != nil || oldServer == nil {
		return err
	}

	current := entry.First(attrMailTransport)
	if mailTransportMatches(oldServer, current) {
		return nil
	}

	return invalidRequest("current value of %s does not match %s, "+
		"computed mail transport from current %s=%s, current %s=%s",
		attrMailHost, attrMailTransport, attrMailHost, p.mailTransport(oldServer, cfg), attrMailTransport, current)
}

// mailTransportMatches is Server.mailTransportMatches.
func mailTransportMatches(server *Entry, transport string) bool {
	if transport == "" {
		return true
	}

	name := server.First(attrServiceHostname)
	parts := strings.Split(transport, ":")

	return name != "" && len(parts) == 3 && strings.EqualFold(parts[0], "lmtp") && parts[1] == name
}

// first is the value of the attribute set with no operator, "" when it is not.
func (m Mods) first(name string) string {
	if v := m[name]; len(v) > 0 {
		return v[0]
	}

	return ""
}

// localBindPost is the postModify of LocalBind: whether a service listens on
// the loopback only is derived from its bind address.
func localBindPost(c *callbackContext, attr string) error {
	if attr == attrAdminBindAddress {
		return c.adminLocalBind()
	}

	if attr != attrMailMode && attr != attrMailBindAddress && attr != attrMailSSLBind {
		return nil
	}

	switch c.kind {
	case kindServer:
		return c.mailLocalBind(c.entry)
	case kindConfig:
		servers, err := c.p.servers()
		if err != nil {
			return err
		}

		for i := range servers {
			if err := c.mailLocalBind(&servers[i]); err != nil {
				return err
			}
		}
	}

	return nil
}

// adminLocalBind sets zimbraAdminLocalBind of the server from its admin bind
// address.
func (c *callbackContext) adminLocalBind() error {
	if c.kind != kindServer {
		return nil
	}

	cfg, err := c.p.config()
	if err != nil {
		return err
	}

	local := c.p.isLocalBind(c.p.serverValue(c.entry, cfg, attrAdminBindAddress))

	return c.p.setBool(c.entry.DN, attrAdminLocalBind, local)
}

// mailLocalBind sets zimbraMailLocalBind of the server: a server in https mode
// is always reachable, otherwise it is when its mail bind address is not the
// loopback.
func (c *callbackContext) mailLocalBind(server *Entry) error {
	cfg, err := c.p.config()
	if err != nil {
		return err
	}

	bind := true

	if c.p.serverValue(server, cfg, attrMailMode) != modeHTTPS {
		bind = c.p.isLocalBind(c.p.serverValue(server, cfg, attrMailBindAddress))
	}

	return c.p.setBool(server.DN, attrMailLocalBind, bind)
}

func (p *Provisioner) setBool(dn, attr string, v bool) error {
	value := boolFalse
	if v {
		value = boolTrue
	}

	return p.Dir.ModifyValues(dn, nil, nil, map[string][]string{attr: {value}})
}

// isLocalBind reports whether a bind address makes the service reachable from
// other hosts: false for no address, a wildcard or a loopback address, and for
// one that does not resolve (LocalBind.isLocalBind).
func (p *Provisioner) isLocalBind(addr string) bool {
	if addr == "" {
		return false
	}

	ip := net.ParseIP(addr)
	if ip == nil {
		ips, err := p.Resolve(addr)
		if err != nil || len(ips) == 0 {
			return false
		}

		ip = ips[0]
	}

	return !ip.IsUnspecified() && !ip.IsLoopback()
}
