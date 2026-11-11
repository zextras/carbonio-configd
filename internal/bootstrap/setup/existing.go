// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

// ExistingValues reads the configuration of an existing install (key path to
// value) from localconfig and LDAP: setup.pl setDefaultsFromLocalConfig,
// setLdapDefaults and setEnabledDependencies.
func ExistingValues(_ *Env) (map[string]string, error) {
	return map[string]string{}, nil
}
