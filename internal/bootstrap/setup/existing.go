// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"maps"
	"regexp"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// Key paths ExistingValues returns that are used more than once.
const (
	existKeyHostname      = "host.hostname"
	existKeyLDAPHost      = "ldap.host"
	existKeyLDAPAdminPass = "ldap.admin_password"
	existKeyLDAPRootPass  = "ldap.root_password"
	existKeySMTPHost      = "store.smtp_host"
	existKeyDomainName    = "domain.name"
)

// Packages that gate the legacy reads.
const (
	existPkgDirectory = "carbonio-directory-server"
	existPkgProxy     = "carbonio-proxy"
	existPkgClamav    = "carbonio-clamav"
)

// existLDAPReader is the part of the native LDAP client ExistingValues reads.
// *ldap.Client implements it; the tests use a fake.
type existLDAPReader interface {
	GetServerConfig(hostname string) (map[string]string, error)
	GetGlobalConfig() (map[string]string, error)
	ReadAttribute(dn, attribute string) (value string, present bool, err error)
	BaseDN() string
	Close() error
}

// existOpenLDAP opens the LDAP reader of the node.
type existOpenLDAP func() (existLDAPReader, error)

// existValues is the result under construction: key path to value. An empty
// value deletes the key, as setup.pl overwrites %config with what it reads
// even when that is empty.
type existValues map[string]string

func (v existValues) set(path, value string) {
	if value == "" {
		delete(v, path)

		return
	}

	v[path] = value
}

// setBool stores value, a legacy boolean (TRUE/FALSE, yes/no), as true/false.
// Unset and unparsable values are not stored.
func (v existValues) setBool(path, value string) {
	b, err := bootstrap.ParseBool(value)
	if err != nil {
		delete(v, path)

		return
	}

	v[path] = strconv.FormatBool(b)
}

// ExistingValues reads the configuration of an existing install (key path to
// value) from localconfig and LDAP: setup.pl setDefaultsFromLocalConfig
// (L1727-1863), setLdapDefaults (L1092-1303) and setEnabledDependencies
// (L2971-3029), applied in the order setup.pl runs them.
//
// Only keys whose value exists on the node are returned; lists are
// comma-separated. LDAP is read when setup.pl would (the master is not this
// node, or this node is a directory server) and when it answers: an
// unreachable LDAP gives fewer values, not an error. Call it after
// DetectFacts. On a new install only a node whose LDAP master is another,
// reachable node has values: those of the master (setup.pl main L203,
// setLdapDefaults); e.Cfg must then hold the operator's LDAP settings.
//
// Deliberate deviations from setup.pl:
//   - the LDAP port comes from ldap_port only: setup.pl tries to parse it from
//     ldap_master_url, but its regexp always leaves the port empty.
//   - zimbra_require_interprocess_security is read as a boolean: setup.pl
//     treats the string "false" as true.
//   - setUseProxy (called by setEnabledDependencies) only rewrites the default
//     ports, which setLdapDefaults then overwrites from the server entry, so
//     it is not ported.
//   - random defaults (spam/ham/quarantine accounts) and fallbacks to other
//     defaults (smtp_source = admin account) are not values of the node and
//     are left to the defaults layer.
func ExistingValues(e *Env) (map[string]string, error) {
	open := func() (existLDAPReader, error) {
		c, err := e.LDAP()
		if err != nil {
			return nil, err
		}

		return c, nil
	}

	if e.Facts.NewInstall {
		return existingFromMaster(e, open), nil
	}

	return existingValues(e, open)
}

// existingFromMaster reads the master's LDAP for a new node that joins it.
// The operator's settings seed the lookup and are not returned: they keep
// their own layer.
func existingFromMaster(e *Env, open existOpenLDAP) map[string]string {
	v := existValues{}

	c := e.Cfg
	if c == nil || c.LDAP.Host == "" || c.LDAP.Host == c.Host.Hostname || !e.LDAPIsAvailable() {
		return v
	}

	seeds := map[string]string{
		existKeyHostname:      c.Host.Hostname,
		existKeyLDAPHost:      c.LDAP.Host,
		"ldap.port":           strconv.Itoa(c.LDAP.Port),
		existKeyLDAPAdminPass: c.LDAP.AdminPassword,
	}
	maps.Copy(v, seeds)

	existFromLDAP(e, v, open)

	for k, seed := range seeds {
		if v[k] == seed {
			delete(v, k)
		}
	}

	return v
}

