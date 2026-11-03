// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestParseMailPort(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, out, want string }{
		{"typical", "# name mbox.example.com\nzimbraMailPort: 8080\n", "8080"},
		{"no trailing newline", "zimbraMailPort: 7070", "7070"},
		{"indented and spaced", "  zimbraMailPort:   8081  \n", "8081"},
		{"CRLF", "# name x\r\nzimbraMailPort: 8082\r\n", "8082"},
		{"no space after colon", "zimbraMailPort:8083\n", "8083"},
		{"first valid wins", "zimbraMailPort: 80\nzimbraMailPort: 81\n", "80"},
		{"non numeric", "zimbraMailPort: abc\n", ""},
		{"other attribute", "zimbraMailSSLPort: 8443\n", ""},
		{"comment only", "# name host\n", ""},
		{"empty", "", ""},
		{"empty value", "zimbraMailPort:\n", ""},
		{"skips bad then takes good", "zimbraMailPort: x\nzimbraMailPort: 90\n", "90"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, parseMailPort(tc.out))
		})
	}
}

// listenPort opens a TCP listener on a random loopback port that accepts and
// closes connections; it returns the port and a close function.
func listenPort(t *testing.T) (port string, stop func()) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			c, aErr := l.Accept()
			if aErr != nil {
				return
			}

			_ = c.Close()
		}
	}()

	t.Cleanup(func() { _ = l.Close() })

	_, port, err = net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)

	return port, func() { _ = l.Close() }
}

func TestWaitForMailboxPort_OpenPort(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, _ := listenPort(t)
	f.Exec.OnPath(ctl.p.bin+"/zmprov", systest.Reply("# name mbox.example.com\nzimbraMailPort: "+port+"\n"))

	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "mbox.example.com"})

	require.NoError(t, err)
	assert.Contains(t, f.Exec.Lines(), ctl.p.bin+"/zmprov -l gs mbox.example.com zimbraMailPort")
}

func TestWaitForMailboxPort_ZmprovRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, _ := listenPort(t)

	var calls atomic.Int32

	f.Exec.OnPath(ctl.p.bin+"/zmprov", func(*sys.Cmd) (sys.Result, error) {
		if calls.Add(1) < 3 {
			return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
		}

		return sys.Result{Stdout: []byte("zimbraMailPort: " + port + "\n")}, nil
	})
	pumpClock(t, f.Clock, mailboxPortInterval)

	require.NoError(t, ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"}))
	assert.Equal(t, int32(3), calls.Load())
}

func TestWaitForMailboxPort_PortNeverResolved(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	var calls atomic.Int32

	f.Exec.OnPath(ctl.p.bin+"/zmprov", func(*sys.Cmd) (sys.Result, error) {
		calls.Add(1)

		return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
	})
	pumpClock(t, f.Clock, mailboxPortInterval)

	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "zimbraMailPort could not be resolved")
	assert.Equal(t, int32(mailboxPortAttempts), calls.Load())
}

func TestWaitForMailboxPort_PortNeverOpens(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, stop := listenPort(t)
	stop() // nothing listens any more: connection refused

	f.Exec.OnPath(ctl.p.bin+"/zmprov", systest.Reply("zimbraMailPort: "+port+"\n"))
	pumpClock(t, f.Clock, mailboxDialInterval)

	start := f.Clock.Now()
	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not open")
	assert.GreaterOrEqual(t, f.Clock.Now().Sub(start), (mailboxDialAttempts-1)*mailboxDialInterval)
}

func TestWaitForMailboxPort_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, stop := listenPort(t)
	stop()

	f.Exec.OnPath(ctl.p.bin+"/zmprov", systest.Reply("zimbraMailPort: "+port+"\n"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctl.waitForMailboxPort(ctx, map[string]string{"zimbra_server_hostname": "h"})

	require.Error(t, err)
}

// configdServer answers every connection with reply (one line) after reading
// the request line, which is sent on the returned channel.
func configdServer(t *testing.T, reply string) (port int, requests <-chan string) {
	t.Helper()

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	reqs := make(chan string, 4*configdReadyAttempts)

	go func() {
		for {
			c, aErr := l.Accept()
			if aErr != nil {
				return
			}

			go func() {
				defer func() { _ = c.Close() }()

				line, _ := bufio.NewReader(c).ReadString('\n')
				reqs <- strings.TrimSpace(line)

				_, _ = c.Write([]byte(reply + "\n"))
			}()
		}
	}()

	port = l.Addr().(*net.TCPAddr).Port

	return port, reqs
}

func configdController(t *testing.T, port int) (*Controller, *systest.Fakes) {
	t.Helper()

	return newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"zmconfigd_listen_port": strconv.Itoa(port)}, nil
	}))
}

