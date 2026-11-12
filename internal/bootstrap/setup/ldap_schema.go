// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// ldapSchemaFile is one schema libexec/ldapschema.sh puts in the slapd
// configuration: where it comes from (under common/etc/openldap), the name
// slapd gives it in cn=schema ("{N}name") and the name of the schema in the
// file ("cn=<schema>").
type ldapSchemaFile struct {
	title  string
	source string
	dest   string
	index  int
	schema string
}

// ldapSchemaFiles are the schemas in the order the script installed them.
var ldapSchemaFiles = []ldapSchemaFile{
	{"core", "schema/core.ldif", "core.ldif", 0, "core"},
	{"cosine", "schema/cosine.ldif", "cosine.ldif", 1, "cosine"},
	{"inetOrgPerson", "schema/inetorgperson.ldif", "inetorgperson.ldif", 2, "inetorgperson"},
	{"zimbra", "zimbra/schema/carbonio.ldif", "carbonio.ldif", 4, "zimbra"},
	{"amavis", "zimbra/schema/amavisd.ldif", "amavisd.ldif", 5, "amavisd"},
	{"dyngroup", "schema/dyngroup.ldif", "dyngroup.ldif", 3, "dyngroup"},
	{"OpenDKIM", "zimbra/schema/opendkim.ldif", "opendkim.ldif", 6, "opendkim"},
}

// ldapSchemaDir is the cn=schema directory of the slapd configuration.
func ldapSchemaDir(e *Env) string {
	return filepath.Join(ldapConfigDir(e), ldapConfigTreeDir, "cn=schema")
}

// ldapRenameSchema is the sed of the script: the dn and cn lines of the
// schema get the "{N}" slapd orders the schemas by.
func ldapRenameSchema(data []byte, f ldapSchemaFile) []byte {
	name := fmt.Sprintf("{%d}%s", f.index, f.schema)
	dnFrom := "dn: cn=" + f.schema + ","
	cnFrom := "cn: " + f.schema

	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, dnFrom):
			lines[i] = strings.Replace(l, dnFrom+"cn=schema,cn=config", "dn: cn="+name, 1)
		case strings.HasPrefix(l, cnFrom):
			lines[i] = strings.Replace(l, cnFrom, "cn: "+name, 1)
		}
	}

	return []byte(strings.Join(lines, "\n"))
}

// ldapInstallSchema is zmldapschema (libexec/ldapschema.sh): it copies the
// schemas of the package into the cn=schema directory of the slapd
// configuration, naming them as slapd numbers them, and prints what the script
// printed to out. The script went on after a copy that failed; here the first
// failure ends it.
func ldapInstallSchema(e *Env, out io.Writer) error {
	ldapPrintf(out, "Looking for LDAP installation...")

	if !ldapOpenLDAPInstalled(e) {
		ldapPrintf(out, "FAILED\nERROR: openldap does not appear to be installed - exiting\n")

		return ldapRefused()
	}

	ldapPrintf(out, "succeeded\n")

	dir := ldapSchemaDir(e)

	if err := e.Sys.FS.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	if err := e.Root("chown", "zextras:zextras", dir); err != nil {
		return err
	}

	if err := e.Sys.FS.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}

	for _, f := range ldapSchemaFiles {
		ldapPrintf(out, "Installing %s schema...\n", f.title)

		if err := ldapInstallOneSchema(e, dir, f); err != nil {
			return err
		}
	}

	// The script kept the sed backups until here.
	old, _ := e.Sys.FS.Glob(filepath.Join(dir, "*.orig"))
	for _, p := range old {
		if err := e.Sys.FS.Remove(p); err != nil {
			return fmt.Errorf("removing %s: %w", p, err)
		}
	}

	return nil
}

func ldapInstallOneSchema(e *Env, dir string, f ldapSchemaFile) error {
	src := e.Paths.Path("common", "etc", "openldap", f.source)

	data, err := e.Sys.FS.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	dst := filepath.Join(dir, fmt.Sprintf("cn={%d}%s", f.index, f.dest))

	if err := e.Sys.FS.WriteFileAtomic(dst, ldapRenameSchema(data, f), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}

	return e.Root("chown", "zextras:zextras", dst)
}

// RunLDAPSchema is zmldapschema: it takes no options (the script ignored what
// it was given) and returns the exit status of the script, 1 when openldap is
// not installed.
func RunLDAPSchema(e *Env, _ []string, stdout, stderr io.Writer) int {
	return ldapToolStatus(ldapInstallSchema(e, stdout), stderr)
}
