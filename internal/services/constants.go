// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import "github.com/zextras/carbonio-configd/internal/config"

// Service name identifiers used across the package.
const (
	svcAmavis          = "amavis"
	svcAntispam        = "antispam"
	svcAntivirus       = "antivirus"
	svcCbpolicyd       = "cbpolicyd"
	svcClamd           = "clamd"
	svcConfigd         = "configd"
	svcFreshclam       = "freshclam"
	svcLdap            = "ldap"
	svcMailbox         = "mailbox"
	svcMailboxd        = "mailboxd"
	svcMemcached       = "memcached"
	svcOpendkim        = "opendkim"
	svcPostconf        = "postconf"
	svcPostconfd       = "postconfd"
	svcProxy           = "proxy"
	svcProxygen        = "proxygen"
	svcSasl            = "sasl"
	svcSaslauthd       = "saslauthd"
	svcMilter          = "milter"
	svcStats           = "stats"
	svcServiceDiscover = "service-discover"
	svcService         = "service"
	svcLocalconfig     = "localconfig"
	svcZmconfigd       = "zmconfigd"
	// Note: serviceMTA = "mta" is defined in manager.go:128 and is the canonical MTA constant
)

// Logical service-group identifiers.
const (
	groupDirectoryServer = "directory-server"
)

// Carbonio systemd unit names.
const (
	unitMailthreat = "carbonio-mailthreat.service"
	unitAntivirus  = "carbonio-antivirus.service"
	unitPolicyd    = "carbonio-policyd.service"
	unitOpenldap   = "carbonio-openldap.service"
	unitAppserver  = "carbonio-appserver.service"
	unitPostfix    = "carbonio-postfix.service"
	unitOpendkim   = "carbonio-opendkim.service"
	unitNginx      = "carbonio-nginx.service"
)

// Postfix LDAP table file names.
const (
	ldapVmm         = "ldap-vmm.cf"
	ldapVmd         = "ldap-vmd.cf"
	ldapVam         = "ldap-vam.cf"
	ldapVad         = "ldap-vad.cf"
	ldapCanonical   = "ldap-canonical.cf"
	ldapTransport   = "ldap-transport.cf"
	ldapSlm         = "ldap-slm.cf"
	ldapSplitdomain = "ldap-splitdomain.cf"
)

// Network defaults.
const (
	localhostName = "localhost"
	loopbackIPv4  = "127.0.0.1"
)

// Action and state words.
const (
	actionStart = "start"
	actionStop  = "stop"
)

// User and group identifiers. Carbonio runs everything as zextras; there is
// no zimbra account on a Carbonio host.
const (
	userZextras = config.ZextrasUser
)

// SASL mechanisms. "zimbra" is the name of the Carbonio-patched saslauthd
// authentication mechanism (saslauthd -a), not an OS user.
const (
	saslMechZimbra = "zimbra"
)

// Legacy zimbraServiceEnabled values that no longer map to a real service.
const (
	ldapServiceZimbra = "zimbra"
)

// Socket families and protocols.
const (
	socketFamilyUnixgram = "unixgram"
)

// zmstat tool names (statistics collectors).
const (
	zmstatProc     = "zmstat-proc"
	zmstatCPU      = "zmstat-cpu"
	zmstatVM       = "zmstat-vm"
	zmstatDF       = "zmstat-df"
	zmstatIO       = "zmstat-io"
	zmstatFD       = "zmstat-fd"
	zmstatAllprocs = "zmstat-allprocs"
)
