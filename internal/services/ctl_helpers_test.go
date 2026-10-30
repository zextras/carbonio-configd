// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/systemd"
)

// emptyConfig is the localconfig loader of test Controllers: no keys, no error.
func emptyConfig() (map[string]string, error) { return map[string]string{}, nil }

// modeExec wraps an Exec and answers the init-mode probe (`systemctl
// is-enabled <target>`, see systemd.Manager.IsSystemdEnabled) with a pinned
// verdict, so the legacy/systemd orchestration mode never depends on the host.
// Every other command goes to the wrapped Exec untouched.
type modeExec struct {
	sys.Exec
	strict bool
}

func (m modeExec) Run(ctx context.Context, c *sys.Cmd) (sys.Result, error) {
	if c.Path == "systemctl" && len(c.Args) > 0 && c.Args[0] == "is-enabled" {
		if m.strict {
			return sys.Result{}, nil
		}

		return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
	}

	return m.Exec.Run(ctx, c)
}

// withMode returns env whose init mode is pinned: strict=true means systemd
// mode (a Carbonio target is enabled), false means legacy zm*ctl mode.
func withMode(env sys.System, strict bool) sys.System {
	env.Exec = modeExec{Exec: env.Exec, strict: strict}

	return env
}

// newFakeController returns a Controller over fresh systest fakes in legacy
// (non-systemd) mode, rooted at the default Carbonio base directory of the
// fake filesystem, with an empty localconfig. Pass options to override any of
// that (later options win).
func newFakeController(t *testing.T, opts ...Option) (*Controller, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	ctl := NewController(withMode(f.System(), false),
		append([]Option{WithConfigLoader(emptyConfig), WithRawConfigLoader(emptyConfig)}, opts...)...)

	return ctl, f
}

// newRealController returns a Controller over the real host (sys.Real()) in
// legacy mode, with the production /opt/zextras layout (registry paths are
// literal in the assertions) and a fresh temp run dir, with an empty
// localconfig. Tests that write files point the relevant ctl.p field at a
// temp dir. Use it when a test is about real files, real processes
// or real signals; options override the defaults (later options win).
func newRealController(t *testing.T, opts ...Option) *Controller {
	t.Helper()

	env := withMode(sys.Real(), false)
	defaults := []Option{
		WithRunDir(t.TempDir()),
		WithConfigLoader(emptyConfig),
		WithRawConfigLoader(emptyConfig),
	}

	return NewController(env, append(defaults, opts...)...)
}

// pumpClock advances clk by step in a loop until the returned stop function is
// called, letting code that waits on the manual clock (timers, tickers,
// Clock.After polling) make progress while the test goroutine blocks on the
// code under test. stop is also registered with t.Cleanup.
func pumpClock(t *testing.T, clk *systest.Clock, step time.Duration) (stop func()) {
	t.Helper()

	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		for {
			select {
			case <-done:
				return
			default:
			}

			if clk.Waiters() > 0 {
				clk.Advance(step)
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}()

	var once bool

	stop = func() {
		if once {
			return
		}

		once = true

		close(done)
		<-finished
	}

	t.Cleanup(stop)

	return stop
}

// pinMode pins the Controller's init mode (strict=true: systemd mode, false:
// legacy zm*ctl mode) regardless of the host, resetting the cached verdict.
func (c *Controller) pinMode(strict bool) {
	c.env = withMode(c.env, strict)
	if fsys, ok := c.env.FS.(*systest.FS); ok && strict {
		fsys.PutDir("/run/systemd/system", 0o755) // sd_booted(3): systemd is the init
	}

	c.systemd = systemd.NewManager(c.env)
	c.modeOnce = sync.Once{}
}

// useProcRoot points the Controller's process table at root, a directory laid
// out like /proc, keeping every other boundary as it was.
func (c *Controller) useProcRoot(root string) {
	c.env.Proc = sys.NewRealProc(root)
}

// blockingExec wraps an Exec so that started processes stay "running": Wait
// blocks until the process is Released (or the test ends), instead of
// returning at once as the systest fake does. Use it for daemons whose exit
// must not race with the code waiting on them (readiness polling).
type blockingExec struct {
	sys.Exec
}

type blockingProc struct {
	sys.Process
	gone chan struct{}
	once sync.Once
}

func (b blockingExec) Start(ctx context.Context, c *sys.Cmd) (sys.Process, error) {
	p, err := b.Exec.Start(ctx, c)
	if err != nil {
		return nil, err
	}

	return &blockingProc{Process: p, gone: make(chan struct{})}, nil
}

func (p *blockingProc) Wait() error {
	<-p.gone

	return p.Process.Wait()
}

func (p *blockingProc) Release() error {
	p.once.Do(func() { close(p.gone) })

	return p.Process.Release()
}

// withBlockingStart makes every process the Controller starts stay running
// until released.
func (c *Controller) withBlockingStart() {
	c.env.Exec = blockingExec{Exec: c.env.Exec}
}
