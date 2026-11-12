// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// pcRunProv applies the zmprov commands of a run (see Env.RunProv); tests
// replace it to see them.
var pcRunProv = func(e *Env, p *Prov) error { return e.RunProv(p) }

// pcIO is where a run reports and how it learns which packages are installed.
type pcIO struct {
	out, err io.Writer
	// installed is isInstalled of zmproxyconfig (rpm -q / dpkg -s).
	installed func(pkg string) (bool, error)
	// warnVIPs prints the deprecated virtual IP warnings after an -e or -f
	// run, as the script did.
	warnVIPs bool
}

// pcExit ends a run with a status after printing out (stdout) and err (stderr).
type pcExit struct {
	code     int
	out, err string
}

func (x *pcExit) Error() string { return strings.TrimSpace(x.out + x.err) }

// pcEntry is an LDAP entry with the commands of the run applied to it in
// memory, and the changes they make.
type pcEntry struct {
	orig, cur srvEntry
	set       map[string]string
	add, del  []string
}

func newPCEntry(orig srvEntry) *pcEntry {
	cur := make(srvEntry, len(orig))
	for k, v := range orig {
		cur[k] = slices.Clone(v)
	}

	return &pcEntry{orig: orig, cur: cur, set: map[string]string{}}
}

func (p *pcEntry) setAttr(attr, val string) {
	p.cur[strings.ToLower(attr)] = []string{val}
	p.set[attr] = val
}

// pcServiceKey is the lower-cased key of the service list in an entry.
var pcServiceKey = strings.ToLower(pcAttrServiceEnabled)

func (p *pcEntry) addService(val string) {
	if !slices.Contains(p.cur[pcServiceKey], val) {
		p.cur[pcServiceKey] = append(p.cur[pcServiceKey], val)
	}

	p.del = slices.DeleteFunc(p.del, func(v string) bool { return v == val })
	p.add = append(p.add, val)
}

func (p *pcEntry) delService(val string) {
	p.cur[pcServiceKey] = slices.DeleteFunc(p.cur[pcServiceKey], func(v string) bool { return v == val })
	p.add = slices.DeleteFunc(p.add, func(v string) bool { return v == val })
	p.del = append(p.del, val)
}

// changes are the modifications that bring the stored entry to the new state;
// what the entry already holds is left out.
func (p *pcEntry) changes() (add, del, replace map[string][]string) {
	add, del, replace = map[string][]string{}, map[string][]string{}, map[string][]string{}

	for attr, val := range p.set {
		if !slices.Equal(p.orig.values(attr), []string{val}) {
			replace[attr] = []string{val}
		}
	}

	for _, v := range p.add {
		if !slices.Contains(p.orig.values(pcAttrServiceEnabled), v) {
			add[pcAttrServiceEnabled] = append(add[pcAttrServiceEnabled], v)
		}
	}

	for _, v := range p.del {
		if slices.Contains(p.orig.values(pcAttrServiceEnabled), v) {
			del[pcAttrServiceEnabled] = append(del[pcAttrServiceEnabled], v)
		}
	}

	return add, del, replace
}

// pcState is one zmproxyconfig run, or a batch of them on one host (the
// bootstrap runs the mail and the web proxy one after the other). The
// commands of every run are applied to the entries in memory and written
// once, as one LDAP modify per entry.
type pcState struct {
	e  *Env
	io pcIO
	// host is the server of the first run: a batch is for one host.
	host           string
	loaded         bool
	loadErr        error
	server, global *pcEntry
}

// proxyConfig runs the zmproxyconfig command lines sets, one after the other
// on the same host, and returns the exit status of the first that failed: 0
// done, 1 refused options or checks, 2 an attribute change failed, 255 LDAP
// unreachable or unusable options. Output goes to pio.
func proxyConfig(e *Env, pio pcIO, sets ...*ProxyConfigOptions) int {
	s := &pcState{e: e, io: pio}
	code := 0

	for _, o := range sets {
		if c := s.runSet(o); code == 0 {
			code = c
		}
	}

	if c := s.commit(); code == 0 {
		code = c
	}

	if pio.warnVIPs {
		for _, o := range sets {
			if o.Enable || o.Full {
				s.warnVirtualIPs()

				break
			}
		}
	}

	return code
}

// proxyConfigStep runs sets for the bootstrap: a failure is the error, made
// of what zmproxyconfig would have printed.
func proxyConfigStep(e *Env, sets ...*ProxyConfigOptions) error {
	var buf bytes.Buffer

	pio := pcIO{out: &buf, err: &buf, installed: func(pkg string) (bool, error) { return e.Facts.IsInstalled(pkg), nil }}
	if code := proxyConfig(e, pio, sets...); code != 0 {
		return fmt.Errorf("zmproxyconfig failed (%d): %s", code, strings.TrimSpace(buf.String()))
	}

	return nil
}

