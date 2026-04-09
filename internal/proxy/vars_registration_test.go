// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/proxy"
)

// newMinimalGenerator creates a Generator with empty configs and nil LDAP/cache,
// sufficient for verifying variable registration without external dependencies.
func newMinimalGenerator(t *testing.T) *proxy.Generator {
	t.Helper()

	cfg := &config.Config{
		BaseDir:  t.TempDir(),
		Hostname: "test.example.com",
	}

	gen, err := proxy.LoadConfiguration(
		context.Background(), cfg, nil, nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("LoadConfiguration failed: %v", err)
	}

	return gen
}

func TestAllVariableKeysRegistered(t *testing.T) {
	gen := newMinimalGenerator(t)

	// Every key listed below comes from one of the 12 vars_*.go files.
	// The table is grouped by source file for easy traceability.
	keys := []struct {
		source string
		key    string
	}{
		// vars_core.go
		{"core", "core.workdir"},
		{"core", "core.includes"},
		{"core", "core.cprefix"},
		{"core", "core.tprefix"},
		{"core", "core.ipv4only.enabled"},
		{"core", "core.ipv6only.enabled"},
		{"core", "core.ipboth.enabled"},
		{"core", "main.workers"},
		{"core", "main.workerConnections"},
		{"core", "main.accept_mutex"},
		{"core", "main.logfile"},
		{"core", "main.krb5keytab"},
		{"core", "main.logLevel"},

		// vars_ssl.go
		{"ssl", "ssl.crt.default"},
		{"ssl", "ssl.key.default"},
		{"ssl", "ssl.clientcertca.default"},
		{"ssl", "ssl.dhparam.enabled"},
		{"ssl", "ssl.dhparam.file"},
		{"ssl", "ssl.ciphers"},
		{"ssl", "ssl.protocols"},
		{"ssl", "ssl.ecdh.curve"},
		{"ssl", "ssl.session.timeout"},
		{"ssl", "ssl.session.cachesize"},
		{"ssl", "web.ssl.ciphers"},
		{"ssl", "web.ssl.preferserverciphers"},
		{"ssl", "web.ssl.ecdh.curve"},
		{"ssl", "web.ssl.dhparam.file"},
		{"ssl", "ssl.clientcertmode"},
		{"ssl", "ssl.clientcertmode.default"},
		{"ssl", "ssl.verify.depth"},
		{"ssl", "ssl.clientcertdepth.default"},

		// vars_web.go
		{"web", "web.http.port"},
		{"web", "web.https.port"},
		{"web", "web.http.uport"},
		{"web", "listen.:addresses"},
		{"web", "web.mailmode"},
		{"web", "web.upstream.name"},
		{"web", "web.upstream.webclient.name"},
		{"web", "web.upstream.zx.name"},
		{"web", "web.ssl.upstream.name"},
		{"web", "web.ssl.upstream.webclient.name"},
		{"web", "web.ssl.upstream.zx.name"},
		{"web", "web.ews.upstream.name"},
		{"web", "web.ssl.ews.upstream.name"},
		{"web", "web.upstream.exactversioncheck"},
		{"web", "web.server_names.max_size"},
		{"web", "web.server_names.bucket_size"},
		{"web", "web.ssl.protocols"},
		{"web", "web.login.upstream.name"},
		{"web", "web.ssl.login.upstream.name"},
		{"web", "web.login.upstream.disable"},
		{"web", "web.ews.upstream.disable"},
		{"web", "web.zx.upstream.disable"},
		{"web", "web.webclient.upstream.disable"},
		{"web", "web.admin.upstream.disable"},
		{"web", "web.admin.upstream.name"},
		{"web", "web.admin.upstream.adminclient.name"},
		{"web", "web.upstream.:servers"},
		{"web", "web.upstream.webclient.:servers"},
		{"web", "web.upstream.zx.:servers"},
		{"web", "web.upstream.zx"},
		{"web", "web.upstream.ews.target"},
		{"web", "web.ssl.upstream.:servers"},
		{"web", "web.ssl.upstream.webclient.:servers"},
		{"web", "web.ssl.upstream.zx.:servers"},
		{"web", "web.ssl.upstream.ewsserver.:servers"},
		{"web", "web.ssl.upstream.loginserver.:servers"},
		{"web", "web.admin.upstream.:servers"},
		{"web", "web.admin.upstream.adminclient.:servers"},
		{"web", "web.admin.ssl.upstream.:servers"},
		{"web", "web.admin.ssl.upstream.adminclient.:servers"},
		{"web", "web.enabled"},
		{"web", "web.http.enabled"},
		{"web", "web.https.enabled"},
		{"web", "web.upstream.target"},
		{"web", "web.server_name.default"},
		{"web", "web.admin.uiport"},
		{"web", "web.admin.default.enabled"},
		{"web", "web.upload.max"},
		{"web", "web.logfile"},
		{"web", "web.response.headers"},
		{"web", "web.add.headers.default"},
		{"web", "web.errpages"},
		{"web", "web.upstream.target.available"},
		{"web", "web.carbonio.webui.login.url.default"},
		{"web", "web.carbonio.webui.login.url.vhost"},
		{"web", "web.carbonio.webui.logout.redirect.default"},
		{"web", "web.carbonio.webui.logout.redirect.vhost"},
		{"web", "web.carbonio.admin.login.url.default"},
		{"web", "web.carbonio.admin.login.url.vhost"},
		{"web", "web.carbonio.admin.logout.redirect.default"},
		{"web", "web.carbonio.admin.logout.redirect.vhost"},
		{"web", "web.carbonio.admin.port"},
		{"web", "web.server.version.check"},
		{"web", "web.upstream.ewsserver.:servers"},
		{"web", "web.upstream.loginserver.:servers"},
		{"web", "web.error.pages.enabled"},
		{"web", "web.strict.servername"},
		{"web", "web.upstream.buffers.num"},
		{"web", "web.upstream.buffers.size"},
		{"web", "proxy.http.compression"},
		{"web", "upstream.fair.shm.size"},
		{"web", "web.admin.port"},
		{"web", "web.admin.uport"},
		{"web", "web.upstream.login.target"},
		{"web", "web.upstream.webclient.target"},

		// vars_mail.go
		{"mail", "mail.enabled"},
		{"mail", "mail.imap.port"},
		{"mail", "mail.imaps.port"},
		{"mail", "mail.pop3.port"},
		{"mail", "mail.pop3s.port"},
		{"mail", "mail.imap.enabled"},
		{"mail", "mail.imaps.enabled"},
		{"mail", "mail.pop3.enabled"},
		{"mail", "mail.pop3s.enabled"},
		{"mail", "mail.mode"},
		{"mail", "mail.defaultrealm"},
		{"mail", "mail.passerrors"},
		{"mail", "mail.imap.proxytimeout"},
		{"mail", "mail.ctimeout"},
		{"mail", "mail.usermax"},
		{"mail", "mail.userttl"},
		{"mail", "mail.userrej"},
		{"mail", "mail.whitelist.ttl"},
		{"mail", "mail.upstream.imapid"},
		{"mail", "mail.proxy.ssl"},
		{"mail", "mail.ssl.preferserverciphers"},
		{"mail", "mail.ssl.protocols"},
		{"mail", "mail.ssl.ciphers"},
		{"mail", "mail.ssl.ecdh.curve"},
		{"mail", "mail.saslapp"},
		{"mail", "mail.sasl_host_from_ip"},
		{"mail", "mail.imapmax"},
		{"mail", "mail.pop3max"},
		{"mail", "mail.ipmax"},
		{"mail", "mail.imapttl"},
		{"mail", "mail.pop3ttl"},
		{"mail", "mail.ipttl"},
		{"mail", "mail.iprej"},

		// vars_admin.go
		{"admin", "admin.console.upstream.name"},
		{"admin", "admin.upstream.:servers"},
		{"admin", "admin.console.proxy.port"},
		{"admin", "admin.console.upstream.adminclient.:servers"},

		// vars_memcache.go
		{"memcache", "memcache.:servers"},
		{"memcache", "memcache.timeout"},
		{"memcache", "memcache.reconnect"},
		{"memcache", "memcache.ttl"},
		{"memcache", "memcache.servers"},

		// vars_imap_pop.go
		{"imap_pop", "mail.imap.greeting"},
		{"imap_pop", "mail.imap.enabled_capability"},
		{"imap_pop", "mail.imap.starttls"},
		{"imap_pop", "mail.imap.sasl.plain.enabled"},
		{"imap_pop", "mail.imap.sasl.gssapi.enabled"},
		{"imap_pop", "mail.pop3.greeting"},
		{"imap_pop", "mail.pop3.enabled_capability"},
		{"imap_pop", "mail.pop3.starttls"},
		{"imap_pop", "mail.pop3.sasl.plain.enabled"},
		{"imap_pop", "mail.pop3.sasl.gssapi.enabled"},
		{"imap_pop", "mail.upstream.pop3xoip"},
		{"imap_pop", "mail.saslhost.from.ip"},
		{"imap_pop", "mail.imapcapa"},
		{"imap_pop", "mail.pop3capa"},
		{"imap_pop", "mail.imapid"},
		{"imap_pop", "mail.imap.authplain.enabled"},
		{"imap_pop", "mail.imap.authgssapi.enabled"},
		{"imap_pop", "mail.pop3.authplain.enabled"},
		{"imap_pop", "mail.pop3.authgssapi.enabled"},
		{"imap_pop", "mail.imap.literalauth"},

		// vars_sso.go
		{"sso", "web.sso.enabled"},
		{"sso", "web.sso.default.enabled"},
		{"sso", "web.sso.certauth.enabled"},
		{"sso", "web.sso.certauth.default.enabled"},
		{"sso", "web.sso.certauth.port"},

		// vars_lookup.go
		{"lookup", "lookup.target"},
		{"lookup", "lookup.target.available"},
		{"lookup", "lookup.caching.enabled"},
		{"lookup", "zmlookup.:handlers"},
		{"lookup", "zmlookup.timeout"},
		{"lookup", "zmlookup.retryinterval"},
		{"lookup", "zmlookup.caching"},
		{"lookup", "zmlookup.dpasswd"},
		{"lookup", "zmprefix.url"},
		{"lookup", "zmroute.timeout.connect"},
		{"lookup", "zmroute.timeout.read"},
		{"lookup", "zmroute.timeout.send"},

		// vars_timeout.go
		{"timeout", "mail.authwait"},
		{"timeout", "mail.inactivity.timeout"},
		{"timeout", "web.upstream.connect.timeout"},
		{"timeout", "web.upstream.read.timeout"},
		{"timeout", "web.upstream.send.timeout"},
		{"timeout", "web.upstream.polling.timeout"},
		{"timeout", "lookup.timeout"},
		{"timeout", "lookup.retryinterval"},
		{"timeout", "lookup.dpasswd.cachettl"},
		{"timeout", "lookup.cachefetchtimeout"},
		{"timeout", "web.upstream.noop.timeout"},
		{"timeout", "web.upstream.waitset.timeout"},

		// vars_throttling.go
		{"throttling", "mail.limit.iplogin"},
		{"throttling", "mail.limit.iplogintime"},
		{"throttling", "mail.limit.ipthrottlemsg"},
		{"throttling", "mail.limit.userlogin"},
		{"throttling", "mail.limit.userlogintime"},
		{"throttling", "mail.limit.userthrottlemsg"},

		// vars_xmpp.go
		{"xmpp", "web.xmpp.bosh.hostname"},
		{"xmpp", "web.xmpp.bosh.port"},
		{"xmpp", "web.xmpp.local.bind.url"},
		{"xmpp", "web.xmpp.remote.bind.url"},
	}

	for _, tc := range keys {
		t.Run(tc.source+"/"+tc.key, func(t *testing.T) {
			if _, err := gen.GetVariable(tc.key); err != nil {
				t.Errorf("variable %q (from vars_%s.go) not registered: %v", tc.key, tc.source, err)
			}
		})
	}
}
