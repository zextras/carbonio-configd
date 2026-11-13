// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

const (
	rcTestHost = "mta.example.com"
	// rcTestIdle is the idle time of the tests: long enough for the gaps
	// between the lines of a slow runner, short enough to wait for.
	rcTestIdle = 250 * time.Millisecond
)

// rcShell is what the "remote command" does: it reads the session's stdin
// and prints to out and errw, and returns its exit status.
type rcShell func(in io.Reader, out, errw io.Writer) int

// rcServer is an in-process SSH server that accepts one public key and runs
// shell for a "shell" request, like a sshd whose authorized_keys forces zmrcd.
type rcServer struct {
	addr  string
	shell rcShell
	done  chan struct{}

	mu    sync.Mutex
	users []string
	pty   atomic.Bool
}

func startRCServer(t *testing.T, authorized ssh.PublicKey, shell rcShell) *rcServer {
	t.Helper()

	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(hostKey)
	require.NoError(t, err)

	s := &rcServer{shell: shell, done: make(chan struct{})}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.mu.Lock()
			s.users = append(s.users, c.User())
			s.mu.Unlock()

			if !bytes.Equal(key.Marshal(), authorized.Marshal()) {
				return nil, errors.New("unknown key")
			}

			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s.addr = l.Addr().String()

	var (
		connMu sync.Mutex
		conns  []net.Conn
	)

	t.Cleanup(func() {
		close(s.done)
		_ = l.Close()

		connMu.Lock()
		defer connMu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}
	})

	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}

			connMu.Lock()
			conns = append(conns, nc)
			connMu.Unlock()

			go s.serve(nc, cfg)
		}
	}()

	return s
}

func (s *rcServer) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		_ = nc.Close()

		return
	}

	defer func() { _ = conn.Close() }()

	go ssh.DiscardRequests(reqs)

	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "session only")

			continue
		}

		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}

		go s.session(ch, creqs)
	}
}

func (s *rcServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for r := range reqs {
		switch r.Type {
		case "shell":
			_ = r.Reply(true, nil)

			go func() {
				status := s.shell(ch, ch, ch.Stderr())
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
				_ = ch.Close()
			}()
		case "pty-req":
			s.pty.Store(true)
			_ = r.Reply(false, nil)
		default:
			_ = r.Reply(false, nil)
		}
	}
}

func (s *rcServer) loggedUsers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.users...)
}

// rcIdentity is the zextras key of a test node: its PEM, as the file holds
// it, and the public key a server authorizes.
func rcIdentity(t *testing.T) ([]byte, ssh.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)

	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)

	return pem.EncodeToMemory(block), sshPub
}

// rcEnv is a node with the identity in .ssh whose zmrc reaches srv (nil: a
// port nothing listens on).
func rcEnv(t *testing.T, identity []byte, srv *rcServer) *Env {
	t.Helper()

	e, fakes, _ := testEnv(t)
	fakes.FS.Put(sshTestKeyPath, identity, 0o600)

	addr := "127.0.0.1:1"
	if srv != nil {
		addr = srv.addr
	}

	e.rc = &rcSeam{addr: func(string) string { return addr }, idle: rcTestIdle}

	return e
}

// rcRelease is a channel that is closed when the test ends: what a remote
// command that never ends waits for.
func rcRelease(t *testing.T) <-chan struct{} {
	t.Helper()

	c := make(chan struct{})

	t.Cleanup(func() { close(c) })

	return c
}

// rcEchoShell reads the first line of stdin, answers with it and returns 0:
// the line it was given is the protocol of zmrcd.
func rcEchoShell(got chan<- string) rcShell {
	return func(in io.Reader, out, _ io.Writer) int {
		line, _ := bufio.NewReader(in).ReadString('\n')
		got <- line

		_, _ = io.WriteString(out, "ran: "+line)

		return 0
	}
}

func TestRunRC_WithoutHostPrintsTheUsageAndExits1(t *testing.T) {
	for _, args := range [][]string{nil, {""}} {
		e, _, _ := testEnv(t)

		var out, errb bytes.Buffer

		status := RunRC(e, args, strings.NewReader("ignored"), &out, &errb)

		assert.Equal(t, 1, status, args)
		assert.Equal(t, "Usage: zmrc <hostname> [command words...]\n", out.String())
		assert.Empty(t, errb.String())
	}
}

