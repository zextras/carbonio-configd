// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

// valueOrEmpty returns the value of a snapshot lookup, "" when it is unset.
func valueOrEmpty(v string, _ bool) string { return v }
