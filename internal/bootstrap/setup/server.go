// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// Packages the server steps name beyond the ones facts.go exports.
const (
	srvPkgDirectory = "carbonio-directory-server"
	srvPkgProxy     = "carbonio-proxy"
	srvPkgMemcached = "carbonio-memcached"

	srvSkinLogoURL = "https://www.zextras.com"

	srvAttrImapBind     = "zimbraImapBindPort"
	srvAttrPop3Bind     = "zimbraPop3BindPort"
	srvAttrMailProxy    = "zimbraMailProxyPort"
	srvSvcAntivirus     = "antivirus"
	srvAttrImapSSL      = "zimbraImapSSLBindPort"
	srvAttrPop3SSL      = "zimbraPop3SSLBindPort"
	srvAttrMailSSLProxy = "zimbraMailSSLProxyPort"
	srvSvcAntispam      = "antispam"
	srvSvcOpendkim      = "opendkim"
	srvSvcAmavis        = "amavis"
	srvSvcDiscover      = "service-discover"
)

// srvHost is the host name of the server entry (setup.pl $config{HOSTNAME}).
func srvHost(e *Env) string { return e.Cfg.Host.Hostname }

// srvIsLDAPReplica is setup.pl $ldapReplica: a directory server that
// replicates from another node. Deviation: setup.pl set it only in the
// interactive host-name dialog, never from a -c file; here it follows
// Config.LDAP.ReplicationType.
func srvIsLDAPReplica(c *bootstrap.Config) bool {
	return c.HasRole(bootstrap.RoleDirectoryServer) &&
		(c.LDAP.ReplicationType == bootstrap.ReplicationReplica || c.LDAP.ReplicationType == bootstrap.ReplicationMMR)
}

// srvSameSet reports whether a and b hold the same values, in any order.
func srvSameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)

	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

func srvEntryMatches(ent srvEntry, want map[string]string) bool {
	for k, v := range want {
		if ent.first(k) != v {
			return false
		}
	}

	return true
}

// srvServerMatches reports whether the server entry of host stores every
// attribute as given.
func srvServerMatches(e *Env, host string, want map[string]string) (bool, error) {
	var ok bool

	err := srvRead(e, func(d srvDirectory) error {
		ent, found, err := d.Server(host)
		if err != nil || !found {
			return err
		}

		ok = srvEntryMatches(ent, want)

		return nil
	})

	return ok, err
}

// srvCOSMatches is srvServerMatches for a class of service.
func srvCOSMatches(e *Env, cos string, want map[string]string) (bool, error) {
	var ok bool

	err := srvRead(e, func(d srvDirectory) error {
		ent, found, err := d.COS(cos)
		if err != nil || !found {
			return err
		}

		ok = srvEntryMatches(ent, want)

		return nil
	})

	return ok, err
}

// srvGlobalHas reports whether the global config stores every attribute as
// given.
func srvGlobalHas(e *Env, want map[string]string) (bool, error) {
	var ok bool

	err := srvRead(e, func(d srvDirectory) error {
		g, err := d.Global()
		if err != nil {
			return err
		}

		ok = srvEntryMatches(g, want)

		return nil
	})

	return ok, err
}

// srvMS appends "ms host k v ..." to p.
func srvMS(p *Prov, host string, kv ...string) {
	p.Add(append([]string{"ms", host}, kv...)...)
}

func srvPairs(kv []string) map[string]string {
	m := make(map[string]string, len(kv)/2)

	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}

	return m
}

func srvItoa(n int) string { return strconv.Itoa(n) }

// srvZmiptool runs libexec/zmiptool as zextras; setup.pl ignored its result.
func srvZmiptool(e *Env) {
	if err := e.Zextras(e.Paths.Path("libexec", "zmiptool")); err != nil {
		e.Log.Detail("zmiptool: %v", err)
	}
}

// srvWarn reports a warning of the step.
func srvWarn(e *Env, format string, args ...any) {
	e.Log.Warn(format, args...)
}

// StepRemoveUnusedWebapps ports setup.pl removeUnusedWebapps /
// defineInstallWebapps / getInstalledWebapps (263-269, 461-486, 2225-2228):
// the "service" web application is enabled on an appserver node. Facts
// already derive it, so the step only restates it before the later steps.
//
// When: isEnabled(carbonio-appserver). INSTALL_WEBAPPS, which nothing else in
// setup.pl reads, is not kept.
func StepRemoveUnusedWebapps() Step {
	return Step{
		Title: "Determine installed web applications",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) },
		Apply: func(e *Env) error {
			e.Facts.ServiceWebapp = true

			return nil
		},
	}
}

// StepConfigInitCore ports setup.pl configInitCore (5658-5665), which does
// nothing but record its stage.
//
// Name: configInitCore. When: always.
func StepConfigInitCore() Step {
	return Step{
		Name:  "configInitCore",
		Title: "Initialize core config",
		Apply: func(*Env) error { return nil },
	}
}

