// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package security

import (
	"errors"
	"io"
	"os/user"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubbedUser(u *user.User, err error) UserResolver {
	return func() (*user.User, error) { return u, err }
}

func TestCheckUserPermissions_ZextrasUser(t *testing.T) {
	t.Parallel()

	assert.NoError(t, CheckUserPermissions(stubbedUser(&user.User{Username: RequiredUser}, nil)))
}

func TestCheckUserPermissions_WrongUser(t *testing.T) {
	t.Parallel()

	err := CheckUserPermissions(stubbedUser(&user.User{Username: "root"}, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be run as 'zextras'")
}

func TestCheckUserPermissions_LookupFailure(t *testing.T) {
	t.Parallel()

	err := CheckUserPermissions(stubbedUser(nil, errors.New("no passwd entry")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get current user")
}

func TestMustCheckUserPermissions_Success(t *testing.T) {
	t.Parallel()

	assert.NoError(t, MustCheckUserPermissions(stubbedUser(&user.User{Username: RequiredUser}, nil), io.Discard))
}

func TestMustCheckUserPermissions_Failure(t *testing.T) {
	t.Parallel()

	assert.Error(t, MustCheckUserPermissions(stubbedUser(&user.User{Username: "nobody"}, nil), io.Discard))
}
