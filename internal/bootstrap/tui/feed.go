// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
)

// Feed carries the progress of a run from the goroutine that runs it to the
// screen: the step events, and the log lines (the Log writes its progress
// output here instead of the terminal). The screen polls it on its tick.
type Feed struct {
	mu      sync.Mutex
	events  []setup.Event
	lines   []string
	partial string
	stop    atomic.Bool
}

// NewFeed returns an empty feed.
func NewFeed() *Feed { return &Feed{} }

// Observe records an event; it is the setup.Observer of the run.
func (f *Feed) Observe(ev setup.Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
}

// Write records log output, a line at a time; it is the writer of the
// run's setup.Log. The text is already redacted by the Log.
func (f *Feed) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	text := f.partial + string(p)
	parts := strings.Split(text, "\n")
	f.partial = parts[len(parts)-1]

	for _, l := range parts[:len(parts)-1] {
		f.lines = append(f.lines, strings.TrimRight(l, "\r"))
	}

	return len(p), nil
}

// RequestStop asks the run to end after the current step.
func (f *Feed) RequestStop() { f.stop.Store(true) }

// Stopping reports whether a stop was requested; it is the Stop of the run.
func (f *Feed) Stopping() bool { return f.stop.Load() }

// drain returns the events recorded since the last call.
func (f *Feed) drain() []setup.Event {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := f.events
	f.events = nil

	return out
}

// logLines returns every complete line so far, and the line being written.
func (f *Feed) logLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := append([]string(nil), f.lines...)
	if f.partial != "" {
		out = append(out, f.partial)
	}

	return out
}