func srvCreateServerEntry(replica bool) Step {
	return Step{
		Name:     "configCreateServerEntry",
		Title:    "Create server entry",
		When:     func(e *Env) bool { return srvIsLDAPReplica(e.Cfg) == replica },
		Optional: true,
		Done: func(e *Env) (bool, error) {
			want := map[string]string{}
			if m := e.Cfg.Host.IPMode; m != "" {
				want["zimbraIPMode"] = m
			}

			return srvServerMatches(e, srvHost(e), want)
		},
		Apply: func(e *Env) error {
			host := srvHost(e)

			var found bool

			err := srvRead(e, func(d srvDirectory) error {
				var err error

				_, found, err = d.Server(host)

				return err
			})
			if err != nil {
				return err
			}

			var p Prov

			if !found {
				p.Add("cs", host)
			}

			if m := e.Cfg.Host.IPMode; m != "" {
				srvMS(&p, host, "zimbraIPMode", m)
			}

			err = e.RunProv(&p)

			srvZmiptool(e)

			return err
		},
	}
}

// StepConfigCreateServerEntryReplica ports setup.pl configCreateServerEntry
// (5127-5148) as applyConfig runs it on an LDAP replica, before
// configSetupLdap: create the server entry when LDAP has none and set its IP
// mode, then libexec/zmiptool.
//
// Name: configCreateServerEntry. When: $ldapReplica. Optional, as setup.pl
// printed "failed." and carried on.
func StepConfigCreateServerEntryReplica() Step { return srvCreateServerEntry(true) }

// StepConfigCreateServerEntry is StepConfigCreateServerEntryReplica for every
// other node, run after configSetupLdap. When: !$ldapReplica.
func StepConfigCreateServerEntry() Step { return srvCreateServerEntry(false) }

// srvPortSet are the service ports with the setup.pl defaults applied.
type srvPortSet struct {
	imap, imaps, pop, pops, http, https                               int
	imapProxy, imapsProxy, popProxy, popsProxy, httpProxy, httpsProxy int
}

// srvPorts are the ports configSetServicePorts (5212-5249) leaves in %config
// on an appserver node, and the configured ones elsewhere: a proxy port that
// equals its backend port moves to the standard port when that proxy is off.
//
// Deviation: setup.pl moved POPSSLPORT (not POPSSLPROXYPORT) from 7995 to 995
// when the mail proxy is off, which bound the backend to the standard port
// and left the proxy port on 7995; the proxy port is moved here.
func srvPorts(e *Env) srvPortSet {
	s, p := e.Cfg.Store.Ports, e.Cfg.Proxy.Ports
	ps := srvPortSet{
		imap: s.IMAP, imaps: s.IMAPS, pop: s.POP3, pops: s.POP3S, http: s.HTTP, https: s.HTTPS,
		imapProxy: p.IMAP, imapsProxy: p.IMAPS, popProxy: p.POP3, popsProxy: p.POP3S,
		httpProxy: p.HTTP, httpsProxy: p.HTTPS,
	}

	if !e.Facts.IsEnabled(PkgAppserver) {
		return ps
	}

	move := func(port int, proxy *int, backend, standard int) {
		if port == backend && *proxy == port {
			*proxy = standard
		}
	}

	if !e.Cfg.Proxy.MailEnabled {
		move(ps.imap, &ps.imapProxy, 7143, 143)
		move(ps.imaps, &ps.imapsProxy, 7993, 993)
		move(ps.pop, &ps.popProxy, 7110, 110)
		move(ps.pops, &ps.popsProxy, 7995, 995)
	}

	if !e.Cfg.Proxy.WebEnabled {
		move(ps.http, &ps.httpProxy, 8080, 80)
		move(ps.https, &ps.httpsProxy, 8443, 443)
	}

	return ps
}

// srvServicePortAttrs are the server attributes configSetServicePorts sets,
// one list per setup.pl setLdapServerConfig call.
func srvServicePortAttrs(e *Env) [][]string {
	ps := srvPorts(e)

	return [][]string{
		{
			srvAttrImapBind, srvItoa(ps.imap), srvAttrImapSSL, srvItoa(ps.imaps),
			"zimbraImapProxyBindPort", srvItoa(ps.imapProxy), "zimbraImapSSLProxyBindPort", srvItoa(ps.imapsProxy),
		},
		{
			srvAttrPop3Bind, srvItoa(ps.pop), srvAttrPop3SSL, srvItoa(ps.pops),
			"zimbraPop3ProxyBindPort", srvItoa(ps.popProxy), "zimbraPop3SSLProxyBindPort", srvItoa(ps.popsProxy),
		},
		{
			"zimbraMailPort", srvItoa(ps.http), "zimbraMailSSLPort", srvItoa(ps.https),
			srvAttrMailProxy, srvItoa(ps.httpProxy), srvAttrMailSSLProxy, srvItoa(ps.httpsProxy),
			"zimbraMailMode", e.Cfg.Store.Mode,
		},
	}
}

