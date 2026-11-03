// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import "strings"

// heavyServices are the services a config change must never restart on a
// systemd host: restarting them interrupts every mailbox user or risks the
// data store (mailbox JVM, its MariaDB, the composite store, the LDAP
// directory). Each name is listed with the aliases the configd.cf RESTART
// lines, the CLI and zimbraServiceEnabled use for it.
var heavyServices = map[string]struct{}{
	"mailbox":          {},
	"mailboxd":         {},
	"service":          {},
	"appserver-db":     {},
	"mysql":            {},
	"mariadb":          {},
	"store":            {},
	"ldap":             {},
	"directory":        {},
	"directory-server": {},
}

// HeavyService reports whether service (any of its names, case-insensitive) is
// a heavy service: on a systemd host a config change flags it as restart
// pending instead of restarting it.
func HeavyService(service string) bool {
	_, ok := heavyServices[strings.ToLower(service)]

	return ok
}

// Classify returns the action a restart entry has on this host. On a systemd
// host a restart of a heavy service becomes ActionPendingRestart; every other
// action is returned unchanged. It is idempotent.
func Classify(action Action, service string, systemd bool) Action {
	if systemd && action == ActionRestart && HeavyService(service) {
		return ActionPendingRestart
	}

	return action
}
