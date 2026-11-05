// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// newSDController returns a Controller whose notify sockets are real unixgram
// sockets under a temp run dir (the sd_notify protocol is kernel I/O), but
// whose processes and clock are fakes: started children stay "running" until
// released, and the 30s readiness deadline runs on the manual clock.
func newSDController(t *testing.T) (*Controller, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	env := sys.Real()
	env.Exec = blockingExec{Exec: f.Exec}
	env.Clock = f.Clock

	ctl := NewController(withMode(env, false),
		WithBaseDir(t.TempDir()), WithRunDir(socketDir(t)),
		WithConfigLoader(emptyConfig))

	return ctl, f
}

// expectedSocketPath returns the socket path startWithSDNotify will create
// for the given service name. The path is service-addressable (not
// per-PID) so both the start-side READY=1 listener and the stop-side
// STOPPING=1 observer can bind to it across independent CLI invocations.
func expectedSocketPath(ctl *Controller, service string) string {
	return ctl.p.sdNotifySocket(service)
}

// sendReady sends a READY=1 datagram to the given Unix socket path, retrying
// until the socket exists or the deadline is reached.
func sendReady(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := net.Dial("unixgram", socketPath)
		if err != nil {
			time.Sleep(10 * time.Millisecond)

			continue
		}

		_, writeErr := conn.Write([]byte("READY=1\n"))
		_ = conn.Close()

		if writeErr != nil {
			t.Fatalf("send READY=1 to %s: %v", socketPath, writeErr)
		}

		return
	}

	t.Fatalf("socket %s never appeared within 2 seconds", socketPath)
}

// waitForSocket fails the test unless the notify socket at path appears.
func waitForSocket(t *testing.T, path string) {
	t.Helper()

	require.Eventually(t, func() bool {
		_, err := os.Stat(path)

		return err == nil
	}, 3*time.Second, time.Millisecond, "notify socket %s never appeared", path)
}

// sdWaitBound bounds every wait of the sd_notify tests on the code under test:
// a regression must fail the test, never hang the package until the go test
// timeout.
const sdWaitBound = 5 * time.Second

type sdResult struct {
	proc sys.Process
	err  error
}

// startAsync runs startWithSDNotify in a goroutine and returns its result
// channel. ctx is cancelled when the test ends, so a call that never reaches
// READY=1 (its deadline runs on a manual clock) cannot outlive the test.
func startAsync(ctx context.Context, t *testing.T, ctl *Controller, cmd *sys.Cmd, service string) <-chan sdResult {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	done := make(chan sdResult, 1)

	go func() {
		proc, err := ctl.startWithSDNotify(ctx, cmd, service)
		done <- sdResult{proc, err}
	}()

	return done
}

// awaitSDResult returns the result of startAsync, failing the test if it does
// not arrive within sdWaitBound.
func awaitSDResult(t *testing.T, done <-chan sdResult) sdResult {
	t.Helper()

	select {
	case r := <-done:
		return r
	case <-time.After(sdWaitBound):
		t.Fatalf("startWithSDNotify did not return within %s", sdWaitBound)

		return sdResult{}
	}
}

// TestStartWithSDNotify_Ready verifies that startWithSDNotify returns nil and
// the started process when READY=1 is sent to the notify socket.
func TestStartWithSDNotify_Ready(t *testing.T) {
	t.Parallel()

	ctl, f := newSDController(t)
	cmd := &sys.Cmd{Path: "slapd"}

	done := startAsync(context.Background(), t, ctl, cmd, "test-ready")

	sendReady(t, expectedSocketPath(ctl, "test-ready"))

	r := awaitSDResult(t, done)
	if r.err != nil {
		t.Errorf("expected nil, got %v", r.err)
	}

	if r.proc == nil {
		t.Error("expected the started process to be returned")
	}

	if got := f.Rec.Of("exec.start"); len(got) != 1 {
		t.Errorf("expected exactly one process start, got %q", got)
	}
}

