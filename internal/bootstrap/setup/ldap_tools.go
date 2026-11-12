// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/services"
)

// The exit statuses the LDAP scripts end with: 1 for what they refuse after
// printing why, 255 for the failures they died of (Perl's die with no errno).
const (
	ldapExitRefused = 1
	ldapExitDied    = 255

	// ldapAttrNone asks a search for no attribute, only the entries.
	ldapAttrNone = "1.1"
	// ldapAttrSyncrepl holds the replication agreements of a database.
	ldapAttrSyncrepl = "olcSyncrepl"
	// ldapSchemeLDAP is the scheme of a plain LDAP URL.
	ldapSchemeLDAP = "ldap"
)

// ldapToolError is how an LDAP tool ends other than by succeeding: Code is
// the exit status and Msg, when not empty, the line the script died with
// (printed on stderr). What a script printed before exiting is already on its
// output.
type ldapToolError struct {
	Code int
	Msg  string
}

func (e *ldapToolError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}

	return fmt.Sprintf("exit status %d", e.Code)
}

// ldapRefused ends a tool after it printed why, with status 1.
func ldapRefused() error { return &ldapToolError{Code: ldapExitRefused} }

// ldapDie ends a tool like a Perl die: the message on stderr, status 255.
func ldapDie(format string, args ...any) error {
	return &ldapToolError{Code: ldapExitDied, Msg: fmt.Sprintf(format, args...)}
}

