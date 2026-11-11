// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package bootstrap resolves the input of "configd bootstrap": the node
// configuration that replaces the legacy setup.pl answers and its -c file.
//
// A configuration is built from layers, lowest precedence first: built-in
// defaults (some derived from the host), the YAML file or a legacy setup.pl
// config file, the CARBONIO_BOOTSTRAP_* environment, the --set flags and the
// wizard answers. Every key records the layer that set it.
//
// Struct tags drive every layer:
//
//	yaml    the key name; the dotted path of the yaml names is the key path
//	        (ldap.root_password) and, upper-cased and joined with "_", the
//	        environment variable (CARBONIO_BOOTSTRAP_LDAP_ROOT_PASSWORD)
//	legacy  the setup.pl config keys the value is imported from, separated by ","
//	secret  "true": never printed or saved; also settable from a file via
//	        the <key>_file YAML key or the <VAR>_FILE environment variable
//	enum    the allowed values, separated by "|"
//	check   the format of the value: fqdn, email, password (admin password
//	        length) or networks (IPs or CIDRs)
package bootstrap

import "slices"

// Role names, one per carbonio package that bootstrap configures.
const (
	RoleDirectoryServer = "directory-server"
	RoleMTA             = "mta"
	RoleAppserver       = "appserver"
	RoleProxy           = "proxy"
	RoleClamAV          = "clamav"
	RoleMemcached       = "memcached"
)

// Key paths the defaults, the validation and the wizard refer to.
const (
	keyAdminAccount                    = "admin.account"
	keyAdminCreate                     = "admin.create"
	keyAdminPassword                   = "admin.password"
	keyDomainCreate                    = "domain.create"
	keyDomainName                      = "domain.name"
	keyHostHostname                    = "host.hostname"
	keyHostIPMode                      = "host.ip_mode"
	keyHostPublicHostname              = "host.public_hostname"
	keyHostTimezone                    = "host.timezone"
	keyLdapAdminPassword               = "ldap.admin_password"
	keyLdapAmavisPassword              = "ldap.amavis_password"
	keyLdapHost                        = "ldap.host"
	keyLdapNginxPassword               = "ldap.nginx_password"
	keyLdapPort                        = "ldap.port"
	keyLdapPostfixPassword             = "ldap.postfix_password"
	keyLdapRootPassword                = "ldap.root_password"
	keyMtaAntispam                     = "mta.antispam"
	keyMtaAntivirus                    = "mta.antivirus"
	keyMtaCbpolicyd                    = "mta.cbpolicyd"
	keyMtaMyNetworks                   = "mta.my_networks"
	keyMtaOpendkim                     = "mta.opendkim"
	keyNotificationsAvNotifyUser       = "notifications.av_notify_user"
	keyNotificationsSMTPDestination    = "notifications.smtp_destination"
	keyNotificationsSMTPSource         = "notifications.smtp_source"
	keyProxyMailEnabled                = "proxy.mail_enabled"
	keyProxyMode                       = "proxy.mode"
	keyProxyStrictServerName           = "proxy.strict_server_name"
	keyProxyWebEnabled                 = "proxy.web_enabled"
	keyRoles                           = "roles"
	keyHostSSLDigest                   = "host.ssl_digest"
	keyHostRequireInterprocessSecurity = "host.require_interprocess_security"
	keyLdapBaseDn                      = "ldap.base_dn"
	keyLdapReplicationType             = "ldap.replication_type"
	keyLdapServerID                    = "ldap.server_id"
	keyLdapReplicationPassword         = "ldap.replication_" + "password" // split: secret scanners flag the whole literal
	keyStoreKeyboardShortcuts          = "store.keyboard_shortcuts"
	keyStoreMailboxdMemoryMb           = "store.mailboxd_memory_mb"
	keyStoreMysqlMemoryPercent         = "store.mysql_memory_percent"
	keyStartServices                   = "start_services"
	keyStoreHamAccount                 = "store.ham_account"
	keyStoreMailProxy                  = "store.mail_proxy"
	keyStoreMode                       = "store.mode"
	keyStoreSMTPHost                   = "store.smtp_host"
	keyStoreSpamAccount                = "store.spam_account"
	keyStoreSpamTraining               = "store.spam_training"
	keyStoreVirusQuarantineAccount     = "store.virus_quarantine_account"
	keyStoreWebProxy                   = "store.web_proxy"
)

// The LDAP replication types of a directory server.
const (
	ReplicationMaster  = "master"
	ReplicationReplica = "replica"
	ReplicationMMR     = "mmr"
)

// Roles lists every role, in install order.
var Roles = []string{RoleDirectoryServer, RoleMTA, RoleAppserver, RoleProxy, RoleClamAV, RoleMemcached}

