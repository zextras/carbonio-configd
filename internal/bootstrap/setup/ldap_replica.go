// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"strings"
)

const (
	ldapEnableReplicaUsage = `  Usage: zmldapenablereplica

  Use zmldapenablereplica to set up this server as an ldap replica
  or to enable the replication database on the master

  zmldapenablereplica must have been run on the master prior to being
  run on any replicas.  It is only necessary to run zmldapenablereplica
  once on the master.

`
	// ldapReplicaRID is the replication id of the single-provider replica.
	ldapReplicaRID = "100"
)

// The ends of the lines the script printed for a step.
const (
	ldapMsgSucceeded = "succeeded\n"
	ldapMsgDone      = "done\n"
)

// ldapReplica is a run of zmldapenablereplica (libexec/ldapenablereplica.pl).
type ldapReplica struct {
	e    *Env
	k    *ldapKit
	out  io.Writer
	lc   map[string]string
	lmr  string
	host string
}

// ldapEnableReplica makes this node the provider of the replication (when it
// is the master) or a replica of the master, printing what the script printed
// to out. The adds of the provider that the script left unchecked (a typo
// dropped the result of two of them) and the zmprov call of the last step are
// checked here.
func ldapEnableReplica(e *Env, k *ldapKit, out io.Writer) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	lmr := ""
	if f := strings.Fields(lc[ldapLCMasterURL]); len(f) > 0 {
		lmr = f[0]
	}

	r := &ldapReplica{e: e, k: k, out: out, lc: lc, lmr: lmr, host: lc["zimbra_server_hostname"]}

	if strings.EqualFold(ldapURLHost(lmr), r.host) {
		return r.enableProvider()
	}

	return r.enableReplica()
}

// enableProvider is the master: it gets the accesslog database and the
// provider overlays.
func (r *ldapReplica) enableProvider() error {
	conn, err := ldapLocalConn(r.e, r.k, r.lc[ldapLCRoot])
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := r.verifyNotEnabled(conn); err != nil {
		return err
	}

	ldapPrintf(r.out, "Enabling sync provider on master...\n")

	if err := ldapEnableAccesslog(r.e, conn); err != nil {
		return err
	}

	ldapPrintf(r.out, ldapMsgSucceeded)

	return nil
}

// verifyNotEnabled refuses a node that has the accesslog database or the
// syncprov overlay already.
func (r *ldapReplica) verifyNotEnabled(conn ldapConn) error {
	exists, err := ldapAccesslogExists(conn)
	if err != nil {
		return err
	}

	if exists {
		ldapPrintf(r.out, "Accesslog is already enabled. Exiting...\n")

		return ldapRefused()
	}

	enabled, err := ldapSyncProvEnabled(conn)
	if err != nil {
		return err
	}

	if enabled {
		ldapPrintf(r.out, "Error: Syncprov already enabled. Exiting...\n")

		return ldapRefused()
	}

	return nil
}

// enableReplica is any other node: it points ldap_url at itself and the master,
// starts slapd, and adds the consumer configuration.
func (r *ldapReplica) enableReplica() error {
	if err := r.verifyMaster(); err != nil {
		return err
	}

	r.updateLdapHost()

	if err := r.startLdap(); err != nil {
		return err
	}

	conn, err := ldapLocalConn(r.e, r.k, r.lc[ldapLCRoot])
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := r.verifyNotEnabled(conn); err != nil {
		return err
	}

	if err := r.createLdapConfig(conn); err != nil {
		return err
	}

	return r.enableLdapService()
}

// verifyMaster binds to the master as the Carbonio admin before anything is
// changed.
func (r *ldapReplica) verifyMaster() error {
	ldapPrintf(r.out, "Verifying ldap on %s...\n", r.lmr)

	d := ldapDial{URLs: []string{r.lmr}}
	if !strings.HasPrefix(strings.ToLower(r.lmr), "ldaps") && ldapTruthy(r.lc["ldap_starttls_supported"]) {
		d.TLS = ldapTLSInsecure
	}

	conn, err := r.k.dial(r.e, d)
	if err != nil {
		return ldapDie("%v", err)
	}
	defer conn.Close()

	if err := conn.Bind(r.lc["zimbra_ldap_userdn"], r.lc[ldapLCAdmin]); err != nil {
		return ldapDie("ERROR: Unable to verify ldap connection on %s - exiting", r.lmr)
	}

	ldapPrintf(r.out, ldapMsgSucceeded)

	return nil
}

