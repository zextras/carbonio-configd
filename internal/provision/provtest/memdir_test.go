// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provtest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/provision/provtest"
)

func TestDir_AddNeedsTheParent(t *testing.T) {
	d := provtest.New()

	require.NoError(t, d.Add("dc=com", map[string][]string{"dc": {"com"}}), "a top-level entry has no parent")
	assert.Error(t, d.Add("ou=people,dc=example,dc=com", nil), "dc=example,dc=com is missing")

	require.NoError(t, d.Add("dc=example,dc=com", nil))
	require.NoError(t, d.Add("ou=people,dc=example,dc=com", nil))
	assert.ErrorIs(t, d.Add("ou=people,dc=example,dc=com", nil), provision.ErrEntryExists)
}

func TestDir_ModifyOfAMissingEntryFails(t *testing.T) {
	d := provtest.New()

	assert.Error(t, d.ModifyValues("dc=com", nil, nil, map[string][]string{"o": {"x"}}))
	assert.False(t, d.Exists("dc=com"), "a failed modify creates nothing")
}

func TestDir_PresenceFilterMatchesTheAttributeOnly(t *testing.T) {
	d := provtest.New()
	d.Set("cn=a", "mail", "a@example.com")
	d.Set("cn=b", "cn", "b")

	got, err := d.Search("", provision.ScopeSub, "(mail=*)")

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "cn=a", got[0].DN)
}