// RolePackage returns the package that installs role: carbonio-<role>.
func RolePackage(role string) string { return "carbonio-" + role }

// Config is the bootstrap configuration of one node.
type Config struct {
	// Roles are the carbonio components installed on this node (default:
	// detected from the installed packages).
	Roles []string `yaml:"roles" enum:"directory-server|mta|appserver|proxy|clamav|memcached"`

	Host   HostConfig   `yaml:"host"`
	LDAP   LDAPConfig   `yaml:"ldap"`
	Domain DomainConfig `yaml:"domain"`
	Admin  AdminConfig  `yaml:"admin"`

	Notifications NotificationsConfig `yaml:"notifications"`

	MTA   MTAConfig   `yaml:"mta"`
	Store StoreConfig `yaml:"store"`
	Proxy ProxyConfig `yaml:"proxy"`

	// StartServices starts the services once the node is configured.
	StartServices bool `yaml:"start_services" legacy:"STARTSERVERS"`
}

// HostConfig identifies the node.
type HostConfig struct {
	Hostname       string `yaml:"hostname" legacy:"HOSTNAME" check:"fqdn"`
	PublicHostname string `yaml:"public_hostname" legacy:"PUBLICSERVICEHOSTNAME" check:"fqdn"`
	IPMode         string `yaml:"ip_mode" legacy:"zimbraIPMode" enum:"ipv4|ipv6|both"`
	Timezone       string `yaml:"timezone" legacy:"zimbraPrefTimeZoneId"`
	// SSLDigest is the default OpenSSL digest of the certificates.
	SSLDigest string `yaml:"ssl_digest" legacy:"ssl_default_digest" enum:"ripemd160|sha|sha1|sha224|sha256|sha384|sha512"`
	// RequireInterprocessSecurity encrypts the traffic between services
	// (zimbra_require_interprocess_security).
	RequireInterprocessSecurity bool `yaml:"require_interprocess_security" legacy:"ZIMBRA_REQ_SECURITY,zimbra_require_interprocess_security"` //nolint:lll // struct tag
}

// LDAPConfig locates the LDAP master and holds the passwords of its accounts.
type LDAPConfig struct {
	Host            string `yaml:"host" legacy:"LDAPHOST" check:"fqdn"`
	Port            int    `yaml:"port" legacy:"LDAPPORT"`
	RootPassword    string `yaml:"root_password" legacy:"LDAPROOTPASS,ldap_root_password" secret:"true"`
	AdminPassword   string `yaml:"admin_password" legacy:"LDAPADMINPASS,zimbra_ldap_password" secret:"true"`
	PostfixPassword string `yaml:"postfix_password" legacy:"LDAPPOSTPASS,ldap_postfix_password" secret:"true"`
	AmavisPassword  string `yaml:"amavis_password" legacy:"LDAPAMAVISPASS,ldap_amavis_password" secret:"true"`
	NginxPassword   string `yaml:"nginx_password" legacy:"ldap_nginx_password,LDAPNGINXPW" secret:"true"`
	// BaseDN is the base of the configuration tree (ldap_dit_base_dn_config).
	BaseDN string `yaml:"base_dn" legacy:"ldap_dit_base_dn_config"`
	// ReplicationType is the role of a directory server: the master, a
	// read-only replica of ldap.host, or a multi-master peer of ldap.host.
	ReplicationType string `yaml:"replication_type" legacy:"LDAPREPLICATIONTYPE" enum:"master|replica|mmr"`
	// ServerID is the multi-master server ID (mmr only).
	ServerID int `yaml:"server_id" legacy:"LDAPSERVERID"`
	// ReplicationPassword is the password of the replication account.
	ReplicationPassword string `yaml:"replication_password" legacy:"LDAPREPPASS,ldap_replication_password" secret:"true"`
	// ForceReplication enables replication on a node whose ldap.host is
	// itself (legacy FORCEREPLICATION, written to .enable_replica).
	ForceReplication bool `yaml:"force_replication" legacy:"FORCEREPLICATION"`
}

// DomainConfig is the default mail domain.
type DomainConfig struct {
	Create bool   `yaml:"create" legacy:"DOCREATEDOMAIN"`
	Name   string `yaml:"name" legacy:"CREATEDOMAIN" check:"fqdn"`
}

// AdminConfig is the first administrator account.
type AdminConfig struct {
	Create   bool   `yaml:"create" legacy:"DOCREATEADMIN"`
	Account  string `yaml:"account" legacy:"CREATEADMIN" check:"email"`
	Password string `yaml:"password" legacy:"CREATEADMINPASS" secret:"true" check:"password"`
}

