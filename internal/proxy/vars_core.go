// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

const nginxConfPrefix = "nginx.conf"

// registerCoreVariables registers core proxy configuration variables
func (g *Generator) registerCoreVariables() {
	g.registerVar("core.workdir", g.WorkingDir,
		withDescription("Working Directory for NGINX worker processes"),
	)
	g.registerVar("core.includes", g.IncludesDir,
		withDescription("Include directory (relative to ${core.workdir}/conf)"),
	)
	g.registerVar("core.cprefix", nginxConfPrefix,
		withValueType(ValueTypeString),
		withDescription("Common config file prefix"),
	)
	g.registerVar("core.tprefix", nginxConfPrefix,
		withValueType(ValueTypeString),
		withDescription("Common template file prefix"),
	)
	g.registerVar("core.ipv4only.enabled", "",
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Indicates whether the IP mode is IPv4 only (empty if false)"),
		withCustomResolver(g.makeIPModeResolver("ipv4")),
	)
	g.registerVar("core.ipv6only.enabled", "",
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Indicates whether the IP mode is IPv6 only (empty if false)"),
		withCustomResolver(g.makeIPModeResolver("ipv6")),
	)
	g.registerVar("core.ipboth.enabled", "",
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Indicates whether the IP mode is dual stack (empty if false)"),
		withCustomResolver(g.makeIPModeResolver(ipModeBoth)),
	)
	g.registerVar("main.workers", 4,
		withAttribute("zimbraReverseProxyWorkerProcesses"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Number of NGINX worker processes"),
	)
	g.registerVar("main.workerConnections", 10240,
		withAttribute("zimbraReverseProxyWorkerConnections"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum number of simultaneous connections per worker process"),
	)
	g.registerVar("main.accept_mutex", "on",
		withAttribute("zimbraReverseProxyAcceptMutex"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Accept mutex for worker load balancing (on/off)"),
	)
	g.registerVar("main.logfile", "/opt/zextras/log/nginx.log",
		withValueType(ValueTypeString),
		withDescription("Path to NGINX error log file"),
	)
	g.registerVar("main.krb5keytab", "/opt/zextras/conf/krb5.keytab",
		withAttribute("zimbraReverseProxyKrb5Keytab"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Kerberos 5 keytab file location"),
	)
	g.registerVar("main.logLevel", "info",
		withAttribute("zimbraReverseProxyLogLevel"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Log level for NGINX error log (debug, info, notice, warn, error, crit)"),
	)
}
