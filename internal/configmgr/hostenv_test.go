// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// instantSleepClock is the real clock except that Sleep returns at once (still
// honouring cancellation). Retry back-off schedules are covered with a manual
// clock in retry_test.go; tests that merely run into a failing loader must not
// spend real seconds in it.
type instantSleepClock struct{ sys.Clock }

func (instantSleepClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// hostEnv is the real host (real filesystem, exec and processes) with back-off
// sleeps skipped. For tests that work on t.TempDir() or need real OS behaviour.
func hostEnv() sys.System {
	env := sys.Real()
	env.Clock = instantSleepClock{env.Clock}

	return env
}
