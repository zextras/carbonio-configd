// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

const (
	ldapSaltLen = 8
	ldapSSHA512 = "{SSHA512}"

	ldapDNReplica = "uid=zmreplica,cn=admins,cn=zimbra"
	ldapDNPostfix = "uid=zmpostfix,cn=appaccts,cn=zimbra"
	ldapDNAmavis  = "uid=zmamavis,cn=appaccts,cn=zimbra"
	ldapDNNginx   = "uid=zmnginx,cn=appaccts,cn=zimbra"
	ldapDNRootDB  = "olcDatabase={0}config,cn=config"
	ldapDNMdbDB   = "olcDatabase={2}mdb,cn=config"

	ldapNameRoot   = "root"
	ldapNameAmavis = "amavis"

	ldapLCRoot  = "ldap_root_password"
	ldapLCAdmin = "zimbra_ldap_password"
)

// ldapAccount is one of the LDAP passwords: what zmldappasswd's -r, none,
// -l, -p, -a and -n options address.
type ldapAccount struct {
	// Name is the account as the operator reads it.
	Name string
	// LCKey is the localconfig key that holds the password.
	LCKey string
	// DN is the bind DN; empty for the admin account (localconfig
	// zimbra_ldap_userdn) and for the cn=config root.
	DN string
	// root marks the cn=config root; admin the zimbra admin account.
	root, admin bool
	// get returns the password the configuration asks for.
	get func(*bootstrap.LDAPConfig) string
}

var (
	ldapAcctRoot = ldapAccount{Name: ldapNameRoot, LCKey: ldapLCRoot, root: true,
		get: func(c *bootstrap.LDAPConfig) string { return c.RootPassword }}
	ldapAcctAdmin = ldapAccount{Name: "admin", LCKey: ldapLCAdmin, admin: true,
		get: func(c *bootstrap.LDAPConfig) string { return c.AdminPassword }}
	ldapAcctReplica = ldapAccount{Name: "replication", LCKey: "ldap_replication_password", DN: ldapDNReplica,
		get: func(c *bootstrap.LDAPConfig) string { return c.ReplicationPassword }}
	ldapAcctPostfix = ldapAccount{Name: "postfix", LCKey: "ldap_postfix_password", DN: ldapDNPostfix,
		get: func(c *bootstrap.LDAPConfig) string { return c.PostfixPassword }}
	ldapAcctAmavis = ldapAccount{Name: ldapNameAmavis, LCKey: "ldap_amavis_password", DN: ldapDNAmavis,
		get: func(c *bootstrap.LDAPConfig) string { return c.AmavisPassword }}
	ldapAcctNginx = ldapAccount{Name: "nginx", LCKey: "ldap_nginx_password", DN: ldapDNNginx,
		get: func(c *bootstrap.LDAPConfig) string { return c.NginxPassword }}
)

// ldapAllAccounts is the order of setup.pl updatePasswordsInLocalConfig.
func ldapAllAccounts() []ldapAccount {
	return []ldapAccount{ldapAcctRoot, ldapAcctAdmin, ldapAcctReplica, ldapAcctPostfix, ldapAcctAmavis, ldapAcctNginx}
}

// ldapHashPassword is Crypt::SaltedHash (SHA-512, 8 byte salt) as zmldappasswd
// and ldapinit.pm use it: {SSHA512} + base64(sha512(password+salt)+salt).
func ldapHashPassword(password string) (string, error) {
	salt := make([]byte, ldapSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}

	return ldapHashWithSalt(password, salt), nil
}

func ldapHashWithSalt(password string, salt []byte) string {
	h := sha512.New()
	h.Write([]byte(password))
	h.Write(salt)

	return ldapSSHA512 + base64.StdEncoding.EncodeToString(append(h.Sum(nil), salt...))
}

// ldapTruthy is Perl truthiness of a localconfig value: empty and "0" are false.
func ldapTruthy(v string) bool { return v != "" && v != "0" }

// ldapMasterDial is the connection to the LDAP masters zmldappasswd opens:
// StartTLS, checked against the Carbonio CA, when ldap_starttls_supported.
func ldapMasterDial(lc map[string]string) ldapDial {
	d := ldapDial{URLs: strings.Fields(lc[ldapLCMasterURL])}
	if ldapTruthy(lc["ldap_starttls_supported"]) {
		d.TLS = ldapTLSVerified
	}

	return d
}

// ldapAccountDN is the DN an account binds as.
func ldapAccountDN(a ldapAccount, lc map[string]string) string {
	switch {
	case a.root:
		return ldapConfigDN
	case a.admin:
		return lc["zimbra_ldap_userdn"]
	}

	return a.DN
}

// ldapPasswordWorks reports whether account a binds with password: the real
// state of the directory (false when the server cannot be reached).
func ldapPasswordWorks(e *Env, k *ldapKit, a ldapAccount, password string) bool {
	lc, err := e.LC()
	if err != nil {
		return false
	}

	d := ldapMasterDial(lc)
	if a.root {
		d = ldapDial{URLs: []string{ldapSocketURL}}
	}

	conn, err := k.dial(e, d)
	if err != nil {
		return false
	}
	defer conn.Close()

	return conn.Bind(ldapAccountDN(a, lc), password) == nil
}

