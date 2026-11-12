// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

const ipToolUsage = `  zmiptool [-d [-m ipmode]]

  Where:
  -d: dry run   Do not make any changes, just print what would be done.
  -m: IP Mode to test for if dryrun is enabled.  Optional

`

const (
	ipV4Stack     = "-Djava.net.preferIPv4Stack=true"
	ipV6Addresses = "-Djava.net.preferIPv6Addresses=true"
)

// ipJava is what an IP mode does to a Java option.
type ipJava int

const (
	ipRemove ipJava = iota
	ipEnsure
)

// ipMode is what zmiptool does for a value of zimbraIPMode: the DNS records
// that must exist, the Java options and the address MySQL listens on.
type ipMode struct {
	records     []ipRecord
	v4Stack     ipJava
	v6Addresses ipJava
	bindAddress string
}

// ipRecord is a DNS record type with the network a lookup of it asks for.
type ipRecord struct{ name, network string }

var (
	ipRecordA    = ipRecord{"A", "ip4"}
	ipRecordAAAA = ipRecord{"AAAA", "ip6"}
)

// ipModes are the values of zimbraIPMode that zmiptool knows.
var ipModes = map[string]ipMode{
	"ipv4": {
		records: []ipRecord{ipRecordA}, v4Stack: ipEnsure, v6Addresses: ipRemove, bindAddress: "127.0.0.1",
	},
	"both": {
		records: []ipRecord{ipRecordA, ipRecordAAAA}, v4Stack: ipRemove, v6Addresses: ipRemove, bindAddress: "::1",
	},
	"ipv6": {
		records: []ipRecord{ipRecordAAAA}, v4Stack: ipRemove, v6Addresses: ipEnsure, bindAddress: "::1",
	},
}

// ipJavaOptions applies the mode to a Java options string: an option to
// ensure is appended when missing, one to remove is cut out with the blank
// before it.
func ipJavaOptions(opts string, m ipMode) string {
	for _, o := range []struct {
		opt  string
		what ipJava
	}{{ipV4Stack, m.v4Stack}, {ipV6Addresses, m.v6Addresses}} {
		has := strings.Contains(opts, o.opt)

		switch {
		case o.what == ipEnsure && !has:
			opts += " " + o.opt
		case o.what == ipRemove && has:
			opts = strings.Replace(opts, " "+o.opt, "", 1)
		}
	}

	return opts
}

// ipIsLoopback is the test of zmiptool for a MySQL address it may change: none
// or one of the loopback addresses.
func ipIsLoopback(addr string) bool {
	return addr == "" || strings.Contains(addr, "localhost") || strings.Contains(addr, "::1") ||
		strings.Contains(addr, "127.0.0.1")
}

// ipLookup resolves host for the DNS record type of a mode.
type ipLookup func(ctx context.Context, network, host string) ([]net.IP, error)

// ipToolRun is a run of zmiptool.
type ipToolRun struct {
	e      *Env
	out    io.Writer
	errw   io.Writer
	dryRun bool
	lookup ipLookup
	// failed is the first failure to write the local configuration.
	failed error
}

// ipToolOptions are the options of zmiptool: -d and the mode of -m.
type ipToolOptions struct {
	DryRun bool
	Mode   string
}