// StepConfigSetServicePorts ports setup.pl configSetServicePorts
// (5212-5249): the IMAP, POP3 and mail bind and proxy ports and the mail mode
// on the server entry, and zimbra_mail_service_port in localconfig.
//
// Name: configSetServicePorts. When: isEnabled(carbonio-appserver).
func StepConfigSetServicePorts() Step {
	return Step{
		Name:  "configSetServicePorts",
		Title: "Set service ports",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) },
		Done: func(e *Env) (bool, error) {
			if !srvLCMatches(e, map[string]string{"zimbra_mail_service_port": srvItoa(srvPorts(e).http)}) {
				return false, nil
			}

			want := map[string]string{}

			for _, kv := range srvServicePortAttrs(e) {
				maps.Copy(want, srvPairs(kv))
			}

			return srvServerMatches(e, srvHost(e), want)
		},
		Apply: func(e *Env) error {
			var p Prov

			for _, kv := range srvServicePortAttrs(e) {
				srvMS(&p, srvHost(e), kv...)
			}

			if err := e.RunProv(&p); err != nil {
				return err
			}

			return e.SetLC(map[string]string{"zimbra_mail_service_port": srvItoa(srvPorts(e).http)})
		},
	}
}

// StepConfigSetKeyboardShortcutsPref ports setup.pl
// configSetKeyboardShortcutsPref (5251-5260): zimbraPrefUseKeyboardShortcuts
// on the default COS.
//
// Name: zimbraPrefUseKeyboardShortcuts. When: isEnabled(carbonio-appserver)
// && !$newinstall. Optional.
func StepConfigSetKeyboardShortcutsPref() Step {
	value := func(e *Env) string { return srvBoolUpper(e.Cfg.Store.KeyboardShortcuts) }

	return Step{
		Name:     "zimbraPrefUseKeyboardShortcuts",
		Title:    "Set keyboard shortcut preferences",
		When:     func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) && !e.Facts.NewInstall },
		Optional: true,
		Done: func(e *Env) (bool, error) {
			return srvCOSMatches(e, "default", map[string]string{"zimbraPrefUseKeyboardShortcuts": value(e)})
		},
		Apply: func(e *Env) error {
			return e.Prov("mc", "default", "zimbraPrefUseKeyboardShortcuts", value(e))
		},
	}
}

// srvStoreDefaultsNeedAdd reports whether the host must be appended to the
// global zimbraReverseProxyAvailableLookupTargets (setup.pl: when the
// attribute is set, always; when empty, if no server is a lookup target yet).
func srvStoreDefaultsNeedAdd(d srvDirectory, host string) (bool, error) {
	g, err := d.Global()
	if err != nil {
		return false, err
	}

	if cur := g.values("zimbraReverseProxyAvailableLookupTargets"); len(cur) > 0 {
		return !slices.Contains(cur, host), nil
	}

	// setup.pl treats a failed query as "none".
	names, err := d.ServerNames("(zimbraReverseProxyLookupTarget=TRUE)")
	if err != nil {
		return true, nil //nolint:nilerr // legacy: an unreadable count adds the host
	}

	return len(names) == 0, nil
}

// srvStoreServerAttrs are the server attributes of configSetStoreDefaults. An
// appserver node is always a lookup target ($config{zimbraReverseProxyLookupTarget}
// is TRUE from setDefaults).
func srvStoreServerAttrs(e *Env) map[string]string {
	return map[string]string{
		"zimbraReverseProxyLookupTarget": srvTrueUpper,
		"zimbraMtaAuthTarget":            srvBoolUpper(e.Facts.ServiceWebapp),
	}
}

// srvStoreJoins is true when configSetStoreDefaults registers the host as a
// reverse proxy target: a new install of a service node.
func srvStoreJoins(e *Env) bool { return e.Facts.NewInstall && e.Facts.ServiceWebapp }

// StepConfigSetStoreDefaults ports setup.pl configSetStoreDefaults
// (5151-5188) without its zmproxyconfig calls (StepConfigSetStoreProxy): on a
// new service node the host joins zimbraReverseProxyAvailableLookupTargets
// (when no other mailstore is a lookup target) and
// zimbraReverseProxyUpstreamEwsServers, and every appserver sets
// zimbraReverseProxyLookupTarget TRUE and zimbraMtaAuthTarget on its server.
//
// When: isEnabled(carbonio-appserver). No stage name.
func StepConfigSetStoreDefaults() Step {
	return Step{
		Title: "Set store defaults",
		When:  func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) },
		Done:  srvStoreDefaultsDone,
		Apply: srvStoreDefaultsApply,
	}
}

func srvStoreDefaultsDone(e *Env) (bool, error) {
	host := srvHost(e)
	done := false

	err := srvRead(e, func(d srvDirectory) error {
		if srvStoreJoins(e) {
			joined, err := srvStoreJoined(d, host)
			if err != nil {
				return err
			}

			if !joined {
				return nil
			}
		}

		ent, found, err := d.Server(host)
		done = found && srvEntryMatches(ent, srvStoreServerAttrs(e))

		return err
	})

	return done, err
}

