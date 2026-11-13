// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/zextras/carbonio-configd/internal/config"
)

const (
	rcPort = "22"
	// rcIdle is the legacy alarm(10) of rc.pl: the time without a line of
	// output after which the client gives up waiting for the remote command.
	rcIdle = 10 * time.Second
	// rcDialTimeout bounds the TCP connection and the SSH handshake (ssh had
	// no bound of its own but the kernel's).
	rcDialTimeout = 30 * time.Second
	rcUsage       = "Usage: zmrc <hostname> [command words...]\n"
)

// rcSeam replaces parts of the zmrc client for one Env (tests).
type rcSeam struct {
	// run replaces the whole client: what the callers of zmrc send.
	run func(host string, in io.Reader, out, errw io.Writer) error
	// addr is the host:port to connect to for a host.
	addr func(host string) string
	// idle is the time without output after which the client stops waiting.
	idle time.Duration
}

// RunRC is zmrc (carbonio-core src/libexec/rc.pl), natively: args are
// "<host> [command words...]". It connects to host as zextras with
// .ssh/zimbra_identity (the host's authorized_keys forces zmrcd, which reads
// the commands from stdin), sends the words joined by a space and a newline,
// or, without words, the caller's stdin, and copies what zmrcd prints to
// stdout. Like the script it exits 1 only when there is no host: a
// connection that fails or an exit status of the remote command are not
// exit statuses (the script's `close` ignored them); the reason of a
// connection that fails is printed on stderr.
func RunRC(e *Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "" {
		_, _ = fmt.Fprint(stdout, rcUsage)

		return 1
	}

	in := stdin
	if len(args) > 1 {
		in = strings.NewReader(strings.Join(args[1:], " ") + "\n")
	}

	if err := e.rcRun(args[0], in, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "zmrc: %v\n", err)
	}

	return 0
}

// rcLine is the line zmrcd reads for a command to run on host.
func rcLine(host, command string) string { return "HOST:" + host + " " + command + "\n" }

// rcRun sends in to the zmrcd of host and relays its output to out (the
// standard error of the session goes to errw). The error is a failure to
// reach the remote command: the key, the connection, the authentication, the
// session. The exit status of the remote command is ignored, as the script
// did.
func (e *Env) rcRun(host string, in io.Reader, out, errw io.Writer) error {
	if e.rc != nil && e.rc.run != nil {
		return e.rc.run(host, in, out, errw)
	}

	signer, err := e.rcSigner()
	if err != nil {
		return err
	}

	ctx := e.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	client, err := e.rcConnect(ctx, host, signer)
	if err != nil {
		return err
	}

	defer func() { _ = client.Close() }()

	return e.rcSession(ctx, client, in, out, errw)
}

// rcSigner reads the identity of the node.
func (e *Env) rcSigner() (ssh.Signer, error) {
	path := e.Paths.Path(".ssh", sshKeyFile)

	pemKey, err := e.Sys.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(pemKey)
	if err != nil {
		return nil, fmt.Errorf("parsing the SSH key %s: %w", path, err)
	}

	return signer, nil
}

// rcConnect opens the SSH connection to host. The host key is not checked,
// like the script's StrictHostKeyChecking=no: the servers of a Carbonio
// installation are known to each other only through this key, the one the
// remote authorized_keys also restricts to zmrcd.
func (e *Env) rcConnect(ctx context.Context, host string, signer ssh.Signer) (*ssh.Client, error) {
	addr := net.JoinHostPort(host, rcPort)
	if e.rc != nil && e.rc.addr != nil {
		addr = e.rc.addr(host)
	}

	cfg := &ssh.ClientConfig{
		User: config.ZextrasUser,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		//nolint:gosec // G106: StrictHostKeyChecking=no of the legacy zmrc, see above.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	dialer := net.Dialer{Timeout: rcDialTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", host, err)
	}

	// The handshake is bounded too, then the connection is free.
	_ = conn.SetDeadline(time.Now().Add(rcDialTimeout))

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("connecting to %s: %w", host, err)
	}

	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(c, chans, reqs), nil
}

