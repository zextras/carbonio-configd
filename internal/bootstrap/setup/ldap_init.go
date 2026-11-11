// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ldapAdminEntryDN is the entry of conf/carbonio.ldif that holds the
// password of the zimbra LDAP admin.
const ldapAdminEntryDN = "uid=zimbra,cn=admins,cn=zimbra"

var errLDAPNoRootPW = errors.New("no olcRootPW line")

// ldapConfigFiles are the files installLdapConfig copies from the package
// template to data/ldap/config (setup.pl installLdapConfig, L1296).
func ldapConfigFiles() []string {
	return []string{
		"cn=config.ldif",
		"cn=config/cn=module{0}.ldif",
		"cn=config/cn=schema.ldif",
		"cn=config/olcDatabase={-1}frontend.ldif",
		"cn=config/olcDatabase={0}config.ldif",
		"cn=config/olcDatabase={1}monitor.ldif",
		"cn=config/olcDatabase={2}mdb.ldif",
		"cn=config/olcDatabase={2}mdb/olcOverlay={0}dynlist.ldif",
		"cn=config/olcDatabase={2}mdb/olcOverlay={1}unique.ldif",
		"cn=config/olcDatabase={2}mdb/olcOverlay={2}noopsrch.ldif",
	}
}

func ldapTemplateDir(e *Env) string {
	return e.Paths.Path("common", "etc", "openldap", "zimbra")
}

func ldapConfigDir(e *Env) string { return e.Paths.Path("data", "ldap", "config") }

// ldapInstallConfig copies the slapd cn=config tree into place.
func ldapInstallConfig(e *Env) error {
	dst := ldapConfigDir(e)
	src := filepath.Join(ldapTemplateDir(e), "config")

	if err := e.Sys.FS.MkdirAll(filepath.Join(dst, "cn=config", "olcDatabase={2}mdb"), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	for _, f := range ldapConfigFiles() {
		if err := e.Sys.FS.CopyFile(e.Ctx, filepath.Join(src, f), filepath.Join(dst, f)); err != nil {
			return fmt.Errorf("copying %s: %w", f, err)
		}
	}

	// slapd refuses a config readable by others; the legacy chmod covers the
	// top file and the *.ldif directly under cn=config.
	private := []string{filepath.Join(dst, "cn=config.ldif")}

	for _, f := range ldapConfigFiles() {
		if filepath.Dir(f) == "cn=config" {
			private = append(private, filepath.Join(dst, f))
		}
	}

	for _, p := range private {
		if err := e.Sys.FS.Chmod(p, 0o600); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}

	return e.Root("chown", "-R", "zextras:zextras", dst)
}

// ldapInstalledConfig reports whether every file of the template is in place.
func ldapInstalledConfig(e *Env) bool {
	for _, f := range ldapConfigFiles() {
		if !ldapExists(e, filepath.Join(ldapConfigDir(e), f)) {
			return false
		}
	}

	return true
}

// ldapWritePasswords sets olcRootPW of the config database and the hash of
// the admin entry of conf/carbonio.ldif: the two files slapd and
// postLdapStart are initialised from (ldapinit.pm preLdapStart, and
// bin/ldapapplyldif.pl).
func ldapWritePasswords(e *Env, rootPassword, adminPassword string) error {
	rootHash, err := ldapHashPassword(rootPassword)
	if err != nil {
		return err
	}

	adminHash, err := ldapHashPassword(adminPassword)
	if err != nil {
		return err
	}

	path := filepath.Join(ldapConfigDir(e), "cn=config", "olcDatabase={0}config.ldif")

	data, err := e.Sys.FS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// The value is written base64 encoded, hash prefix included.
	out, n := ldapReplaceLine(data, "olcRootPW", "olcRootPW:: "+base64.StdEncoding.EncodeToString([]byte(rootHash)))
	if n == 0 {
		return fmt.Errorf("%s: %w", path, errLDAPNoRootPW)
	}

	if err := ldapWriteOwned(e, path, out); err != nil {
		return err
	}

	src := filepath.Join(ldapTemplateDir(e), "carbonio.ldif")

	tmpl, err := e.Sys.FS.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	out, found, err := ldapLDIFSetPassword(tmpl, ldapAdminEntryDN, adminHash)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}

	if !found {
		return fmt.Errorf("%s: no entry %s", src, ldapAdminEntryDN)
	}

	return ldapWriteOwned(e, e.Paths.Path("conf", "carbonio.ldif"), out)
}