// srvStoreJoined reports whether host is already registered as a reverse
// proxy lookup target and EWS upstream.
func srvStoreJoined(d srvDirectory, host string) (bool, error) {
	need, err := srvStoreDefaultsNeedAdd(d, host)
	if err != nil {
		return false, err
	}

	g, err := d.Global()
	if err != nil {
		return false, err
	}

	return !need && slices.Contains(g.values("zimbraReverseProxyUpstreamEwsServers"), host), nil
}

func srvStoreDefaultsApply(e *Env) error {
	host := srvHost(e)

	var p Prov

	if srvStoreJoins(e) {
		if err := srvStoreJoin(e, &p, host); err != nil {
			return err
		}
	}

	attrs := srvStoreServerAttrs(e)

	srvMS(&p, host, "zimbraReverseProxyLookupTarget", attrs["zimbraReverseProxyLookupTarget"],
		"zimbraMtaAuthTarget", attrs["zimbraMtaAuthTarget"])

	return e.RunProv(&p)
}

// srvStoreJoin queues the registration of host as a reverse proxy target.
func srvStoreJoin(e *Env, p *Prov, host string) error {
	var need bool

	err := srvRead(e, func(d srvDirectory) error {
		var err error

		need, err = srvStoreDefaultsNeedAdd(d, host)

		return err
	})
	if err != nil {
		return err
	}

	if need {
		e.Log.Info("Adding %s to ReverseProxyAvailableLookupTargets", host)
		p.Add("mcf", "+zimbraReverseProxyAvailableLookupTargets", host)
	}

	p.Add("mcf", "+zimbraReverseProxyUpstreamEwsServers", host)

	return nil
}

// srvToggle is what a zmproxyconfig run does to one proxy.
type srvToggle int

const (
	srvLeave srvToggle = iota
	srvOff
	srvOn
)

// srvProxyOptions are the zmproxyconfig options of one run on this host: -e
// or -d, -o, -H, and for the upstream -U with interprocess security, -u
// without (enable only).
func srvProxyOptions(e *Env, on bool) *ProxyConfigOptions {
	o := &ProxyConfigOptions{Override: true, Host: srvHost(e), Enable: on, Disable: !on}

	if on {
		o.UpstreamSSL = e.Cfg.Host.RequireInterprocessSecurity
		o.NoUpstreamSSL = !o.UpstreamSSL
	}

	return o
}

func srvMailProxyOptions(e *Env, on bool) *ProxyConfigOptions {
	ps, o := srvPorts(e), srvProxyOptions(e, on)

	o.Mail = true
	o.ImapPorts = fmt.Sprintf("%d:%d:%d:%d", ps.imap, ps.imapProxy, ps.imaps, ps.imapsProxy)
	o.PopPorts = fmt.Sprintf("%d:%d:%d:%d", ps.pop, ps.popProxy, ps.pops, ps.popsProxy)

	return o
}

// srvWebProxyOptions: enabling passes the proxy mode, disabling the store mode,
// as setup.pl did.
func srvWebProxyOptions(e *Env, on bool) *ProxyConfigOptions {
	ps, o := srvPorts(e), srvProxyOptions(e, on)

	o.Web = true
	o.WebPorts = fmt.Sprintf("%d:%d:%d:%d", ps.http, ps.httpProxy, ps.https, ps.httpsProxy)
	o.Mode = e.Cfg.Store.Mode

	if on {
		o.Mode = e.Cfg.Proxy.Mode
	}

	return o
}

// srvProxyConfig is the zmproxyconfig calls of setup.pl for the mail proxy,
// then the web proxy, run in process (see proxyConfig). A failure of either is
// returned after both ran.
func srvProxyConfig(e *Env, mail, web srvToggle) error {
	var sets []*ProxyConfigOptions

	if mail != srvLeave {
		sets = append(sets, srvMailProxyOptions(e, mail == srvOn))
	}

	if web != srvLeave {
		sets = append(sets, srvWebProxyOptions(e, web == srvOn))
	}

	return proxyConfigStep(e, sets...)
}

func srvOnIf(b bool) srvToggle {
	if b {
		return srvOn
	}

	return srvLeave
}

// StepConfigSetStoreProxy ports the zmproxyconfig calls at the end of setup.pl
// configSetStoreDefaults (5176-5188): on a new install, enable the mail and
// web proxy on the store as zimbraMailProxy / zimbraWebProxy ask. The calls
// run in process (proxyConfig, the port of libexec/zmproxyconfig) and set the
// attributes of both with one command.
//
// When: isEnabled(carbonio-appserver) && $newinstall && (zimbraWebProxy ||
// zimbraMailProxy). Optional (setup.pl ignored the result). The proxy config sets
// attributes, so running it again is harmless and the step has no Done.
func StepConfigSetStoreProxy() Step {
	return Step{
		Title: "Enable proxy on the store",
		When: func(e *Env) bool {
			return e.Facts.IsEnabled(PkgAppserver) && e.Facts.NewInstall &&
				(e.Cfg.Store.MailProxy || e.Cfg.Store.WebProxy)
		},
		Optional: true,
		Apply: func(e *Env) error {
			return srvProxyConfig(e, srvOnIf(e.Cfg.Store.MailProxy), srvOnIf(e.Cfg.Store.WebProxy))
		},
	}
}