// ipTool is zmiptool (libexec/iptool.pl): it reads zimbraIPMode of this server
// (or takes the mode of -m in a dry run), checks that the host resolves, and
// makes the local configuration and the MySQL configuration files agree with
// the mode: the IPv4 and IPv6 Java options, and the address MySQL binds to. A
// dry run prints what would be set. The ini edits that zmiptool had zminiutil
// make are done by iniApply.
//
// The local configuration is the effective one (the script read the raw values
// of the Java options through zmlocalconfig and the expanded values of the
// rest); a failure to write it is returned after the rest was done, where the
// script ignored it.
func ipTool(e *Env, k *ldapKit, o ipToolOptions, lookup ipLookup, out, errw io.Writer) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	conn, err := ipToolConn(e, k, lc)
	if err != nil {
		return err
	}
	defer conn.Close()

	host := lc["zimbra_server_hostname"]
	mode := o.Mode

	if mode == "" {
		if mode, err = ipServerMode(conn, host, out); err != nil {
			return err
		}
	}

	store, err := ipServerHas(conn, host, "mailbox")
	if err != nil {
		return err
	}

	mta, err := ipServerHas(conn, host, "mta")
	if err != nil {
		return err
	}

	m, ok := ipModes[mode]
	if !ok {
		ldapPrintf(out, "Error: Unknown IP mode %s\n", mode)

		return ldapRefused()
	}

	t := &ipToolRun{e: e, out: out, errw: errw, dryRun: o.DryRun, lookup: lookup}

	return t.apply(host, m, lc, store, mta)
}

// ipToolConn connects to the servers of ldap_url (StartTLS, not verified, when
// the node supports it and the URL is not ldaps) and binds as the admin.
func ipToolConn(e *Env, k *ldapKit, lc map[string]string) (ldapConn, error) {
	url := lc[ldapLCURL]
	d := ldapDial{URLs: strings.Fields(url)}

	if ldapTruthy(lc["ldap_starttls_supported"]) && !ldapURLHasScheme(url, "ldaps") {
		d.TLS = ldapTLSInsecure
	}

	conn, err := k.dial(e, d)
	if err != nil {
		return nil, ldapDie("Error connecting to LDAP server: %s", url)
	}

	if err := conn.Bind(lc["zimbra_ldap_userdn"], lc[ldapLCAdmin]); err != nil {
		conn.Close()

		return nil, ldapDie("Bind: %s", ldapErrText(err))
	}

	return conn, nil
}

// ipServerMode reads zimbraIPMode on the entry of the server.
func ipServerMode(conn ldapConn, host string, out io.Writer) (string, error) {
	entries, err := conn.Search("", "(&(objectClass=zimbraServer)(cn="+goldap.EscapeFilter(host)+"))", true,
		[]string{"zimbraIPMode"})
	if err != nil {
		return "", ldapDie("%s", ldapErrText(err))
	}

	if len(entries) == 0 {
		ldapPrintf(out, "Error: Server %s not found\n", host)

		return "", ldapRefused()
	}

	if v := entries[0].ldapAttr("zimbraIPMode"); len(v) > 0 {
		return v[0], nil
	}

	return "", nil
}

// ipServerHas tells whether the server entry lists service.
func ipServerHas(conn ldapConn, host, service string) (bool, error) {
	filter := "(&(objectClass=zimbraServer)(cn=" + goldap.EscapeFilter(host) + ")(zimbraServiceEnabled=" + service + "))"

	entries, err := conn.Search("", filter, true, []string{"1.1"})
	if err != nil {
		return false, ldapDie("%s", ldapErrText(err))
	}

	return len(entries) > 0, nil
}

// apply does what the mode asks on this node.
func (t *ipToolRun) apply(host string, m ipMode, lc map[string]string, store, mta bool) error {
	for _, r := range m.records {
		if ips, err := t.lookup(t.e.Ctx, r.network, host); err != nil || len(ips) == 0 {
			ldapPrintf(t.out, "Error: Unable to resolve %s %s record\n", host, r.name)
		}
	}

	if err := t.javaOptions("zimbra_zmjava_options", m); err != nil {
		return err
	}

	if store {
		if err := t.javaOptions("mailboxd_java_options", m); err != nil {
			return err
		}

		if ipIsLoopback(lc["mysql_bind_address"]) {
			t.bindAddress("mysql_bind_address", lc["mysql_mycnf"], m.bindAddress, false)
		}
	}

	if mta && ipIsLoopback(lc["antispam_mysql_host"]) {
		t.bindAddress("antispam_mysql_host", lc["antispam_mysql_mycnf"], m.bindAddress, true)
	}

	return t.failed
}