// RunProxyConfig is zmproxyconfig: it parses the legacy options in args,
// runs them against the node and returns the exit status. Nothing is printed
// on success.
func RunProxyConfig(e *Env, args []string, stdout, stderr io.Writer) int {
	o, ok := ParseProxyConfigArgs(args, stderr)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "Unable to set options")

		return 255
	}

	return proxyConfig(e, pcIO{out: stdout, err: stderr, installed: pcInstalledPackages(e), warnVIPs: true}, o)
}

// pcInstalledPackages is isInstalled over the package manager.
func pcInstalledPackages(e *Env) func(string) (bool, error) {
	var set map[string]bool

	return func(pkg string) (bool, error) {
		if set == nil {
			names, err := installedPackages(e, osReleaseID(e))
			if err != nil {
				return false, err
			}

			set = map[string]bool{}
			for _, n := range names {
				set[n] = true
			}
		}

		return set[pkg], nil
	}
}

func (s *pcState) usage(msg string) int {
	_, _ = io.WriteString(s.io.out, msg+ProxyConfigUsage(s.e.Paths.Path("libexec", "zmproxyconfig")))

	return 1
}

func (s *pcState) exit(x *pcExit) int {
	_, _ = io.WriteString(s.io.out, x.out)
	_, _ = io.WriteString(s.io.err, x.err)

	return x.code
}

// load reads the server and the global config once.
func (s *pcState) load(host string) {
	if s.loaded {
		return
	}

	s.loaded, s.host = true, host
	s.loadErr = srvRead(s.e, func(d srvDirectory) error {
		g, err := d.Global()
		if err != nil {
			return err
		}

		s.global = newPCEntry(g)

		ent, found, err := d.Server(host)
		if found {
			s.server = newPCEntry(ent)
		}

		return err
	})
}

// need fails when the server entry cannot be read: checkPackage of the
// script ("Error: No services found in KEY on HOST", exit 1; a failed LDAP
// connection died with 255).
func (s *pcState) need(key string) *pcExit {
	if s.loadErr != nil {
		return &pcExit{code: 255, err: fmt.Sprintf("Error connecting to LDAP server: %v\n", s.loadErr)}
	}

	if s.server == nil {
		return &pcExit{code: 1, out: fmt.Sprintf("Error: No services found in %s on %s\n", key, s.host)}
	}

	return nil
}

// effective is an attribute of the server with the global config as default
// (what zmprov gs prints).
func (s *pcState) effective(attr string) string {
	if s.server != nil {
		if v := s.server.cur.first(attr); v != "" {
			return v
		}
	}

	if s.global != nil {
		return s.global.cur.first(attr)
	}

	return ""
}

// sslToUp is $SSLToUp (zmproxyconfig 146-161): the current value, set by -u
// and -U.
func (s *pcState) sslToUp(o *ProxyConfigOptions) string {
	ssl := pcSSLRemain
	if v := s.effective(pcAttrSSLToUpstream); v != "" {
		ssl = v
	}

	switch {
	case o.NoUpstreamSSL:
		return srvFalseUpper
	case o.UpstreamSSL:
		return srvTrueUpper
	default:
		return ssl
	}
}

// pcHasPackage is checkPackage's match: the service names of
// %packageServiceMap.
func pcHasPackage(services []string, pkg string) bool {
	for _, svc := range services {
		if svc == pcServiceMailbox && pkg == PkgAppserver || svc == pcServiceProxy && pkg == srvPkgProxy {
			return true
		}
	}

	return false
}

// installedPair is isInstalled of the store and the proxy package.
func (s *pcState) installedPair(o *ProxyConfigOptions) (app, proxy bool, x *pcExit) {
	if o.Remote {
		if x := s.need("zimbraServiceInstalled"); x != nil {
			return false, false, x
		}

		svc := s.server.cur.values("zimbraServiceInstalled")

		return pcHasPackage(svc, PkgAppserver), pcHasPackage(svc, srvPkgProxy), nil
	}

	app, err := s.io.installed(PkgAppserver)
	if err != nil {
		return false, false, &pcExit{code: 1, err: fmt.Sprintf("Error: %v\n", err)}
	}

	proxy, err = s.io.installed(srvPkgProxy)
	if err != nil {
		return false, false, &pcExit{code: 1, err: fmt.Sprintf("Error: %v\n", err)}
	}

	return app, proxy, nil
}