func existingValues(e *Env, open existOpenLDAP) (map[string]string, error) {
	lc, err := e.LC()
	if err != nil {
		return nil, err
	}

	v := existValues{}

	existFromLocalConfig(e, lc, v)
	existEnabledDependencies(e, v)
	existFromLDAP(e, v, open)

	return v, nil
}

var (
	existSchemeRe = regexp.MustCompile(`ldaps?://`)
	existPortRe   = regexp.MustCompile(`:\d+`)
)

// existLDAPHostFromURL is the host part of the first ldap_master_url entry
// (setup.pl L1731-1752): quotes, scheme and ":port" removed.
func existLDAPHostFromURL(masterURL string) string {
	fields := strings.Fields(masterURL)
	if len(fields) == 0 {
		return ""
	}

	h := strings.ReplaceAll(fields[0], `"`, "")
	h = existSchemeRe.ReplaceAllString(h, "")

	return existPortRe.ReplaceAllString(h, "")
}

// existFromLocalConfig ports setDefaultsFromLocalConfig.
func existFromLocalConfig(e *Env, lc map[string]string, v existValues) {
	v.set(existKeyHostname, strings.ToLower(lc["zimbra_server_hostname"]))

	v.set("ldap.port", lc["ldap_port"])

	if h := existLDAPHostFromURL(lc["ldap_master_url"]); h != "" {
		v.set(existKeyLDAPHost, h)
	} else {
		v.set(existKeyLDAPHost, lc["ldap_host"])
	}

	v.set(existKeyLDAPRootPass, lc["ldap_root_password"])
	v.set(existKeyLDAPAdminPass, lc["zimbra_ldap_password"])

	if n, err := strconv.Atoi(lc["mailboxd_java_heap_size"]); err == nil {
		v.set("store.mailboxd_memory_mb", strconv.Itoa(n))
	}

	v.setBool("host.require_interprocess_security", lc["zimbra_require_interprocess_security"])
	v.set("ldap.base_dn", lc["ldap_dit_base_dn_config"])
	v.set("notifications.smtp_source", lc["smtp_source"])
	v.set("notifications.smtp_destination", lc["smtp_destination"])
	v.set("notifications.av_notify_user", lc["av_notify_user"])

	existLDAPPasswords(e, lc, v)
}

// existLDAPPasswords reads the passwords of the LDAP service accounts a node
// uses; an empty one falls back to the admin password (setup.pl L1807-1832).
func existLDAPPasswords(e *Env, lc map[string]string, v existValues) {
	dir := e.Facts.IsEnabled(existPkgDirectory)
	mta := e.Facts.IsEnabled(bootstrap.RolePackage(bootstrap.RoleMTA))
	proxy := e.Facts.IsEnabled(existPkgProxy)

	for _, p := range []struct {
		enabled bool
		path    string
		key     string
	}{
		{dir, "ldap.replication_password", "ldap_replication_password"},
		{dir || mta, "ldap.postfix_password", "ldap_postfix_password"},
		{dir || mta, "ldap.amavis_password", "ldap_amavis_password"},
		{dir || proxy, "ldap.nginx_password", "ldap_nginx_password"},
	} {
		if !p.enabled {
			continue
		}

		if lc[p.key] != "" {
			v.set(p.path, lc[p.key])
		} else {
			v.set(p.path, v[existKeyLDAPAdminPass])
		}
	}
}

// existEnabledDependencies ports setEnabledDependencies for an existing
// install.
func existEnabledDependencies(e *Env, v existValues) {
	f := e.Facts

	if f.IsEnabled(existPkgDirectory) {
		if v[existKeyLDAPHost] == "" {
			v.set(existKeyLDAPHost, v[existKeyHostname])
		}
	} else if v[existKeyLDAPHost] == v[existKeyHostname] {
		v.set(existKeyLDAPHost, "")
		v.set(existKeyLDAPAdminPass, "")
		v.set(existKeyLDAPRootPass, "")
	}

	mta := f.IsEnabled(bootstrap.RolePackage(bootstrap.RoleMTA))

	if f.IsEnabled(bootstrap.RolePackage(bootstrap.RoleAppserver)) && mta {
		v.set(existKeySMTPHost, v[existKeyHostname])
	}

	if mta {
		v["mta.antispam"] = strconv.FormatBool(f.IsServiceEnabled("antispam"))
		v["mta.antivirus"] = strconv.FormatBool(f.IsServiceEnabled("antivirus"))
		v["mta.opendkim"] = strconv.FormatBool(f.IsServiceEnabled("opendkim"))
		v["mta.cbpolicyd"] = strconv.FormatBool(f.IsServiceEnabled("cbpolicyd"))
	}

	if f.IsEnabled(existPkgClamav) {
		v["mta.antivirus"] = strconv.FormatBool(f.IsServiceEnabled("antivirus"))
	}
}