// updateLdapHost sets ldap_url to this node first and the master second. The
// script ignored a failure of zmlocalconfig; it is logged here.
func (r *ldapReplica) updateLdapHost() {
	proto := ldapSchemeLDAP
	if r.lc["ldap_port"] == "636" {
		proto = "ldaps"
	}

	ldapPrintf(r.out, "Setting ldap_url on %s...", r.host)
	r.setURL(fmt.Sprintf("%s://%s:%s %s", proto, r.host, r.lc["ldap_port"], r.lmr))
	ldapPrintf(r.out, ldapMsgDone)
}

func (r *ldapReplica) setURL(url string) {
	if err := r.e.SetLC(map[string]string{ldapLCURL: url}); err != nil {
		r.e.Log.Detail("setting %s: %v", ldapLCURL, err)
	}
}

// resetLdapUrl gives ldap_url the master only.
func (r *ldapReplica) resetLdapURL() {
	ldapPrintf(r.out, "Resetting ldap_url on %s...", r.lmr)
	r.setURL(r.lmr)
	ldapPrintf(r.out, ldapMsgDone)
}

// startLdap starts slapd unless it runs; when it cannot, ldap_url is reset.
func (r *ldapReplica) startLdap() error {
	ldapPrintf(r.out, "Starting LDAP on %s...", r.host)

	if err := r.k.start(r.e); err != nil {
		r.e.Log.Detail("%v", err)
		ldapPrintf(r.out, "Error: Unable to start ldap, exiting.\n")
		r.resetLdapURL()

		return ldapRefused()
	}

	ldapPrintf(r.out, ldapMsgDone)

	return nil
}

// createLdapConfig makes the main database a consumer of the master and gives
// it the syncprov overlay.
func (r *ldapReplica) createLdapConfig(conn ldapConn) error {
	ldapPrintf(r.out, "Enabling sync replication on replica...")

	value := ldapSyncrepl(r.e, ldapReplicaRID, r.lmr, r.lc["ldap_replication_password"],
		ldapSyncreplTLS(r.lmr, r.lc))

	err := conn.Modify(ldapDNMdbDB, []ldapMod{
		{Op: ldapModAdd, Attr: ldapAttrSyncrepl, Values: []string{value}},
		{Op: ldapModAdd, Attr: "olcUpdateRef", Values: []string{r.lmr}},
	})
	if err != nil {
		ldapPrintf(r.out, "FAILED\nERROR: Unable to add syncrepl configuration to %s - exiting\nERROR: %s\n",
			r.host, ldapErrText(err))
		r.resetLdapURL()

		return ldapRefused()
	}

	err = conn.Add("olcOverlay={0}syncprov,"+ldapDNMdbDB, []ldapLDIFAttr{
		{Name: "objectClass", Values: []string{"olcOverlayConfig", "olcSyncProvConfig"}},
		{Name: "olcOverlay", Values: []string{"{0}syncprov"}},
	})
	if err != nil {
		ldapPrintf(r.out, "FAILEDERROR: Unable to add syncprov overlay to %s - exiting\nERROR: %s\n",
			r.host, ldapErrText(err))
		r.resetLdapURL()

		return ldapRefused()
	}

	ldapPrintf(r.out, ldapMsgSucceeded)

	return nil
}

// enableLdapService adds ldap to the services of this server.
func (r *ldapReplica) enableLdapService() error {
	ldapPrintf(r.out, "Enabling LDAP service on %s...", r.host)

	if err := r.e.Prov("ms", r.host, "+zimbraServiceEnabled", "ldap"); err != nil {
		return err
	}

	ldapPrintf(r.out, ldapMsgSucceeded)

	return nil
}

// RunLDAPEnableReplica is zmldapenablereplica: -h/--help prints the usage and
// exits 1, as does any other option; it returns the exit status of the script.
func RunLDAPEnableReplica(e *Env, args []string, stdout, stderr io.Writer) int {
	if !ldapOpenLDAPInstalled(e) {
		_, _ = fmt.Fprintln(stdout, "ERROR: openldap does not appear to be installed - exiting")

		return ldapExitRefused
	}

	o := parseGetoptLong(args, []optSpec{{names: []string{"h", optHelp}}}, stderr)
	if !o.allGood {
		_, _ = fmt.Fprintln(stderr)
	}

	if !o.allGood || o.has("h") {
		_, _ = io.WriteString(stderr, ldapEnableReplicaUsage)

		return ldapExitRefused
	}

	return ldapToolStatus(ldapEnableReplica(e, ldapToolKit(e), stdout), stderr)
}
