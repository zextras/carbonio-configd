// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package security provides user permission validation for configd.
// It enforces that configd runs as the required 'zextras' user and provides
// functions for checking current user identity.
package security

import (
	"fmt"
	"os"
	"os/user"
)

// RequiredUser is the username that must be running configd
const RequiredUser = "zextras"

// CheckUserPermissions verifies that configd is running strictly as the zextras user.
// Root is not accepted. Returns an error for any other user.
func CheckUserPermissions() error {
	currentUser, err := user.Current()
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
// If the check fails, it logs an error to stderr and returns the error.
// It is up to the caller to handle the error (e.g., by exiting).
func MustCheckUserPermissions() error {
	if err := CheckUserPermissions(); err != nil {
		fmt.Fprintf(os.Stderr, "Please run this program as the '%s' user.\n", RequiredUser)

		return err
	}

	return nil
}

// getCurrentUser returns the current user information.
func getCurrentUser() (*user.User, error) {
	return user.Current()
}
