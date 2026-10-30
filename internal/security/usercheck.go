// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package security provides user permission validation for configd.
// It enforces that configd runs as the required 'zextras' user and provides
// functions for checking current user identity.
package security

import (
	"fmt"
	"io"
	"os/user"

	"github.com/zextras/carbonio-configd/internal/config"
)

// RequiredUser is the username that must be running configd
const RequiredUser = config.ZextrasUser

// UserResolver resolves the OS user the process runs as. Production passes
// OSUser; tests pass a stub to exercise both the zextras and non-zextras
// branches regardless of the user running the test binary.
type UserResolver func() (*user.User, error)

// OSUser is the production UserResolver: the user of the running process, as
// reported by os/user.
func OSUser() (*user.User, error) {
	return user.Current() //nolint:wrapcheck // CheckUserPermissions adds the context
}

// CheckUserPermissions verifies that configd is running strictly as the zextras user.
// Root is not accepted. Returns an error for any other user.
func CheckUserPermissions(resolve UserResolver) error {
	currentUser, err := resolve()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	if currentUser.Username != RequiredUser {
		return fmt.Errorf("configd must be run as '%s' user, current user is '%s'",
			RequiredUser, currentUser.Username)
	}

	return nil
}

// MustCheckUserPermissions verifies user permissions.
// If the check fails, it writes the usage message to stderr and returns the
// error. It is up to the caller to handle the error (e.g., by exiting).
func MustCheckUserPermissions(resolve UserResolver, stderr io.Writer) error {
	if err := CheckUserPermissions(resolve); err != nil {
		_, _ = fmt.Fprintf(stderr, "Please run this program as the '%s' user.\n", RequiredUser)

		return err
	}

	return nil
}
