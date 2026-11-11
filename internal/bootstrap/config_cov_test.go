// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRolePackage_NamesTheCarbonioPackageOfEveryRole(t *testing.T) {
	assert.Equal(t, "carbonio-directory-server", RolePackage(RoleDirectoryServer))
	assert.Equal(t, "carbonio-mta", RolePackage(RoleMTA))

	for _, r := range Roles {
		assert.Equal(t, "carbonio-"+r, RolePackage(r))
	}
}
