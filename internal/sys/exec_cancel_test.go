// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys

import (
	"context"
	"testing"
	"time"
)

// A grandchild that inherits the stdout pipe must not outlive a cancelled Run.
func TestRealExecRun_CancelKillsProcessGroup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := RealExec{}.Run(ctx, &Cmd{Path: "/bin/sh", Args: []string{"-c", "sleep 30 & wait"}})

	if err == nil {
		t.Fatal("expected error from cancelled command")
	}

	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Run returned after %v; process group was not killed", d)
	}
}
