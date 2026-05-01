// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

const (
	attrZimbraServiceEnabled         = "zimbraServiceEnabled"
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
)