// StepConfigSetTimeZonePref ports setup.pl configSetTimeZonePref
// (5262-5273): zimbraPrefTimeZoneId on the default COS of the LDAP master.
//
// Name: zimbraPrefTimeZoneId. When: isEnabled(carbonio-directory-server) &&
// LDAPHOST eq HOSTNAME (and a time zone is configured). Optional.
func StepConfigSetTimeZonePref() Step {
	return Step{
		Name:     "zimbraPrefTimeZoneId",
		Title:    "Set time zone preference",
		When:     func(e *Env) bool { return e.Facts.IsEnabled(srvPkgDirectory) },
		Optional: true,
		// setup.pl records the stage on every directory server, and writes
		// the COS only on the master.
		Done: func(e *Env) (bool, error) {
			if e.Cfg.LDAP.Host != e.Cfg.Host.Hostname || e.Cfg.Host.Timezone == "" {
				return true, nil
			}

			return srvCOSMatches(e, "default", map[string]string{"zimbraPrefTimeZoneId": e.Cfg.Host.Timezone})
		},
		Apply: func(e *Env) error {
			return e.Prov("mc", "default", "zimbraPrefTimeZoneId", e.Cfg.Host.Timezone)
		},
	}
}

// StepSetSkinLogoURL ports the setLdapGlobalConfig("zimbraSkinLogoURL", ...)
// call of setup.pl applyConfig (5915).
//
// When: isEnabled(carbonio-directory-server). Optional (setup.pl ignored the
// result).
func StepSetSkinLogoURL() Step {
	return Step{
		Title:    "Set skin logo URL",
		When:     func(e *Env) bool { return e.Facts.IsEnabled(srvPkgDirectory) },
		Optional: true,
		Done: func(e *Env) (bool, error) {
			return srvGlobalHas(e, map[string]string{"zimbraSkinLogoURL": srvSkinLogoURL})
		},
		Apply: func(e *Env) error { return e.Prov("mcf", "zimbraSkinLogoURL", srvSkinLogoURL) },
	}
}

// srvProxyEnabled is isEnabled(carbonio-proxy) after configSetProxyPrefs: an
// installed proxy with neither mail nor web proxy is disabled by that stage.
func srvProxyEnabled(e *Env) bool {
	if !e.Facts.IsEnabled(srvPkgProxy) {
		return false
	}

	return !e.Facts.IsInstalled(srvPkgProxy) || e.Cfg.Proxy.MailEnabled || e.Cfg.Proxy.WebEnabled
}

// srvProxyLookups warns when the proxy has no store or no memcached to use
// (setup.pl garpu and gamcs checks, read from LDAP).
func srvProxyLookups(e *Env) {
	err := srvRead(e, func(d srvDirectory) error {
		if !e.Facts.IsEnabled(PkgAppserver) {
			names, err := d.ServerNames("(zimbraReverseProxyLookupTarget=TRUE)")
			if err != nil || len(names) == 0 {
				srvWarn(e, "There is currently no mailstore to proxy. Proxy will restart once one becomes available.")
			}
		}

		if !e.Facts.IsEnabled(srvPkgMemcached) {
			names, err := d.ServerNames("(zimbraServiceEnabled=memcached)")
			if err != nil || len(names) == 0 {
				srvWarn(e, "There are currently no memcached servers for the proxy. "+
					"Proxy will start once one becomes available.")
			}
		}

		return nil
	})
	if err != nil {
		e.Log.Detail("proxy lookups: %v", err)
	}
}

// srvSetProxyPublicHostname sets zimbraPublicServiceHostname unless LDAP
// already has one (setup.pl $publicServiceHostnameAlreadySet).
func srvSetProxyPublicHostname(e *Env) error {
	name := e.Cfg.Host.PublicHostname
	if name == "" {
		return nil
	}

	var set bool

	err := srvRead(e, func(d srvDirectory) error {
		g, err := d.Global()
		if err != nil {
			return err
		}

		set = g.first("zimbraPublicServiceHostname") != ""

		return nil
	})
	if err != nil || set {
		return err
	}

	e.Log.Info("Setting Public Service Hostname %s", name)

	return e.Prov("mcf", "zimbraPublicServiceHostname", name)
}