// rcSessionError is the error of a session that could not be opened.
func rcSessionError(err error) error { return fmt.Errorf("opening the session: %w", err) }

// rcSession runs the default command of the connection (ssh -T: no
// terminal, no command: a shell request, which the forced command of
// authorized_keys turns into zmrcd), feeds it in and relays its output.
func (e *Env) rcSession(ctx context.Context, client *ssh.Client, in io.Reader, out, errw io.Writer) error {
	session, err := client.NewSession()
	if err != nil {
		return rcSessionError(err)
	}

	defer func() { _ = session.Close() }()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return rcSessionError(err)
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return rcSessionError(err)
	}

	// The session copies its stderr on its own goroutine, which may outlive
	// this call (the alarm, a cancelled context): its sink closes with it.
	stderr := &rcSink{w: errw}
	defer stderr.close()

	session.Stderr = stderr

	if err := session.Shell(); err != nil {
		return fmt.Errorf("starting the remote command: %w", err)
	}

	// Not waited for, like ssh: a stdin that never ends (a terminal) must not
	// keep the client once the remote command is done.
	go func() {
		_, _ = io.Copy(stdinPipe, in)
		_ = stdinPipe.Close()
	}()

	idle := rcIdle
	if e.rc != nil && e.rc.idle > 0 {
		idle = e.rc.idle
	}

	relay := &rcRelay{out: out, idle: idle, fired: make(chan struct{})}
	defer relay.stop()

	read := make(chan struct{})

	go func() {
		defer close(read)

		relay.copyLines(stdoutPipe)
	}()

	waited := make(chan error, 1)

	go func() { waited <- session.Wait() }()

	select {
	case <-relay.fired:
		return nil // the legacy ALRM handler: exit 0
	case <-ctx.Done():
		return ctx.Err()
	case err := <-waited:
		// What the command printed last is still on its way to out.
		select {
		case <-read:
		case <-relay.fired:
		}

		return rcWaitError(err)
	}
}

// rcWaitError is the error of the end of a session: the exit status of the
// remote command (or its absence) is not one.
func rcWaitError(err error) error {
	var exit *ssh.ExitError

	var missing *ssh.ExitMissingError

	if err == nil || errors.As(err, &exit) || errors.As(err, &missing) {
		return nil
	}

	return fmt.Errorf("the connection failed: %w", err)
}

// rcRelay copies the output of the remote command a line at a time and, like
// rc.pl (`while (<P>) { print; alarm(10) }`), gives up waiting once idle has
// passed since the last line: the alarm is armed after a line, not before
// the first one.
type rcRelay struct {
	mu      sync.Mutex
	out     io.Writer
	idle    time.Duration
	timer   *time.Timer
	fired   chan struct{}
	stopped bool
}

// copyLines relays r to the output until it ends.
func (r *rcRelay) copyLines(src io.Reader) {
	br := bufio.NewReader(src)

	for {
		line, err := br.ReadString('\n')
		if line != "" {
			r.line(line)
		}

		if err != nil {
			return
		}
	}
}

// line prints one line and re-arms the alarm, unless it has fired.
func (r *rcRelay) line(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return
	}

	_, _ = io.WriteString(r.out, s)

	if r.timer == nil {
		r.timer = time.AfterFunc(r.idle, r.alarm)

		return
	}

	r.timer.Reset(r.idle)
}

// alarm is the timer: from now on nothing is printed and the session ends.
func (r *rcRelay) alarm() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return
	}

	r.stopped = true

	close(r.fired)
}

// stop disarms the alarm and the output: the session is over.
func (r *rcRelay) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stopped = true

	if r.timer != nil {
		r.timer.Stop()
	}
}

// rcSink is a writer that stops writing once it is closed, so a goroutine of
// the SSH session that is still copying cannot touch the caller's writer
// after the call returned.
type rcSink struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (s *rcSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return len(p), nil
	}

	return s.w.Write(p)
}

func (s *rcSink) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
