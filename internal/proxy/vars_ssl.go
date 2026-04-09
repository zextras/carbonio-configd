// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// webSSLCiphersDefault is the default cipher suite for web proxy SSL.
//
//nolint:lll
const webSSLCiphersDefault = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384"

// registerSSLVariables registers SSL-related configuration variables
func (g *Generator) registerSSLVariables() {
	g.registerVar("ssl.crt.default", g.ConfDir+"/nginx.crt",
		withValueType(ValueTypeString),
		withDescription("Default SSL certificate path"),
	)
	g.registerVar("ssl.key.default", g.ConfDir+"/nginx.key",
		withValueType(ValueTypeString),
		withDescription("Default SSL private key path"),
	)
	g.registerVar("ssl.clientcertca.default", g.ConfDir+"/nginx.client.ca.crt",
		withOverrideType(OverrideCustom),
		withDescription("Default client CA certificate path"),
		withCustomResolver(g.resolveClientCertCADefault),
	)
	g.registerVar("ssl.dhparam.enabled", "",
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideCustom),
		withDescription("Indicates whether DH parameters are enabled (empty if disabled)"),
		withCustomResolver(g.resolveDHParamEnabled),
	)
	g.registerVar("ssl.dhparam.file", g.ConfDir+"/dhparam.pem",
		withValueType(ValueTypeString),
		withDescription("DH parameters file path"),
	)
	g.registerVar("ssl.ciphers", "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256",
		withAttribute("zimbraReverseProxySSLCiphers"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SSL cipher suite configuration"),
	)
	g.registerVar("ssl.protocols", "TLSv1.2 TLSv1.3",
		withAttribute("zimbraReverseProxySSLProtocols"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SSL/TLS protocol versions to enable"),
	)
	g.registerVar("ssl.ecdh.curve", "auto",
		withAttribute("zimbraReverseProxySSLECDHCurve"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SSL ECDH curve for key exchange"),
	)
	g.registerVar("ssl.session.timeout", 600000,
		withAttribute("zimbraReverseProxySSLSessionTimeout"),
		withValueType(ValueTypeTimeInSec),
		withOverrideType(OverrideServer),
		withDescription("SSL session timeout value for the proxy in secs"),
	)
	g.registerVar("ssl.session.cachesize", "10m",
		withAttribute("zimbraReverseProxySSLSessionCacheSize"),
		withOverrideType(OverrideCustom),
		withDescription("SSL session cache size (formatted as shared:SSL:<size>)"),
		withCustomResolver(g.resolveSSLSessionCacheSize),
	)
	g.registerVar("web.ssl.ciphers", webSSLCiphersDefault,
		withAttribute("zimbraReverseProxySSLCiphers"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Permitted ciphers for web proxy"),
	)
	g.registerVar("web.ssl.preferserverciphers", "on",
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Requires TLS protocol server ciphers be preferred over the client's ciphers"),
	)
	g.registerVar("web.ssl.ecdh.curve", "auto",
		withAttribute("zimbraReverseProxySSLECDHCurve"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SSL ECDH cipher curve for web proxy"),
	)
	g.registerVar("web.ssl.dhparam.file", "/opt/zextras/conf/dhparam.pem",
		withDescription("Filename with DH parameters for EDH ciphers to be used by the proxy"),
	)
	g.registerVar("ssl.clientcertmode", "off",
		withAttribute("zimbraReverseProxyClientCertMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Client certificate mode (off/on/optional/optional_no_ca)"),
	)
	g.registerVar("ssl.clientcertmode.default", "off",
		withAttribute("zimbraReverseProxyClientCertMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Client certificate mode for default vhost (off/on/optional/optional_no_ca)"),
	)
	g.registerVar("ssl.verify.depth", 10,
		withAttribute("zimbraReverseProxySSLVerifyDepth"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum depth for SSL certificate verification chain"),
	)
	g.registerVar("ssl.clientcertdepth.default", 10,
		withAttribute("zimbraReverseProxySSLVerifyDepth"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum depth for SSL certificate verification chain for default vhost"),
	)
}
