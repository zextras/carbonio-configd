// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/getopt"
)

//nolint:lll // the text of the script
const ldapEnableMMRUsage = `  zmldapenable-mmr [-r RID] [-s SID] -m MMRURI

  Where:
  RID is a unique Integer Replication ID for this replication instance.  It must be unique inside this server.  Example: 100 Default: 100
  SID is an unique Integer Server ID for THIS LDAP Master.  It CANNOT be in use by any other LDAP master.  Example: 2
  MMRURI is the LDAP URI for an alternate master.  Example: ldap://ldap-master2.example.com:389/

`

// ldapMMROptions are the options of zmldapenable-mmr: the server id (0 when
// none was given), the replication id and the URL of the other master.
type ldapMMROptions struct {
	SID, RID int
	URI      string
}

// ldapMMRDB is the main database once the accesslog database has been created
// in front of it.
const ldapMMRDB = "olcDatabase={3}mdb,cn=config"

// ldapEnableMMR is zmldapenable-mmr (libexec/ldapenable-mmr.pl): on a master it
// sets the server id, creates the accesslog database when the master has none
// and adds a syncrepl agreement with the master at o.URI, switching the main
// database to mirror mode. Nothing is printed on success. The adds and the
// syncrepl modify, whose result the script never looked at, are checked here.
func ldapEnableMMR(e *Env, k *ldapKit, o ldapMMROptions, out io.Writer) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	if !strings.EqualFold(lc[ldapLCIsMaster], ldapTrue) {
		ldapPrintf(out, "Not a master, please use zmldappromote-replica-mmr\n")

		return nil
	}

	conn, err := ldapLocalConn(e, k, lc[ldapLCRoot])
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := ldapMMRServerID(conn, o, out); err != nil {
		return err
	}

	exists, err := ldapAccesslogExists(conn)
	if err != nil {
		return err
	}

	if !exists {
		if err := ldapEnableAccesslog(e, conn); err != nil {
			return err
		}
	}

	return ldapMMRAgreement(e, conn, o, lc)
}

// ldapMMRServerID sets the server id of the master, or checks that what the
// run was given fits what it has: a master without an id needs one, a master
// with one takes none, and the replication id must be free.
func ldapMMRServerID(conn ldapConn, o ldapMMROptions, out io.Writer) error {
	ids, err := conn.Search(ldapConfigDN, "(&(objectClass=olcGlobal)(olcServerID=*))", true, []string{"olcServerID"})
	if err != nil {
		return ldapSearchError(err)
	}

	if len(ids) == 0 {
		if o.SID <= 0 {
			ldapPrintf(out, "Error: No Server ID exists on master and none provided.\n"+
				"       Provide valid Server ID value via the -s option.\n")

			return ldapRefused()
		}

		err := conn.Modify(ldapConfigDN, []ldapMod{
			{Op: ldapModAdd, Attr: "olcServerID", Values: []string{strconv.Itoa(o.SID)}},
		})
		if err != nil {
			return ldapDie("Server ID: %s", ldapErrText(err))
		}

		return nil
	}

	if o.SID > 0 {
		ldapPrintf(out, "Error: Server ID provided when the Server ID is already configured.\n")

		return ldapRefused()
	}

	return ldapMMRCheckRID(conn, o.RID, out)
}

var ldapRIDRE = regexp.MustCompile(`^[^ ]*`)

// ldapMMRCheckRID refuses a replication id that an agreement of the main
// database has.
func ldapMMRCheckRID(conn ldapConn, rid int, out io.Writer) error {
	agreements, err := conn.Search(ldapMMRDB, "(olcSyncrepl=*)", true, []string{ldapAttrSyncrepl})
	if err != nil {
		return ldapSearchError(err)
	}

	if len(agreements) == 0 {
		return nil
	}

	for _, v := range agreements[0].ldapAttr(ldapAttrSyncrepl) {
		_, after, found := strings.Cut(v, "rid=")
		if !found {
			continue
		}

		// A value that is not a number counts as 0, as in Perl's ==.
		if n, _ := strconv.Atoi(ldapRIDRE.FindString(after)); n == rid {
			ldapPrintf(out, "Error: A replication agreement for rid %d already exists.\n", rid)

			return ldapRefused()
		}
	}

	return nil
}

// ldapMMRAgreement adds the syncrepl agreement with the other master and
// turns the mirror mode on.
func ldapMMRAgreement(e *Env, conn ldapConn, o ldapMMROptions, lc map[string]string) error {
	value := ldapSyncrepl(e, strconv.Itoa(o.RID), o.URI, lc["ldap_replication_password"], ldapSyncreplTLS(o.URI, lc))

	err := conn.Modify(ldapMMRDB, []ldapMod{{Op: ldapModAdd, Attr: ldapAttrSyncrepl, Values: []string{value}}})
	if err != nil {
		return ldapDie("Syncrepl: %s", ldapErrText(err))
	}

	mirror, err := conn.Search(ldapMMRDB, "(&(objectClass=olcMdbConfig)(olcMirrorMode:booleanMatch:=TRUE))", true,
		[]string{"olcMirrorMode"})
	if err != nil {
		return ldapSearchError(err)
	}

	if len(mirror) > 0 {
		return nil
	}

	err = conn.Modify(ldapMMRDB, []ldapMod{{Op: ldapModReplace, Attr: "olcMirrorMode", Values: []string{"TRUE"}}})
	if err != nil {
		return ldapDie("Mirror mode: %s", ldapErrText(err))
	}

	return nil
}

var ldapMMRURIRE = regexp.MustCompile(`^ldaps?://`)

// RunLDAPEnableMMR is zmldapenable-mmr: -h/--help, -s/--sid N, -m/--mmr URI
// (required, ending in a slash) and -r/--rid N (100 by default). A refused
// command line prints the usage on stderr and exits 1.
func RunLDAPEnableMMR(e *Env, args []string, stdout, stderr io.Writer) int {
	if !ldapOpenLDAPInstalled(e) {
		_, _ = fmt.Fprintln(stdout, "ERROR: openldap does not appear to be installed - exiting")

		return ldapExitRefused
	}

	o := getopt.Parse(args, []getopt.Spec{
		{Names: []string{"h", getopt.Help}},
		{Names: []string{"s", "sid"}, Kind: getopt.Int},
		{Names: []string{"m", "mmr"}, Kind: getopt.String},
		{Names: []string{"r", "rid"}, Kind: getopt.Int},
	}, stderr)
	if !o.AllGood {
		_, _ = fmt.Fprintln(stderr)
	}

	opts := ldapMMROptions{SID: o.Ints["s"], RID: 100, URI: o.Strs["m"]}
	if o.Has("r") {
		opts.RID = o.Ints["r"]
	}

	if !o.AllGood || o.Has("h") || !ldapMMRURIRE.MatchString(opts.URI) || !strings.HasSuffix(opts.URI, "/") {
		_, _ = io.WriteString(stderr, ldapEnableMMRUsage)

		return ldapExitRefused
	}

	return ldapToolStatus(ldapEnableMMR(e, ldapToolKit(e), opts, stdout), stderr)
}
