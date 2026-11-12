// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import "strings"

// pcKV is one attribute of a zmprov "ms" command. A name that starts with +
// or - adds or removes a value of a multi-valued attribute.
type pcKV struct{ attr, val string }

// pcCmd is one "ms <host> attr value ..." command of zmproxyconfig, applied
// as a whole or not at all, as zmprov applied it.
type pcCmd []pcKV

const (
	pcSSLRemain = "REMAIN"

	pcModeHTTP  = "http"
	pcModeHTTPS = "https"
	pcModeBoth  = "both"

	pcAttrServiceEnabled = "zimbraServiceEnabled"
	pcAttrSSLToUpstream  = "zimbraReverseProxySSLToUpstreamEnabled"
	pcAttrLookupTarget   = "zimbraReverseProxyLookupTarget"
	pcAttrMailEnabled    = "zimbraReverseProxyMailEnabled"
	pcAttrHTTPEnabled    = "zimbraReverseProxyHttpEnabled"
	pcAttrMailMode       = "zimbraMailMode"
	pcAttrProxyMailMode  = "zimbraReverseProxyMailMode"
	pcAttrReferMode      = "zimbraMailReferMode"
	pcAttrMailPort       = "zimbraMailPort"
	pcAttrImapCleartext  = "zimbraImapCleartextLoginEnabled"
	pcAttrPop3Cleartext  = "zimbraPop3CleartextLoginEnabled"
	pcAttrAdminEnabled   = "zimbraReverseProxyAdminEnabled"
	pcAttrAdminPort      = "zimbraAdminPort"
	pcAttrAdminProxyPort = "zimbraAdminProxyPort"
	pcServiceProxy       = "proxy"
	pcServiceMailbox     = "mailbox"
	pcAttrMailSSLPort    = "zimbraMailSSLPort"
	pcAttrImapProxy      = "zimbraImapProxyBindPort"
	pcAttrImapSSLProxy   = "zimbraImapSSLProxyBindPort"
	pcAttrPop3Proxy      = "zimbraPop3ProxyBindPort"
	pcAttrPop3SSLProxy   = "zimbraPop3SSLProxyBindPort"
)

// pcPorts are the port lists of one run, split.
type pcPorts struct{ imapL, popL, webL, adminL []string }

// pcSplit is Perl's split /:/, v, n: fewer fields than n leave the rest empty
// (the Perl script then sent misaligned arguments, which zmprov rejects; an
// empty port is rejected here the same way).
func pcSplit(v string, n int) []string {
	parts := strings.SplitN(v, ":", n)
	for len(parts) < n {
		parts = append(parts, "")
	}

	return parts
}

// pcPortsFor are the lists of a run: the option or the default of enable
// (zmproxyconfig 27-34) or disable.
func pcPortsFor(o *ProxyConfigOptions, enable bool) pcPorts {
	imap, pop, web, admin := "143:7143:993:7993", "110:7110:995:7995", "80:0:443:0", "7071:9071"
	if enable {
		imap, pop, web = "7143:143:7993:993", "7110:110:7995:995", "8080:80:8443:443"
	}

	pick := func(given, def string) string {
		if given != "" {
			return given
		}

		return def
	}

	return pcPorts{
		imapL:  pcSplit(pick(o.ImapPorts, imap), 4),
		popL:   pcSplit(pick(o.PopPorts, pop), 4),
		webL:   pcSplit(pick(o.WebPorts, web), 4),
		adminL: pcSplit(pick(o.AdminPorts, admin), 2),
	}
}

func pcPair(attr, val string) pcCmd { return pcCmd{{attr, val}} }

func pcJoin(parts ...pcCmd) pcCmd {
	var out pcCmd

	for _, p := range parts {
		out = append(out, p...)
	}

	return out
}

// mail is the eight IMAP and POP3 port attributes, in the script's order.
func (p *pcPorts) mail() pcCmd {
	return pcCmd{
		{srvAttrImapBind, p.imapL[0]}, {pcAttrImapProxy, p.imapL[1]},
		{srvAttrImapSSL, p.imapL[2]}, {pcAttrImapSSLProxy, p.imapL[3]},
		{srvAttrPop3Bind, p.popL[0]}, {pcAttrPop3Proxy, p.popL[1]},
		{srvAttrPop3SSL, p.popL[2]}, {pcAttrPop3SSLProxy, p.popL[3]},
	}
}

// web is the four HTTP port attributes.
func (p *pcPorts) web() pcCmd {
	return pcCmd{
		{pcAttrMailPort, p.webL[0]}, {srvAttrMailProxy, p.webL[1]},
		{pcAttrMailSSLPort, p.webL[2]}, {srvAttrMailSSLProxy, p.webL[3]},
	}
}

// admin is the admin console proxy switch and ports. The store branch of -e
// -c sent n2 as the store port (zmproxyconfig 326); quirk keeps that.
func (p *pcPorts) admin(on, quirk bool) pcCmd {
	enabled, storePort := srvFalseUpper, p.adminL[0]
	if on {
		enabled = srvTrueUpper
	}

	if quirk && !on {
		storePort = p.adminL[1]
	}

	return pcCmd{{pcAttrAdminEnabled, enabled}, {pcAttrAdminPort, storePort}, {pcAttrAdminProxyPort, p.adminL[1]}}
}

