// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"sync"
)

// fileScratchKey is the context key of a planning call's private FILE cache.
type fileScratchKey struct{}

// fileScratch is the FILE lookup cache of one read-only planning call
// (PlanHost). The daemon cycle caches FILE lookups in State.FileCache, which
// the next load clears; a planning call must leave no trace in the state, so
// its lookups are cached here and dropped with the call. It is safe for
// concurrent use: the transformer is handed the same context.
type fileScratch struct {
	mu    sync.Mutex
	files map[string]string
}

// withFileScratch returns ctx carrying a fresh private FILE cache: every FILE
// lookup made with it neither reads nor writes State.FileCache.
func withFileScratch(ctx context.Context) context.Context {
	return context.WithValue(ctx, fileScratchKey{}, &fileScratch{files: make(map[string]string)})
}

// fileScratchFrom returns the private FILE cache of ctx, or nil when the
// lookup belongs to a daemon cycle.
func fileScratchFrom(ctx context.Context) *fileScratch {
	s, _ := ctx.Value(fileScratchKey{}).(*fileScratch)

	return s
}

func (s *fileScratch) lookup(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, ok := s.files[key]

	return val, ok
}

func (s *fileScratch) store(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files[key] = value
}

// lookupCachedFile returns the cached content of a FILE lookup key: from the
// private cache of a planning call, else from the cycle's State.FileCache.
func (cm *ConfigManager) lookupCachedFile(ctx context.Context, key string) (string, bool) {
	if s := fileScratchFrom(ctx); s != nil {
		return s.lookup(key)
	}

	return cm.State.LookupFile(key)
}

// storeCachedFile caches the content of a FILE lookup key where
// lookupCachedFile finds it.
func (cm *ConfigManager) storeCachedFile(ctx context.Context, key, value string) {
	if s := fileScratchFrom(ctx); s != nil {
		s.store(key, value)

		return
	}

	cm.State.StoreFile(key, value)
}
