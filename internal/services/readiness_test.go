// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
)

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

// mailPorts stubs Controller.mailPortOf: it answers the results in order (the
// last one for the calls after them), and counts the calls and the hosts.
type mailPorts struct {
	results []mailPortResult
	calls   atomic.Int32
	host    atomic.Value
}

type mailPortResult struct {
	port string
	err  error
}

func (m *mailPorts) lookup(_ context.Context, _ map[string]string, host string) (string, error) {
	m.host.Store(host)

	i := int(m.calls.Add(1)) - 1
	if i >= len(m.results) {
		i = len(m.results) - 1
	}

	return m.results[i].port, m.results[i].err
}

var errNoLDAP = errors.New("ldap down")

func TestWaitForMailboxPort_OpenPort(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	port, _ := listenPort(t)
	ports := &mailPorts{results: []mailPortResult{{port: port}}}
	ctl.mailPortOf = ports.lookup

	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "mbox.example.com"})

	require.NoError(t, err)
	assert.Equal(t, "mbox.example.com", ports.host.Load(), "the port of the local server is read")
}

func TestWaitForMailboxPort_LDAPRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, _ := listenPort(t)
	ports := &mailPorts{results: []mailPortResult{{err: errNoLDAP}, {err: errNoLDAP}, {port: port}}}
	ctl.mailPortOf = ports.lookup
	pumpClock(t, f.Clock, mailboxPortInterval)

	require.NoError(t, ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"}))
	assert.Equal(t, int32(3), ports.calls.Load())
}

func TestWaitForMailboxPort_PortNeverResolved(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ports := &mailPorts{results: []mailPortResult{{err: errNoLDAP}}}
	ctl.mailPortOf = ports.lookup
	pumpClock(t, f.Clock, mailboxPortInterval)

	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "zimbraMailPort could not be resolved from LDAP")
	assert.ErrorIs(t, err, errNoLDAP, "the last failure is reported")
	assert.Equal(t, int32(mailboxPortAttempts), ports.calls.Load())
}

func TestWaitForMailboxPort_PortNeverOpens(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	port, stop := listenPort(t)
	stop() // nothing listens any more: connection refused

	ctl.mailPortOf = (&mailPorts{results: []mailPortResult{{port: port}}}).lookup
	pumpClock(t, f.Clock, mailboxDialInterval)

	start := f.Clock.Now()
	err := ctl.waitForMailboxPort(context.Background(), map[string]string{"zimbra_server_hostname": "h"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not open")
	assert.GreaterOrEqual(t, f.Clock.Now().Sub(start), (mailboxDialAttempts-1)*mailboxDialInterval)
}

func TestWaitForMailboxPort_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	ports := &mailPorts{results: []mailPortResult{{err: errNoLDAP}}}
	ctl.mailPortOf = ports.lookup

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctl.waitForMailboxPort(ctx, map[string]string{"zimbra_server_hostname": "h"})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), ports.calls.Load(), "no further attempt once the context ended")
}

const attrsXML = `<attrs><attr id="154" name="zimbraMailPort" type="port" cardinality="single" ` +
	`optionalIn="globalConfig,server" flags="serverInherited"/></attrs>`

// ldapMailPortConfig is the localconfig of a node whose directory is the fake
// server, with the attribute schema under a temporary template directory.
func ldapMailPortConfig(t *testing.T, srv *ldapfake.Server) map[string]string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "conf", "attrs"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conf", "attrs", "attrs.xml"), []byte(attrsXML), 0o600))

	return map[string]string{
		"ldap_url":               srv.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":     "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password":   "secret",
		"zmconfigd_template_dir": dir,
	}
}

func TestReadMailPort_ServerValueThenInheritedGlobalValue(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{Entries: map[string]map[string][]string{
		"cn=config,cn=zimbra": {"zimbraMailPort": {"8080"}, "objectClass": {"zimbraGlobalConfig"}},
		"cn=own.example.com,cn=servers,cn=zimbra": {
			"zimbraMailPort": {"8181"}, "objectClass": {"zimbraServer"},
		},
		"cn=inherit.example.com,cn=servers,cn=zimbra": {"objectClass": {"zimbraServer"}},
	}})
	ctl := newRealController(t)
	lc := ldapMailPortConfig(t, srv)

	port, err := ctl.readMailPort(context.Background(), lc, "own.example.com")
	require.NoError(t, err)
	assert.Equal(t, "8181", port, "the value of the server entry wins")

	port, err = ctl.readMailPort(context.Background(), lc, "inherit.example.com")
	require.NoError(t, err)
	assert.Equal(t, "8080", port, "a server without the attribute inherits the global config one")
}