// pcPlanner turns the options into the commands zmproxyconfig sent to zmprov.
type pcPlanner struct {
	o          *ProxyConfigOptions
	pp         pcPorts
	app, proxy bool
	// enabled is the LDAP zimbraServiceEnabled state when the run began: the
	// script's zmprov commands ran only when it ended.
	enabled func(pkg string) (bool, error)
	// ssl is zimbraReverseProxySSLToUpstreamEnabled after -u/-U (REMAIN when
	// neither was given and LDAP has no value); mode is the current store
	// zimbraMailMode.
	ssl, mode string
}

// plan is the body of zmproxyconfig 216-534 (enable 216-381, disable 383-534).
func (p *pcPlanner) plan() ([]pcCmd, error) {
	if p.o.Enable {
		p.pp = pcPortsFor(p.o, true)

		return p.enable()
	}

	if p.o.Disable {
		p.pp = pcPortsFor(p.o, false)

		return p.disable()
	}

	return nil, nil
}

// modes is getMailMode (zmproxyconfig 781-812): the proxy and the store mail
// mode. With SSL to the upstream the store is https or both and the proxy
// defaults to https; without it the store is http.
func (p *pcPlanner) modes() (proxyMode, storeMode string) {
	proxyMode = pcModeHTTP
	if p.o.Mode != "" {
		proxyMode = p.o.Mode
	}

	if p.ssl != srvTrueUpper {
		return proxyMode, pcModeHTTP
	}

	if p.o.Mode == "" {
		proxyMode = pcModeHTTPS
	}

	if p.mode == pcModeHTTP || p.mode == pcModeBoth {
		return proxyMode, pcModeBoth
	}

	return proxyMode, pcModeHTTPS
}

func (p *pcPlanner) sslCmd() []pcCmd {
	if p.ssl == pcSSLRemain {
		return nil
	}

	return []pcCmd{pcPair(pcAttrSSLToUpstream, p.ssl)}
}

// proxyServiceCmd adds the proxy service when the server does not list it.
func (p *pcPlanner) proxyServiceCmd() ([]pcCmd, error) {
	on, err := p.enabled(srvPkgProxy)
	if err != nil || on {
		return nil, err
	}

	return []pcCmd{pcPair("+"+pcAttrServiceEnabled, pcServiceProxy)}, nil
}

// storeActive is isEnabled(appserver) || (isInstalled(appserver) && -o).
func (p *pcPlanner) storeActive() (bool, error) {
	on, err := p.enabled(PkgAppserver)
	if err != nil || on {
		return on, err
	}

	return p.app && p.o.Override, nil
}

func (p *pcPlanner) adminCmds(quirk bool) []pcCmd {
	switch {
	case p.o.AdminOn:
		return []pcCmd{p.pp.admin(true, quirk)}
	case p.o.AdminOff:
		return []pcCmd{p.pp.admin(false, quirk)}
	default:
		return nil
	}
}

func (p *pcPlanner) enable() ([]pcCmd, error) {
	if p.app && p.proxy {
		return p.enableBoth()
	}

	var cmds []pcCmd

	store, err := p.storeActive()
	if err != nil {
		return nil, err
	}

	if store {
		cmds = append(cmds, p.enableStore()...)
	}

	if p.proxy {
		more, err := p.enableProxy()
		if err != nil {
			return nil, err
		}

		cmds = append(cmds, more...)
	}

	return cmds, nil
}

// enableBoth is zmproxyconfig 239-290: store and proxy on one host.
func (p *pcPlanner) enableBoth() ([]pcCmd, error) {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, pcJoin(p.pp.mail(),
			pcPair(pcAttrImapCleartext, srvTrueUpper), pcPair(pcAttrPop3Cleartext, srvTrueUpper),
			pcPair(pcAttrLookupTarget, srvTrueUpper), pcPair(pcAttrMailEnabled, srvTrueUpper)))
	}

	if p.o.Web {
		proxyMode, storeMode := p.modes()

		cmds = append(cmds, pcJoin(pcPair(pcAttrReferMode, "reverse-proxied"), p.pp.web(),
			pcPair(pcAttrMailMode, storeMode), pcPair(pcAttrProxyMailMode, proxyMode),
			pcPair(pcAttrLookupTarget, srvTrueUpper), pcPair(pcAttrHTTPEnabled, srvTrueUpper)))
		cmds = append(cmds, p.adminCmds(false)...)
	}

	svc, err := p.proxyServiceCmd()
	if err != nil {
		return nil, err
	}

	return append(append(cmds, p.sslCmd()...), svc...), nil
}

