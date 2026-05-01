// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

// CLI flags
const (
	flagDryRun  = "--dry-run"
	flagVerbose = "--verbose"
)

// LDAP/Zimbra attributes
const (
	attrZimbraServiceEnabled           = "zimbraServiceEnabled"
	attrZimbraReverseProxyLookupTarget = "zimbraReverseProxyLookupTarget"
	attrZimbraMailMode                 = "zimbraMailMode"
	attrZimbraMailSSLPort              = "zimbraMailSSLPort"
)

// Command names
const (
	cmdAmavis      = "amavis"
	cmdAntispam    = "antispam"
	cmdAntivirus   = "antivirus"
	cmdCBPolicyd   = "cbpolicyd"
	cmdGACF        = "gacf"
	cmdGAMAU       = "gamau"
	cmdGARPB       = "garpb"
	cmdGARPU       = "garpu"
	cmdGS          = "gs"
	cmdGSEnabled   = "gs:enabled"
	cmdLDAP        = "ldap"
	cmdLocalconfig = "localconfig"
	cmdMailbox     = "mailbox"
	cmdMailboxd    = "mailboxd"
	cmdMemcached   = "memcached"
	cmdMTA         = "mta"
	cmdOpenDKIM    = "opendkim"
	cmdPostconf    = "postconf"
	cmdPostconfd   = "postconfd"
	cmdProxy       = "proxy"
	cmdProxygen    = "proxygen"
	cmdSASL        = "sasl"
	cmdService     = "service"
	cmdStats       = "stats"
)
