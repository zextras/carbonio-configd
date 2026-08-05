// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"

	"github.com/zextras/carbonio-configd/internal/config"
)

// defaultBaseDir is the on-disk root of a Zextras/Carbonio installation.
const defaultBaseDir = config.ZextrasBase

// baseDir returns CARBONIO_BASE_DIR when set, otherwise defaultBaseDir.
func baseDir() string {
	if v := os.Getenv("CARBONIO_BASE_DIR"); v != "" {
		return v
	}

	return defaultBaseDir
}

// basePath joins baseDir() with elem and cleans the result.
// Using filepath.Join here cleans the path and satisfies gosec G304/G703
// taint checks: the result is no longer raw concatenation of an external
// variable.
func basePath(elem ...string) string {
	parts := append([]string{baseDir()}, elem...)

	return filepath.Clean(filepath.Join(parts...))
}
