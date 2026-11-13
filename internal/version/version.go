// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package version reports the release the binaries were built as.
package version

import "runtime/debug"

// stamped is set at link time by the package build:
//
//	-ldflags "-X github.com/zextras/carbonio-configd/internal/version.stamped=<pkgver>"
//
// Builds from a source tree otherwise only know "(devel)".
var stamped string

// Main returns the stamped release, else the module version from the build
// info ("(devel)" for source-tree builds), else "(unknown)".
func Main() string {
	if stamped != "" {
		return stamped
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}

	return "(unknown)"
}
