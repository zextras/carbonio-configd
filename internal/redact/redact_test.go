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