// StepConfigSetProxyPrefs ports setup.pl configSetProxyPrefs (5346-5409). On
// a node with the proxy enabled: strict server name on the server entry, the
// proxy config for mail and web (proxyConfig, in process), warnings when no mailstore or
// memcached is known, and zimbraPublicServiceHostname. A proxy with neither
// mail nor web proxy is marked disabled (Facts.Enabled), as setup.pl did to
// %enabledPackages. On any other node with the proxy installed, mail and web
// proxying are switched off.
//
// When: isInstalled(carbonio-proxy). Optional: setup.pl ignored every result.
// No Done: the proxy config and the zmprov calls only set attributes, and the
// step must run to keep Facts.Enabled consistent. The garpu / gamcs checks
// read the lookup-target and memcached servers from LDAP directly.
func StepConfigSetProxyPrefs() Step {
	return Step{
		Title:    "Set proxy preferences",
		When:     func(e *Env) bool { return e.Facts.IsInstalled(srvPkgProxy) },
		Optional: true,
		Apply: func(e *Env) error {
			if !e.Facts.IsEnabled(srvPkgProxy) {
				return srvProxyConfig(e, srvOff, srvOff)
			}

			errs := []error{e.Prov("ms", srvHost(e), "zimbraReverseProxyStrictServerNameEnabled",
				srvBoolUpper(e.Cfg.Proxy.StrictServerName))}

			if !e.Cfg.Proxy.MailEnabled && !e.Cfg.Proxy.WebEnabled {
				e.Facts.Enabled[srvPkgProxy] = false
			} else {
				toggle := func(b bool) srvToggle {
					if b {
						return srvOn
					}

					return srvOff
				}

				errs = append(errs, srvProxyConfig(e, toggle(e.Cfg.Proxy.MailEnabled), toggle(e.Cfg.Proxy.WebEnabled)))
			}

			srvProxyLookups(e)

			errs = append(errs, srvSetProxyPublicHostname(e))

			return errors.Join(errs...)
		},
	}
}

// srvProxyBitDefaults are the global config attributes setProxyBits sets when
// they are empty, in setup.pl order. The three LDAP filters are the Carbonio
// defaults, written without the backslashes the Perl source carried.
var srvProxyBitDefaults = [][2]string{
	{
		"zimbraReverseProxyMailHostQuery",
		"(|(zimbraMailDeliveryAddress=${USER})(zimbraMailAlias=${USER})(zimbraId=${USER}))",
	},
	{"zimbraReverseProxyPortQuery", "(&(zimbraServiceHostname=${MAILHOST})(objectClass=zimbraServer))"},
	{"zimbraReverseProxyDomainNameQuery", "(&(zimbraVirtualIPAddress=${IPADDR})(objectClass=zimbraDomain))"},
	{"zimbraReverseProxyMailHostAttribute", "zimbraMailHost"},
	{"zimbraReverseProxyPop3PortAttribute", srvAttrPop3Bind},
	{"zimbraReverseProxyPop3SSLPortAttribute", "zimbraPop3SSLBindPort"},
	{"zimbraReverseProxyImapPortAttribute", srvAttrImapBind},
	{"zimbraReverseProxyImapSSLPortAttribute", "zimbraImapSSLBindPort"},
	{"zimbraReverseProxyDomainNameAttribute", "zimbraDomainName"},
	{"zimbraImapCleartextLoginEnabled", srvFalseUpper},
	{"zimbraPop3CleartextLoginEnabled", srvFalseUpper},
	{"zimbraReverseProxyAuthWaitInterval", "10s"},
	{"zimbraReverseProxyIPLoginLimit", "0"},
	{"zimbraReverseProxyIPLoginLimitTime", "3600"},
	{"zimbraReverseProxyUserLoginLimit", "0"},
	{"zimbraReverseProxyUserLoginLimitTime", "3600"},
	{srvAttrMailProxy, "0"},
	{srvAttrMailSSLProxy, "0"},
	{"zimbraReverseProxyHttpEnabled", srvFalseUpper},
	{"zimbraReverseProxyMailEnabled", srvTrueUpper},
}

// srvMissingProxyBits are the setProxyBits attributes the global config lacks.
func srvMissingProxyBits(e *Env) ([]string, error) {
	var args []string

	err := srvRead(e, func(d srvDirectory) error {
		g, err := d.Global()
		if err != nil {
			return err
		}

		for _, kv := range srvProxyBitDefaults {
			if g.first(kv[0]) == "" {
				args = append(args, kv[0], kv[1])
			}
		}

		return nil
	})

	return args, err
}

// StepSetProxyBits ports setup.pl setProxyBits (5275-5344): the reverse
// proxy lookup queries and login limits, set on the global config where they
// are empty.
//
// When: !$newinstall && isInstalled(carbonio-directory-server). Deviation: the
// three filter values are written unescaped (the Perl strings carried
// backslashes before the parentheses); LDAP normally has them already.
func StepSetProxyBits() Step {
	return Step{
		Title: "Set proxy options",
		When: func(e *Env) bool {
			return !e.Facts.NewInstall && e.Facts.IsInstalled(srvPkgDirectory)
		},
		Done: func(e *Env) (bool, error) {
			missing, err := srvMissingProxyBits(e)

			return len(missing) == 0, err
		},
		Apply: func(e *Env) error {
			missing, err := srvMissingProxyBits(e)
			if err != nil || len(missing) == 0 {
				return err
			}

			return e.Prov(append([]string{"mcf"}, missing...)...)
		},
	}
}

