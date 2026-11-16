// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBindURLAndDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lc         map[string]string
		wantBind   string
		wantDomain string
	}{
		{"empty", map[string]string{}, "", ""},
		{"bind url wins, all kept", map[string]string{"ldap_bind_url": "ldap://a:389 ldap://b:389", "ldap_url": "ldap://c:389"},
			"ldap://a:389 ldap://b:389", "a"},
		{"first of ldap_url", map[string]string{"ldap_url": "ldaps://p.example.com:636 ldap://s:389"},
			"ldaps://p.example.com:636", "p.example.com"},
		{"empty bind url falls back", map[string]string{"ldap_bind_url": "", "ldap_url": "ldap://h:389"}, "ldap://h:389", "h"},
		{"no port", map[string]string{"ldap_url": "ldap://h"}, "ldap://h", "h"},
		{"no scheme", map[string]string{"ldap_url": "h:389"}, "h:389", "h"},
		{"leading spaces", map[string]string{"ldap_url": "  ldap://h:1  "}, "ldap://h:1", "h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantBind, BindURL(tt.lc))
			assert.Equal(t, tt.wantDomain, LDAPDomain(tt.lc))
		})
	}
}

func TestLaunchEnv(t *testing.T) {
	t.Parallel()

	full := map[string]string{
		"antispam_enable_restarts":            "TRUE",
		"antispam_enable_rule_compilation":    "FALSE",
		"antispam_enable_rule_updates":        "TRUE",
		"ldap_url":                            "ldap://l1.example.com:389 ldap://l2:389",
		"zmconfigd_listen_port":               "7171",
		"zimbra_configrewrite_timeout":        "30",
		"zimbra_zmjava_options":               "-Xmx256m",
		"mailboxd_java_heap_size":             "2048",
		"ldap_port":                           "389",
		"zimbra_log_directory":                "/opt/zextras/log",
		"mailboxd_directory":                  "/opt/zextras/mailboxd",
		"mailboxd_java_heap_new_size_percent": "30",
		"zimbra_mail_service_port":            "8080",
		"mailboxd_java_options":               "-server",
		"mailboxd_thread_stack_size":          "512k",
		"networkaddress_cache_ttl":            "10",
		"zimbra_log4j_properties":             "/etc/log4j.properties",
		"mysql_errlogfile":                    "/e.log",
		"mysql_mycnf":                         "/my.cnf",
	}

	got := LaunchEnv(full)
	assert.Equal(t, map[string]string{
		"antispam_enable_restarts":            "TRUE",
		"antispam_enable_rule_compilation":    "FALSE",
		"antispam_enable_rule_updates":        "TRUE",
		"bind_url":                            "ldap://l1.example.com:389",
		"configd_listen_port":                 "7171",
		"configd_rewrite_timeout":             "30",
		"java_options":                        "-Xmx256m",
		"java_xms":                            "2048",
		"java_xmx":                            "2048",
		"ldap_domain":                         "l1.example.com",
		"ldap_port":                           "389",
		"log_directory":                       "/opt/zextras/log",
		"mailboxd_directory":                  "/opt/zextras/mailboxd",
		"mailboxd_java_heap_new_size_percent": "30",
		"mail_service_port":                   "8080",
		"mailboxd_java_options":               "-server -Xss512k -Dsun.net.inetaddr.ttl=10 -Dlog4j.configurationFile=/etc/log4j.properties",
		"mysql_errlogfile":                    "/e.log",
		"mysql_mycnf":                         "/my.cnf",
	}, got)

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		got := LaunchEnv(map[string]string{"zimbra_log4j_properties": "/l4j"})
		assert.Equal(t, "512", got["java_xms"])
		assert.Equal(t, "512", got["java_xmx"])
		assert.Equal(t, "25", got["mailboxd_java_heap_new_size_percent"])
		assert.Equal(t, " -Xss256k -Dsun.net.inetaddr.ttl=60 -Dlog4j.configurationFile=/l4j", got["mailboxd_java_options"])
		assert.Empty(t, got["bind_url"])
		assert.Empty(t, got["ldap_domain"])
	})

	tests := []struct {
		name, opts, want string
	}{
		{"has Xss", "-Xss1m", "-Xss1m -Dsun.net.inetaddr.ttl=60 -Dlog4j.configurationFile=/l4j"},
		{"has ttl", "-Dsun.net.inetaddr.ttl=5", "-Dsun.net.inetaddr.ttl=5 -Xss256k -Dlog4j.configurationFile=/l4j"},
		{"has log4j", "-Dlog4j2.x=1", "-Dlog4j2.x=1 -Xss256k -Dsun.net.inetaddr.ttl=60"},
		{"has all", "-Xss1m -Dsun.net.inetaddr.ttl=5 -Dlog4j.configurationFile=x", "-Xss1m -Dsun.net.inetaddr.ttl=5 -Dlog4j.configurationFile=x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := LaunchEnv(map[string]string{"mailboxd_java_options": tt.opts, "zimbra_log4j_properties": "/l4j"})
			assert.Equal(t, tt.want, got["mailboxd_java_options"])
		})
	}
}
