// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

// defaultMailboxdJavaOptions is the default value for mailboxd_java_options,
// extracted as a constant to avoid a long map literal line.
// This must match the Java LocalConfigCLI default exactly.
const defaultMailboxdJavaOptions = "" +
	"-server" +
	" -Dhttps.protocols=TLSv1.2,TLSv1.3" +
	" -Djdk.tls.client.protocols=TLSv1.2,TLSv1.3" +
	" -Djava.awt.headless=true" +
	" -Djava.net.preferIPv4Stack=true" +
	" -Dsun.net.inetaddr.ttl=${networkaddress_cache_ttl}" +
	" -Dorg.apache.jasper.compiler.disablejsr199=true" +
	" -XX:+UseG1GC" +
	" -XX:SoftRefLRUPolicyMSPerMB=1" +
	" -XX:+UnlockExperimentalVMOptions" +
	" -XX:G1NewSizePercent=15" +
	" -XX:G1MaxNewSizePercent=45" +
	" -XX:-OmitStackTraceInFastThrow" +
	" -verbose:gc" +
	" -Xlog:gc*=info,safepoint=info:file=/opt/zextras/log/gc.log:time:filecount=20,filesize=10m" +
	" -Djava.security.egd=file:/dev/./urandom" +
	" --add-opens java.base/java.lang=ALL-UNNAMED"

// defaultZmjavaOptions is the default value for zimbra_zmjava_options.
const defaultZmjavaOptions = "" +
	"-Xmx256m" +
	" -Dhttps.protocols=TLSv1.2,TLSv1.3" +
	" -Djdk.tls.client.protocols=TLSv1.2,TLSv1.3" +
	" -Djava.net.preferIPv4Stack=true"

// Defaults contains the hardcoded default values for localconfig keys,
// matching the Java LocalConfigCLI behavior. Only keys consumed by
// systemd-envscript.sh and configd itself are included here — not all ~600
// keys from the Java implementation.
//
// Values may contain ${variable} references that are resolved by
// Interpolate after merging with XML overrides.
var Defaults = map[string]string{
	// Core paths
	"zimbra_home":          "/opt/zextras",
	"zimbra_log_directory": "${zimbra_home}/log",
	"mailboxd_directory":   "${zimbra_home}/mailboxd",

	// Logging
	"zimbra_log4j_properties": "${zimbra_home}/conf/log4j.properties",

	// Antispam
	"antispam_enable_restarts":         "true",
	"antispam_enable_rule_compilation": "false",
	"antispam_enable_rule_updates":     "true",

	// JVM / Mailbox
	"networkaddress_cache_ttl":            "60",
	"mailboxd_thread_stack_size":          "256k",
	"mailboxd_java_heap_new_size_percent": "25",
	"mailboxd_java_options":               defaultMailboxdJavaOptions,
	"zimbra_zmjava_options":               defaultZmjavaOptions,
	"zimbra_zmjava_java_library_path":     "",
	"mailboxd_java_heap_size":             "",

	// Configd
	"zmconfigd_listen_port":        "7171",
	"zimbra_configrewrite_timeout": "120",

	// MySQL
	"mysql_errlogfile": "/opt/zextras/log/mysql_error.log",
	"mysql_mycnf":      "/opt/zextras/conf/my.cnf",

	// LDAP (empty defaults — set in localconfig.xml per-installation)
	"ldap_port":     "",
	"ldap_url":      "",
	"ldap_bind_url": "",

	// Server identity
	"zimbra_server_hostname": "localhost",
	"ldap_host":              "",

	// Mail service
	"mail_service_port": "",
}
