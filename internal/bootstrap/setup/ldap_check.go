// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"strings"
)

// LDAPIsAvailable is setup.pl ldapIsAvailable (L3874-3973) with checkLdapBind
// and checkLdapReplicationEnabled: it checks that the accounts of this node
// bind to the LDAP master LDAPHOST:LDAPPORT, and that a replica can query the
// accesslog of the master. setup.pl calls it before applyConfig, as
// `$ldapConfigured || (LDAPHOST ne HOSTNAME && ldapIsAvailable())`, and runs
// setLdapDefaults and getAvailableComponents when it is true; setLdapDefaults
// is not part of this function here.
//
// Its effects, as in setup.pl: the admin password is written to localconfig
// when it binds; on a new install ldap_url, ldap_starttls_supported,
// zimbra_require_interprocess_security and ssl_allow_untrusted_certs are
// written, and a master that does not offer StartTLS clears
// Cfg.Host.RequireInterprocessSecurity.
func (e *Env) LDAPIsAvailable() bool { return ldapIsAvailable(e, ldapNativeKit()) }

func ldapIsAvailable(e *Env, k *ldapKit) bool {
	c := e.Cfg.LDAP

	if ldapOnMaster(e) && !e.LDAPConfigured() {
		e.Log.Detail("This is the ldap master and ldap hasn't been configured yet.")

		return false
	}

	if c.AdminPassword == "" || c.Port == 0 || c.Host == "" {
		e.Log.Detail("ldap configuration not complete")

		return false
	}

	lc, err := e.LC()
	if err != nil {
		e.Log.Detail("%v", err)

		return false
	}

	failed := 0

	if ldapCheckBind(e, k, lc["zimbra_ldap_userdn"], c.AdminPassword) {
		if err := e.SetLC(map[string]string{ldapLCAdmin: c.AdminPassword}); err != nil {
			e.Log.Detail("%v", err)
		}
	} else {
		failed++
	}

	failed += ldapCountFailedChecks(e, k)

	if ldapIsDirectoryInstalled(e) && !ldapOnMaster(e) &&
		!ldapCheckReplication(e, k, lc["zimbra_ldap_userdn"], c.AdminPassword) {
		failed++
	}

	return failed == 0
}

// ldapCountFailedChecks binds the application accounts and returns how many
// did not.
func ldapCountFailedChecks(e *Env, k *ldapKit) int {
	base := e.Cfg.LDAP.BaseDN
	if base == "" {
		base = "cn=zimbra"
	}

	failed := 0

	for _, chk := range ldapAvailabilityChecks(e, base) {
		if chk.password == "" {
			e.Log.Detail("%s configuration not complete", chk.name)

			failed++

			continue
		}

		if !ldapCheckBind(e, k, chk.dn, chk.password) {
			failed++
		}
	}

	return failed
}

// ldapAvailabilityCheck is one account bound against the master.
type ldapAvailabilityCheck struct{ name, dn, password string }

func ldapAvailabilityChecks(e *Env, base string) []ldapAvailabilityCheck {
	c := e.Cfg.LDAP

	var chks []ldapAvailabilityCheck

	if e.Facts.IsInstalled(ldapPkgProxy) {
		chks = append(chks, ldapAvailabilityCheck{"nginx", "uid=zmnginx,cn=appaccts," + base, c.NginxPassword})
	}

	if e.Facts.IsInstalled(PkgMTA) {
		chks = append(chks,
			ldapAvailabilityCheck{"postfix", "uid=zmpostfix,cn=appaccts," + base, c.PostfixPassword},
			ldapAvailabilityCheck{ldapNameAmavis, "uid=zmamavis,cn=appaccts," + base, c.AmavisPassword})
	}

	if ldapIsDirectoryInstalled(e) && !ldapOnMaster(e) {
		chks = append(chks, ldapAvailabilityCheck{"replication", "uid=zmreplica,cn=admins," + base, c.ReplicationPassword})
	}

	return chks
}

func ldapIsDirectoryInstalled(e *Env) bool { return e.Facts.IsInstalled(ldapPkgDirectory) }

// ldapProbe opens the connection checkLdapBind opens: StartTLS without
// verification when the configuration requires interprocess security; a
// master that refuses it clears the requirement.
func ldapProbe(e *Env, k *ldapKit) (conn ldapConn, startTLS bool, url string, err error) {
	c := e.Cfg.LDAP

	secure := ""
	if c.Port == 636 {
		secure = "s"
	}

	url = fmt.Sprintf("ldap%s://%s:%d", secure, c.Host, c.Port)

	if secure == "" && e.Cfg.Host.RequireInterprocessSecurity {
		conn, err = k.dial(e, ldapDial{URLs: []string{url}, TLS: ldapTLSInsecure})
		if err == nil {
			return conn, true, url, nil
		}

		e.Log.Detail("Unable to startTLS: %v", err)
		e.Log.Detail("Disabling the requirement for interprocess security.")

		conn, err = k.dial(e, ldapDial{URLs: []string{url}})
		if err == nil {
			e.Cfg.Host.RequireInterprocessSecurity = false
		}

		return conn, false, url, err
	}

	conn, err = k.dial(e, ldapDial{URLs: []string{url}})

	return conn, false, url, err
}

// ldapCheckBind is setup.pl checkLdapBind: true when dn binds with password.
func ldapCheckBind(e *Env, k *ldapKit, dn, password string) bool {
	e.Log.Detail("Checking ldap on %s:%d", e.Cfg.LDAP.Host, e.Cfg.LDAP.Port)

	conn, startTLS, url, err := ldapProbe(e, k)
	if err != nil {
		e.Log.Detail("failed: Unable to contact ldap at %s: %v", url, err)

		return false
	}
	defer conn.Close()

	if err := conn.Bind(dn, password); err != nil {
		e.Log.Detail("Unable to bind to %s with user %s: %v", url, dn, err)

		return false
	}

	e.Log.Detail("Verified ldap running at %s", url)

	if e.Facts.NewInstall {
		tls, sec := "0", "0"
		if startTLS {
			tls = "1"
		}

		if e.Cfg.Host.RequireInterprocessSecurity {
			sec = "1"
		}

		err := e.SetLC(map[string]string{
			ldapLCURL:                              url,
			"ldap_starttls_supported":              tls,
			"zimbra_require_interprocess_security": sec,
			"ssl_allow_untrusted_certs":            ldapTrue,
		})
		if err != nil {
			e.Log.Detail("%v", err)
		}
	}

	return true
}

// ldapCheckReplication is setup.pl checkLdapReplicationEnabled: the accesslog
// database of the master can be queried.
func ldapCheckReplication(e *Env, k *ldapKit, dn, password string) bool {
	conn, _, url, err := ldapProbe(e, k)
	if err != nil {
		e.Log.Detail("failed: Unable to contact ldap at %s: %v", url, err)

		return false
	}
	defer conn.Close()

	if err := conn.Bind(dn, password); err != nil {
		e.Log.Detail("Unable to bind to %s with user %s: %v", url, dn, err)

		return false
	}

	if _, err := conn.Search("cn=accesslog", "cn=accesslog", false, []string{"cn"}); err != nil {
		e.Log.Detail("Unable to find accesslog database on master.")

		switch strings.ToLower(e.Cfg.LDAP.ReplicationType) {
		case "replica":
			e.Log.Detail("Please run zmldapenablereplica on the master.")
		case "mmr":
			e.Log.Detail("Please run zmldapenable-mmr on the master.")
		}

		return false
	}

	e.Log.Detail("Verified ability to query accesslog on master.")

	return true
}
