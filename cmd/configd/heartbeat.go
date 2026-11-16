// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/heartbeat.go
package main

import (
	"sync/atomic"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// The longest each stage of the main loop may legitimately run before the
// systemd watchdog stops being fed. The watchdog exists to restart a wedged
// loop, not a busy one: loading waits on LDAP up to its own timeouts, and a
// restart of mailboxd or mysqld takes minutes, far past WatchdogSec. Every
// stage is bounded, so a hang still ends in a restart.
const (
	loadBudget    = 10 * time.Minute
	planBudget    = 5 * time.Minute
	executeBudget = 15 * time.Minute
	restartBudget = 30 * time.Minute

	// sleepSlack is the grace added to a sleep, which the loop ends by itself.
	sleepSlack = time.Minute
)

// loopHeartbeat is the main loop's proof of life: before each stage (and each
// sleep) it says how long it may take, and the systemd keep-alive only pings
// while that deadline has not passed. A nil heartbeat is always healthy.
type loopHeartbeat struct {
	clock    sys.Clock
	deadline atomic.Int64 // unix nanoseconds
}

func newLoopHeartbeat(clock sys.Clock) *loopHeartbeat {
	h := &loopHeartbeat{clock: clock}
	h.expect(loadBudget)

	return h
}

// expect declares that the loop is alive for the next within.
func (h *loopHeartbeat) expect(within time.Duration) {
	if h == nil {
		return
	}

	h.deadline.Store(h.clock.Now().Add(within).UnixNano())
}

// healthy reports whether the loop is within the time it declared.
func (h *loopHeartbeat) healthy() bool {
	return h == nil || h.clock.Now().UnixNano() <= h.deadline.Load()
}