// TestStartWithSDNotify_Timeout verifies that startWithSDNotify returns an error
// when no READY=1 datagram is received within the (manual-clock) deadline.
func TestStartWithSDNotify_Timeout(t *testing.T) {
	t.Parallel()

	ctl, f := newSDController(t)

	done := startAsync(context.Background(), t, ctl, &sys.Cmd{Path: "slapd"}, "test-timeout")
	bound := time.After(sdWaitBound)

	for {
		select {
		case r := <-done:
			if r.err == nil {
				t.Fatal("expected error on timeout, got nil")
			}

			if !strings.Contains(r.err.Error(), "READY=1") {
				t.Errorf("expected error about READY=1, got: %v", r.err)
			}

			if r.proc == nil {
				t.Error("the started process must still be returned on a readiness timeout")
			}

			return
		case <-bound:
			t.Fatalf("startWithSDNotify did not time out within %s of the readiness deadline passing", sdWaitBound)
		case <-time.After(time.Millisecond):
			// Keep the clock beyond the deadline: it is computed after the
			// process start, so a single early Advance could be missed.
			f.Clock.Advance(sdNotifyReadyTimeout + time.Second)
		}
	}
}

// TestStartWithSDNotify_ChildExitFailsFast verifies that a child exiting
// before READY=1 aborts the wait with its exit error.
func TestStartWithSDNotify_ChildExitFailsFast(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("slapd", systest.Fail(1, ""))

	env := sys.Real()
	env.Exec = f.Exec
	env.Clock = f.Clock

	ctl := NewController(withMode(env, false), WithBaseDir(t.TempDir()), WithRunDir(socketDir(t)),
		WithConfigLoader(emptyConfig))

	r := awaitSDResult(t, startAsync(context.Background(), t, ctl, &sys.Cmd{Path: "slapd"}, "test-exit"))
	if r.err == nil || !strings.Contains(r.err.Error(), "exited during startup before signaling READY=1") {
		t.Fatalf("error = %v, want early-exit failure", r.err)
	}
}

// TestStartWithSDNotify_ContextCancel verifies that startWithSDNotify returns
// context.Canceled when the context is cancelled before READY=1 is received.
func TestStartWithSDNotify_ContextCancel(t *testing.T) {
	t.Parallel()

	ctl, _ := newSDController(t)

	ctx, cancel := context.WithCancel(context.Background())
	socketPath := expectedSocketPath(ctl, "test-cancel")

	done := startAsync(ctx, t, ctl, &sys.Cmd{Path: "slapd"}, "test-cancel")

	// Wait until the socket exists (startWithSDNotify is in the wait loop).
	waitForSocket(t, socketPath)

	cancel()

	if r := awaitSDResult(t, done); !errors.Is(r.err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", r.err)
	}
}

// TestStartWithSDNotify_NotifySocketInEnv verifies that cmd.Env contains
// NOTIFY_SOCKET pointing to the expected socket path.
func TestStartWithSDNotify_NotifySocketInEnv(t *testing.T) {
	t.Parallel()

	ctl, f := newSDController(t)
	cmd := &sys.Cmd{Path: "slapd"}
	socketPath := expectedSocketPath(ctl, "test-env")

	done := startAsync(context.Background(), t, ctl, cmd, "test-env")

	sendReady(t, socketPath)
	awaitSDResult(t, done)

	started := f.Exec.Calls()
	if len(started) != 1 {
		t.Fatalf("expected one started command, got %d", len(started))
	}

	notifyVal := ""
	for _, e := range started[0].Env {
		if val, ok := strings.CutPrefix(e, "NOTIFY_SOCKET="); ok {
			notifyVal = val

			break
		}
	}

	if notifyVal == "" {
		t.Fatal("NOTIFY_SOCKET not set in cmd.Env")
	}

	if notifyVal != socketPath {
		t.Errorf("NOTIFY_SOCKET = %q, want %q", notifyVal, socketPath)
	}
}

// TestStartWithSDNotify_SocketCleanedUp verifies the socket file is removed
// after startWithSDNotify returns.
func TestStartWithSDNotify_SocketCleanedUp(t *testing.T) {
	t.Parallel()

	ctl, _ := newSDController(t)
	socketPath := expectedSocketPath(ctl, "test-cleanup")

	done := startAsync(context.Background(), t, ctl, &sys.Cmd{Path: "slapd"}, "test-cleanup")

	sendReady(t, socketPath)
	awaitSDResult(t, done)

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket %s still exists after startWithSDNotify returned", socketPath)
	}
}

