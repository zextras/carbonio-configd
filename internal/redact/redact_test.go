// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSecretKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"ldap_amavis_password", true},
		{"antispam_mysql_password", true},
		{"ldap_postfix_password", true},
		{"mailboxd_keystore_password", true},
		{"ldap_root_password", true},
		{"zimbraLdapPassword", true},
		{"zimbra_ldap_PASSWORD", true},
		{"zimbraAuthTokenKey", true},
		{"some_secret_value", true},
		{"ssl_private_key", true},
		{"ssl_key", true},
		{"ldap_root_pw", true},
		{"zimbraImapBindPort", false},
		{"ldap_master_url", false},
		{"zimbraMailKeyPort", false},
		{"mailboxd_keystore", false},
		{"zimbraMtaMyNetworks", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsSecretKey(tt.key))
		})
	}
}

func TestForLog(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Placeholder, ForLog("ldap_amavis_password", "s3cret"))
	assert.Equal(t, "7143", ForLog("zimbraImapBindPort", "7143"))
	assert.Empty(t, ForLog("ldap_amavis_password", ""), "empty values carry no secret")
}

func TestText(t *testing.T) {
	t.Parallel()

	begin := "-----BEGIN " + "PRIVATE KEY-----"
	end := "-----END " + "PRIVATE KEY-----"
	rsa := "-----BEGIN RSA " + "PRIVATE KEY-----\nabc\n-----END RSA " + "PRIVATE KEY-----"
	enc := "-----BEGIN ENCRYPTED " + "PRIVATE KEY-----\nabc\n-----END ENCRYPTED " + "PRIVATE KEY-----"

	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"no secret", "hello world", "hello world", false},
		{"certificate", "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----", "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----", false},
		{"pkcs8", begin + "\nabc\n" + end, Placeholder, true},
		{"rsa in text", "a " + rsa + " b", "a " + Placeholder + " b", true},
		{"encrypted", enc, Placeholder, true},
		{"two blocks", begin + "x" + end + "|" + begin + "y" + end, Placeholder + "|" + Placeholder, true},
		{"truncated", "x " + begin + "\nabc", "x " + Placeholder, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, changed := Text(tt.in)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.changed, changed)
		})
	}
}

func TestAttrNames(t *testing.T) {
	t.Parallel()

	for _, n := range []string{"value", "previous_value", "current_value", "have", "want"} {
		assert.True(t, IsValueAttr(n), n)
	}

	for _, n := range []string{"key", "values", "valuex", "service"} {
		assert.False(t, IsValueAttr(n), n)
	}

	for _, n := range []string{"key", "attr", "variable"} {
		assert.True(t, IsKeyAttr(n), n)
	}

	assert.False(t, IsKeyAttr("value"))
}
