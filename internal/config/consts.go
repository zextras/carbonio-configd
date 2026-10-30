// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "strings"

// ZextrasBase is the default root directory for Carbonio installation.
const ZextrasBase = "/opt/zextras"

// ZextrasUser is the default system user and group for Carbonio processes.
const ZextrasUser = "zextras"

// IsTruthy returns true for "TRUE" (case-insensitive) or "1".
func IsTruthy(s string) bool {
	return strings.EqualFold(s, "TRUE") || s == "1"
}

// IsFalseValue reports whether val is a legacy "false" value.
// It mirrors zmconfigd's state.py isFalseValue:
//
//	not val or re.match(r"no|false|0+", str(val), re.I)
//
// re.match anchors at the start only, so this is a case-insensitive PREFIX
// match: "no", "none", "nobody", "false", "falsey", "0", "0.5" and "00:30"
// are all false values, as is the empty string.
func IsFalseValue(val string) bool {
	if val == "" || val[0] == '0' {
		return true
	}

	return hasPrefixFold(val, "no") || hasPrefixFold(val, "false")
}

// IsTrueValue checks if a string represents a true value.
func IsTrueValue(val string) bool {
	return !IsFalseValue(val)
}

// hasPrefixFold reports whether s begins with prefix, ignoring ASCII case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
