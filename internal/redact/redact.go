// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package redact hides secret configuration values from log output.
package redact

import "strings"

// Placeholder replaces the value of a secret configuration key in log output.
const Placeholder = "[REDACTED]"

// secretKeyTokens are case-insensitive substrings that mark a configuration key
// as holding a secret (e.g. ldap_amavis_password, mailboxd_keystore_password,
// zimbraAuthTokenKey).
var secretKeyTokens = []string{
	"password",
	"passwd",
	"passphrase",
	"secret",
	"token",
	"credential",
	"privatekey",
	"private_key",
	"apikey",
	"api_key",
}

// secretKeySuffixes are case-insensitive key suffixes that mark a secret
// (e.g. ssl_key, ldap_root_pw). A bare "key" substring is deliberately not a
// token: it would match harmless names such as "zimbraMailKeyPort".
var secretKeySuffixes = []string{"_key", "_pass", "_pw"}

// IsSecretKey reports whether the configuration key name denotes a secret whose
// value must never be written to the logs.
func IsSecretKey(key string) bool {
	k := strings.ToLower(key)

	for _, token := range secretKeyTokens {
		if strings.Contains(k, token) {
			return true
		}
	}

	for _, suffix := range secretKeySuffixes {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}

	return false
}

// ForLog returns value unchanged for ordinary keys and a fixed placeholder
// for secret keys. Empty values are returned as-is: they carry no secret, and
// operators rely on seeing that a key is unset.
func ForLog(key, value string) string {
	if value == "" || !IsSecretKey(key) {
		return value
	}

	return Placeholder
}
