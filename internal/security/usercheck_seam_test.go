// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package security

import (
	"errors"
	"os/user"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStubbedUser(t *testing.T, u *user.User, err error) {
	t.Helper()

	prev := currentUserFn
	currentUserFn = func() (*user.User, error) { return u, err }

	t.Cleanup(func() { currentUserFn = prev })
}

func TestCheckUserPermissions_ZextrasUser(t *testing.T) {
	withStubbedUser(t, &user.User{Username: RequiredUser}, nil)
	assert.NoError(t, CheckUserPermissions())
}

func TestCheckUserPermissions_WrongUser(t *testing.T) {
	withStubbedUser(t, &user.User{Username: "root"}, nil)

	err := CheckUserPermissions()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be run as 'zextras'")
}

func TestCheckUserPermissions_LookupFailure(t *testing.T) {
	withStubbedUser(t, nil, errors.New("no passwd entry"))

	err := CheckUserPermissions()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get current user")
}

func TestMustCheckUserPermissions_Success(t *testing.T) {
	withStubbedUser(t, &user.User{Username: RequiredUser}, nil)
	assert.NoError(t, MustCheckUserPermissions())
}

func TestMustCheckUserPermissions_Failure(t *testing.T) {
	withStubbedUser(t, &user.User{Username: "nobody"}, nil)
	assert.Error(t, MustCheckUserPermissions())
}
