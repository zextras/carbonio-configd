// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// ldapUpdateFiles are the LDIF files zmldapupdateldif adds to the directory,
// in order: the templates of the package and the mime types, which the
// configuration directory may override.
func ldapUpdateFiles(e *Env) []string {
	tmpl := ldapTemplateDir(e)
	mime := e.Paths.Path("conf", "ldap", "mimehandlers.ldif")

	if !ldapExists(e, mime) {
		mime = filepath.Join(tmpl, "mimehandlers.ldif")
	}

	return []string{
		filepath.Join(tmpl, ldapCarbonioLDIF),
		filepath.Join(tmpl, "zimbra_globalconfig.ldif"),
		filepath.Join(tmpl, "zimbra_defaultcos.ldif"),
		filepath.Join(tmpl, "zimbra_defaultexternalcos.ldif"),
		mime,
	}
}

// ldapUpdateLDIF is zmldapupdateldif (libexec/ldapupdateldif.pl): on the master
// it starts slapd, adds the entries of the package LDIF files that the
// directory lacks and applies the LDIF files of lib/conf, in which an entry
// replaces the attributes it lists. The script printed what it printed to out;
// it did nothing on a node that is not the master.
//
// An entry that cannot be added for any reason but existing is logged here
// (the script said nothing); the failures to start slapd and to bind end the
// run here, where the script went on.
func ldapUpdateLDIF(e *Env, k *ldapKit, out io.Writer) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	if !strings.EqualFold(lc[ldapLCIsMaster], ldapTrue) {
		return nil
	}

	if err := ldapEnsureTmpDir(e, lc["zimbra_tmp_directory"]); err != nil {
		return err
	}

	// The script ignored the result of the start.
	if err := k.start(e); err != nil {
		e.Log.Detail("%v", err)
	}

	conn, err := ldapUpdateConn(e, k, lc)
	if err != nil {
		return err
	}
	defer conn.Close()

	for _, f := range ldapUpdateFiles(e) {
		if err := ldapAddMissing(e, conn, f, out); err != nil {
			return err
		}
	}

	return ldapApplyLibConf(e, conn, out)
}

// ldapEnsureTmpDir creates the temporary directory of Carbonio when it is
// missing.
func ldapEnsureTmpDir(e *Env, dir string) error {
	if dir == "" {
		return nil
	}

	if info, err := e.Sys.FS.Stat(dir); err == nil && info.IsDir() {
		return nil
	}

	if err := e.Sys.FS.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	return e.Root("chown", "-R", "zextras:zextras", dir)
}

// ldapUpdateConn connects to the masters of ldap_master_url (StartTLS, not
// verified, when ldap_starttls_supported and the URL is not ldaps) and binds as
// the cn=config root.
func ldapUpdateConn(e *Env, k *ldapKit, lc map[string]string) (ldapConn, error) {
	d := ldapDial{URLs: strings.Fields(lc[ldapLCMasterURL])}

	if ldapTruthy(lc["ldap_starttls_supported"]) && !ldapURLHasScheme(lc[ldapLCMasterURL], "ldaps") {
		d.TLS = ldapTLSInsecure
	}

	conn, err := k.dial(e, d)
	if err != nil {
		return nil, ldapDie("%v", err)
	}

	if err := conn.Bind(ldapConfigDN, lc[ldapLCRoot]); err != nil {
		conn.Close()

		return nil, ldapDie("Bind: %s", ldapErrText(err))
	}

	return conn, nil
}

// ldapReadLDIF reads and parses an LDIF file. A file whose content cannot be
// parsed is reported on out, as the script did, and has no entries.
func ldapReadLDIF(e *Env, path string, out io.Writer) ([]ldapLDIFEntry, error) {
	data, err := e.Sys.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	entries, err := ldapParseLDIF(data)
	if err != nil {
		ldapPrintf(out, "Error msg: %v\nError lines:\n%s\n", err, path)

		return nil, nil
	}

	return entries, nil
}

// ldapAddMissing adds the entries of an LDIF file; those the directory has are
// left as they are.
func ldapAddMissing(e *Env, conn ldapConn, path string, out io.Writer) error {
	entries, err := ldapReadLDIF(e, path, out)
	if err != nil {
		return err
	}

	for _, en := range entries {
		if err := conn.Add(en.DN, en.Attrs); err != nil && !ldapIsAlreadyExists(err) {
			e.Log.Detail("adding %s (from %s): %s", en.DN, path, ldapErrText(err))
		}
	}

	return nil
}

// ldapApplyLibConf applies the LDIF files of lib/conf, in the order of their
// names: each entry replaces the attributes it lists on the entry of the same
// DN, and a failure is printed on out.
func ldapApplyLibConf(e *Env, conn ldapConn, out io.Writer) error {
	dir := e.Paths.Path("lib", "conf")

	if info, err := e.Sys.FS.Stat(dir); err != nil || !info.IsDir() {
		return nil //nolint:nilerr // the script skipped a directory it could not find
	}

	dirents, err := e.Sys.FS.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("can't opendir %s: %w", dir, err)
	}

	names := make([]string, 0, len(dirents))

	for _, d := range dirents {
		p := filepath.Join(dir, d.Name())
		if info, err := e.Sys.FS.Stat(p); err == nil && info.Mode().IsRegular() && strings.HasSuffix(d.Name(), "ldif") {
			names = append(names, p)
		}
	}

	slices.Sort(names)

	for _, p := range names {
		entries, err := ldapReadLDIF(e, p, out)
		if err != nil {
			return err
		}

		for _, en := range entries {
			ldapReplaceEntry(conn, en, out)
		}
	}

	return nil
}

// ldapReplaceEntry replaces the attributes of the entry en.DN by those of en.
func ldapReplaceEntry(conn ldapConn, en ldapLDIFEntry, out io.Writer) {
	mods := make([]ldapMod, 0, len(en.Attrs))
	for _, a := range en.Attrs {
		mods = append(mods, ldapMod{Op: ldapModReplace, Attr: a.Name, Values: a.Values})
	}

	if len(mods) == 0 {
		return
	}

	if err := conn.Modify(en.DN, mods); err != nil {
		ldapPrintf(out, "Error msg: %s %s\n", en.DN, ldapErrText(err))
	}
}

// RunLDAPUpdateLDIF is zmldapupdateldif: the script took no options (it ignored
// what it was given) and ran as root; it returns the exit status of the script.
func RunLDAPUpdateLDIF(e *Env, _ []string, stdout, stderr io.Writer) int {
	return ldapToolStatus(ldapUpdateLDIF(e, ldapToolKit(e), stdout), stderr)
}
