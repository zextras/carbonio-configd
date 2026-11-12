// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import "strings"

// applyWhen lists, per key, when the key applies to a node. The keys the
// wizard asks follow their question; the others follow the role they
// configure. A key absent from the table always applies.
var applyWhen = func() map[string]func(*Config) bool {
	m := make(map[string]func(*Config) bool, len(questions))

	for _, q := range questions {
		if q.when != nil {
			m[q.path] = q.when
		}
	}

	appserver := role(RoleAppserver)

	for _, p := range []string{
		keyNotificationsAvNotifyUser, keyNotificationsSMTPDestination, keyNotificationsSMTPSource,
		keyStoreMode, keyStoreMailProxy, keyStoreWebProxy, keyStoreSpamTraining, keyStoreVirusQuarantineAccount,
		keyStoreKeyboardShortcuts, keyStoreMailboxdMemoryMb, keyStoreMysqlMemoryPercent,
	} {
		m[p] = appserver
	}

	m[keyStoreSpamAccount] = func(c *Config) bool { return c.HasRole(RoleAppserver) && c.Store.SpamTraining }
	m[keyStoreHamAccount] = m[keyStoreSpamAccount]
	m[keyLdapForceReplication] = role(RoleDirectoryServer)

	for _, p := range []string{keyMtaAntispam, keyMtaAntivirus, keyMtaCbpolicyd, keyMtaOpendkim} {
		m[p] = role(RoleMTA)
	}

	for _, f := range Fields {
		switch {
		case strings.HasPrefix(f.Path, "store.ports."):
			m[f.Path] = appserver
		case strings.HasPrefix(f.Path, "proxy."):
			m[f.Path] = role(RoleProxy)
		}
	}

	return m
}()

// Applies reports whether the key f applies to a node configured as c: the
// wizard asks a key only when it does, and the other keys follow the role they
// configure.
func Applies(f *Field, c *Config) bool {
	when := applyWhen[f.Path]

	return when == nil || when(c)
}