func TestConfigdStatus_SendsSTATUSAndReturnsReply(t *testing.T) {
	t.Parallel()

	port, reqs := configdServer(t, "SUCCESS ACTIVE")
	ctl, _ := configdController(t, port)

	reply, err := ctl.configdStatus(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "SUCCESS ACTIVE", reply)
	assert.Equal(t, "STATUS", <-reqs)
}

func TestConfigdStatus_Unreachable(t *testing.T) {
	t.Parallel()

	port, _ := configdServer(t, "x")
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)

	closedPort := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	_ = port

	ctl, _ := configdController(t, closedPort)

	_, err = ctl.configdStatus(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestConfigdPort_DefaultAndOverride(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	assert.Equal(t, defaultConfigdPort, ctl.configdEndpoint().Port)

	ctl2, _ := configdController(t, 7272)
	assert.Equal(t, 7272, ctl2.configdEndpoint().Port)
}

func TestWaitForConfigd_Active(t *testing.T) {
	t.Parallel()

	port, _ := configdServer(t, configdStatusReply)
	ctl, _ := configdController(t, port)

	require.NoError(t, ctl.waitForConfigd(context.Background()))
}

func TestWaitForConfigd_NotActiveTimesOut(t *testing.T) {
	t.Parallel()

	port, reqs := configdServer(t, "SUCCESS STARTING")
	ctl, f := configdController(t, port)
	pumpClock(t, f.Clock, configdReadyInterval)

	start := f.Clock.Now()
	err := ctl.waitForConfigd(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not become ready")
	assert.Contains(t, err.Error(), "SUCCESS STARTING")
	assert.GreaterOrEqual(t, f.Clock.Now().Sub(start), time.Duration(configdReadyAttempts-1)*configdReadyInterval)
	assert.Len(t, reqs, configdReadyAttempts)
}

func TestWaitForConfigd_BecomesActiveAfterRetries(t *testing.T) {
	t.Parallel()

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	var served atomic.Int32

	go func() {
		for {
			c, aErr := l.Accept()
			if aErr != nil {
				return
			}

			_, _ = bufio.NewReader(c).ReadString('\n')

			reply := "SUCCESS STARTING\n"
			if served.Add(1) >= 3 {
				reply = configdStatusReply + "\n"
			}

			_, _ = c.Write([]byte(reply))
			_ = c.Close()
		}
	}()

	ctl, f := configdController(t, l.Addr().(*net.TCPAddr).Port)
	pumpClock(t, f.Clock, configdReadyInterval)

	require.NoError(t, ctl.waitForConfigd(context.Background()))
	assert.Equal(t, int32(3), served.Load())
}

func TestConfigdCustomStart_StartsDaemonThenWaitsReady(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	def := ctl.registry[svcConfigd]
	f.FS.Put(def.BinaryPath, []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	var ready int

	ctl.configdReady = func(context.Context) error {
		ready++

		assert.Contains(t, strings.Join(f.Rec.Strings(), "\n"), "exec.start "+def.BinaryPath,
			"the daemon is started before readiness is checked")

		return nil
	}

	require.NoError(t, ctl.configdCustomStart(context.Background(), def))
	assert.Equal(t, 1, ready)
}

func TestConfigdCustomStart_ReadyFailureIsReturned(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	def := ctl.registry[svcConfigd]
	f.FS.Put(def.BinaryPath, []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	ctl.configdReady = func(context.Context) error { return assert.AnError }

	require.ErrorIs(t, ctl.configdCustomStart(context.Background(), def), assert.AnError)
}

func TestConfigdCustomStart_StartFailureSkipsReadiness(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t) // binary absent from the fake FS
	def := ctl.registry[svcConfigd]

	ctl.configdReady = func(context.Context) error {
		t.Error("readiness must not be probed when the start failed")

		return nil
	}

	require.Error(t, ctl.configdCustomStart(context.Background(), def))
}