// NotificationsConfig are the addresses of the system notifications.
type NotificationsConfig struct {
	SMTPSource      string `yaml:"smtp_source" legacy:"SMTPSOURCE" check:"email"`
	SMTPDestination string `yaml:"smtp_destination" legacy:"SMTPDEST" check:"email"`
	AVNotifyUser    string `yaml:"av_notify_user" legacy:"AVUSER" check:"email"`
}

// MTAConfig configures an MTA node.
type MTAConfig struct {
	MyNetworks []string `yaml:"my_networks" legacy:"zimbraMtaMyNetworks" check:"networks"`
	Antispam   bool     `yaml:"antispam" legacy:"RUNSA"`
	Antivirus  bool     `yaml:"antivirus" legacy:"RUNAV"`
	OpenDKIM   bool     `yaml:"opendkim" legacy:"RUNDKIM"`
	CBPolicyd  bool     `yaml:"cbpolicyd" legacy:"RUNCBPOLICYD"`
}

// StoreConfig configures an application server node.
type StoreConfig struct {
	SMTPHost               string     `yaml:"smtp_host" legacy:"SMTPHOST" check:"fqdn"`
	Mode                   string     `yaml:"mode" legacy:"MODE" enum:"http|https|both|mixed|redirect"`
	MailProxy              bool       `yaml:"mail_proxy" legacy:"zimbraMailProxy"`
	WebProxy               bool       `yaml:"web_proxy" legacy:"zimbraWebProxy"`
	SpamTraining           bool       `yaml:"spam_training" legacy:"DOTRAINSA"`
	SpamAccount            string     `yaml:"spam_account" legacy:"TRAINSASPAM" check:"email"`
	HamAccount             string     `yaml:"ham_account" legacy:"TRAINSAHAM" check:"email"`
	VirusQuarantineAccount string     `yaml:"virus_quarantine_account" legacy:"VIRUSQUARANTINE" check:"email"`
	KeyboardShortcuts      bool       `yaml:"keyboard_shortcuts" legacy:"USEKBSHORTCUTS"`
	MailboxdMemoryMB       int        `yaml:"mailboxd_memory_mb" legacy:"MAILBOXDMEMORY"`
	MySQLMemoryPercent     int        `yaml:"mysql_memory_percent" legacy:"MYSQLMEMORYPERCENT"`
	Ports                  StorePorts `yaml:"ports"`
}

// StorePorts are the ports the mailbox server listens on.
type StorePorts struct {
	IMAP  int `yaml:"imap" legacy:"IMAPPORT"`
	IMAPS int `yaml:"imaps" legacy:"IMAPSSLPORT"`
	POP3  int `yaml:"pop3" legacy:"POPPORT"`
	POP3S int `yaml:"pop3s" legacy:"POPSSLPORT"`
	HTTP  int `yaml:"http" legacy:"HTTPPORT"`
	HTTPS int `yaml:"https" legacy:"HTTPSPORT"`
}

// ProxyConfig configures a proxy node.
type ProxyConfig struct {
	Mode             string     `yaml:"mode" legacy:"PROXYMODE" enum:"https|redirect"`
	MailEnabled      bool       `yaml:"mail_enabled" legacy:"MAILPROXY"`
	WebEnabled       bool       `yaml:"web_enabled" legacy:"HTTPPROXY"`
	StrictServerName bool       `yaml:"strict_server_name" legacy:"STRICTSERVERNAMEENABLED"`
	Ports            ProxyPorts `yaml:"ports"`
}

// ProxyPorts are the ports the proxy listens on.
type ProxyPorts struct {
	IMAP  int `yaml:"imap" legacy:"IMAPPROXYPORT"`
	IMAPS int `yaml:"imaps" legacy:"IMAPSSLPROXYPORT"`
	POP3  int `yaml:"pop3" legacy:"POPPROXYPORT"`
	POP3S int `yaml:"pop3s" legacy:"POPSSLPROXYPORT"`
	HTTP  int `yaml:"http" legacy:"HTTPPROXYPORT"`
	HTTPS int `yaml:"https" legacy:"HTTPSPROXYPORT"`
}

// IsLDAPMaster reports whether the node is the LDAP master: a directory
// server whose ldap.host is itself and that does not replicate from another
// node.
func (c *Config) IsLDAPMaster() bool {
	return c.HasRole(RoleDirectoryServer) && c.LDAP.Host == c.Host.Hostname &&
		(c.LDAP.ReplicationType == "" || c.LDAP.ReplicationType == ReplicationMaster)
}

// HasRole reports whether the node runs role.
func (c *Config) HasRole(role string) bool {
	return slices.Contains(c.Roles, role)
}
