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
	"_pwd",
	"bindpw",
	"creds",
}

// secretKeySuffixes are case-insensitive key suffixes that mark a secret
// (e.g. ssl_key, ldap_root_pw). A bare "key" substring is deliberately not a
// token: it would match harmless names such as "zimbraMailKeyPort".
var secretKeySuffixes = []string{"_key", "_pass", "_pw", "_pin"}

// IsSecretKey reports whether the configuration key name denotes a secret whose
// value must never be written to the logs.
func IsSecretKey(key string) bool {
	k := strings.ToLower(key)
	if k == "pin" {
		return true
	}

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

// urlUserinfo matches the password part of the userinfo of a URL (the text
// between the colon after the user name and the at sign before the host).
var urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]*:)[^/\s@]+(@)`)

// Text returns s with every PEM private key block and every URL userinfo
// password replaced by Placeholder and reports whether anything was replaced.
// Text without either is returned unchanged without allocating.
func Text(s string) (string, bool) {
	out := s

	if strings.Contains(out, pemMarker) {
		out = pemPrivateKey.ReplaceAllLiteralString(out, Placeholder)
	}

	if strings.Contains(out, "://") && strings.Contains(out, "@") {
		out = urlUserinfo.ReplaceAllString(out, "${1}"+Placeholder+"${2}")
	}

	return out, out != s
}

// Args returns a copy of argv that is safe to log: the value of a secret flag
// ("--password secret", "--password=secret", "key=secret" where the name is a
// secret key, see IsSecretKey) and any URL userinfo password are replaced by
// Placeholder. argv is never modified.
func Args(argv []string) []string {
	out := make([]string, len(argv))
	maskNext := false

	for i, arg := range argv {
		if maskNext {
			out[i] = Placeholder
			maskNext = false

			continue
		}

		out[i] = arg

		if name, _, isAssign := strings.Cut(arg, "="); isAssign {
			if IsSecretKey(strings.TrimLeft(name, "-")) {
				out[i] = name + "=" + Placeholder

				continue
			}
		} else if strings.HasPrefix(arg, "-") && IsSecretKey(strings.TrimLeft(arg, "-")) {
			maskNext = true

			continue
		}

		out[i], _ = Text(arg)
	}

	return out
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