// existReadsLDAP mirrors setup.pl L203: LDAP is read when the node hosts it
// or when the master is another node whose connection data is complete.
func existReadsLDAP(e *Env, v existValues) bool {
	if v[existKeyLDAPHost] == v[existKeyHostname] {
		return e.Facts.IsEnabled(existPkgDirectory)
	}

	return v[existKeyLDAPHost] != "" && v["ldap.port"] != "" && v[existKeyLDAPAdminPass] != ""
}

// existFromLDAP ports setLdapDefaults. Failures to reach or read LDAP are
// logged, not returned.
func existFromLDAP(e *Env, v existValues, open existOpenLDAP) {
	if !existReadsLDAP(e, v) {
		e.Log.Detail("LDAP not read: not configured or master incomplete")

		return
	}

	c, err := open()
	if err != nil {
		e.Log.Warn("Cannot read the existing LDAP configuration: %v", err)

		return
	}
	defer func() { _ = c.Close() }()

	global, err := c.GetGlobalConfig()
	if err != nil {
		e.Log.Warn("Cannot read the existing LDAP configuration: %v", err)

		return
	}

	var ports existPorts

	if server, err := c.GetServerConfig(v[existKeyHostname]); err == nil && server["zimbraId"] != "" {
		existFromServer(e, v, server, &ports)
	}

	existFromGlobal(v, global)
	existFromCOS(e, v, c)

	ports.reconcile(e.Facts.IsInstalled(existPkgProxy) && e.Facts.IsEnabled(existPkgProxy), v)
	ports.store(v)
}

// existFromServer reads the server entry (L1096-1138).
func existFromServer(e *Env, v existValues, server map[string]string, ports *existPorts) {
	v.set("host.ip_mode", server["zimbraIPMode"])
	v.set("store.mode", server["zimbraMailMode"])
	v.set("proxy.mode", server["zimbraReverseProxyMailMode"])
	v.setBool("proxy.mail_enabled", server["zimbraReverseProxyMailEnabled"])
	v.setBool("proxy.web_enabled", server["zimbraReverseProxyHttpEnabled"])
	v.set(existKeySMTPHost, server["zimbraSmtpHostname"])

	ports.read(server)

	if e.Facts.IsEnabled(bootstrap.RolePackage(bootstrap.RoleMTA)) {
		if nets := strings.FieldsFunc(server["zimbraMtaMyNetworks"], existListSep); len(nets) > 0 {
			v.set("mta.my_networks", strings.Join(nets, ","))
		}
	}
}

func existListSep(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }

// existFromGlobal reads the global config (L1140-1182).
func existFromGlobal(v existValues, global map[string]string) {
	if h := global["zimbraPublicServiceHostname"]; h != "" {
		v.set("host.public_hostname", h)
	}

	if d := global["zimbraDefaultDomainName"]; d != "" {
		v.set(existKeyDomainName, d)
		v.set("admin.account", "zextras@"+d)
	}

	if v[existKeySMTPHost] == "" {
		smtp := strings.ReplaceAll(global["zimbraSmtpHostname"], "\n", " ")
		if smtp != "localhost" {
			v.set(existKeySMTPHost, smtp)
		}
	}

	v.set("store.spam_account", global["zimbraSpamIsSpamAccount"])
	v.set("store.ham_account", global["zimbraSpamIsNotSpamAccount"])
	v.set("store.virus_quarantine_account", global["zimbraAmavisQuarantineAccount"])
}

// existFromCOS reads the default COS (L1184-1186).
func existFromCOS(e *Env, v existValues, c existLDAPReader) {
	dn := "cn=default,cn=cos," + c.BaseDN()

	if val, ok, err := c.ReadAttribute(dn, "zimbraPrefUseKeyboardShortcuts"); err == nil && ok {
		v.setBool("store.keyboard_shortcuts", val)
	} else if err != nil {
		e.Log.Detail("default COS not read: %v", err)
	}

	if val, ok, err := c.ReadAttribute(dn, "zimbraPrefTimeZoneId"); err == nil && ok {
		v.set("host.timezone", val)
	}
}

// existPorts are the mailbox and proxy ports of the server entry. Zero is
// unset, as in Perl where "" == 0.
type existPorts struct {
	imap, imaps, pop, pops, http, https        int
	proxyIMAP, proxyIMAPS, proxyPOP, proxyPOPS int
	proxyHTTP, proxyHTTPS                      int
	// present are the paths the server entry set.
	present map[string]bool
}

