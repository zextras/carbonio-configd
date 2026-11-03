// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
)

// TestHeavyServicesMatchRegistry keeps the restart policy's heavy set in step
// with the registry: every heavy name resolves to mailbox, appserver-db, store
// or ldap, and no light service (clamd, antivirus, antispam, ...) is heavy.
func TestHeavyServicesMatchRegistry(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)
	heavy := map[string]bool{svcMailbox: true, svcAppserverDB: true, svcStore: true, svcLdap: true}

	for _, name := range []string{"mailbox", "mailboxd", "service", "appserver-db", "mysql", "mariadb",
		"store", "ldap", "directory", "directory-server"} {
		def := ctl.LookupService(name)
		require.NotNil(t, def, name)
		assert.True(t, heavy[def.Name], "%s -> %s", name, def.Name)
		assert.True(t, plan.HeavyService(name), name)
	}

	for _, name := range ctl.AllServiceNames() {
		assert.Equal(t, heavy[name], plan.HeavyService(name), name)
	}

	// mailbox restarts the JVM only; store covers both units.
	assert.Equal(t, []string{unitAppserver}, ctl.LookupService("mailbox").SystemdUnits)
	assert.ElementsMatch(t, []string{unitAppserverDB, unitAppserver}, ctl.LookupService("store").SystemdUnits)
	assert.Equal(t, ctl.LookupService("store"), ctl.LookupService("service"))
}