// runSet is the script from the option checks to the end of the zmprov
// commands (zmproxyconfig 55-536), without writing.
func (s *pcState) runSet(o *ProxyConfigOptions) int {
	var usage *pcUsageError
	if err := o.Check(); errors.As(err, &usage) {
		return s.usage(usage.msg)
	}

	s.load(o.Host)

	ssl := s.sslToUp(o)
	if ssl == srvTrueUpper && o.Enable && slices.Contains([]string{pcModeHTTP, pcModeBoth, "mixed"}, o.Mode) {
		return s.usage(fmt.Sprintf("Error: proxy mail mode %s is invalid when SSL to upstream is enabled\n", o.Mode))
	}

	if len(o.Extra) > 0 {
		return s.usage("")
	}

	if x := s.checkNode(o); x != nil {
		return s.exit(x)
	}

	app, proxy, x := s.installedPair(o)
	if x != nil {
		return s.exit(x)
	}

	if !app && !proxy {
		return s.exit(&pcExit{code: 1, err: "Error: The store or proxy package must be installed.\n"})
	}

	if o.Full {
		s.fullReset()
	}

	return s.apply(o, app, proxy, ssl)
}

// checkNode refuses a host that is not this node without -r (179-182).
func (s *pcState) checkNode(o *ProxyConfigOptions) *pcExit {
	local, err := s.e.LCValue("zimbra_server_hostname")
	if err != nil {
		return &pcExit{code: 1, err: fmt.Sprintf("Error: %v\n", err)}
	}

	if !strings.EqualFold(o.Host, local) && !o.Remote {
		return &pcExit{code: 1, out: "Must use -r option to modify a remote host.\n"}
	}

	return nil
}

// fullReset is -f (192-214): the global attributes, one mcf after another.
func (s *pcState) fullReset() {
	if s.global == nil {
		return
	}

	for _, kv := range pcFullReset {
		s.global.setAttr(kv.attr, kv.val)
	}
}

// apply plans the commands of o and applies them to the server in memory;
// it returns 2 when any failed, as zmprov did.
func (s *pcState) apply(o *ProxyConfigOptions, app, proxy bool, ssl string) int {
	var services []string
	if s.server != nil {
		services = slices.Clone(s.server.cur.values(pcAttrServiceEnabled))
	}

	p := &pcPlanner{
		o: o, app: app, proxy: proxy, ssl: ssl, mode: pcMailModeOf(s.effective(pcAttrMailMode)),
		enabled: func(pkg string) (bool, error) {
			if x := s.need(pcAttrServiceEnabled); x != nil {
				return false, x
			}

			return pcHasPackage(services, pkg), nil
		},
	}

	cmds, err := p.plan()
	if err != nil {
		if x, ok := errors.AsType[*pcExit](err); ok {
			return s.exit(x)
		}

		return s.exit(&pcExit{code: 1, err: fmt.Sprintf("Error: %v\n", err)})
	}

	code := 0

	for _, c := range cmds {
		if err := s.applyMS(c); err != nil {
			_, _ = fmt.Fprintf(s.io.err, "ERROR: %v\n", err)

			code = 2
		}
	}

	return code
}

func pcMailModeOf(v string) string {
	if v == "" {
		return pcModeHTTP
	}

	return v
}

// pcPortAttrs are the attributes of type port zmproxyconfig sets.
var pcPortAttrs = []string{
	srvAttrImapBind, pcAttrImapProxy, srvAttrImapSSL, pcAttrImapSSLProxy,
	srvAttrPop3Bind, pcAttrPop3Proxy, srvAttrPop3SSL, pcAttrPop3SSLProxy,
	pcAttrMailPort, srvAttrMailProxy, pcAttrMailSSLPort, srvAttrMailSSLProxy,
	pcAttrAdminPort, pcAttrAdminProxyPort,
}

// pcEnums are the enumerated attributes zmproxyconfig sets, with the values of
// the "value" in attrs.xml. The proxy mail mode has no http and mixed: a run
// that sends one (the default of -w -d without -x, or -x http) is refused by
// zmprov, as it was for the script.
var pcEnums = map[string][]string{
	pcAttrMailMode:      {pcModeHTTP, pcModeHTTPS, pcModeBoth, "mixed", "redirect"},
	pcAttrProxyMailMode: {pcModeHTTPS, "redirect", pcModeBoth},
	pcAttrReferMode:     {"always", "wronghost", "reverse-proxied"},
}