func TestRunRC_SendsTheWordsAndRelaysTheOutput(t *testing.T) {
	identity, pub := rcIdentity(t)
	got := make(chan string, 1)
	srv := startRCServer(t, pub, rcEchoShell(got))
	e := rcEnv(t, identity, srv)

	var out, errb bytes.Buffer

	status := RunRC(e, []string{rcTestHost, "zmqstat", "all"}, strings.NewReader("not sent"), &out, &errb)

	assert.Equal(t, 0, status)
	assert.Equal(t, "zmqstat all\n", <-got, "the words, joined by a space, then a newline: echo @ARGV")
	assert.Equal(t, "ran: zmqstat all\n", out.String())
	assert.Empty(t, errb.String())
	assert.Equal(t, []string{"zextras"}, srv.loggedUsers(), "ssh zextras@host with the identity")
	assert.False(t, srv.pty.Load(), "ssh -T: no terminal")
}

func TestRunRC_WithoutWordsSendsTheCallersStdin(t *testing.T) {
	identity, pub := rcIdentity(t)
	got := make(chan string, 1)
	srv := startRCServer(t, pub, rcEchoShell(got))
	e := rcEnv(t, identity, srv)

	var out, errb bytes.Buffer

	status := RunRC(e, []string{rcTestHost}, strings.NewReader("HOST:"+rcTestHost+" zmupdateauthkeys\n"), &out, &errb)

	assert.Equal(t, 0, status)
	assert.Equal(t, "HOST:mta.example.com zmupdateauthkeys\n", <-got)
	assert.Equal(t, "ran: HOST:mta.example.com zmupdateauthkeys\n", out.String())
}

// The key of a node is the PKCS#1 "RSA PRIVATE KEY" the key generation writes.
func TestRunRC_AuthenticatesWithTheRSAKeyOfZmsshkeygen(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pub, err := ssh.NewPublicKey(&key.PublicKey)
	require.NoError(t, err)

	identity := pem.EncodeToMemory(&pem.Block{Type: sshPEMType, Bytes: x509.MarshalPKCS1PrivateKey(key)})
	got := make(chan string, 1)
	srv := startRCServer(t, pub, rcEchoShell(got))
	e := rcEnv(t, identity, srv)

	var out, errb bytes.Buffer

	RunRC(e, []string{rcTestHost, "status"}, nil, &out, &errb)

	assert.Equal(t, "ran: status\n", out.String(), errb.String())
}

// The script ignored the exit status of the remote command, and so does
// the client: what it printed is still relayed, stderr included.
func TestRunRC_RemoteExitStatusIsNotOursAndStderrIsRelayed(t *testing.T) {
	identity, pub := rcIdentity(t)
	srv := startRCServer(t, pub, func(_ io.Reader, out, errw io.Writer) int {
		_, _ = io.WriteString(out, "partial\nlast without newline")
		_, _ = io.WriteString(errw, "remote complaint\n")

		return 3
	})
	e := rcEnv(t, identity, srv)

	var out, errb bytes.Buffer

	status := RunRC(e, []string{rcTestHost, "status"}, nil, &out, &errb)

	assert.Equal(t, 0, status)
	assert.Equal(t, "partial\nlast without newline", out.String())
	assert.Equal(t, "remote complaint\n", errb.String())
}

func TestRunRC_ConnectionFailuresAreOnStderrWithStatus0(t *testing.T) {
	identity, pub := rcIdentity(t)
	_, otherPub := rcIdentity(t)
	known := startRCServer(t, pub, rcEchoShell(make(chan string, 1)))
	rejecting := startRCServer(t, otherPub, rcEchoShell(make(chan string, 1)))

	// A port that nothing listens on: the listener is closed before the call.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	closed := l.Addr().String()
	require.NoError(t, l.Close())

	for _, tc := range []struct {
		name string
		addr string
		want string
	}{
		{"nobody listens", closed, "zmrc: connecting to mta.example.com: dial tcp " + closed},
		{"the server does not know the key", rejecting.addr, "zmrc: connecting to mta.example.com: ssh: handshake failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := rcEnv(t, identity, known)
			e.rc.addr = func(string) string { return tc.addr }

			var out, errb bytes.Buffer

			status := RunRC(e, []string{rcTestHost, "status"}, nil, &out, &errb)

			assert.Equal(t, 0, status, "the script always exited 0")
			assert.Empty(t, out.String())
			assert.Contains(t, errb.String(), tc.want)
		})
	}
}

