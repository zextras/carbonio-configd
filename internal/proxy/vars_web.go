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
	g.registerWebPortVariables()
	g.registerWebUpstreamNameVariables()
	g.registerWebSSLVariables()
	g.registerWebAuthVariables()
	g.registerWebHeaderVariables()
	g.registerWebMiscVariables()
}

// registerWebPortVariables registers port-related configuration variables
func (g *Generator) registerWebPortVariables() {
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

	// web.admin.port - Admin console HTTP port
	g.registerVar("web.admin.port", 7071,
		withAttribute("zimbraAdminPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console HTTP port"),
	)

	// web.admin.uiport - Admin UI port
	g.registerVar("web.admin.uiport", 7071,
		withAttribute("zimbraAdminPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console UI port"),
	)

	// web.admin.uport - Admin console upstream port
	g.registerVar("web.admin.uport", 9071,
		withAttribute("zimbraAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console upstream proxy port"),
	)

	// web.carbonio.admin.port - Carbonio Admin UI proxy port
	g.registerVar("web.carbonio.admin.port", 6071,
		withAttribute("carbonioAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("Carbonio Admin UI proxy port"),
	)
}

// registerWebUpstreamNameVariables registers upstream name configuration variables
func (g *Generator) registerWebUpstreamNameVariables() {
	// web.mailmode - Reverse proxy mail mode (https|redirect|both)
	g.registerVar("web.mailmode", "https",
		withAttribute("zimbraReverseProxyMailMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Reverse Proxy Mail Mode - can be https|redirect|both"),
	)

	// Plain upstream-name string variables (no attribute/override binding).
	g.registerStringVar("web.upstream.name", "zimbra", "Upstream name for web proxy")
	g.registerStringVar("web.upstream.webclient.name", "zimbra_webclient", "Upstream name for webclient")
	g.registerStringVar("web.upstream.zx.name", "zx", "Upstream name for zx services")
	g.registerStringVar("web.ssl.upstream.name", "zimbra_ssl", "Upstream name for SSL web proxy")
	g.registerStringVar("web.ssl.upstream.webclient.name", "zimbra_ssl_webclient", "Upstream name for SSL webclient")
	g.registerStringVar("web.ssl.upstream.zx.name", "zx_ssl", "Upstream name for SSL zx services")
	g.registerStringVar("web.ews.upstream.name", "zimbra_ews", "Upstream name for Exchange Web Services")
	g.registerStringVar("web.ssl.ews.upstream.name", "zimbra_ews_ssl", "Upstream name for SSL Exchange Web Services")
	g.registerStringVar("web.login.upstream.name", "zimbra_login", "Upstream name for login")
	g.registerStringVar("web.ssl.login.upstream.name", "zimbra_login_ssl", "Upstream name for SSL login")
	g.registerStringVar("web.admin.upstream.name", "zimbra_admin", "Upstream name for admin console")
	g.registerStringVar("web.admin.upstream.adminclient.name", "zimbra_adminclient", "Upstream name for admin client")
	g.registerStringVar("web.upstream.target", "http://zimbra", "Upstream target name for web proxy")

	// web.server_name.default - Server name for default vhost
	g.registerVar("web.server_name.default", "",
		withAttribute("zimbraServiceHostname"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideServer),
		withDescription("Server name (hostname) for default HTTPS/HTTP virtual host"),
	)
}

// registerWebSSLVariables registers SSL/TLS and security-related configuration variables
func (g *Generator) registerWebSSLVariables() {
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

	// web.server.version.check - Exact server version check
	g.registerVar("web.server.version.check", true,
		withAttribute("zimbraReverseProxyExactServerVersionCheck"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Whether to check exact server version for compatibility"),
	)

	// web.ssl.dhparam.enabled - SSL DH parameter file exists
	g.registerVar("web.ssl.dhparam.enabled", false,
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Whether the web SSL DH parameter file exists (legacy WebSSLDhparamEnablerVar)"),
		withCustomResolver(g.resolveWebSSLDhparamEnabled),
	)

	// web.strict.servername - Enforce strict server name matching
	g.registerVar("web.strict.servername", "#",
		withAttribute("zimbraReverseProxyStrictServerNameEnabled"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideConfig),
		withDescription("Returns '' to enable strict server name block or '#' to comment it out"),
		withCustomResolver(g.resolveStrictServerName),
	)

	// proxy.http.compression - HTTP compression directives (gzip + brotli)
	g.registerVar("proxy.http.compression", "",
		withAttribute("zimbraHttpCompressionEnabled"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideServer),
		withDescription("HTTP compression directives (gzip and brotli configuration block)"),
		withCustomResolver(g.resolveProxyHTTPCompression),
	)
}

// registerWebAuthVariables registers authentication and upstream-related configuration variables
func (g *Generator) registerWebAuthVariables() {
	// web.login.upstream.url - Login URL
	g.registerVar("web.login.upstream.url", "/",
		withAttribute("zimbraMailURL"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideServer),
		withDescription("Zimbra Login URL"),
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

	// Upstream :servers variables. Each mirrors a Java *UpstreamServersVar subclass:
	// the candidate servers come from a per-service union (getAllMailClientServers /
	// getAllWebClientServers / getAllAdminClientServers) or an explicit attribute list
	// (EWS / login), filtered by isValidUpstream, with ports from the configured port
	// attribute (or the fixed ZX ports). See upstream_spec.go.
	mailClientSvcs := []string{serviceMailbox, serviceMailclient}
	webClientSvcs := []string{serviceMailbox, serviceWebclient}
	adminClientSvcs := []string{serviceMailbox, serviceAdminclient}

	// HTTP (non-SSL): port from zimbraReverseProxyHttpPortAttribute (def zimbraMailPort)
	g.registerUpstreamServersVar("web.upstream.:servers", "List of upstream HTTP servers used by Web Proxy",
		&upstreamSpec{services: mailClientSvcs, portAttrKey: attrHTTPPortAttribute, portAttrDefault: defaultHTTPPortAttr})
	g.registerUpstreamServersVar("web.upstream.webclient.:servers", "List of upstream HTTP webclient servers",
		&upstreamSpec{services: webClientSvcs, portAttrKey: attrHTTPPortAttribute, portAttrDefault: defaultHTTPPortAttr})
	g.registerUpstreamServersVar("web.upstream.zx.:servers", "List of upstream HTTP zx servers",
		&upstreamSpec{services: mailClientSvcs, fixedPort: zxUpstreamPort})
	g.registerUpstreamServersVar("web.upstream.ewsserver.:servers", "List of EWS upstream servers",
		&upstreamSpec{
			attrList: attrUpstreamEwsServers, portAttrKey: attrHTTPPortAttribute, portAttrDefault: defaultHTTPPortAttr,
		})
	g.registerUpstreamServersVar("web.upstream.loginserver.:servers", "List of upstream login servers",
		&upstreamSpec{
			attrList: attrUpstreamLoginServers, portAttrKey: attrHTTPPortAttribute, portAttrDefault: defaultHTTPPortAttr,
		})

	// HTTPS (SSL): port from zimbraReverseProxyHttpSSLPortAttribute (def zimbraMailSSLPort)
	g.registerUpstreamServersVar("web.ssl.upstream.:servers", "List of upstream HTTPS servers",
		&upstreamSpec{
			services: mailClientSvcs, portAttrKey: attrHTTPSSLPortAttribute, portAttrDefault: defaultHTTPSSLPortAttr,
		})
	g.registerUpstreamServersVar("web.ssl.upstream.webclient.:servers", "List of upstream HTTPS webclient servers",
		&upstreamSpec{
			services: webClientSvcs, portAttrKey: attrHTTPSSLPortAttribute, portAttrDefault: defaultHTTPSSLPortAttr,
		})
	g.registerUpstreamServersVar("web.ssl.upstream.zx.:servers", "List of upstream HTTPS zx servers",
		&upstreamSpec{services: mailClientSvcs, fixedPort: zxUpstreamSSLPort})
	g.registerUpstreamServersVar("web.ssl.upstream.ewsserver.:servers", "List of upstream HTTPS EWS servers",
		&upstreamSpec{
			attrList: attrUpstreamEwsServers, portAttrKey: attrHTTPSSLPortAttribute, portAttrDefault: defaultHTTPSSLPortAttr,
		})
	g.registerUpstreamServersVar("web.ssl.upstream.loginserver.:servers", "List of upstream HTTPS login servers",
		&upstreamSpec{
			attrList: attrUpstreamLoginServers, portAttrKey: attrHTTPSSLPortAttribute, portAttrDefault: defaultHTTPSSLPortAttr,
		})

	// Admin console: port from zimbraReverseProxyAdminPortAttribute (def zimbraAdminPort).
	// Java has no separate SSL admin servers var; the SSL keys mirror the non-SSL ones.
	g.registerUpstreamServersVar("web.admin.upstream.:servers", "List of upstream admin console servers",
		&upstreamSpec{services: mailClientSvcs, portAttrKey: attrAdminPortAttribute, portAttrDefault: defaultAdminPortAttr})
	g.registerUpstreamServersVar("web.admin.upstream.adminclient.:servers", "List of upstream admin client servers",
		&upstreamSpec{services: adminClientSvcs, portAttrKey: attrAdminPortAttribute, portAttrDefault: defaultAdminPortAttr})
	g.registerUpstreamServersVar("web.admin.ssl.upstream.:servers", "List of upstream HTTPS admin console servers",
		&upstreamSpec{services: mailClientSvcs, portAttrKey: attrAdminPortAttribute, portAttrDefault: defaultAdminPortAttr})
	g.registerUpstreamServersVar("web.admin.ssl.upstream.adminclient.:servers",
		"List of upstream HTTPS admin client servers",
		&upstreamSpec{services: adminClientSvcs, portAttrKey: attrAdminPortAttribute, portAttrDefault: defaultAdminPortAttr})

	// upstream target URLs (scheme determined by zimbraReverseProxySSLToUpstreamEnabled)
	g.registerUpstreamTargetVar("web.upstream.zx", "http://zx", "zx_ssl", "zx",
		"Target URL for zx upstream paths (scheme based on SSL setting)")
	g.registerUpstreamTargetVar("web.upstream.ews.target", "http://zimbra_ews", "zimbra_ews_ssl", "zimbra_ews",
		"Target URL for EWS upstream paths (scheme based on SSL setting)")
	g.registerUpstreamTargetVar("web.upstream.login.target", "http://zimbra_login", "zimbra_login_ssl", "zimbra_login",
		"Target URL for login upstream (scheme determined by SSL setting)")
	g.registerUpstreamTargetVar("web.upstream.webclient.target", "http://zimbra_webclient",
		"zimbra_ssl_webclient", "zimbra_webclient",
		"Target URL for webclient upstream (scheme determined by SSL setting)")

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
}

// registerWebHeaderVariables registers header and response-related configuration variables
func (g *Generator) registerWebHeaderVariables() {
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

	// web.add.headers.vhost - Generated add_header directives for per-domain vhost
	g.registerVar("web.add.headers.vhost", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Generated add_header directives block for per-domain virtual hosts"),
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

	// web.error.pages.enabled - Enable custom error pages
	g.registerVar("web.error.pages.enabled", false,
		withAttribute("zimbraReverseProxyErrorPagesEnabled"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Enable custom error pages for proxy"),
	)
}

// registerWebMiscVariables registers miscellaneous configuration variables
func (g *Generator) registerWebMiscVariables() {
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

	// web.admin.default.enabled - Admin console proxy enabler
	g.registerVar("web.admin.default.enabled", false,
		withAttribute("zimbraReverseProxyAdminEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether admin console proxy is enabled"),
	)

	// web.available - Web availability check
	g.registerVar("web.available", false,
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Whether at least one web upstream server is configured (legacy ZMWebAvailableVar)"),
		withCustomResolver(g.resolveWebAvailable),
	)

	// web.upstream.target.available - Check if web upstream targets exist (custom resolver)
	g.registerVar("web.upstream.target.available", true,
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideCustom),
		withDescription("Whether web upstream targets are available"),
		withCustomResolver(g.resolveWebUpstreamTargetAvailable),
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

	// upstream.fair.shm.size - Shared memory size for fair upstream (custom resolver)
	g.registerVar("upstream.fair.shm.size", "32",
		withAttribute("zimbraReverseProxyUpstreamFairShmSize"),
		withOverrideType(OverrideCustom),
		withDescription("Shared memory size for fair load balancing (formatted as upstream_fair_shm_size <size>k;)"),
		withCustomResolver(g.resolveUpstreamFairShmSize),
	)
}

// registerStringVar registers a plain string variable with no attribute or
// override binding. It collapses the many near-identical upstream-name
// registrations into a single call site.
func (g *Generator) registerStringVar(key, defaultVal, desc string) {
	g.registerVar(key, defaultVal,
		withValueType(ValueTypeString),
		withDescription(desc),
	)
}

// registerUpstreamServersVar registers a :servers variable resolved by the
// Java-faithful upstream resolver (see upstream_spec.go / makeUpstreamResolver).
func (g *Generator) registerUpstreamServersVar(key, desc string, spec *upstreamSpec) {
	g.registerVar(key, "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription(desc),
		withCustomResolver(g.makeUpstreamResolver(spec)),
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
