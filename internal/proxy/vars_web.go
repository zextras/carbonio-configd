// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"strings"
)

// registerWebVariables registers web proxy configuration variables
func (g *Generator) registerWebVariables() {
	// web.http.port - HTTP port for web proxy
	g.registerVar("web.http.port", 0,
		withAttribute("zimbraMailProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("HTTP port for web proxy"),
	)

	// web.https.port - HTTPS port for web proxy
	g.registerVar("web.https.port", 0,
		withAttribute("zimbraMailSSLProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("HTTPS port for web proxy"),
	)

	// web.http.uport - Upstream mailbox HTTP port (used in stray redirect handling)
	g.registerVar("web.http.uport", 8080,
		withAttribute("zimbraMailPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("Upstream mailbox HTTP port for stray redirect handling"),
	)

	// listen.:addresses - Listen directives for virtual IP addresses (custom resolver)
	g.registerVar("listen.:addresses", "${web.strict.servername}",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Listen directives for virtual IP addresses (expanded from domain zimbraVirtualIPAddress)"),
		withCustomResolver(g.resolveListenAddresses),
	)

	// web.mailmode - Reverse proxy mail mode (https|redirect|both)
	g.registerVar("web.mailmode", "https",
		withAttribute("zimbraReverseProxyMailMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Reverse Proxy Mail Mode - can be https|redirect|both"),
	)

	// web.upstream.name - Upstream name for web proxy
	g.registerVar("web.upstream.name", "zimbra",
		withValueType(ValueTypeString),
		withDescription("Upstream name for web proxy"),
	)

	// web.upstream.webclient.name - Upstream name for webclient
	g.registerVar("web.upstream.webclient.name", "zimbra_webclient",
		withValueType(ValueTypeString),
		withDescription("Upstream name for webclient"),
	)

	// web.upstream.zx.name - Upstream name for zx
	g.registerVar("web.upstream.zx.name", "zx",
		withValueType(ValueTypeString),
		withDescription("Upstream name for zx services"),
	)

	// web.ssl.upstream.name - Upstream name for SSL web proxy
	g.registerVar("web.ssl.upstream.name", "zimbra_ssl",
		withValueType(ValueTypeString),
		withDescription("Upstream name for SSL web proxy"),
	)

	// web.ssl.upstream.webclient.name - Upstream name for SSL webclient
	g.registerVar("web.ssl.upstream.webclient.name", "zimbra_ssl_webclient",
		withValueType(ValueTypeString),
		withDescription("Upstream name for SSL webclient"),
	)

	// web.ssl.upstream.zx.name - Upstream name for SSL zx
	g.registerVar("web.ssl.upstream.zx.name", "zx_ssl",
		withValueType(ValueTypeString),
		withDescription("Upstream name for SSL zx services"),
	)

	// web.ews.upstream.name - Upstream name for EWS
	g.registerVar("web.ews.upstream.name", "zimbra_ews",
		withValueType(ValueTypeString),
		withDescription("Upstream name for Exchange Web Services"),
	)

	// web.ssl.ews.upstream.name - Upstream name for SSL EWS
	g.registerVar("web.ssl.ews.upstream.name", "zimbra_ews_ssl",
		withValueType(ValueTypeString),
		withDescription("Upstream name for SSL Exchange Web Services"),
	)

	// web.upstream.exactversioncheck - Whether to check exact server version for upstreams
	g.registerVar("web.upstream.exactversioncheck", "on",
		withAttribute("zimbraReverseProxyExactServerVersionCheck"),
		withOverrideType(OverrideServer),
		withValueType(ValueTypeString),
		withDescription("Whether nginx matches exact server version against client request"),
	)

	// web.server_names.max_size - Server names hash max size
	g.registerVar("web.server_names.max_size", 512,
		withValueType(ValueTypeInteger),
		withDescription("Server names hash table max size (for many virtual hosts)"),
	)

	// web.server_names.bucket_size - Server names hash bucket size
	g.registerVar("web.server_names.bucket_size", 64,
		withValueType(ValueTypeInteger),
		withDescription("Server names hash table bucket size (for many virtual hosts)"),
	)

	// web.ssl.protocols - SSL/TLS protocols enabled for web proxy
	g.registerVar("web.ssl.protocols", "TLSv1.2 TLSv1.3",
		withAttribute("zimbraReverseProxySSLProtocols"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideServer),
		withDescription("SSL/TLS protocols enabled for web proxy (space-separated)"),
		withCustomResolver(g.resolveWebSSLProtocols),
		withCustomFormatter(func(val any) (string, error) {
			// Format array as space-separated string
			if arr, ok := val.([]string); ok {
				return " " + strings.Join(arr, " "), nil
			}

			return fmt.Sprintf(" %v", val), nil
		}),
	)

	// web.login.upstream.name - Upstream name for login
	g.registerVar("web.login.upstream.name", "zimbra_login",
		withValueType(ValueTypeString),
		withDescription("Upstream name for login"),
	)

	// web.ssl.login.upstream.name - Upstream name for SSL login
	g.registerVar("web.ssl.login.upstream.name", "zimbra_login_ssl",
		withValueType(ValueTypeString),
		withDescription("Upstream name for SSL login"),
	)

	// upstream.disable vars: return "#" to comment out upstream block when no servers configured
	g.registerUpstreamDisableVar("web.login.upstream.disable", "zimbraReverseProxyUpstreamLoginServers",
		"Returns '#' to comment out login upstream block if no servers configured",
		withCustomResolver(g.resolveLoginUpstreamDisable))
	g.registerUpstreamDisableVar("web.ews.upstream.disable", "zimbraReverseProxyUpstreamEwsServers",
		"Returns '#' to comment out EWS upstream block if no servers configured",
		withCustomResolver(g.resolveEwsUpstreamDisable))
	g.registerUpstreamDisableVar("web.zx.upstream.disable", "zimbraReverseProxyUpstreamZxServers",
		"Returns '#' to comment out ZX upstream block if no servers configured",
		withCustomResolver(g.resolveZxUpstreamDisable))
	g.registerUpstreamDisableVar("web.webclient.upstream.disable", "zimbraReverseProxyUpstreamClientServers",
		"Returns '#' to comment out webclient upstream block if no servers configured",
		withCustomResolver(g.resolveWebclientUpstreamDisable))
	g.registerUpstreamDisableVar("web.admin.upstream.disable", "zimbraReverseProxyUpstreamAdminServers",
		"Returns '#' to comment out admin upstream block if no servers configured",
		withCustomResolver(g.resolveAdminUpstreamDisable))

	// web.admin.upstream.name - Upstream name for admin console
	g.registerVar("web.admin.upstream.name", "zimbra_admin",
		withValueType(ValueTypeString),
		withDescription("Upstream name for admin console"),
	)

	// web.admin.upstream.adminclient.name - Upstream name for admin client
	g.registerVar("web.admin.upstream.adminclient.name", "zimbra_adminclient",
		withValueType(ValueTypeString),
		withDescription("Upstream name for admin client"),
	)

	// HTTP upstream :servers (makeBackendResolver(false))
	g.registerBackendServerVar("web.upstream.:servers", "List of upstream servers for web proxy", false)
	g.registerBackendServerVar("web.upstream.webclient.:servers", "List of upstream HTTP webclient servers", false)
	g.registerBackendServerVar("web.upstream.zx.:servers", "List of upstream HTTP zx servers", false)

	// upstream target URLs (scheme determined by zimbraReverseProxySSLToUpstreamEnabled)
	g.registerUpstreamTargetVar("web.upstream.zx", "http://zx", "zx_ssl", "zx",
		"Target URL for zx upstream paths (scheme based on SSL setting)")
	g.registerUpstreamTargetVar("web.upstream.ews.target", "http://zimbra_ews", "zimbra_ews_ssl", "zimbra_ews",
		"Target URL for EWS upstream paths (scheme based on SSL setting)")

	// HTTPS upstream :servers (makeBackendResolver(true))
	g.registerBackendServerVar("web.ssl.upstream.:servers", "List of upstream HTTPS servers", true)
	g.registerBackendServerVar("web.ssl.upstream.webclient.:servers", "List of upstream HTTPS webclient servers", true)
	g.registerBackendServerVar("web.ssl.upstream.zx.:servers", "List of upstream HTTPS zx servers", true)

	// web.ssl.upstream.ewsserver.:servers - Upstream SSL EWS servers
	g.registerVar("web.ssl.upstream.ewsserver.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of upstream HTTPS EWS servers"),
		withCustomResolver(g.makeAttributeResolver("zimbraReverseProxyUpstreamEwsServers", true)),
	)

	// web.ssl.upstream.loginserver.:servers - Upstream SSL login servers
	g.registerVar("web.ssl.upstream.loginserver.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of upstream HTTPS login servers"),
		withCustomResolver(g.makeAttributeResolver("zimbraReverseProxyUpstreamLoginServers", true)),
	)

	// admin console upstream :servers
	g.registerBackendServerVar("web.admin.upstream.:servers", "List of upstream admin console servers", false)
	g.registerBackendServerVar("web.admin.upstream.adminclient.:servers", "List of upstream admin client servers", false)
	g.registerBackendServerVar("web.admin.ssl.upstream.:servers", "List of upstream HTTPS admin console servers", true)
	g.registerBackendServerVar("web.admin.ssl.upstream.adminclient.:servers",
		"List of upstream HTTPS admin client servers", true)

	// web.enabled - Web proxy enabler
	g.registerVar("web.enabled", false,
		withAttribute("zimbraReverseProxyHttpEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether HTTP/HTTPS web proxy is enabled"),
	)

	// web.http.enabled - HTTP protocol enabler (false if zimbraReverseProxyMailMode is 'https')
	g.registerVar("web.http.enabled", true,
		withValueType(ValueTypeEnabler),
		//nolint:lll
		withDescription("Indicates whether HTTP proxy will accept connections (true unless zimbraReverseProxyMailMode is 'https')"),
		withCustomResolver(g.resolveHTTPEnabled),
	)

	// web.https.enabled - HTTPS protocol enabler (false if zimbraReverseProxyMailMode is 'http')
	g.registerVar("web.https.enabled", true,
		withValueType(ValueTypeEnabler),
		//nolint:lll
		withDescription("Indicates whether HTTPS proxy will accept connections (true unless zimbraReverseProxyMailMode is 'http')"),
		withCustomResolver(g.resolveHTTPSEnabled),
	)

	// web.upstream.target - Web proxy upstream target
	g.registerVar("web.upstream.target", "http://zimbra",
		withValueType(ValueTypeString),
		withDescription("Upstream target name for web proxy"),
	)

	// web.server_name.default - Server name for default vhost
	g.registerVar("web.server_name.default", "",
		withAttribute("zimbraServiceHostname"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideServer),
		withDescription("Server name (hostname) for default HTTPS/HTTP virtual host"),
	)

	// web.admin.uiport - Admin UI port
	g.registerVar("web.admin.uiport", 7071,
		withAttribute("zimbraAdminPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console UI port"),
	)

	// web.admin.default.enabled - Admin console proxy enabler
	g.registerVar("web.admin.default.enabled", false,
		withAttribute("zimbraReverseProxyAdminEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether admin console proxy is enabled"),
	)

	// web.upload.max - Maximum upload file size
	g.registerVar("web.upload.max", 10485760,
		withAttribute("zimbraFileUploadMaxSize"),
		withValueType(ValueTypeLong),
		withOverrideType(OverrideConfig),
		withDescription("Maximum file upload size in bytes"),
	)

	// web.logfile - Web proxy access log file path
	g.registerVar("web.logfile", "/opt/zextras/log/nginx.access.log",
		withValueType(ValueTypeString),
		withDescription("Path to nginx access log file"),
	)

	// web.response.headers - Custom response headers
	g.registerVar("web.response.headers", "",
		withAttribute("zimbraReverseProxyResponseHeaders"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Custom HTTP response headers to add"),
	)

	// web.add.headers.default - Generated add_header directives for default vhost
	g.registerVar("web.add.headers.default", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Generated add_header directives block for default virtual host"),
		withCustomResolver(g.resolveAddHeadersDefault),
	)

	// web.errpages - Error page directives for 502 and 504 (custom resolver)
	g.registerVar("web.errpages", "",
		withAttribute("zimbraReverseProxyErrorHandlerURL"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Error page directives for 502 and 504 errors"),
		withCustomResolver(g.resolveErrorPages),
	)

	// web.upstream.target.available - Check if web upstream targets exist (custom resolver)
	g.registerVar("web.upstream.target.available", true,
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideCustom),
		withDescription("Whether web upstream targets are available"),
		withCustomResolver(g.resolveWebUpstreamTargetAvailable),
	)

	// Carbonio WebUI Custom URLs
	g.registerLoginURLVar("web.carbonio.webui.login.url.default", "carbonioWebUILoginURL",
		"Custom login URL for Carbonio WebUI on default virtual host")
	g.registerLoginURLVar("web.carbonio.webui.login.url.vhost", "carbonioWebUILoginURL",
		"Custom login URL for Carbonio WebUI on per-domain virtual hosts")
	g.registerLogoutRedirectVar("web.carbonio.webui.logout.redirect.default", "carbonioWebUILogoutURL",
		"Custom logout redirect for Carbonio WebUI on default virtual host (return statement)")
	g.registerLogoutRedirectVar("web.carbonio.webui.logout.redirect.vhost", "carbonioWebUILogoutURL",
		"Custom logout redirect for Carbonio WebUI on per-domain virtual hosts (return statement)")

	// Carbonio Admin Console Custom URLs
	g.registerLoginURLVar("web.carbonio.admin.login.url.default", "carbonioAdminUILoginURL",
		"Custom login URL for Carbonio Admin Console on default virtual host")
	g.registerLoginURLVar("web.carbonio.admin.login.url.vhost", "carbonioAdminUILoginURL",
		"Custom login URL for Carbonio Admin Console on per-domain virtual hosts")
	g.registerLogoutRedirectVar("web.carbonio.admin.logout.redirect.default", "carbonioAdminUILogoutURL",
		"Custom logout redirect for Carbonio Admin Console on default virtual host (return statement)")
	g.registerLogoutRedirectVar("web.carbonio.admin.logout.redirect.vhost", "carbonioAdminUILogoutURL",
		"Custom logout redirect for Carbonio Admin Console on per-domain virtual hosts (return statement)")

	// web.carbonio.admin.port - Carbonio Admin UI proxy port
	g.registerVar("web.carbonio.admin.port", 6071,
		withAttribute("carbonioAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("Carbonio Admin UI proxy port"),
	)

	// web.server.version.check - Exact server version check
	g.registerVar("web.server.version.check", true,
		withAttribute("zimbraReverseProxyExactServerVersionCheck"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Whether to check exact server version for compatibility"),
	)

	// web.upstream.ewsserver.:servers - EWS upstream servers
	g.registerVar("web.upstream.ewsserver.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of EWS (Exchange Web Services) upstream servers"),
		withCustomResolver(g.makeAttributeResolver("zimbraReverseProxyUpstreamEwsServers", false)),
	)

	// web.upstream.loginserver.:servers - Login upstream servers
	g.registerVar("web.upstream.loginserver.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of upstream login servers"),
		withCustomResolver(g.makeAttributeResolver("zimbraReverseProxyUpstreamLoginServers", false)),
	)

	// web.error.pages.enabled - Enable custom error pages
	g.registerVar("web.error.pages.enabled", false,
		withAttribute("zimbraReverseProxyErrorPagesEnabled"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Enable custom error pages for proxy"),
	)

	// web.strict.servername - Enforce strict server name matching
	g.registerVar("web.strict.servername", "#",
		withAttribute("zimbraReverseProxyStrictServerNameEnabled"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideConfig),
		withDescription("Returns '' to enable strict server name block or '#' to comment it out"),
		withCustomResolver(g.resolveStrictServerName),
	)

	// web.upstream.buffers.num - Number of upstream buffers
	g.registerVar("web.upstream.buffers.num", 8,
		withAttribute("zimbraReverseProxyUpstreamBuffersNumber"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Number of buffers for reading upstream response"),
	)

	// web.upstream.buffers.size - Size of each upstream buffer
	g.registerVar("web.upstream.buffers.size", "4k",
		withAttribute("zimbraReverseProxyUpstreamBuffersSize"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Size of each buffer for reading upstream response"),
	)

	// proxy.http.compression - HTTP compression directives (gzip + brotli)
	g.registerVar("proxy.http.compression", "",
		withAttribute("zimbraHttpCompressionEnabled"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideServer),
		withDescription("HTTP compression directives (gzip and brotli configuration block)"),
		withCustomResolver(g.resolveProxyHTTPCompression),
	)

	// upstream.fair.shm.size - Shared memory size for fair upstream (custom resolver)
	g.registerVar("upstream.fair.shm.size", "32",
		withAttribute("zimbraReverseProxyUpstreamFairShmSize"),
		withOverrideType(OverrideCustom),
		withDescription("Shared memory size for fair load balancing (formatted as upstream_fair_shm_size <size>k;)"),
		withCustomResolver(g.resolveUpstreamFairShmSize),
	)

	// web.admin.port - Admin console HTTP port
	g.registerVar("web.admin.port", 7071,
		withAttribute("zimbraAdminPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console HTTP port"),
	)

	// web.admin.uport - Admin console upstream port
	g.registerVar("web.admin.uport", 9071,
		withAttribute("zimbraAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console upstream proxy port"),
	)

	// upstream target URLs for login and webclient
	g.registerUpstreamTargetVar("web.upstream.login.target", "http://zimbra_login", "zimbra_login_ssl", "zimbra_login",
		"Target URL for login upstream (scheme determined by SSL setting)")
	g.registerUpstreamTargetVar("web.upstream.webclient.target", "http://zimbra_webclient",
		"zimbra_ssl_webclient", "zimbra_webclient",
		"Target URL for webclient upstream (scheme determined by SSL setting)")
}

// registerBackendServerVar registers a :servers variable using makeBackendResolver.
// ssl=true registers an HTTPS upstream, ssl=false registers an HTTP upstream.
func (g *Generator) registerBackendServerVar(key, desc string, ssl bool) {
	g.registerVar(key, "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription(desc),
		withCustomResolver(g.makeBackendResolver(ssl)),
	)
}

// registerUpstreamDisableVar registers an upstream.disable variable that returns "#"
// to comment out the upstream block when no servers are configured.
func (g *Generator) registerUpstreamDisableVar(key, attr, desc string, resolver varOpt) {
	g.registerVar(key, "#",
		withAttribute(attr),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideConfig),
		withDescription(desc),
		resolver,
	)
}

// registerUpstreamTargetVar registers an upstream target URL variable whose scheme
// (http:// vs https://) is determined by zimbraReverseProxySSLToUpstreamEnabled.
func (g *Generator) registerUpstreamTargetVar(key, defaultVal, sslName, plainName, desc string) {
	g.registerVar(key, defaultVal,
		withAttribute("zimbraReverseProxySSLToUpstreamEnabled"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideServer),
		withDescription(desc),
		withCustomResolver(g.makeUpstreamTargetResolver(sslName, plainName)),
	)
}

// registerLoginURLVar registers a login URL variable for a Carbonio UI attribute.
func (g *Generator) registerLoginURLVar(key, attr, desc string) {
	g.registerVar(key, staticLoginPath,
		withAttribute(attr),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription(desc),
		withCustomResolver(g.makeLoginURLResolver(attr)),
	)
}

// registerLogoutRedirectVar registers a logout redirect variable for a Carbonio UI attribute.
func (g *Generator) registerLogoutRedirectVar(key, attr, desc string) {
	g.registerVar(key, "",
		withAttribute(attr),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription(desc),
		withCustomResolver(g.makeLogoutRedirectResolver(attr)),
	)
}
