// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeExecutable writes an executable stand-in binary (shell script) at path.
//
// Tests that write a script and then exec it race with each other under
// t.Parallel: a fork in a sibling goroutine between this file's open(O_WRONLY)
// and close() hands the child a copy of the write descriptor until it execs,
// and execve of a file that is open for writing anywhere fails with ETXTBSY
// ("text file busy"). os/exec holds syscall.ForkLock exclusively across
// fork+exec, so holding it for reading across the whole write guarantees no
// child can be forked while the descriptor is open. (Renaming a temp file into
// place does not help: the inode, and the leaked descriptor, stay the same.)
func writeExecutable(t *testing.T, path string, content []byte) {
	t.Helper()

	syscall.ForkLock.RLock()
	err := os.WriteFile(path, content, 0o755) //nolint:gosec // executable test stand-in
	syscall.ForkLock.RUnlock()

	require.NoError(t, err)
}
