// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

// section groups the keys of the configuration screen.
type section struct {
	title string
	// desc is the one-line description under the title.
	desc string
	keys []string
}

// sections lists every key of bootstrap.Fields (but roles, the chips) once.
var sections = []section{
	{"Node", "Identity of this server and what to start when done.", []string{
		"host.hostname", "host.public_hostname", "host.ip_mode", "host.timezone", "start_services",
	}},
	{"Directory (LDAP)", "Where the LDAP master is and the passwords of its accounts.", []string{
		"ldap.host", "ldap.port", "ldap.replication_type", "ldap.server_id", "ldap.force_replication",
		"ldap.root_password", "ldap.admin_password", "ldap.replication_password",
		"ldap.postfix_password", "ldap.amavis_password", "ldap.nginx_password",
	}},
	{"Domain & admin", "The default mail domain and the first administrator.", []string{
		"domain.create", "domain.name", "admin.create", "admin.account", "admin.password",
	}},
	{"Mail transport", "Postfix: trusted networks, filters and the relay of the mailbox.", []string{
		"mta.my_networks", "mta.antispam", "mta.antivirus", "mta.opendkim", "mta.cbpolicyd", "store.smtp_host",
	}},
	{"Mailbox store", "The application server: protocols, spam training and system accounts.", []string{
		"store.mode", "store.mail_proxy", "store.web_proxy", "store.spam_training", "store.spam_account",
		"store.ham_account", "store.virus_quarantine_account",
	}},
	{"Proxy", "The reverse proxy in front of the mailbox.", []string{
		"proxy.mode", "proxy.mail_enabled", "proxy.web_enabled", "proxy.strict_server_name",
	}},
	{"Notifications", "Addresses of the system notifications.", []string{
		"notifications.smtp_source", "notifications.smtp_destination", "notifications.av_notify_user",
	}},
	{"Advanced", "Rarely changed: certificates, interprocess security, memory, ports, base DN.", []string{
		"host.ssl_digest", "host.require_interprocess_security", "ldap.base_dn", "store.keyboard_shortcuts",
		"store.mailboxd_memory_mb", "store.mysql_memory_percent",
		"store.ports.imap", "store.ports.imaps", "store.ports.pop3", "store.ports.pop3s",
		"store.ports.http", "store.ports.https",
		"proxy.ports.imap", "proxy.ports.imaps", "proxy.ports.pop3", "proxy.ports.pop3s",
		"proxy.ports.http", "proxy.ports.https",
	}},
}

// meta is the label and the help of a key.
type meta struct{ label, help string }