// StepWarnDeprecatedVirtualIPs ports setup.pl warnDeprecatedVirtualIPs
// (5445-5488): warn about every domain that still sets
// zimbraVirtualIPAddress and when zimbraReverseProxySNIEnabled is FALSE.
//
// When: always. LDAP problems are logged and skipped, as in setup.pl.
func StepWarnDeprecatedVirtualIPs() Step {
	return Step{
		Title:    "Check deprecated virtual IPs",
		Optional: true,
		Apply: func(e *Env) error {
			err := srvRead(e, func(d srvDirectory) error {
				msgs, err := srvVirtualIPWarnings(d)
				for _, m := range msgs {
					srvWarn(e, "%s", m)
				}

				return err
			})
			if err != nil {
				e.Log.Detail("virtual IP check skipped: %v", err)
			}

			return nil
		},
	}
}

// srvVirtualIPWarnings are the warnings warnDeprecatedVirtualIPs prints: a
// domain per zimbraVirtualIPAddress, and SNI turned off. The warnings found
// before an LDAP error are returned with it.
func srvVirtualIPWarnings(d srvDirectory) ([]string, error) {
	domains, err := d.VIPDomains()
	if err != nil {
		return nil, err
	}

	var msgs []string

	for _, dom := range domains {
		msgs = append(msgs, fmt.Sprintf("domain %s has zimbraVirtualIPAddress set (%s): "+
			"IP-based virtual hosting is deprecated, use SNI with zimbraVirtualHostname instead.",
			dom.first("zimbraDomainName"), strings.Join(dom.values("zimbraVirtualIPAddress"), ", ")))
	}

	g, err := d.Global()
	if err != nil {
		return msgs, err
	}

	if strings.EqualFold(g.first("zimbraReverseProxySNIEnabled"), srvFalseUpper) {
		msgs = append(msgs, "zimbraReverseProxySNIEnabled is FALSE: "+
			"disabling SNI to use IP-based virtual hosts is deprecated.")
	}

	return msgs, nil
}

// srvServiceName is the zimbraService value of a package.
func srvServiceName(pkg string) string {
	switch pkg {
	case PkgAppserver:
		return "mailbox"
	case existPkgClamav:
		return srvSvcAntivirus
	default:
		return strings.TrimPrefix(pkg, "carbonio-")
	}
}

func srvAddUnique(list []string, v ...string) []string {
	for _, s := range v {
		if !slices.Contains(list, s) {
			list = append(list, s)
		}
	}

	return list
}

// srvMTAServices are the services of an MTA node that setup.pl configInitMta
// (5683-5699) pushed on the lists: the installed ones, and the enabled ones
// as the MTA configuration asks.
func srvMTAServices(c *bootstrap.Config) (installed, enabled []string) {
	installed = []string{srvSvcAmavis, srvSvcAntivirus, srvSvcAntispam, srvSvcOpendkim}
	enabled = []string{srvSvcAmavis}

	for _, svc := range []struct {
		on   bool
		name string
	}{
		{c.MTA.Antivirus, srvSvcAntivirus}, {c.MTA.Antispam, srvSvcAntispam},
		{c.MTA.OpenDKIM, srvSvcOpendkim}, {c.MTA.CBPolicyd, "cbpolicyd"},
	} {
		if svc.on {
			enabled = append(enabled, svc.name)
		}
	}

	return installed, enabled
}

// srvServiceLists are the zimbraServiceInstalled and zimbraServiceEnabled
// values setup.pl configSetEnabledServices (with the lists configInitMta
// pushes on an MTA node) stores for the node.
func srvServiceLists(e *Env) (installed, enabled []string) {
	for _, pkg := range Packages {
		switch {
		case !e.Facts.IsInstalled(pkg):
		case pkg == PkgCore:
			installed = srvAddUnique(installed, "stats", srvSvcDiscover)
		default:
			installed = srvAddUnique(installed, srvServiceName(pkg))
		}
	}

	for _, pkg := range Packages {
		if !e.Facts.IsEnabled(pkg) || (pkg == srvPkgProxy && !srvProxyEnabled(e)) {
			continue
		}

		switch pkg {
		case PkgCore:
			enabled = srvAddUnique(enabled, "stats")
		case PkgAppserver:
			if e.Facts.ServiceWebapp {
				enabled = srvAddUnique(enabled, "service")
			}

			enabled = srvAddUnique(enabled, "mailbox")
		default:
			enabled = srvAddUnique(enabled, srvServiceName(pkg))
		}
	}

	if e.Facts.IsEnabled(PkgMTA) {
		mtaInstalled, mtaEnabled := srvMTAServices(e.Cfg)

		installed = srvAddUnique(installed, mtaInstalled...)
		enabled = srvAddUnique(enabled, mtaEnabled...)
	}

	return installed, enabled
}

// srvKeepServiceDiscover adds service-discover to enabled when the server
// entry already lists it: bootstrap preserves that value but never introduces
// it (CO-4122).
func srvKeepServiceDiscover(ent srvEntry, enabled []string) []string {
	if slices.Contains(ent.values("zimbraServiceEnabled"), srvSvcDiscover) {
		return srvAddUnique(slices.Clone(enabled), srvSvcDiscover)
	}

	return enabled
}

