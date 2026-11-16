// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import "strings"

// BindURL returns the LDAP bind URL a localconfig names: ldap_bind_url when it
// is non-empty (it may list several space-separated URLs), else the first URL
// of ldap_url, else "".
func BindURL(lc map[string]string) string {
	if bindURL := lc["ldap_bind_url"]; bindURL != "" {
		return bindURL
	}

	if urls := strings.Fields(lc["ldap_url"]); len(urls) > 0 {
		return urls[0]
	}

	return ""
}

// LDAPDomain returns the host part of the first URL of BindURL: the scheme
// (everything up to the first "//") and the ":port" suffix are stripped.
func LDAPDomain(lc map[string]string) string {
	urls := strings.Fields(BindURL(lc))
	if len(urls) == 0 {
		return ""
	}

	host := urls[0]
	if i := strings.Index(host, "//"); i >= 0 {
		host = host[i+2:]
	}

	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}

	return host
}

// valueOr returns lc[key], or def when the value is empty or unset.
func valueOr(lc map[string]string, key, def string) string {
	if v := lc[key]; v != "" {
		return v
	}

	return def
}

// appendUnless appends suffix to opts unless opts already contains marker.
func appendUnless(opts, marker, suffix string) string {
	if strings.Contains(opts, marker) {
		return opts
	}

	return opts + suffix
}

// LaunchEnv derives from an effective localconfig the environment variables
// the systemd units consume (the former systemd-envscript / systemd.env set).
func LaunchEnv(lc map[string]string) map[string]string {
	heap := valueOr(lc, "mailboxd_java_heap_size", "512")

	opts := lc["mailboxd_java_options"]
	opts = appendUnless(opts, "Xss", " -Xss"+valueOr(lc, "mailboxd_thread_stack_size", "256k"))
	opts = appendUnless(opts, "sun.net.inetaddr.ttl",
		" -Dsun.net.inetaddr.ttl="+valueOr(lc, "networkaddress_cache_ttl", "60"))
	opts = appendUnless(opts, "log4j", " -Dlog4j.configurationFile="+lc["zimbra_log4j_properties"])

	return map[string]string{
		"antispam_enable_restarts":            lc["antispam_enable_restarts"],
		"antispam_enable_rule_compilation":    lc["antispam_enable_rule_compilation"],
		"antispam_enable_rule_updates":        lc["antispam_enable_rule_updates"],
		"bind_url":                            BindURL(lc),
		"configd_listen_port":                 lc["zmconfigd_listen_port"],
		"configd_rewrite_timeout":             lc["zimbra_configrewrite_timeout"],
		"java_options":                        lc["zimbra_zmjava_options"],
		"java_xms":                            heap,
		"java_xmx":                            heap,
		"ldap_domain":                         LDAPDomain(lc),
		"ldap_port":                           lc["ldap_port"],
		"log_directory":                       lc["zimbra_log_directory"],
		"mailboxd_directory":                  lc["mailboxd_directory"],
		"mailboxd_java_heap_new_size_percent": valueOr(lc, "mailboxd_java_heap_new_size_percent", "25"),
		"mail_service_port":                   lc["zimbra_mail_service_port"],
		"mailboxd_java_options":               opts,
		"mysql_errlogfile":                    lc["mysql_errlogfile"],
		"mysql_mycnf":                         lc["mysql_mycnf"],
	}
}
