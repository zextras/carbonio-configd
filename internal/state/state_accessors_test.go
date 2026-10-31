// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// TestState_FileCacheConcurrent drives the FILE cache from several goroutines.
// Run under -race.
func TestState_FileCacheConcurrent(t *testing.T) {
	t.Parallel()

	s := NewState()

	var wg sync.WaitGroup

	for i := range 8 {
		wg.Go(func() {
			key := fmt.Sprintf("file-%d", i)

			for range 200 {
				s.StoreFile(key, "content")
				s.LookupFile(key)
				s.ClearFileCache(context.Background())
			}
		})
	}

	wg.Wait()
}

func TestState_FileCacheStoreLookup(t *testing.T) {
	t.Parallel()

	s := NewState()

	_, ok := s.LookupFile("missing")
	require.False(t, ok)

	s.StoreFile("a", "alpha")

	got, ok := s.LookupFile("a")
	assert.True(t, ok)
	assert.Equal(t, "alpha", got)

	s.ClearFileCache(context.Background())

	_, ok = s.LookupFile("a")
	assert.False(t, ok)
}

func TestState_MtaConfigAndFirstRunConcurrent(t *testing.T) {
	t.Parallel()

	s := NewState()

	var wg sync.WaitGroup

	wg.Go(func() {
		for range 500 {
			s.SetMtaConfig(&config.MtaConfig{Sections: make(map[string]*config.MtaConfigSection)})
			s.SetFirstRun(false)
		}
	})

	wg.Go(func() {
		for range 500 {
			_ = s.GetMtaConfig().Sections
			_ = s.IsFirstRun()
		}
	})

	wg.Wait()

	assert.False(t, s.IsFirstRun())
}

func TestState_Snapshot_InitialIsEmpty(t *testing.T) {
	t.Parallel()

	snap := NewState().Snapshot()

	require.NotNil(t, snap)
	assert.Empty(t, snap.LocalMap())
	assert.Empty(t, snap.GlobalMap())
	assert.Empty(t, snap.ServerMap())
	assert.Empty(t, snap.ServiceNames())
}

func TestState_Snapshot_ZeroStateNeverNil(t *testing.T) {
	t.Parallel()

	var s State

	require.NotNil(t, s.Snapshot())
	assert.Empty(t, s.Snapshot().ServiceNames())
}

func TestState_Publish(t *testing.T) {
	t.Parallel()

	s := NewState()
	first := s.Snapshot()

	published := snapshot.New(snapshot.Parts{
		Local:    map[string]string{"k": "v"},
		Services: map[string]string{"mailbox": "x"},
	})
	s.Publish(published)

	assert.Same(t, published, s.Snapshot())
	assert.NotSame(t, first, s.Snapshot())

	v, ok := s.Snapshot().Local("k")
	assert.True(t, ok)
	assert.Equal(t, "v", v)
	assert.True(t, s.Snapshot().ServiceEnabled("mailbox"))

	// The previously handed-out snapshot is unaffected.
	_, ok = first.Local("k")
	assert.False(t, ok)
}

func TestState_Publish_Nil(t *testing.T) {
	t.Parallel()

	s := NewState()
	s.Publish(nil)

	require.NotNil(t, s.Snapshot())
	assert.Empty(t, s.Snapshot().ServiceNames())
}

// TestState_Snapshot_ConcurrentReadersWhilePublishing checks lock-free reads
// against a publishing writer. Run under -race.
func TestState_Snapshot_ConcurrentReadersWhilePublishing(t *testing.T) {
	t.Parallel()

	s := NewState()

	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range 500 {
			val := fmt.Sprintf("v%d", i)
			s.Publish(snapshot.New(snapshot.Parts{
				Local:    map[string]string{"a": val, "b": val},
				Services: map[string]string{"svc": val},
			}))
		}
	})

	for range 4 {
		wg.Go(func() {
			for range 500 {
				snap := s.Snapshot()
				if !assert.NotNil(t, snap) {
					return
				}

				// Both keys come from the same immutable snapshot.
				a, okA := snap.Local("a")
				b, okB := snap.Local("b")
				if okA != okB || a != b {
					t.Errorf("torn snapshot read: a=%q(%v) b=%q(%v)", a, okA, b, okB)

					return
				}
			}
		})
	}

	wg.Wait()

	v, ok := s.Snapshot().Local("a")
	assert.True(t, ok)
	assert.Equal(t, "v499", v)
}

func TestState_TakeRequestedConfigs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewState()

	assert.Empty(t, s.TakeRequestedConfigs())

	s.AddRequestedConfigs(ctx, []string{"proxy", "mta", "ldap", "mta"})

	assert.Equal(t, []string{"ldap", "mta", "proxy"}, s.TakeRequestedConfigs())
	assert.Empty(t, s.TakeRequestedConfigs(), "taking clears the set")

	s.SetRequestedConfig(ctx, "mailbox")
	assert.Equal(t, []string{"mailbox"}, s.TakeRequestedConfigs())
}

func TestState_TakeRequestedConfigs_Concurrent(t *testing.T) {
	t.Parallel()

	const (
		writers = 4
		perW    = 100
	)

	ctx := context.Background()
	s := NewState()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		taken = map[string]bool{}
	)

	for w := range writers {
		wg.Go(func() {
			for i := range perW {
				s.AddRequestedConfigs(ctx, []string{fmt.Sprintf("s-%d-%d", w, i)})
			}
		})
	}

	wg.Go(func() {
		for range 200 {
			got := s.TakeRequestedConfigs()

			mu.Lock()
			for _, k := range got {
				taken[k] = true
			}
			mu.Unlock()
		}
	})

	wg.Wait()

	for _, k := range s.TakeRequestedConfigs() {
		taken[k] = true
	}

	assert.Len(t, taken, writers*perW, "every requested section is taken exactly once or more, never lost")
}

func TestState_ForcedSections_Sorted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewState()

	assert.Empty(t, s.ForcedSections())

	for _, sec := range []string{"zeta", "alpha", "mid"} {
		s.SetForcedConfig(ctx, sec)
	}

	assert.Equal(t, []string{"alpha", "mid", "zeta"}, s.ForcedSections())
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, s.ForcedSections(), "reading does not clear")

	s.ResetForcedConfig(ctx)
	assert.Empty(t, s.ForcedSections())
}