func TestRunRC_UnusableIdentityIsReported(t *testing.T) {
	_, pub := rcIdentity(t)
	srv := startRCServer(t, pub, rcEchoShell(make(chan string, 1)))

	t.Run("missing", func(t *testing.T) {
		e, _, _ := testEnv(t)
		e.rc = &rcSeam{addr: func(string) string { return srv.addr }}

		var errb bytes.Buffer

		assert.Equal(t, 0, RunRC(e, []string{rcTestHost, "status"}, nil, io.Discard, &errb))
		assert.Contains(t, errb.String(), "zmrc: reading the SSH key: ")
	})

	t.Run("not a key", func(t *testing.T) {
		e := rcEnv(t, []byte("garbage\n"), srv)

		var errb bytes.Buffer

		assert.Equal(t, 0, RunRC(e, []string{rcTestHost, "status"}, nil, io.Discard, &errb))
		assert.Contains(t, errb.String(), "zmrc: parsing the SSH key /opt/zextras/.ssh/zimbra_identity: ")
	})
}

// The legacy alarm(10) is armed after each line of output: a remote command
// that goes quiet after printing something is not waited for, and the client
// leaves with what it had (exit status 0).
func TestRunRC_StopsWaitingWhenTheOutputGoesIdle(t *testing.T) {
	identity, pub := rcIdentity(t)
	release := rcRelease(t)
	srv := startRCServer(t, pub, func(_ io.Reader, out, _ io.Writer) int {
		_, _ = io.WriteString(out, "first\nsecond\n")
		<-release // a command that never ends: the test ends first

		return 0
	})
	e := rcEnv(t, identity, srv)

	var out, errb bytes.Buffer

	start := time.Now()
	status := RunRC(e, []string{rcTestHost, "status"}, nil, &out, &errb)

	assert.Equal(t, 0, status)
	assert.GreaterOrEqual(t, time.Since(start), rcTestIdle, "it waited for the idle time first")
	assert.Equal(t, "first\nsecond\n", out.String())
	assert.Empty(t, errb.String())
}

// Output that keeps coming re-arms the alarm: only silence ends the wait.
func TestRunRC_EveryLineRestartsTheIdleTime(t *testing.T) {
	identity, pub := rcIdentity(t)
	srv := startRCServer(t, pub, func(_ io.Reader, out, _ io.Writer) int {
		for i := range 6 {
			_, _ = io.WriteString(out, strings.Repeat("x", i+1)+"\n")

			time.Sleep(rcTestIdle / 4)
		}

		return 0
	})
	e := rcEnv(t, identity, srv)

	var out bytes.Buffer

	start := time.Now()
	RunRC(e, []string{rcTestHost, "status"}, nil, &out, io.Discard)

	assert.Greater(t, time.Since(start), rcTestIdle, "longer than the idle time in all")
	assert.Equal(t, "x\nxx\nxxx\nxxxx\nxxxxx\nxxxxxx\n", out.String())
}

// Before the first line there is no alarm: a command that takes longer than
// the idle time to print anything is waited for.
func TestRunRC_NoAlarmBeforeTheFirstLine(t *testing.T) {
	identity, pub := rcIdentity(t)
	srv := startRCServer(t, pub, func(_ io.Reader, out, _ io.Writer) int {
		time.Sleep(3 * rcTestIdle)

		_, _ = io.WriteString(out, "late\n")

		return 0
	})
	e := rcEnv(t, identity, srv)

	var out bytes.Buffer

	RunRC(e, []string{rcTestHost, "status"}, nil, &out, io.Discard)

	assert.Equal(t, "late\n", out.String())
}

// ssh did not wait for its stdin once the remote command was over: a stdin
// that never ends (the caller's terminal) does not keep the client.
func TestRunRC_AStdinThatNeverEndsDoesNotKeepTheClient(t *testing.T) {
	identity, pub := rcIdentity(t)
	srv := startRCServer(t, pub, func(_ io.Reader, out, _ io.Writer) int {
		_, _ = io.WriteString(out, "done\n")

		return 0
	})
	e := rcEnv(t, identity, srv)

	pr, pw := io.Pipe()

	t.Cleanup(func() { _ = pw.Close() })

	var out bytes.Buffer

	finished := make(chan struct{})

	go func() {
		defer close(finished)

		RunRC(e, []string{rcTestHost}, pr, &out, io.Discard)
	}()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the client waited for a stdin that does not end")
	}

	assert.Equal(t, "done\n", out.String())
}

