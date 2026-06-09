// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

const (
	attrZimbraServiceEnabled         = "zimbraServiceEnabled"
	attrZimbraDomainName             = "zimbraDomainName"
	keyLDAPCommonLoglevel            = "ldap_common_loglevel"
	keyLDAPCommonThreads             = "ldap_common_threads"
	keyLDAPCommonToolthreads         = "ldap_common_toolthreads"
	keyLDAPCommonRequireTLS          = "ldap_common_require_tls"
	keyLDAPDBMaxsize                 = "ldap_db_maxsize"
	keyLDAPDBEnvflags                = "ldap_db_envflags"
	keyLDAPAccesslogMaxsize          = "ldap_accesslog_maxsize"
	keyLDAPOverlaySyncprovCheckpoint = "ldap_overlay_syncprov_checkpoint"
)

// OpenLDAP cn=config attribute names and DNs reused across the schema map.
const (
	olcDBMaxsizeAttr         = "olcDbMaxsize"
	ldapOverlaySyncprovDB3DN = "olcOverlay={0}syncprov,olcDatabase={3}mdb,cn=config"
	attrOlcSpSessionlog      = "olcSpSessionlog"

	// ldapAccesslogSuffix is the data suffix probed to confirm LDAP master
	// status; it exists only on masters carrying the accesslog overlay.
	// Mirrors atbase="cn=accesslog" in jylibs/ldap.py modify_attribute.
	ldapAccesslogSuffix = "cn=accesslog"
)

// Config-backend (cn=config) connection constants.
//
// The slapd-config backend is only writable by the cn=config rootDN over the
// local ldapi unix socket (the data-suffix admin uid=zimbra has no access —
// OpenLDAP returns code 50). This mirrors the legacy Jython zmconfigd, which
// bound as cn=config over ldapi:/// with ldap_root_password.
const (
	// ConfigBackendDN is the rootDN used to bind to the slapd-config backend.
	ConfigBackendDN = "cn=config"
	// ConfigBackendURL is the explicit ldapi unix-socket URL for Carbonio's
	// slapd. The path component is mandatory: go-ldap defaults a bare
	// ldapi:/// to /var/run/slapd/ldapi, which is NOT Carbonio's socket
	// (verified on a live host: slapd listens on /run/carbonio/run/ldapi).
	ConfigBackendURL = "ldapi:///run/carbonio/run/ldapi"
)