// javaOptions sets a local configuration key holding Java options. The value
// is the raw one: setting what an expansion gave would write the expanded
// text back.
func (t *ipToolRun) javaOptions(key string, m ipMode) error {
	raw, err := ipRawLC(t.e, key)
	if err != nil {
		return err
	}

	t.setLC(key, ipJavaOptions(raw, m))

	return nil
}

// ipRawLC is the unexpanded value of a local configuration key, as zmlocalconfig
// -s prints it: what localconfig.xml holds, else the default of the key.
func ipRawLC(e *Env, key string) (string, error) {
	configured, err := localconfig.LoadLocalConfigFromFile(e.Paths.LocalConfig)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	v, _ := localconfig.NewLegacy(configured).Raw(key)

	return v, nil
}

// setLC sets a local configuration key, or says in a dry run that it would.
func (t *ipToolRun) setLC(key, val string) {
	if t.dryRun {
		ldapPrintf(t.out, "Would set %s to %s\n", key, val)

		return
	}

	ldapPrintf(t.out, "Setting local config %s=%s\n", key, val)

	if err := t.e.SetLC(map[string]string{key: val}); err != nil && t.failed == nil {
		t.failed = fmt.Errorf("setting %s: %w", key, err)
	}
}

// bindAddress sets the address MySQL binds to, and in the MySQL configuration
// file, as zminiutil did: the bind-address of [mysqld] is removed (backup
// ".pre"), then set (backup ".pre-bind"). The file of the antispam MySQL is only
// edited when it exists (onlyIfFile).
func (t *ipToolRun) bindAddress(key, mycnf, addr string, onlyIfFile bool) {
	if t.dryRun {
		ldapPrintf(t.out, "Would set %s to %s\n", key, addr)

		return
	}

	t.setLC(key, addr)

	if onlyIfFile {
		if info, err := t.e.Sys.FS.Stat(mycnf); err != nil || !info.Mode().IsRegular() {
			return
		}
	}

	for _, ed := range []*iniEdit{
		{Op: iniUnset, Section: "mysqld", Key: "bind-address", Backup: ".pre", HasBackup: true},
		{Op: iniSet, Section: "mysqld", Key: "bind-address", Value: addr, HasValue: true,
			Backup: ".pre-bind", HasBackup: true},
	} {
		if _, err := iniApply(t.e.Sys.FS, mycnf, ed, t.out, nil); err != nil {
			// zminiutil printed its failure and zmiptool went on.
			_, _ = fmt.Fprintln(t.errw, err)
		}
	}
}

// ipToolOptionSpecs are the options of zmiptool.
var ipToolOptionSpecs = []optSpec{
	{names: []string{"h", "help"}},
	{names: []string{"d", "dryrun"}},
	{names: []string{"m", "mode"}, kind: optString},
}

// RunIPTool is zmiptool: -h/--help, -d/--dryrun and, with -d, -m/--mode
// (ipv4, both or ipv6). It returns the exit status of the script; a refused
// command line prints the usage on stderr and exits 1.
func RunIPTool(e *Env, args []string, stdout, stderr io.Writer) int {
	o := parseGetoptLong(args, ipToolOptionSpecs, stderr)
	if !o.allGood {
		_, _ = fmt.Fprintln(stderr)
	}

	// Perl's truth: a mode of "" or "0" is no mode.
	mode := o.strs["m"]
	if mode == "0" {
		mode = ""
	}

	_, known := ipModes[mode]

	if !o.allGood || o.has("h") || (mode != "" && (!o.has("d") || !known)) {
		_, _ = io.WriteString(stderr, ipToolUsage)

		return ldapExitRefused
	}

	opts := ipToolOptions{DryRun: o.has("d"), Mode: mode}

	return ldapToolStatus(ipTool(e, ldapToolKit(e), opts, net.DefaultResolver.LookupIP, stdout, stderr), stderr)
}