// TestAwaitSDNotifyStopping_ReturnsOnStoppingDatagram asserts the shutdown
// observer exits as soon as a STOPPING=1 datagram is delivered. Regression
// guard for the stop-side sd_notify enhancement: without it, operators can't
// distinguish a daemon that engaged its graceful-shutdown hook from one that
// silently ignored SIGTERM and was forcibly reaped.
func TestAwaitSDNotifyStopping_ReturnsOnStoppingDatagram(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: uses a real Unix datagram socket")
	}

	socketPath := expectedSocketPath(ctl, "test-stopping")
	_ = os.Remove(socketPath)

	ctx := t.Context()

	done := make(chan struct{})

	go func() {
		ctl.awaitSDNotifyStopping(ctx, "test-stopping")

		close(done)
	}()

	waitForSocket(t, socketPath)

	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("failed to dial observer socket %s: %v", socketPath, err)
	}

	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("STOPPING=1\n")); err != nil {
		t.Fatalf("failed to send STOPPING=1: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not return within 3s of receiving STOPPING=1")
	}
}

// TestAwaitSDNotifyStopping_ReturnsOnContextCancel asserts the observer
// exits when its parent context is cancelled, so a stop() call never leaks
// the goroutine past its own lifetime.
func TestAwaitSDNotifyStopping_ReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	_ = os.Remove(expectedSocketPath(ctl, "test-cancel-stopping"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // a failed wait below must not leave the observer running

	done := make(chan struct{})

	go func() {
		ctl.awaitSDNotifyStopping(ctx, "test-cancel-stopping")

		close(done)
	}()

	// Cancel only once the observer is up (its socket exists), so the test
	// exercises cancellation of a running observer rather than a race with its
	// start-up.
	socketPath := expectedSocketPath(ctl, "test-cancel-stopping")

	waitForSocket(t, socketPath)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("observer did not return within 2s of ctx cancellation")
	}
}

func TestStartWithSDNotify_CmdStartFails(t *testing.T) {
	t.Parallel()

	ctl, f := newSDController(t)
	f.Exec.FailStart(func(*sys.Cmd) bool { return true }, errors.New("fork/exec: no such file or directory"))

	proc, err := ctl.startWithSDNotify(context.Background(), &sys.Cmd{Path: "slapd"}, "test-start-fail")
	if err == nil {
		t.Fatal("expected error when the process cannot be started")
	}

	if proc != nil {
		t.Error("no process may be returned when the start fails")
	}
}

func TestSdNotifySocketPath_Format(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	path := ctl.p.sdNotifySocket("myservice")
	if !strings.Contains(path, "notify-myservice.sock") {
		t.Errorf("expected socket path to contain 'notify-myservice.sock', got %s", path)
	}
}

// TestStopWithSDNotify_RestartKeepsNotifySocket is the regression test of the
// stop-side observer unlinking the notify socket of the start that follows: a
// restart of an sd_notify service in legacy mode stops it (observer started),
// then starts it again, and the daemon's READY=1 must still reach the start.
// Before the stop waited for its observer, the observer's deferred unlink ran
// after the start had bound its own socket at the same path.
func TestStopWithSDNotify_RestartKeepsNotifySocket(t *testing.T) {
	t.Parallel()

	ctl, f := newSDController(t)
	def := ctl.registry[svcOpendkim]

	if err := os.MkdirAll(filepath.Dir(def.BinaryPath), 0o755); err != nil {
		t.Fatal(err)
	}

	writeExecutable(t, def.BinaryPath, []byte("x"))

	// The daemon reports READY=1 a moment after the launch, after the window
	// in which the stop-side observer (one read deadline) used to unlink the
	// socket path: derived from the production constant, so the test keeps
	// exercising that window if the deadline changes.
	readyDelay := 2 * sdNotifyObserverReadDeadline

	f.Exec.OnPath(def.BinaryPath, func(c *sys.Cmd) (sys.Result, error) {
		for _, e := range c.Env {
			if socket, ok := strings.CutPrefix(e, "NOTIFY_SOCKET="); ok {
				go func() {
					time.Sleep(readyDelay)

					if conn, err := net.Dial("unixgram", socket); err == nil {
						_, _ = conn.Write([]byte("READY=1\n"))
						_ = conn.Close()
					}
				}()
			}
		}

		return sys.Result{}, nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := ctl.stopWithoutSystemd(ctx, def.Name, def); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if err := ctl.startWithoutSystemd(ctx, def.Name, def); err != nil {
		t.Fatalf("start right after stop must see READY=1: %v", err)
	}
}
