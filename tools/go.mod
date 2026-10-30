// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Local development tools (not used by CI): run with
// go tool -modfile=tools/go.mod <deadcode|dupl>.
module github.com/zextras/carbonio-configd/tools

go 1.26.0

tool (
	github.com/mibk/dupl
	golang.org/x/tools/cmd/deadcode
)

require (
	github.com/mibk/dupl v1.1.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261009151841-97e8ea77aa2a // indirect
	golang.org/x/tools v0.52.0 // indirect
)
