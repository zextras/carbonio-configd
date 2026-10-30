// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package redact hides secret configuration values from log output.
package redact

import (
	"regexp"
	"strings"
)

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

// pemMarker is the cheap substring every PEM private key block contains; it
// gates the regular expression so ordinary log text costs one Contains call.
const pemMarker = "PRIVATE KEY-----"

// pemPrivateKey matches a PEM private key block ("BEGIN PRIVATE KEY",
// "BEGIN RSA PRIVATE KEY", "BEGIN ENCRYPTED PRIVATE KEY", ...). A block whose
// END line is missing (truncated text) is redacted up to the end of the string.
var pemPrivateKey = regexp.MustCompile(
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)

// Text returns s with every PEM private key block replaced by Placeholder and
// reports whether anything was replaced. Text without a private key is
// returned unchanged without allocating.
func Text(s string) (string, bool) {
	if !strings.Contains(s, pemMarker) {
		return s, false
	}

	out := pemPrivateKey.ReplaceAllLiteralString(s, Placeholder)

	return out, out != s
}

// valueAttrNames are the log attribute names that carry the value of the
// configuration key named by a sibling "key" attribute.
var valueAttrNames = map[string]struct{}{
	"value": {}, "old": {}, "new": {}, "have": {}, "want": {}, "previous": {}, "current": {},
}

// IsValueAttr reports whether a log attribute name denotes the value of a
// configuration key logged next to a key-name attribute ("value",
// "previous_value", "current_value", ...).
func IsValueAttr(name string) bool {
	if _, ok := valueAttrNames[name]; ok {
		return true
	}

	return strings.HasSuffix(name, "_value")
}

// keyAttrNames are the log attribute names that carry a configuration key
// name; when the name is a secret key, sibling value attributes are redacted.
var keyAttrNames = map[string]struct{}{
	"key": {}, "attr": {}, "attribute": {}, "variable": {}, "var": {}, "lc_key": {}, "param": {}, "setting": {},
}

// IsKeyAttr reports whether a log attribute name denotes a configuration key
// name (as opposed to a value).
func IsKeyAttr(name string) bool {
	_, ok := keyAttrNames[name]

	return ok
}
