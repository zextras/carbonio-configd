// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"slices"
	"testing"
)

func knownHandler(trigger *MockActionTrigger) *ConfigdRequestHandler {
	return &ConfigdRequestHandler{
		ActionTrigger: trigger,
		KnownSections: func() []string { return []string{"mta", "proxy"} },
	}
}

// Unknown names never reach the main loop; the reply stays the legacy one.
func TestRewriteDropsUnknownSections(t *testing.T) {
	trigger := &MockActionTrigger{}
	h := knownHandler(trigger)

	got := h.HandleRequest(context.Background(), "REWRITE", []string{"mta", "../etc/passwd", "proxy", "bogus"})
	if got != respRewritesComplete {
		t.Fatalf("reply = %q, want %q", got, respRewritesComplete)
	}

	if want := []string{"mta", "proxy"}; !slices.Equal(trigger.lastConfigs, want) {
		t.Fatalf("forwarded %v, want %v", trigger.lastConfigs, want)
	}
}

// A request naming only unknown sections is acknowledged like legacy (nothing
// matches, nothing is rewritten) without waking the main loop.
func TestRewriteAllUnknownSectionsSkipsTrigger(t *testing.T) {
	trigger := &MockActionTrigger{}
	h := knownHandler(trigger)

	if got := h.HandleRequest(context.Background(), "REWRITE", []string{"bogus"}); got != respRewritesComplete {
		t.Fatalf("reply = %q, want %q", got, respRewritesComplete)
	}

	if n := trigger.GetRewriteCallCount(); n != 0 {
		t.Fatalf("trigger called %d times, want 0", n)
	}
}

// Without a section source nothing is filtered.
func TestRewriteWithoutKnownSectionsPassesThrough(t *testing.T) {
	trigger := &MockActionTrigger{}
	h := &ConfigdRequestHandler{ActionTrigger: trigger}

	h.HandleRequest(context.Background(), "REWRITE", []string{"anything"})

	if want := []string{"anything"}; !slices.Equal(trigger.lastConfigs, want) {
		t.Fatalf("forwarded %v, want %v", trigger.lastConfigs, want)
	}
}
