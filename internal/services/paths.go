// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

// basePath is the root directory for all Carbonio paths.
// Defaults to /opt/zextras; overridable for testing.
var basePath = "/opt/zextras"

// path shortcuts for frequently used absolute directories.
// These use basePath so they can be overridden in tests.
var (
	binPath      = basePath + "/bin"
	confPath     = basePath + "/conf"
	commonPath   = basePath + "/common"
	dataPath     = basePath + "/data"
	libPath      = basePath + "/lib"
	logPath      = basePath + "/log"
	mailboxPath  = basePath + "/mailbox"
	mailboxdPath = basePath + "/mailboxd"
	storePath    = basePath + "/store"
)

// pidDir is the directory for PID files.
const pidDir = "/run/carbonio"