// StepConfigSetEnabledServices ports setup.pl configSetEnabledServices
// (5761-5818): zimbraServiceInstalled and zimbraServiceEnabled of the server
// entry from the installed and enabled packages (the MTA services setup.pl
// configInitMta contributed included), keeping service-discover when the
// entry has it, then libexec/zmiptool.
//
// Name: configSetEnabledServices. When: always. Deviation: the MTA services
// are always included, not only in the run where configInitMta executed;
// failures end the run (setup.pl printed "done." regardless).
func StepConfigSetEnabledServices() Step {
	return Step{
		Name:  "configSetEnabledServices",
		Title: "Set enabled services",
		Done: func(e *Env) (bool, error) {
			installed, enabled := srvServiceLists(e)
			done := false

			err := srvRead(e, func(d srvDirectory) error {
				ent, found, err := d.Server(srvHost(e))
				if err != nil || !found {
					return err
				}

				done = srvSameSet(ent.values("zimbraServiceInstalled"), installed) &&
					srvSameSet(ent.values("zimbraServiceEnabled"), srvKeepServiceDiscover(ent, enabled))

				return nil
			})

			return done, err
		},
		Apply: func(e *Env) error {
			installed, enabled := srvServiceLists(e)

			err := srvRead(e, func(d srvDirectory) error {
				ent, _, err := d.Server(srvHost(e))
				if err != nil {
					return err
				}

				enabled = srvKeepServiceDiscover(ent, enabled)

				return nil
			})
			if err != nil {
				return err
			}

			var kv []string

			for _, v := range installed {
				kv = append(kv, "zimbraServiceInstalled", v)
			}

			for _, v := range enabled {
				kv = append(kv, "zimbraServiceEnabled", v)
			}

			var p Prov

			srvMS(&p, srvHost(e), kv...)

			err = e.RunProv(&p)

			srvZmiptool(e)

			return err
		},
	}
}

// srvHostPool returns the server id, the default COS pool, and whether the
// pool already holds the id.
func srvHostPool(e *Env) (id string, pool []string, member bool, err error) {
	err = srvRead(e, func(d srvDirectory) error {
		srv, found, err := d.Server(srvHost(e))
		if err != nil {
			return err
		}

		id = srv.first("zimbraId")
		if !found || id == "" {
			return fmt.Errorf("couldn't find a server entry for %s", srvHost(e))
		}

		cos, _, err := d.COS("default")
		if err != nil {
			return err
		}

		pool = cos.values("zimbraMailHostPool")
		member = slices.Contains(pool, id)

		return nil
	})

	return id, pool, member, err
}

// StepAddServerToHostPool ports setup.pl addServerToHostPool (6152-6173): the
// server id joins zimbraMailHostPool of the default COS.
//
// When: isEnabled(carbonio-appserver) && isStoreServiceNode(). Optional:
// setup.pl printed "failed." and carried on.
func StepAddServerToHostPool() Step {
	return Step{
		Title:    "Add server to MailHostPool in default COS",
		When:     func(e *Env) bool { return e.Facts.IsEnabled(PkgAppserver) && e.Facts.ServiceWebapp },
		Optional: true,
		Done: func(e *Env) (bool, error) {
			_, _, member, err := srvHostPool(e)

			return member, err
		},
		Apply: func(e *Env) error {
			id, pool, member, err := srvHostPool(e)
			if err != nil || member {
				return err
			}

			kv := []string{"default"}

			for _, v := range srvAddUnique(slices.Clone(pool), id) {
				kv = append(kv, "zimbraMailHostPool", v)
			}

			return e.Prov(append([]string{"mc"}, kv...)...)
		},
	}
}

// StepSetProxyPortsOnExisting ports the proxy port block near the end of
// setup.pl applyConfig (5964-5969): on an existing install with the web proxy
// enabled, zimbraMailProxyPort and zimbraMailSSLProxyPort of the server.
//
// When: !$newinstall && isInstalled(carbonio-proxy) && isEnabled(carbonio-proxy)
// && HTTPPROXY eq TRUE.
func StepSetProxyPortsOnExisting() Step {
	attrs := func(e *Env) []string {
		return []string{
			srvAttrMailProxy, srvItoa(e.Cfg.Proxy.Ports.HTTP),
			srvAttrMailSSLProxy, srvItoa(e.Cfg.Proxy.Ports.HTTPS),
		}
	}

	return Step{
		Title: "Set proxy ports",
		When: func(e *Env) bool {
			return !e.Facts.NewInstall && e.Facts.IsInstalled(srvPkgProxy) && e.Facts.IsEnabled(srvPkgProxy) &&
				e.Cfg.Proxy.WebEnabled
		},
		Done: func(e *Env) (bool, error) {
			return srvServerMatches(e, srvHost(e), srvPairs(attrs(e)))
		},
		Apply: func(e *Env) error {
			var p Prov

			srvMS(&p, srvHost(e), attrs(e)...)

			return e.RunProv(&p)
		},
	}
}