type existPortRef struct {
	path string
	attr string
	val  *int
}

// refs lists every port with its key path and server attribute.
func (p *existPorts) refs() []existPortRef {
	return []existPortRef{
		{"store.ports.imap", "zimbraImapBindPort", &p.imap},
		{"store.ports.imaps", "zimbraImapSSLBindPort", &p.imaps},
		{"store.ports.pop3", "zimbraPop3BindPort", &p.pop},
		{"store.ports.pop3s", "zimbraPop3SSLBindPort", &p.pops},
		{"store.ports.http", "zimbraMailPort", &p.http},
		{"store.ports.https", "zimbraMailSSLPort", &p.https},
		{"proxy.ports.imap", "zimbraImapProxyBindPort", &p.proxyIMAP},
		{"proxy.ports.imaps", "zimbraImapSSLProxyBindPort", &p.proxyIMAPS},
		{"proxy.ports.pop3", "zimbraPop3ProxyBindPort", &p.proxyPOP},
		{"proxy.ports.pop3s", "zimbraPop3SSLProxyBindPort", &p.proxyPOPS},
		{"proxy.ports.http", "zimbraMailProxyPort", &p.proxyHTTP},
		{"proxy.ports.https", "zimbraMailSSLProxyPort", &p.proxyHTTPS},
	}
}

// read loads the ports of the server entry; a missing or non-numeric one is 0.
func (p *existPorts) read(server map[string]string) {
	p.present = map[string]bool{}

	for _, r := range p.refs() {
		n, err := strconv.Atoi(server[r.attr])
		if err != nil {
			*r.val = 0

			continue
		}

		*r.val = n
		p.present[r.path] = true
	}

	// L1229-1230: a port explicitly 0 means the default.
	if p.present["store.ports.http"] && p.http == 0 {
		p.http = 80
	}

	if p.present["store.ports.https"] && p.https == 0 {
		p.https = 443
	}
}

// reconcile ports L1236-1297: keep the mailbox and the proxy off the same
// port. proxyOn: carbonio-proxy is installed and enabled on this node.
func (p *existPorts) reconcile(proxyOn bool, v existValues) {
	mailPairs := []struct{ port, proxy *int }{
		{&p.imap, &p.proxyIMAP}, {&p.imaps, &p.proxyIMAPS}, {&p.pop, &p.proxyPOP}, {&p.pops, &p.proxyPOPS},
	}
	lows := []int{143, 993, 110, 995}

	if proxyOn {
		if v["proxy.mail_enabled"] == "true" {
			for i, pr := range mailPairs {
				existProxyOnMail(pr.port, pr.proxy, lows[i])
			}
		}

		if v["proxy.web_enabled"] == "true" {
			existProxyOnWeb(&p.http, &p.proxyHTTP, 80, 8080)
			existProxyOnWeb(&p.https, &p.proxyHTTPS, 443, 8443)
		}

		return
	}

	for i, pr := range mailPairs {
		existProxyOffPair(pr.port, pr.proxy, lows[i], lows[i]+7000)
	}

	existProxyOffPair(&p.http, &p.proxyHTTP, 80, 8080)
	existProxyOffPair(&p.https, &p.proxyHTTPS, 443, 8443)
}

// existProxyOnMail moves the mailbox port of a node with a mail proxy from
// lo to lo+7000, keeping the proxy on lo.
func existProxyOnMail(port, proxy *int, lo int) {
	hi := lo + 7000

	if *port == lo && *proxy == lo {
		*port = hi
	}

	if *port == hi && *proxy == hi {
		*proxy = lo
	}
}

// existProxyOnWeb is the web proxy variant: an unset proxy port takes lo.
func existProxyOnWeb(port, proxy *int, lo, hi int) {
	if (*port == lo || *port == 0) && *proxy == 0 {
		*proxy = lo
	}

	if *port == lo && *proxy == lo {
		*port = hi
	}

	if *port == hi && *proxy == hi {
		*proxy = lo
	}
}

// existProxyOffPair moves the proxy port of a node without proxy from lo to
// hi, keeping the mailbox on lo.
func existProxyOffPair(port, proxy *int, lo, hi int) {
	if *port == lo && *proxy == lo {
		*proxy = hi
	}

	if *port == hi && *proxy == hi {
		*port = lo
	}
}

// store writes the ports that exist (read from LDAP or set by reconcile).
func (p *existPorts) store(v existValues) {
	for _, r := range p.refs() {
		if p.present[r.path] || *r.val != 0 {
			v[r.path] = strconv.Itoa(*r.val)
		}
	}
}