func TestRunRC_CancelledContextEndsTheSession(t *testing.T) {
	identity, pub := rcIdentity(t)
	started := make(chan struct{})
	release := rcRelease(t)
	srv := startRCServer(t, pub, func(_ io.Reader, _, _ io.Writer) int {
		close(started)
		<-release

		return 0
	})
	e := rcEnv(t, identity, srv)

	ctx, cancel := context.WithCancel(context.Background())
	e.Ctx = ctx

	go func() {
		<-started
		cancel()
	}()

	var errb bytes.Buffer

	status := RunRC(e, []string{rcTestHost, "status"}, nil, io.Discard, &errb)

	assert.Equal(t, 0, status)
	assert.Contains(t, errb.String(), "zmrc: context canceled")
}

// rcLines is a remote command that keeps the lines it was sent and answers
// each with "ok".
type rcLines struct {
	mu    sync.Mutex
	lines []string
}

func (r *rcLines) shell(in io.Reader, out, _ io.Writer) int {
	line, _ := bufio.NewReader(in).ReadString('\n')

	r.mu.Lock()
	r.lines = append(r.lines, line)
	r.mu.Unlock()

	_, _ = io.WriteString(out, "ok\n")

	return 0
}

func (r *rcLines) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.lines...)
}

// zmupdateauthkeys -a reaches every server through the SSH client of the
// process (not a libexec/zmrc), and logs what they print.
func TestSSHAuthKeysEverywhere_ReachesTheServersOverSSH(t *testing.T) {
	identity, pub := rcIdentity(t)
	remote := &rcLines{}
	srv := startRCServer(t, pub, remote.shell)

	e, fakes, logs := sshEnv(t)
	fakes.FS.Put(sshTestKeyPath, identity, 0o600)
	e.rc = &rcSeam{addr: func(string) string { return srv.addr }, idle: rcTestIdle}
	svcWithDir(t, &svcFakeDir{keys: map[string]string{"host-a.example.com": sshTestKeyA}})

	require.NoError(t, UpdateAuthKeysEverywhere(e))

	assert.Equal(t, []string{"HOST:host-a.example.com zmupdateauthkeys\n"}, remote.got())
	assert.Contains(t, logs.String(), "ok")
	assert.NotContains(t, logs.String(), "failed")
}

// deploycrt -allservers runs getcrt then deploycrt on each other server
// over SSH, and a server that cannot be reached is reported like before.
func TestRunCertMgr_DeploysToTheOtherServersOverSSH(t *testing.T) {
	identity, pub := rcIdentity(t)
	remote := &rcLines{}
	srv := startRCServer(t, pub, remote.shell)

	h := newCMTest(t, nil)
	h.dir.all = []string{cmHost, "a.example.com"}
	h.fakes.FS.Put(sshTestKeyPath, identity, 0o600)
	h.e.rc = &rcSeam{addr: func(string) string { return srv.addr }, idle: rcTestIdle}

	h.ok("createca", "-new")

	status, out, errs := h.run("deploycrt", "self", "-allservers")

	assert.Equal(t, 0, status, "stdout: %s\nstderr: %s", out, errs)
	assert.Equal(t, []string{
		"HOST:a.example.com zmcertmgr getcrt self -allservers\n",
		"HOST:a.example.com zmcertmgr deploycrt self\n",
	}, remote.got())

	h2 := newCMTest(t, nil)
	h2.dir.all = []string{cmHost, "a.example.com"}
	h2.fakes.FS.Put(sshTestKeyPath, identity, 0o600)
	h2.e.rc = &rcSeam{addr: func(string) string { return "127.0.0.1:1" }, idle: rcTestIdle}

	h2.ok("createca", "-new")

	status, out, _ = h2.run("deploycrt", "self", "-allservers")

	assert.Equal(t, 1, status)
	assert.Contains(t, out, "ERROR: zmrc: zmcertmgr getcrt self -allservers failed(1):\nconnecting to a.example.com: dial tcp 127.0.0.1:1")
}
