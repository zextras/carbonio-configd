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
	attrZimbraMailPort                 = "zimbraMailPort"
	attrZimbraMailSSLPort              = "zimbraMailSSLPort"
	attrZimbraMtaAuthTarget            = "zimbraMtaAuthTarget"
	attrZimbraMtaAuthPort              = "zimbraMtaAuthPort"
)

// Provisioning attribute defaults, mirroring the ZAttr getters used by
// jylibs/commands.py (URLUtil.getMtaAuthURL and garpb backend-port selection).
const (
	defaultMtaAuthPort = "7073" // zimbraMtaAuthPort
	defaultMailPort    = "80"   // zimbraMailPort
	defaultMailSSLPort = "0"    // zimbraMailSSLPort
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
