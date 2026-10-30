// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestState_FileCacheConcurrent drives the FILE cache from several goroutines.
// Run under -race: before the locked accessors existed the loader replaced and
// the lookup wrote the map without holding the State lock.
func TestState_FileCacheConcurrent(t *testing.T) {
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
	s := NewState()

	if _, ok := s.LookupFile("missing"); ok {
		t.Fatal("LookupFile(missing) found a value in an empty cache")
	}

	s.StoreFile("a", "alpha")

	if got, ok := s.LookupFile("a"); !ok || got != "alpha" {
		t.Fatalf("LookupFile(a) = %q, %v; want alpha, true", got, ok)
	}

	s.ClearFileCache(context.Background())

	if _, ok := s.LookupFile("a"); ok {
		t.Fatal("LookupFile(a) still cached after ClearFileCache")
	}
}

// TestState_ConfigGettersConcurrentWithSetConfigs reads every config pointer
// while SetConfigs swaps them, as the watchdog config lookup does.
func TestState_ConfigGettersConcurrentWithSetConfigs(t *testing.T) {
	s := NewState()

	var wg sync.WaitGroup

	wg.Go(func() {
		for range 500 {
			s.SetConfigs(
				&config.LocalConfig{Data: config.NewConfigMap()},
				&config.GlobalConfig{Data: config.NewConfigMap()},
				&config.MiscConfig{Data: config.NewConfigMap()},
				&config.ServerConfig{Data: config.NewConfigMap(), ServiceConfig: config.NewConfigMap()},
				&config.MtaConfig{Sections: make(map[string]*config.MtaConfigSection)},
			)
			s.SetMtaConfig(&config.MtaConfig{Sections: make(map[string]*config.MtaConfigSection)})
			s.SetFirstRun(false)
		}
	})

	wg.Go(func() {
		for range 500 {
			_ = s.GetLocalConfig().Data
			_ = s.GetGlobalConfig().Data
			_ = s.GetMiscConfig().Data
			_ = s.GetServerConfig().Data
			_ = s.GetMtaConfig().Sections
			_ = s.IsFirstRun()
		}
	})

	wg.Wait()

	if s.IsFirstRun() {
		t.Error("IsFirstRun() = true after SetFirstRun(false)")
	}
}
