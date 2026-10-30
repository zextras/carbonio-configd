// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package configtest provides test-only constructors for config types.
package configtest

import "github.com/zextras/carbonio-configd/internal/config"

// NewConfigMapFrom returns a ConfigMap populated with the entries of src.
// A nil src yields an empty map.
func NewConfigMapFrom(src map[string]string) *config.ConfigMap {
	cm := config.NewConfigMap()

	for k, v := range src {
		cm.Set(k, v)
	}

	return cm
}