var fieldMeta = withPortMeta(map[string]meta{
	"host.hostname":        {"Hostname", "Fully qualified name of this node (default: hostname --fqdn)."},
	"host.public_hostname": {"Public hostname", "Name users and clients connect to (certificates and links use it)."},
	"host.ip_mode":         {"IP mode", "Address families the services listen on: ipv4, ipv6 or both."},
	"host.timezone":        {"Timezone", "Olson name of the default timezone, e.g. Europe/Rome."},
	"start_services":       {"Start services when done", "Start every service once the node is configured."},

	"ldap.host":             {"LDAP master host", "The directory server with the master copy."},
	"ldap.port":             {"LDAP master port", "389 for LDAP with StartTLS, 636 for LDAPS."},
	"ldap.replication_type": {"Directory role", "master, replica (read-only copy) or mmr (multi-master)."},
	"ldap.server_id":        {"Multi-master server ID", "Unique number of this peer among the multi-master servers."},
	"ldap.force_replication": {"Force replication", "Enable replication although ldap.host is this node " +
		"(written to .enable_replica)."},
	"ldap.root_password":        {"LDAP root password", "Password of the LDAP root DN (cn=config)."},
	"ldap.admin_password":       {"LDAP admin password", "Password of uid=zimbra, the administrator of the directory."},
	"ldap.replication_password": {"LDAP replication password", "Password the replicas use to read the master."},
	"ldap.postfix_password":     {"LDAP postfix password", "Password Postfix uses to read the directory."},
	"ldap.amavis_password":      {"LDAP amavis password", "Password of the account Amavis reads the directory with."},
	"ldap.nginx_password":       {"LDAP nginx password", "Password the proxy uses to read the directory."},

	"domain.create":  {"Create the default domain", "Create the first mail domain on this node."},
	"domain.name":    {"Default domain", "Name of the first mail domain; its MX record is checked in DNS."},
	"admin.create":   {"Create the administrator", "Create the first administrator account."},
	"admin.account":  {"Administrator account", "Email address of the first administrator."},
	"admin.password": {"Administrator password", "Password of the first administrator (at least 6 characters)."},

	"mta.my_networks": {"Trusted networks", "Comma-separated IPs or CIDRs allowed to relay through this MTA."},
	"mta.antispam":    {"Antispam", "Run the spam filter (SpamAssassin)."},
	"mta.antivirus":   {"Antivirus", "Scan mail with ClamAV."},
	"mta.opendkim":    {"DKIM", "Sign and verify mail with OpenDKIM."},
	"mta.cbpolicyd":   {"Policy daemon", "Run the cbpolicyd policy daemon (quotas, throttling)."},
	"store.smtp_host": {"SMTP relay", "Host the mailbox sends outgoing mail to (an MTA)."},

	"store.mode":                     {"Web mode", "http, https, both, mixed or redirect."},
	"store.mail_proxy":               {"Mail proxy", "Serve IMAP and POP3 through the proxy."},
	"store.web_proxy":                {"Web proxy", "Serve the web client through the proxy."},
	"store.spam_training":            {"Spam training", "Train the spam filter from the spam and ham mailboxes."},
	"store.spam_account":             {"Spam account", "Mailbox users move junk to for training."},
	"store.ham_account":              {"Ham account", "Mailbox users move wrongly flagged mail to for training."},
	"store.virus_quarantine_account": {"Virus quarantine account", "Mailbox that receives quarantined messages."},

	"proxy.mode":               {"Proxy mode", "https: encrypted only; redirect: http redirects to https."},
	"proxy.mail_enabled":       {"Mail proxy", "Proxy IMAP and POP3."},
	"proxy.web_enabled":        {"Web proxy", "Proxy the web client."},
	"proxy.strict_server_name": {"Strict server name", "Reject requests for unknown virtual hosts."},

	"notifications.smtp_source":      {"Notification sender", "From address of the system notifications."},
	"notifications.smtp_destination": {"Notification recipient", "Who receives the system notifications."},
	"notifications.av_notify_user":   {"Antivirus recipient", "Who is told when the antivirus finds a virus."},

	"host.ssl_digest":                    {"SSL digest", "Default OpenSSL digest of the generated certificates."},
	"host.require_interprocess_security": {"Interprocess security", "Encrypt the traffic between the services."},
	"ldap.base_dn":                       {"LDAP base DN", "Base of the configuration tree (ldap_dit_base_dn_config)."},
	"store.keyboard_shortcuts":           {"Keyboard shortcuts", "Web client keyboard shortcuts on by default."},
	"store.mailboxd_memory_mb":           {"Mailbox memory (MB)", "Mailbox server Java heap (default: from node memory)."},
	"store.mysql_memory_percent":         {"MySQL memory (%)", "Share of the node memory for the mailbox database."},
})

// withPortMeta adds the port fields, which differ only by the protocol.
func withPortMeta(m map[string]meta) map[string]meta {
	for _, p := range []string{"imap", "imaps", "pop3", "pop3s", "http", "https"} {
		m["store.ports."+p] = meta{"Store " + p + " port", "Port the mailbox server listens on for " + p + "."}
		m["proxy.ports."+p] = meta{"Proxy " + p + " port", "Port the proxy listens on for " + p + "."}
	}

	return m
}