// ldapToolStatus prints the reason of err on stderr and returns the exit
// status of the tool: 0 for nil, the status of an ldapToolError, else 1.
func ldapToolStatus(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	if te, ok := errors.AsType[*ldapToolError](err); ok {
		if te.Msg != "" {
			_, _ = fmt.Fprintln(stderr, te.Msg)
		}

		return te.Code
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

	return ldapExitRefused
}

// ldapKit is the kit of an in-process tool: the one tests put on the Env, else
// the native one.
func (e *Env) ldapKit() *ldapKit {
	if e.kit != nil {
		return e.kit
	}

	return ldapNativeKit()
}

// ldapToolKit is the kit of a tool run from the command line: the service
// control the native kit needs is set up here, as a tool has no bootstrap run
// behind it.
func ldapToolKit(e *Env) *ldapKit {
	if e.kit != nil {
		return e.kit
	}

	if e.Ctl == nil {
		e.Ctl = services.NewController(e.Sys)
	}

	if e.Facts == nil {
		e.Facts = &Facts{Systemd: e.Ctl.IsSystemdMode()}
	}

	return ldapNativeKit()
}

// ldapToolOutput runs fn for a bootstrap step that calls a tool in-process:
// what the tool prints goes to the log file, and the error it ends with
// carries the last lines of it (as a command's CmdError did).
func ldapToolOutput(e *Env, fn func(out io.Writer) error) error {
	var tail strings.Builder

	lw := e.Log.Writer()
	err := fn(io.MultiWriter(&tail, lw))

	lw.Flush()

	if te, ok := errors.AsType[*ldapToolError](err); ok && te.Msg == "" {
		return fmt.Errorf("%s%s", te.Error(), lastLines(tail.String(), false))
	}

	return err
}

// ldapOpenLDAPInstalled is the check of the scripts that openldap is there.
func ldapOpenLDAPInstalled(e *Env) bool {
	info, err := e.Sys.FS.Stat(e.Paths.Path("common", "etc", "openldap", "schema"))

	return err == nil && info.IsDir()
}

// ldapPrintf writes to a script's output; the error is of no interest, as it
// was not to print.
func ldapPrintf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// ldapLocalConn opens the ldapi socket of the local slapd and binds as the
// cn=config root.
func ldapLocalConn(e *Env, k *ldapKit, rootPassword string) (ldapConn, error) {
	conn, err := k.dial(e, ldapDial{URLs: []string{ldapSocketURL}})
	if err != nil {
		return nil, ldapDie("%v", err)
	}

	if err := conn.Bind(ldapConfigDN, rootPassword); err != nil {
		conn.Close()

		return nil, ldapDie("Bind: %s", ldapErrText(err))
	}

	return conn, nil
}

// ldapAccesslogExists reports whether the accesslog database is there.
func ldapAccesslogExists(conn ldapConn) (bool, error) {
	entries, err := conn.Search("cn=accesslog", "(objectClass=*)", false, []string{ldapAttrNone})

	return len(entries) > 0, ldapSearchError(err)
}

// ldapSyncProvEnabled reports whether the main database has the syncprov
// overlay.
func ldapSyncProvEnabled(conn ldapConn) (bool, error) {
	entries, err := conn.Search(ldapDNMdbDB, "(olcOverlay=syncprov)", true, []string{ldapAttrNone})

	return len(entries) > 0, ldapSearchError(err)
}

// ldapSearchError is err, except that an entry that is not there is no error:
// the scripts counted the entries a search returned.
func ldapSearchError(err error) error {
	if err == nil || ldapIsNoSuchObject(err) {
		return nil
	}

	return ldapDie("%s", ldapErrText(err))
}

// ldapAccesslogDir is the directory of the accesslog database.
func ldapAccesslogDir(e *Env) string { return e.Paths.Path("data", "ldap", "accesslog") }

// ldapEnableAccesslog creates the accesslog database next to the main one
// (which becomes {3}mdb) and gives both the overlays of a replication
// provider. The scripts ignored a failure of the last two adds; it is
// reported here.
func ldapEnableAccesslog(e *Env, conn ldapConn) error {
	dir := ldapAccesslogDir(e)

	if err := e.Sys.FS.MkdirAll(filepath.Join(dir, "db"), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	if err := e.Root("chown", "-R", "zextras:zextras", dir); err != nil {
		return err
	}

	for _, en := range ldapAccesslogEntries(e) {
		if err := conn.Add(en.DN, en.Attrs); err != nil {
			return ldapDie("Add Failed: %s", ldapErrText(err))
		}
	}

	return nil
}

// ldapAccesslogEntries are the entries of the accesslog database and of the
// overlays that feed it.
func ldapAccesslogEntries(e *Env) []ldapLDIFEntry {
	a := func(name string, values ...string) ldapLDIFAttr { return ldapLDIFAttr{Name: name, Values: values} }

	return []ldapLDIFEntry{
		{DN: ldapDNMdbDB, Attrs: []ldapLDIFAttr{
			a("objectClass", "olcDatabaseConfig", "olcMdbConfig"),
			a("olcDatabase", "{2}mdb"),
			a("olcDbDirectory", filepath.Join(ldapAccesslogDir(e), "db")),
			a("olcSuffix", "cn=accesslog"),
			a("olcAccess", `{0}to dn.subtree="cn=accesslog"  by dn.exact="uid=zimbra,cn=admins,cn=zimbra" read `+
				` by dn.exact="cn=config" read  by dn.exact="uid=zmreplica,cn=admins,cn=zimbra" read`),
			a("olcLastMod", "TRUE"),
			a("olcMaxDerefDepth", "15"),
			a("olcReadOnly", "FALSE"),
			a("olcRootDN", "cn=config"),
			a("olcSizeLimit", "unlimited"),
			a("olcTimeLimit", "unlimited"),
			a("olcMonitoring", "TRUE"),
			a("olcDbCheckpoint", "0 0"),
			a("olcDbEnvFlags", "writemap", "nometasync"),
			a("olcDbNoSync", "TRUE"),
			a("olcDbIndex", "entryCSN eq", "objectClass eq", "reqEnd eq", "reqResult eq", "reqStart eq"),
			a("olcDbMode", "0600"),
			a("olcDbSearchStack", "16"),
			a("olcDbMaxsize", "85899345920"),
		}},
		{DN: "olcOverlay=syncprov," + ldapDNMdbDB, Attrs: []ldapLDIFAttr{
			a("objectClass", "olcOverlayConfig", "olcSyncProvConfig"),
			a("olcOverlay", "syncprov"),
			a("olcSpNoPresent", "TRUE"),
			a("olcSpReloadHint", "TRUE"),
		}},
		{DN: "olcOverlay={0}syncprov,olcDatabase={3}mdb,cn=config", Attrs: []ldapLDIFAttr{
			a("objectClass", "olcOverlayConfig", "olcSyncProvConfig"),
			a("olcOverlay", "{0}syncprov"),
			a("olcSpCheckpoint", "20 10"),
			a("olcSpSessionlog", "10000000"),
		}},
		{DN: "olcOverlay={1}accesslog,olcDatabase={3}mdb,cn=config", Attrs: []ldapLDIFAttr{
			a("objectClass", "olcOverlayConfig", "olcAccessLogConfig"),
			a("olcOverlay", "{1}accesslog"),
			a("olcAccessLogDB", "cn=accesslog"),
			a("olcAccessLogOps", "writes"),
			a("olcAccessLogSuccess", "TRUE"),
			a("olcAccessLogPurge", "01+00:00  00+04:00"),
		}},
	}
}

// ldapSyncrepl is the olcSyncrepl value of a consumer of provider with the
// replication account, rid being its replication id. startTLS adds
// "starttls=critical": the provider must be reached over TLS.
func ldapSyncrepl(e *Env, rid, provider, password string, startTLS bool) string {
	tls := ""
	if startTLS {
		tls = " starttls=critical"
	}

	return "rid=" + rid + " provider=" + provider + " bindmethod=simple timeout=0 network-timeout=0 binddn=" +
		ldapDNReplica + " credentials=" + password + tls + ` filter="(objectclass=*)" searchbase="" ` +
		`logfilter="(&(objectClass=auditWriteObject)(reqResult=0))" logbase=cn=accesslog scope=sub ` +
		`schemachecking=off type=refreshAndPersist retry="60 +" syncdata=accesslog tls_cacertdir=` +
		e.Paths.Path("conf", "ca") + ` keepalive=240:10:30`
}

// ldapSyncreplTLS tells whether the connection to provider must use StartTLS:
// not for ldaps://, else when the node supports it and requires interprocess
// security.
func ldapSyncreplTLS(provider string, lc map[string]string) bool {
	if strings.HasPrefix(provider, "ldaps:") {
		return false
	}

	return ldapTruthy(lc["ldap_starttls_supported"]) && ldapTruthy(lc["zimbra_require_interprocess_security"])
}

// ldapURLHasScheme is whether url starts with scheme (ldap, ldaps), in any case.
func ldapURLHasScheme(url, scheme string) bool {
	return len(url) > len(scheme)+2 && strings.EqualFold(url[:len(scheme)+3], scheme+"://")
}
