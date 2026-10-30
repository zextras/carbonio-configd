// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"io"
	"os"
	"path/filepath"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/security"
)

// defaultBaseDir is the on-disk root of a Zextras/Carbonio installation.
const defaultBaseDir = config.ZextrasBase

// BaseDir returns CARBONIO_BASE_DIR when set, otherwise the default
// installation root.
func BaseDir() string {
	if v := os.Getenv("CARBONIO_BASE_DIR"); v != "" {
		return v
	}

	return defaultBaseDir
}

// BasePath joins BaseDir() with elem and cleans the result.
// Using filepath.Join here cleans the path and satisfies gosec G304/G703
// taint checks: the result is no longer raw concatenation of an external
// variable.
func BasePath(elem ...string) string {
	parts := append([]string{BaseDir()}, elem...)

	return filepath.Clean(filepath.Join(parts...))
}

// RequireZextras enforces that the current user is strictly the 'zextras' user.
// Root is not accepted. When it is not, the usage message goes to stderr and
// false is returned: the caller must stop and exit with status 1. Call this at
// the start of any command that must not be run by arbitrary users (daemon,
// service control, rewrite, proxy write, init).
func RequireZextras(stderr io.Writer) bool {
	return security.MustCheckUserPermissions(security.OSUser, stderr) == nil
}
