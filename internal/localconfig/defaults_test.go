// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"strings"
	"testing"
)

// TestDefaults_ResolvesKeysRequiredBySetupScripts defends the exact regression
// observed in production: carbonio-bootstrap (zmmyinit, zmldappasswd, ...)
// reads these keys via "configd localconfig -q -s -m export" with no
// localconfig.xml overrides yet on disk. Before lcdefaults.go ported the full
// LC.java KnownKey registry, most of these resolved to "", which made
// zmmyinit's "-r ${zimbra_db_directory}/db.sql" assertion fail and left
// zmldappasswd writing to the unwritable path "/carbonio.ldif.$$".
func TestDefaults_ResolvesKeysRequiredBySetupScripts(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"zimbra_home", "/opt/zextras"},
		{"zimbra_db_directory", "/opt/zextras/db"},
		{"mysql_data_directory", "/opt/zextras/db/data"},
		{"zimbra_tmp_directory", "/opt/zextras/data/tmp"},
		{"zimbra_index_directory", "/opt/zextras/index"},
		{"zimbra_store_directory", "/opt/zextras/store"},
		{"zimbra_log_directory", "/opt/zextras/log"},
		{"zimbra_java_home", "/opt/zextras/common/lib/jvm/java"},
		{"mailboxd_directory", "/opt/zextras/mailboxd"},
		{"mailboxd_keystore", "/opt/zextras/mailboxd/etc/keystore"},
		{"mailboxd_truststore", "/opt/zextras/common/lib/jvm/java/lib/security/cacerts"},
		{"mysql_mycnf", "/opt/zextras/conf/my.cnf"},
		{"mysql_port", "7306"},
		{"mysql_root_password", "zimbra"},
		{"zimbra_mysql_user", "zextras"},
		{"zimbra_mysql_password", "zextras"},
		{"antispam_mysql_mycnf", "/opt/zextras/conf/antispam-my.cnf"},
		{"antispam_mysql_data_directory", "/opt/zextras/data/amavisd/mysql/data"},
		{"antispam_mysql_errlogfile", "/opt/zextras/log/antispam-mysqld.log"},
		{"antispam_mysql_port", "7308"},
		{"cbpolicyd_db_file", "/opt/zextras/data/cbpolicyd/db/cbpolicyd.sqlitedb"},
		{"zimbra_user", "zextras"},
		{"zmconfigd_listen_port", "7171"},
		{"zimbra_configrewrite_timeout", "120"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			config := map[string]string{}
			MergeDefaults(config)
			Interpolate(config)

			if got := config[tt.key]; got != tt.expected {
				t.Errorf("resolved %s = %q, want %q", tt.key, got, tt.expected)
			}
		})
	}
}

// TestDefaults_NoUnresolvedReferences ensures every ${...} reference among the
// defaults resolves to a concrete value within maxInterpolationDepth passes
// when no localconfig.xml supplies overrides. A leftover "${...}" here is
// exactly the class of bug that silently produced unusable paths like
// "${zimbra_home}/db" instead of "/opt/zextras/db".
func TestDefaults_NoUnresolvedReferences(t *testing.T) {
	config := map[string]string{}
	MergeDefaults(config)
	Interpolate(config)

	var unresolved []string
	for key, value := range config {
		if strings.Contains(value, "${") {
			unresolved = append(unresolved, key)
		}
	}

	if len(unresolved) > 0 {
		t.Errorf("keys with unresolved ${...} references after Interpolate: %v", unresolved)
	}
}

// TestDefaults_JavaNullDefaultsAreEmpty covers keys whose LC.java default is
// the Java null literal. They must still be present in Defaults with an empty
// string value so that "configd localconfig <key>" reports an empty value
// rather than a "key not found" warning.
func TestDefaults_JavaNullDefaultsAreEmpty(t *testing.T) {
	keys := []string{
		"mysql_bind_address",
		"mailboxd_java_heap_size",
		"milter_bind_address",
		"antispam_mysql_host",
		"imap_max_message_size",
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			value, ok := Defaults[key]
			if !ok {
				t.Fatalf("expected %s to be present in Defaults", key)
			}
			if value != "" {
				t.Errorf("expected %s default to be empty (Java null), got %q", key, value)
			}
		})
	}
}

// TestDefaults_NumericConstantsEvaluated covers LC.java defaults that were
// expressed as constant arithmetic (Constants.MILLIS_PER_*, Integer.MAX_VALUE,
// etc.) and had to be evaluated to literal decimal strings during the port.
func TestDefaults_NumericConstantsEvaluated(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"conversation_max_age_ms", "2678400000"},
		{"tombstone_max_age_ms", "8035200000"},
		{"purge_initial_sleep_ms", "1800000"},
		{"zimbra_index_lucene_max_merge", "2147483647"},
		{"imap_max_request_size", "10240"},
		{"xmpp_offline_quota", "102400"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := Defaults[tt.key]; got != tt.expected {
				t.Errorf("Defaults[%q] = %q, want %q", tt.key, got, tt.expected)
			}
		})
	}
}

// TestMergeDefaults_XMLOverridesWin defends the exact composition zmmyinit
// depends on: an operator-supplied zimbra_db_directory in localconfig.xml
// must survive MergeDefaults, and mysql_data_directory (default
// "${zimbra_db_directory}/data") must interpolate against the OVERRIDDEN
// base rather than the compiled-in default.
func TestMergeDefaults_XMLOverridesWin(t *testing.T) {
	config := map[string]string{
		"zimbra_db_directory": "/custom/db",
	}

	MergeDefaults(config)
	Interpolate(config)

	if got := config["zimbra_db_directory"]; got != "/custom/db" {
		t.Errorf("zimbra_db_directory = %q, want %q (XML override must not be clobbered)", got, "/custom/db")
	}

	if got := config["mysql_data_directory"]; got != "/custom/db/data" {
		t.Errorf("mysql_data_directory = %q, want %q (must interpolate against the overridden base)", got, "/custom/db/data")
	}
}