// enableStore is zmproxyconfig 291-333: the store of a host without a proxy
// (or with an enabled one on another host).
func (p *pcPlanner) enableStore() []pcCmd {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, pcJoin(p.pp.mail(), pcPair(pcAttrImapCleartext, srvTrueUpper),
			pcPair(pcAttrLookupTarget, srvTrueUpper), pcPair(pcAttrPop3Cleartext, srvTrueUpper)))
	}

	if p.o.Web {
		_, storeMode := p.modes()

		cmds = append(cmds, pcJoin(pcPair(pcAttrReferMode, "reverse-proxied"), p.pp.web(),
			pcPair(pcAttrLookupTarget, srvTrueUpper), pcPair(pcAttrMailMode, storeMode)))
		cmds = append(cmds, p.adminCmds(true)...)
	}

	return append(cmds, p.sslCmd()...)
}

// enableProxy is zmproxyconfig 334-381: the proxy package.
func (p *pcPlanner) enableProxy() ([]pcCmd, error) {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, pcJoin(p.pp.mail(), pcPair(pcAttrMailEnabled, srvTrueUpper)))
	}

	if p.o.Web {
		proxyMode, _ := p.modes()

		cmds = append(cmds, pcJoin(p.pp.web(), pcPair(pcAttrProxyMailMode, proxyMode),
			pcPair(pcAttrHTTPEnabled, srvTrueUpper)))
		cmds = append(cmds, p.adminCmds(false)...)
	}

	svc, err := p.proxyServiceCmd()
	if err != nil {
		return nil, err
	}

	return append(append(cmds, p.sslCmd()...), svc...), nil
}

func (p *pcPlanner) disable() ([]pcCmd, error) {
	if p.app && p.proxy {
		return p.disableBoth(), nil
	}

	var cmds []pcCmd

	store, err := p.storeActive()
	if err != nil {
		return nil, err
	}

	if store {
		cmds = append(cmds, p.disableStore()...)
	}

	if p.proxy {
		cmds = append(cmds, p.disableProxy()...)
	}

	return cmds, nil
}

// cleartextOff is -s.
func (p *pcPlanner) cleartextOff() []pcCmd {
	if !p.o.ClearTextOff {
		return nil
	}

	return []pcCmd{{{pcAttrImapCleartext, srvFalseUpper}, {pcAttrPop3Cleartext, srvFalseUpper}}}
}

func pcModeOr(given string) string {
	if given != "" {
		return given
	}

	return pcModeHTTP
}

// disableBoth is zmproxyconfig 403-441. The script then read
// zimbraReverseProxyHttpEnabled and zimbraReverseProxyMailEnabled back to
// remove the proxy service and clear the lookup target once both were FALSE
// (442-451), but its zmprov line redirected stdout to /dev/null, so the values
// never arrived and that code never ran: it is not ported.
func (p *pcPlanner) disableBoth() []pcCmd {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, pcJoin(p.pp.mail(), pcPair(pcAttrMailEnabled, srvFalseUpper)))
		cmds = append(cmds, p.cleartextOff()...)
	}

	if p.o.Web {
		mode := pcModeOr(p.o.Mode)

		cmds = append(cmds, pcJoin(pcPair(pcAttrReferMode, "wronghost"), p.pp.web(),
			pcPair(pcAttrMailMode, mode), pcPair(pcAttrProxyMailMode, mode), pcPair(pcAttrHTTPEnabled, srvFalseUpper)),
			p.pp.admin(false, false))
	}

	return cmds
}

// disableStore is zmproxyconfig 453-493.
func (p *pcPlanner) disableStore() []pcCmd {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, p.pp.mail())
		cmds = append(cmds, p.cleartextOff()...)
	}

	if p.o.Web {
		cmds = append(cmds, pcJoin(pcPair(pcAttrReferMode, "wronghost"), p.pp.web(),
			pcPair(pcAttrMailMode, pcModeOr(p.o.Mode))), p.pp.admin(false, false))
	}

	if p.o.LookupTargetOff {
		cmds = append(cmds, pcPair(pcAttrLookupTarget, srvFalseUpper))
	}

	return cmds
}

// disableProxy is zmproxyconfig 494-534; the read-back at its end (526-533)
// never ran either (see disableBoth).
func (p *pcPlanner) disableProxy() []pcCmd {
	var cmds []pcCmd

	if p.o.Mail {
		cmds = append(cmds, pcJoin(p.pp.mail(), pcPair(pcAttrMailEnabled, srvFalseUpper)))
	}

	if p.o.Web {
		cmds = append(cmds, pcJoin(p.pp.web(), pcPair(pcAttrProxyMailMode, pcModeOr(p.o.Mode)),
			pcPair(pcAttrHTTPEnabled, srvFalseUpper)), p.pp.admin(false, false))
	}

	return cmds
}

// pcFullReset are the global config attributes of -f (zmproxyconfig
// 192-214): the zimbraReverseProxy* attributes of setProxyBits (the
// srvProxyBitDefaults table, the same values, without the backslashes the
// script's LDAP filters carried and zmprov's line parser removed) but the two
// switches it sets on the server.
var pcFullReset = func() pcCmd {
	var c pcCmd

	for _, kv := range srvProxyBitDefaults {
		switch kv[0] {
		case pcAttrHTTPEnabled, pcAttrMailEnabled:
		default:
			if strings.HasPrefix(kv[0], "zimbraReverseProxy") {
				c = append(c, pcKV{kv[0], kv[1]})
			}
		}
	}

	return c
}()
