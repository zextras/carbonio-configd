// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"runtime/debug"
	"strings"
)

// VersionInfo holds structured version information extracted from build metadata.
type VersionInfo struct {
	Version  string
	Revision string
	Time     string
	Modified bool
}

// GetVersionInfo extracts version information from Go's embedded build info.
func GetVersionInfo() VersionInfo {
	info := VersionInfo{
		Version: "(unknown)",
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}

	if bi.Main.Version != "" {
		info.Version = bi.Main.Version
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Revision = s.Value
		case "vcs.time":
			info.Time = s.Value
		case "vcs.modified":
			info.Modified = strings.EqualFold(s.Value, "true")
		}
	}

	return info
}