// ldapSetPassword is zmldappasswd for account a (bin/ldappasswd.pl, the -r, -l,
// -p, -a, -n options and the plain one) done in-process, so that the password
// is never on a command line: localconfig first, then the directory, then the
// hash in conf/carbonio.ldif.
func ldapSetPassword(e *Env, k *ldapKit, a ldapAccount, password string) error {
	if password == "" {
		return fmt.Errorf("%s password is empty", a.Name)
	}

	lc, err := e.LC()
	if err != nil {
		return err
	}

	// The root password in force before this call is what authorises the
	// change of any other password.
	curRoot := lc[ldapLCRoot]

	hash, err := ldapHashPassword(password)
	if err != nil {
		return err
	}

	if err := e.SetLC(map[string]string{a.LCKey: password}); err != nil {
		return fmt.Errorf("setting %s: %w", a.LCKey, err)
	}

	if a.root {
		return ldapSetRootPassword(e, k, curRoot, password, hash)
	}

	conn, err := k.dial(e, ldapMasterDial(lc))
	if err != nil {
		return fmt.Errorf("connecting to the LDAP master: %w", err)
	}
	defer conn.Close()

	dn := ldapAccountDN(a, lc)

	if conn.Bind(dn, password) != nil {
		if err := conn.Bind(ldapConfigDN, curRoot); err != nil {
			return fmt.Errorf("binding as %s to set the %s password: %w", ldapConfigDN, a.Name, err)
		}

		if err := conn.SetPassword(dn, password); err != nil {
			return fmt.Errorf("setting the %s password: %w", a.Name, err)
		}
	}

	if err := ldapStoreLDIFPassword(e, dn, hash); err != nil {
		return err
	}

	if a.DN == ldapDNReplica {
		return ldapUpdateSyncrepl(conn, password)
	}

	return nil
}

// ldapSetRootPassword changes olcRootPW of the cn=config database over the
// ldapi socket; it is a no-op when the new password already binds.
func ldapSetRootPassword(e *Env, k *ldapKit, curRoot, password, hash string) error {
	conn, err := k.dial(e, ldapDial{URLs: []string{ldapSocketURL}})
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", ldapSocketURL, err)
	}
	defer conn.Close()

	if conn.Bind(ldapConfigDN, password) == nil {
		return nil
	}

	if err := conn.Bind(ldapConfigDN, curRoot); err != nil {
		return fmt.Errorf("binding as %s with the current root password: %w", ldapConfigDN, err)
	}

	if err := conn.Replace(ldapDNRootDB, "olcRootPW", []string{hash}); err != nil {
		return fmt.Errorf("setting olcRootPW: %w", err)
	}

	return nil
}

// ldapUpdateSyncrepl swaps the credentials of the olcSyncrepl value of the
// mdb database (a replica that follows the master with the replication account).
func ldapUpdateSyncrepl(conn ldapConn, password string) error {
	entries, err := conn.Search(ldapDNMdbDB, "(olcSyncrepl=*)", false, []string{"olcSyncrepl"})
	if err != nil || len(entries) == 0 {
		// zmldappasswd ignored the result of this search.
		return nil //nolint:nilerr // legacy: a failed or empty search changes nothing
	}

	vals := entries[0].Attrs["olcSyncrepl"]
	if len(vals) == 0 {
		return nil
	}

	head, rest, ok := strings.Cut(vals[0], "credentials=")
	if !ok {
		return nil
	}

	_, tail, _ := strings.Cut(rest, "filter=")

	repl := head + "credentials=" + password + " filter=" + tail

	if err := conn.Replace(entries[0].DN, "olcSyncrepl", []string{repl}); err != nil {
		return fmt.Errorf("updating olcSyncrepl: %w", err)
	}

	return nil
}

// ldapStoreLDIFPassword records the hash of dn in conf/carbonio.ldif, the
// file the directory is rebuilt from.
func ldapStoreLDIFPassword(e *Env, dn, hash string) error {
	path := e.Paths.Path("conf", "carbonio.ldif")

	data, err := e.Sys.FS.ReadFile(path)
	if err != nil {
		if ldapMissing(e, path) {
			e.Log.Detail("%s is not there: not updated", path)

			return nil
		}

		return fmt.Errorf("reading %s: %w", path, err)
	}

	out, found, err := ldapLDIFSetPassword(data, dn, hash)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	if !found {
		e.Log.Detail("%s has no entry %s: not updated", path, dn)

		return nil
	}

	return ldapWriteOwned(e, path, out)
}

// ldapWriteOwned writes a file that holds password hashes: mode 0600,
// owned by the zextras user the tools read it as.
func ldapWriteOwned(e *Env, path string, data []byte) error {
	if err := e.Sys.FS.WriteFileAtomic(path, data, 0o640); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return e.Root("chown", "zextras:zextras", path)
}