func TestReadMailPort_Failures(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{Entries: map[string]map[string][]string{
		"cn=config,cn=zimbra": {"objectClass": {"zimbraGlobalConfig"}},
		"cn=bad.example.com,cn=servers,cn=zimbra": {
			"zimbraMailPort": {"http"}, "objectClass": {"zimbraServer"},
		},
		"cn=none.example.com,cn=servers,cn=zimbra": {"objectClass": {"zimbraServer"}},
	}})
	ctl := newRealController(t)
	lc := ldapMailPortConfig(t, srv)

	for name, tc := range map[string]struct {
		host string
		lc   map[string]string
		want string
	}{
		"no such server":   {"nosuch.example.com", lc, "nosuch.example.com"},
		"not a number":     {"bad.example.com", lc, "no valid zimbraMailPort"},
		"no port anywhere": {"none.example.com", lc, "no valid zimbraMailPort"},
		"no LDAP url":      {"bad.example.com", map[string]string{"zimbra_ldap_userdn": "x", "zimbra_ldap_password": "y"}, "URL"},
	} {
		_, err := ctl.readMailPort(context.Background(), tc.lc, tc.host)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
}

// configdServer answers every connection with reply (one line) after reading
// the request line, which is sent on the returned channel.
func configdServer(t *testing.T, reply string) (port int, requests <-chan string) {
	t.Helper()

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	reqs := make(chan string, 1024)

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

// refusedPort returns a loopback TCP port that refuses connections and cannot
// be taken by a concurrent test: a socket bound but never listening answers
// SYNs with RST while holding the port. (Closing a listener instead released
// the port, which a parallel test's listener could then be handed before the
// dial happened: the flake of this test.)
func refusedPort(t *testing.T) int {
	t.Helper()

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Close(fd) })

	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}))

	sa, err := syscall.Getsockname(fd)
	require.NoError(t, err)

	return sa.(*syscall.SockaddrInet4).Port
}

func TestConfigdStatus_Unreachable(t *testing.T) {
	t.Parallel()

	ctl, _ := configdController(t, refusedPort(t))

	_, err := ctl.configdStatus(context.Background())

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
	// Advance 3s per pause: the whole 300s window in ~100 probes.
	pumpClock(t, f.Clock, 3*time.Second)

	start := f.Clock.Now()
	err := ctl.waitForConfigd(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not become ready within 5m0s")
	assert.Contains(t, err.Error(), "SUCCESS STARTING")
	assert.GreaterOrEqual(t, f.Clock.Now().Sub(start), configdReadyTimeout)
	assert.NotEmpty(t, reqs)
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

	start := f.Clock.Now()

	require.NoError(t, ctl.waitForConfigd(context.Background()))
	assert.Equal(t, int32(3), served.Load())
	assert.Less(t, f.Clock.Now().Sub(start), time.Second,
		"start returns as soon as the daemon is ready, not a full legacy 3s interval later")
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

	require.NoError(t, ctl.startDirect(context.Background(), def.Name, def))
	assert.Equal(t, 1, ready)
}

func TestConfigdCustomStart_ReadyFailureIsReturned(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	def := ctl.registry[svcConfigd]
	f.FS.Put(def.BinaryPath, []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	ctl.configdReady = func(context.Context) error { return assert.AnError }

	require.ErrorIs(t, ctl.startDirect(context.Background(), def.Name, def), assert.AnError)
}

func TestConfigdCustomStart_StartFailureSkipsReadiness(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t) // binary absent from the fake FS
	def := ctl.registry[svcConfigd]

	ctl.configdReady = func(context.Context) error {
		t.Error("readiness must not be probed when the start failed")

		return nil
	}

	require.Error(t, ctl.startDirect(context.Background(), def.Name, def))
}
