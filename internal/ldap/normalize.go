// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"strings"

	"github.com/zextras/carbonio-configd/internal/intern"
)

// NormalizeAttrs returns a fresh config map from LDAP attributes
// (multi-valued attributes already joined with newlines). Each value line is
// trimmed of surrounding whitespace, matching the zmprov-text round trip this
// replaces, and attribute names are interned.
func NormalizeAttrs(attrs map[string]string) map[string]string {
	configData := make(map[string]string, len(attrs))

	for key, value := range attrs {
		if !strings.Contains(value, "\n") {
			configData[intern.Attr(strings.TrimSpace(key))] = strings.TrimSpace(value)

			continue
		}

		lines := strings.Split(value, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}

		configData[intern.Attr(strings.TrimSpace(key))] = strings.Join(lines, "\n")
	}

	return configData
}
