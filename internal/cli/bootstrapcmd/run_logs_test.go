// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// lockedBuf is a writer other tests of the package may also reach while the
// process-wide logger points at it.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func restoreStderrLogs(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		logger.InitStructuredLogging(&logger.Config{Format: logger.FormatText, Level: slog.LevelWarn, Output: os.Stderr})
	})
}

// Not parallel: it swaps the process-wide logger.
func TestQuietScreenLogs_WarningsGoToTheLogFileWhileTheScreensAreUp(t *testing.T) {
	restoreStderrLogs(t)

	var file, stderr lockedBuf

	restore := quietScreenLogs(&file, &stderr)
	logger.WarnContext(context.Background(), "LDAP URL failed, trying next")
	restore()
	logger.WarnContext(context.Background(), "after the screens")

	assert.Contains(t, file.String(), "LDAP URL failed, trying next")
	assert.NotContains(t, stderr.String(), "LDAP URL failed", "nothing is drawn over the interface")
	assert.Contains(t, stderr.String(), "after the screens", "stderr gets them back afterwards")
	assert.NotContains(t, file.String(), "after the screens")
}

func TestQuietScreenLogs_DryRunDropsThem(t *testing.T) {
	restoreStderrLogs(t)

	var stderr lockedBuf

	restore := quietScreenLogs(nil, &stderr)
	logger.WarnContext(context.Background(), "dropped")
	restore()

	assert.NotContains(t, stderr.String(), "dropped")
}