// ldapPreStart is ldapinit.pm preLdapStart: the root and admin passwords go
// to localconfig and into the files slapd and postLdapStart start from.
func ldapPreStart(e *Env, rootPassword, adminPassword string) error {
	err := e.SetLC(map[string]string{
		ldapLCRoot:     rootPassword,
		ldapLCAdmin:    adminPassword,
		ldapLCIsMaster: ldapTrue,
	})
	if err != nil {
		return err
	}

	return ldapWritePasswords(e, rootPassword, adminPassword)
}

// ldapApplyLDIF is zmldapapplyldif: preLdapStart without the localconfig
// writes, from the passwords localconfig holds.
func ldapApplyLDIF(e *Env) error {
	lc, err := e.LC()
	if err != nil {
		return err
	}

	return ldapWritePasswords(e, lc[ldapLCRoot], lc[ldapLCAdmin])
}

// ldapPostStartFiles are the LDIF files postLdapStart loads, in order.
func ldapPostStartFiles(e *Env) []string {
	mime := e.Paths.Path("conf", "ldap", "mimehandlers.ldif")
	if !ldapExists(e, mime) {
		mime = filepath.Join(ldapTemplateDir(e), "mimehandlers.ldif")
	}

	tmpl := ldapTemplateDir(e)

	return []string{
		e.Paths.Path("conf", "carbonio.ldif"),
		filepath.Join(tmpl, "zimbra_globalconfig.ldif"),
		filepath.Join(tmpl, "zimbra_defaultcos.ldif"),
		filepath.Join(tmpl, "zimbra_defaultexternalcos.ldif"),
		mime,
	}
}

// ldapPostStart is ldapinit.pm postLdapStart: it loads the base entries
// (admins, application accounts, global config, default COS, mime types)
// into the new directory. rc follows the legacy codes: 0 done, 1 an LDIF file
// that cannot be read, 2 a bind or add the server refused. An entry that
// already exists is skipped, so a run that stopped half way can be repeated.
func ldapPostStart(e *Env, k *ldapKit) (rc int, err error) {
	lc, err := e.LC()
	if err != nil {
		return 2, err
	}

	d := ldapDial{URLs: strings.Fields(lc[ldapLCMasterURL])}
	if ldapTruthy(lc["ldap_starttls_supported"]) {
		d.TLS = ldapTLSInsecure
	}

	conn, err := k.dial(e, d)
	if err != nil {
		return 2, fmt.Errorf("connecting to %s: %w", lc[ldapLCMasterURL], err)
	}
	defer conn.Close()

	if err := conn.Bind(ldapConfigDN, lc[ldapLCRoot]); err != nil {
		return 2, fmt.Errorf("bind to %s failed: %w", ldapConfigDN, err)
	}

	for _, f := range ldapPostStartFiles(e) {
		data, err := e.Sys.FS.ReadFile(f)
		if err != nil {
			return 1, fmt.Errorf("reading %s: %w", f, err)
		}

		entries, err := ldapParseLDIF(data)
		if err != nil {
			return 1, fmt.Errorf("%s: %w", f, err)
		}

		for _, en := range entries {
			if err := conn.Add(en.DN, en.Attrs); err != nil && !ldapIsAlreadyExists(err) {
				return 2, fmt.Errorf("ldap add failed for %s (from %s): %w", en.DN, f, err)
			}
		}
	}

	return 0, nil
}
