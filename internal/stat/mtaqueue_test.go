// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// hangingExec starts postfix-prometheus as a process that only ends when the
// test releases it, and counts the starts.
type hangingExec struct {
	sys.Exec
	release chan struct{}
	mu      sync.Mutex
	starts  int
	ctxs    []context.Context
}

func (h *hangingExec) Start(ctx context.Context, c *sys.Cmd) (sys.Process, error) {
	if filepath.Base(c.Path) != "postfix-prometheus" {
		return h.Exec.Start(ctx, c)
	}

	h.mu.Lock()
	h.starts++
	h.ctxs = append(h.ctxs, ctx)
	h.mu.Unlock()

	return hangingProcess{h.release}, nil
}

type hangingProcess struct{ release chan struct{} }

func (hangingProcess) Pid() int       { return 1234 }
func (p hangingProcess) Wait() error  { <-p.release; return nil }
func (hangingProcess) Release() error { return nil }

// A postfix-prometheus run that has not ended is not stacked under by the next
// sample, and every run is bounded by a deadline.
func TestMtaqueuePrometheusDoesNotPileUp(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("1", "1")))

	h := &hangingExec{Exec: te.Sys.Exec, release: make(chan struct{})}
	te.Sys.Exec = h

	c, err := mtaqueueSpec().build(te.Env, opts30())
	require.NoError(t, err)

	te.sample(t, c, 0)
	te.sample(t, c, 1)
	te.sample(t, c, 2)

	h.mu.Lock()
	assert.Equal(t, 1, h.starts)
	_, hasDeadline := h.ctxs[0].Deadline()
	h.mu.Unlock()
	assert.True(t, hasDeadline)

	close(h.release)
	assert.Eventually(t, func() bool {
		te.sample(t, c, 3)

		h.mu.Lock()
		defer h.mu.Unlock()

		return h.starts == 2
	}, 5*time.Second, time.Millisecond)
}

func postqueueOutput(kb, reqs string) string {
	return "-Queue ID-  --Size-- ----Arrival Time---- -Sender/Recipient-------\n" +
		"ABCDEF0123*   1234 Thu Jan  1 00:00:00  a@example.com\n                                         b@example.com\n\n" +
		"-- " + kb + " Kbytes in " + reqs + " Requests.\n"
}

// stat-mtaqueue.pl: the size and request count come from the last line of
// postqueue -p (0, 0 when it says nothing); the postfix-prometheus helper is
// started on every sample.
func TestMtaqueueGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("12", "3")))

	c, err := mtaqueueSpec().build(te.Env, opts30())
	require.NoError(t, err)

	assert.Equal(t, "timestamp, KBytes, requests", c.Heading())
	assert.Equal(t, []string{stamp(0) + ", 12, 3"}, te.sample(t, c, 0))
	assert.Equal(t, []string{"postqueue -p", filepath.Join(te.Base, "libexec", "postfix-prometheus")},
		te.fakes.Exec.Lines())

	te.fakes.Exec.OnPath("postqueue", systest.Reply("postqueue: fatal: Queue report unavailable\n"))
	assert.Equal(t, []string{stamp(1) + ", 0, 0"}, te.sample(t, c, 1))
}

// A big queue backs the monitoring off by --backoff-interval up to
// --max-interval and returns to the interval when it drains.
func TestMtaqueueBackoff(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("900", "50")))

	c, err := mtaqueueSpec().build(te.Env, &options{
		interval: 30 * time.Second,
		ints:     map[string]int{"max-qsize": 10, "backoff-interval": 30, "max-interval": 60},
	})
	require.NoError(t, err)

	p, ok := c.(Paced)
	require.True(t, ok)

	te.sample(t, c, 0)
	assert.Equal(t, 60*time.Second, p.Interval())
	assert.Equal(t, stamp(0)+": Queue is too big! (50 msgs); Increasing monitor interval to 60 seconds\n",
		te.stderr.String())

	te.stderr.Reset()
	te.sample(t, c, 1)
	assert.Equal(t, 60*time.Second, p.Interval())
	assert.Equal(t, stamp(1)+": Queue is too big! (50 msgs); Already at maximum monitor interval of 60 seconds\n",
		te.stderr.String())

	te.stderr.Reset()
	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("1", "5")))
	te.sample(t, c, 2)
	assert.Equal(t, 30*time.Second, p.Interval())
	assert.Equal(t, stamp(2)+": Queue size is back within the maximum of 10; Restoring default monitor interval of 30 seconds\n",
		te.stderr.String())
}

func TestMtaqueueDefaultLimits(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("1", "1000")))

	c, err := mtaqueueSpec().build(te.Env, opts30())
	require.NoError(t, err)

	te.sample(t, c, 0)
	assert.Equal(t, 30*time.Second, c.(Paced).Interval(), "1000 requests is not over the default limit")

	te.fakes.Exec.OnPath("postqueue", systest.Reply(postqueueOutput("1", "1001")))
	te.sample(t, c, 1)
	assert.Equal(t, 60*time.Second, c.(Paced).Interval())
}
