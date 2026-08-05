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