// pcValidate is the value check of zmprov (checkValue) for the attributes of a
// command; an enum fails like LdapAttributeInfo.checkValue.
func pcValidate(c pcCmd) error {
	for _, kv := range c {
		attr := strings.TrimLeft(kv.attr, "+-")

		if enum, ok := pcEnums[attr]; ok {
			if !slices.Contains(enum, kv.val) {
				return fmt.Errorf("account.INVALID_ATTR_VALUE (%s must be one of: %s)", attr, strings.Join(enum, ","))
			}

			continue
		}

		if !pcValidValue(attr, kv.val) {
			return fmt.Errorf("service.INVALID_REQUEST (invalid request: invalid value for %s: %s)", attr, kv.val)
		}
	}

	return nil
}

func pcValidValue(attr, val string) bool {
	switch {
	case attr == pcAttrServiceEnabled:
		return true
	case slices.Contains(pcPortAttrs, attr):
		return pcIsPort(val)
	case strings.HasSuffix(attr, "Enabled") || attr == pcAttrLookupTarget:
		return val == srvTrueUpper || val == srvFalseUpper
	default:
		return true
	}
}

// pcIsPort is the zmprov port type: an integer from 0 to 65535.
func pcIsPort(v string) bool {
	if v == "" || len(v) > 5 || strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return false
	}

	n := 0
	for _, r := range v {
		n = n*10 + int(r-'0')
	}

	return n <= 65535
}

// applyMS is "ms <host> ..." on the in-memory server: the value checks of
// zmprov, then the changes. A failure leaves the server as it was; the
// attribute callbacks run when RunProv gives the whole change to zmprov.
func (s *pcState) applyMS(c pcCmd) error {
	if s.loadErr != nil {
		return fmt.Errorf("service.FAILURE (system failure: %w)", s.loadErr)
	}

	if s.server == nil {
		return fmt.Errorf("account.NO_SUCH_SERVER (no such server: %s)", s.host)
	}

	if err := pcValidate(c); err != nil {
		return err
	}

	for _, kv := range c {
		switch {
		case strings.HasPrefix(kv.attr, "+"):
			s.server.addService(kv.val)
		case strings.HasPrefix(kv.attr, "-"):
			s.server.delService(kv.val)
		default:
			s.server.setAttr(kv.attr, kv.val)
		}
	}

	return nil
}

// commit gives the changes of the run to RunProv: one "mcf" for the global
// config and one "ms" for the server, each with every change of the batch, so
// zmprov's attribute callbacks (CheckPortConflict, MailPort, LocalBind) see the
// whole set. RunProv writes a command of allowlisted attributes to LDAP and
// sends the others to zmprov.
func (s *pcState) commit() int {
	var p Prov

	if s.global != nil {
		s.provCmd(&p, []string{"mcf"}, s.global)
	}

	if s.server != nil {
		s.provCmd(&p, []string{"ms", s.host}, s.server)
	}

	if p.Len() == 0 {
		return 0
	}

	if err := pcRunProv(s.e, &p); err != nil {
		_, _ = fmt.Fprintf(s.io.err, "%v\n", err)

		return 2
	}

	return 0
}

// provCmd appends the command verb (and its target) with the changes of en, if
// there are any.
func (s *pcState) provCmd(p *Prov, verb []string, en *pcEntry) {
	add, del, replace := en.changes()
	if len(add)+len(del)+len(replace) == 0 {
		return
	}

	args := slices.Clone(verb)

	for _, attr := range slices.Sorted(maps.Keys(replace)) {
		args = append(args, attr, replace[attr][0])
	}

	for _, v := range add[pcAttrServiceEnabled] {
		args = append(args, "+"+pcAttrServiceEnabled, v)
	}

	for _, v := range del[pcAttrServiceEnabled] {
		args = append(args, "-"+pcAttrServiceEnabled, v)
	}

	p.Add(args...)
	s.e.Log.Detail("zmproxyconfig: %s", strings.Join(args, " "))
}

// warnVirtualIPs is warnDeprecatedVirtualIPs (646-693); a problem reading
// LDAP is a warning too.
func (s *pcState) warnVirtualIPs() {
	err := srvRead(s.e, func(d srvDirectory) error {
		msgs, err := srvVirtualIPWarnings(d)
		for _, m := range msgs {
			_, _ = fmt.Fprintf(s.io.err, "Warning: %s\n", m)
		}

		return err
	})
	if err != nil {
		_, _ = fmt.Fprintf(s.io.err, "Warning: unable to check domains for deprecated zimbraVirtualIPAddress: %v\n", err)
	}
}
