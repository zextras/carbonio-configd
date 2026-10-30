// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"sync"

	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// loadSink receives the data a loader produced. The loaders never write the
// shared state themselves: during a cycle they fill a stagedLoad that is
// published as one snapshot once every loader succeeded.
type loadSink interface {
	setLocal(data map[string]string)
	setGlobal(data map[string]string)
	setMisc(data map[string]string)
	setServer(data, services map[string]string)
}

// stagedLoad collects the output of one load attempt. Each loader writes its
// own part, so the mutex only guards against the loaders running concurrently.
type stagedLoad struct {
	mu    sync.Mutex
	parts snapshot.Parts
}

func (s *stagedLoad) setLocal(data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.parts.Local = data
}

func (s *stagedLoad) setGlobal(data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.parts.Global = data
}

func (s *stagedLoad) setMisc(data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.parts.Misc = data
}

func (s *stagedLoad) setServer(data, services map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.parts.Server = data
	s.parts.Services = services
}

// snapshot builds the snapshot of the staged parts. It copies them, so a
// loader still running (and later discarded) cannot alter the result.
func (s *stagedLoad) snapshot() *snapshot.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return snapshot.New(s.parts)
}

// stateSink patches the published snapshot in place. It serves a loader that
// is invoked on its own, outside a full load cycle.
type stateSink struct {
	cm *ConfigManager
}

func (s stateSink) setLocal(data map[string]string) {
	s.cm.State.UpdateSnapshot(func(old *snapshot.Snapshot) *snapshot.Snapshot { return old.WithLocal(data) })
}

func (s stateSink) setGlobal(data map[string]string) {
	s.cm.State.UpdateSnapshot(func(old *snapshot.Snapshot) *snapshot.Snapshot { return old.WithGlobal(data) })
}

func (s stateSink) setMisc(data map[string]string) {
	s.cm.State.UpdateSnapshot(func(old *snapshot.Snapshot) *snapshot.Snapshot { return old.WithMisc(data) })
}

func (s stateSink) setServer(data, services map[string]string) {
	s.cm.State.UpdateSnapshot(func(old *snapshot.Snapshot) *snapshot.Snapshot { return old.WithServer(data, services) })
}

type stageKey struct{}

// withStage returns ctx carrying the staging area of a load attempt.
func withStage(ctx context.Context, st *stagedLoad) context.Context {
	return context.WithValue(ctx, stageKey{}, st)
}

// sink returns the sink a loader running under ctx must publish into: the
// staging area of the current load attempt, or the shared state when the
// loader runs outside one.
func (cm *ConfigManager) sink(ctx context.Context) loadSink {
	if st, ok := ctx.Value(stageKey{}).(*stagedLoad); ok {
		return st
	}

	return stateSink{cm: cm}
}
