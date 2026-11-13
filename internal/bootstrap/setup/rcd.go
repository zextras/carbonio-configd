// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	rcdUsage = "Usage: zmrcd (the commands are read from the standard input)\n"
	// rcdMaxLine is the longest line zmrcd reads: the longest legitimate one
	// is a certificate command with its subject alternative names.
	rcdMaxLine = 64 << 10
	rcdLogFile = "zmrcd.log"
	// The localconfig keys zmrcd reads.
	rcdHostKey    = "zimbra_server_hostname"
	rcdLoggingKey = "zimbra_zmrcd_logging_enabled"
	rcdServiceKey = "zimbraServiceEnabled"
	// rcdOriginalCommand is the variable sshd sets to what the client asked
	// to run (the forced command replaced it): rsync over ssh arrives here.
	rcdOriginalCommand = "SSH_ORIGINAL_COMMAND"
	// The type of a host specification and the name that stands for all of them.
	rcdHostSpec    = "HOST"
	rcdServiceSpec = "SERVICE"
	rcdAll         = "ALL"
	// rcdNoEntry is how the legacy script (perl's $!) told a program that is not there.
	rcdNoEntry = "No such file or directory"
)

// errRCDHostList is a host field that is not a list of HOST:<name> and SERVICE:<name>.
var errRCDHostList = errors.New("invalid hostlist")

// rcdSeam replaces parts of zmrcd for one Env (tests).
type rcdSeam struct {
	getenv func(string) string
	// execve replaces the process by the program (path found in PATH when it
	// has no slash) with argv, in the environment of the process.
	execve func(path string, argv []string) error
	// services lists the enabled services of every server of the directory.
	services func(e *Env) (map[string][]string, error)
}

// rcdSeamOf is the seam of the Env over the defaults of the real process.
func (e *Env) rcdSeamOf() rcdSeam {
	s := rcdSeam{getenv: os.Getenv, execve: rcdExecve, services: rcdServices}

	if e.rcd == nil {
		return s
	}

	if e.rcd.getenv != nil {
		s.getenv = e.rcd.getenv
	}

	if e.rcd.execve != nil {
		s.execve = e.rcd.execve
	}

	if e.rcd.services != nil {
		s.services = e.rcd.services
	}

	return s
}

// rcdExecve is the real execve of the rsync of an ssh session.
func rcdExecve(path string, argv []string) error {
	found, err := exec.LookPath(path)
	if err != nil {
		return err
	}

	//nolint:gosec // G204: the program is one of two fixed names (checked by rcdRsync), the arguments are argv.
	return syscall.Exec(found, argv, os.Environ())
}

// rcdServerLister is the part of the LDAP client that lists the servers.
type rcdServerLister interface {
	GetAllServersWithAttributes() (map[string]map[string]string, error)
}

// rcdServices is the directory side of the host lists: the servers and their
// zimbraServiceEnabled, like "zmprov -l gas" and "zmprov -l gs <host>".
func rcdServices(e *Env) (map[string][]string, error) {
	c, err := e.LDAP()
	if err != nil {
		return nil, err
	}

	defer func() { _ = c.Close() }()

	return rcdServiceMap(c)
}

func rcdServiceMap(l rcdServerLister) (map[string][]string, error) {
	all, err := l.GetAllServersWithAttributes()
	if err != nil {
		return nil, err
	}

	out := make(map[string][]string, len(all))

	for host, attrs := range all {
		for name, v := range attrs {
			if strings.EqualFold(name, rcdServiceKey) {
				out[host] = splitValues(v)
			}
		}
	}

	return out, nil
}

// rcdSession is one run of zmrcd.
type rcdSession struct {
	e    *Env
	seam rcdSeam
	// thisHost is zimbra_server_hostname: commands for any other host are
	// only announced ("Remote: ...").
	thisHost string
	errw     io.Writer
	// services is the lazily read list of the servers and their services.
	services map[string][]string
	loaded   bool

	mu  sync.Mutex // serialises out and log
	out io.Writer
	log io.WriteCloser // nil: logging is off
}

// RunRCD is zmrcd (carbonio-core src/libexec/rcd.pl), natively: the receiving
// side of zmrc, the forced command of the SSH key of the installation. It
// reads one command line from stdin ("<hosts> <command> [<args>]", hosts being
// HOST:<name> or SERVICE:<name> lists, ALL for every server) and runs the
// command, if the table of rcd_commands.go has it, on this server; for other
// servers it prints "Remote: ..." and does nothing. "?" lists the table. The
// line is untrusted: it never reaches a shell, only a command of the table
// is run, and its arguments are checked before they become argv.
// Like the script it exits 0 unless a command or its checks fail: the exit
// status of a failed command is the status of zmrcd.
func RunRCD(e *Env, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprint(stderr, rcdUsage)

		return 1
	}

	s, err := newRCDSession(e, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ERROR: %v\n", err)

		return 1
	}

	defer s.closeLog()

	if status, done := s.rsync(); done {
		return status
	}

	return s.serve(stdin)
}

func newRCDSession(e *Env, stdout, stderr io.Writer) (*rcdSession, error) {
	host, err := e.LCValue(rcdHostKey)
	if err != nil {
		return nil, fmt.Errorf("reading the local configuration: %w", err)
	}

	logging, err := e.LCValue(rcdLoggingKey)
	if err != nil {
		return nil, fmt.Errorf("reading the local configuration: %w", err)
	}

	s := &rcdSession{
		e: e, seam: e.rcdSeamOf(), out: stdout, errw: rcdSyncWriter(stderr),
		thisHost: strings.TrimRight(host, "\r\n"),
	}

	if strings.EqualFold(strings.TrimRight(logging, "\r\n"), "TRUE") {
		// Like the script, a log that cannot be opened is not an error.
		const flags = os.O_WRONLY | os.O_APPEND | os.O_CREATE

		if f, err := e.Sys.FS.OpenFile(e.Paths.Path("log", rcdLogFile), flags, 0o640); err == nil {
			s.log = f
		}
	}

	return s, nil
}

// rcdSyncWriter makes w safe for the several goroutines that copy the standard
// error of the stages of a pipeline; a file is left as it is, so the commands
// get the descriptor itself.
func rcdSyncWriter(w io.Writer) io.Writer {
	if _, ok := w.(*os.File); ok {
		return w
	}

	return &rcdLocked{w: w}
}

type rcdLocked struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *rcdLocked) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p)
}

func (s *rcdSession) closeLog() {
	if s.log != nil {
		_ = s.log.Close()
	}
}

// write sends p to the caller.
func (s *rcdSession) write(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = io.WriteString(s.out, p)
}

// logLine appends one line to the log file, if there is one.
func (s *rcdSession) logLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.log != nil {
		_, _ = io.WriteString(s.log, line+"\n")
	}
}

// msg is the script's logMsg: a line for the caller, and for the log.
func (s *rcdSession) msg(text string) {
	s.write(text + "\n")
	s.logLine(text)
}

// fail is the script's logError: "ERROR: text" on stderr, text in the log.
// It returns the exit status of the failure.
func (s *rcdSession) fail(text string) int {
	_, _ = fmt.Fprintf(s.errw, "ERROR: %s\n", text)

	s.logLine(text)

	return 1
}

// rsync handles the rsync of a client that comes over ssh: sshd puts its
// command line in SSH_ORIGINAL_COMMAND, and the forced command runs it
// instead of reading commands. done is true when the run is over (the
// process is replaced on success).
func (s *rcdSession) rsync() (status int, done bool) {
	cmd := s.seam.getenv(rcdOriginalCommand)

	prog := cmd
	if i := strings.IndexFunc(cmd, unicode.IsSpace); i >= 0 {
		prog = cmd[:i]
	}

	if !strings.HasSuffix(prog, rcdPlainRsync) {
		return 0, false
	}

	if prog != rcdPlainRsync && prog != rcdZextrasRsync {
		return s.fail("command '" + prog + "' not allowed"), true
	}

	words, ok := rcdWords(cmd)
	if !ok || !rcdCheckedCommands[rcdPlainRsync].re.MatchString(cmd) {
		return s.fail("invalid arguments in command [" + cmd + "]"), true
	}

	s.logLine("exec'ing: " + cmd)

	err := s.seam.execve(words[0], words)

	return s.fail("Can't run " + cmd + ": " + rcdErrText(err)), true
}

// serve reads the commands: help lines are answered and the next line is
// read; the first other line is the command, run and over.
func (s *rcdSession) serve(stdin io.Reader) int {
	br := bufio.NewReaderSize(stdin, rcdMaxLine)

	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return s.fail("line too long")
		}

		if len(line) == 0 {
			return 0 // end of input
		}

		host, command, args := rcdSplitLine(string(line))
		if host != "?" {
			return s.dispatch(host, command, args)
		}

		for _, h := range rcdHelpLines() {
			s.write(h)
		}
	}
}

// rcdField cuts the first blank-separated word off s and skips the blanks
// after it.
func rcdField(s string) (field, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}

	return s[:i], strings.TrimLeftFunc(s[i:], unicode.IsSpace)
}

// rcdSplitLine is perl's `split(' ', $_, 3)` and a chomp of the third field:
// leading blanks are skipped, the host and the command are blank-separated
// words and the arguments are the rest of the line, trailing blanks kept.
func rcdSplitLine(line string) (host, command, args string) {
	host, rest := rcdField(strings.TrimLeftFunc(line, unicode.IsSpace))
	command, rest = rcdField(rest)

	return host, command, strings.TrimSuffix(rest, "\n")
}

// dispatch runs the command on each host of the list.
func (s *rcdSession) dispatch(hosts, command, args string) int {
	list, err := s.hostList(hosts)
	if errors.Is(err, errRCDHostList) {
		return s.fail("Invalid hostlist")
	}

	if err != nil {
		return s.fail("listing the servers: " + err.Error())
	}

	for _, h := range list {
		if status := s.runOn(h, command, args); status != 0 {
			return status
		}
	}

	return 0
}

// rcdSplitSpecs is perl's split(sep, s): no fields for an empty string, no
// empty fields at the end.
func rcdSplitSpecs(s, sep string, limit int) []string {
	if s == "" {
		return nil
	}

	f := strings.SplitN(s, sep, limit)
	for len(f) > 0 && f[len(f)-1] == "" {
		f = f[:len(f)-1]
	}

	return f
}

// loadServices reads the servers and their services once.
func (s *rcdSession) loadServices() error {
	if s.loaded {
		return nil
	}

	m, err := s.seam.services(s.e)
	if err != nil {
		return err
	}

	s.services, s.loaded = m, true

	return nil
}

// allHosts is every server with at least one service, sorted.
func (s *rcdSession) allHosts() []string {
	var hosts []string

	for h, svc := range s.services {
		if len(svc) > 0 {
			hosts = append(hosts, h)
		}
	}

	slices.Sort(hosts)

	return hosts
}

// hostsOf are the servers that run service.
func (s *rcdSession) hostsOf(service string) []string {
	var hosts []string

	for h, svc := range s.services {
		if slices.Contains(svc, service) {
			hosts = append(hosts, h)
		}
	}

	return hosts
}

// rcdSpec splits KIND:item; either part is empty when missing.
func rcdSpec(spec string) (kind, item string) {
	parts := rcdSplitSpecs(spec, ":", 3)

	if len(parts) > 0 {
		kind = parts[0]
	}

	if len(parts) > 1 {
		item = parts[1]
	}

	return kind, item
}

// hostList resolves the host field: HOST:<name> and SERVICE:<name>
// specifications, comma separated, de-duplicated and sorted; a specification
// with ALL stands for every server and ends the list.
func (s *rcdSession) hostList(field string) ([]string, error) {
	hosts := map[string]struct{}{}

	for _, spec := range rcdSplitSpecs(field, ",", -1) {
		kind, item := rcdSpec(spec)

		if kind != rcdHostSpec && kind != rcdServiceSpec {
			return nil, errRCDHostList
		}

		if kind == rcdHostSpec && item != rcdAll {
			hosts[item] = struct{}{}

			continue
		}

		if err := s.loadServices(); err != nil {
			return nil, err
		}

		if item == rcdAll {
			return s.allHosts(), nil
		}

		for _, h := range s.hostsOf(item) {
			hosts[h] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(hosts)), nil
}

// runOn runs the command on one host and returns the status that ends zmrcd
// (0: go on with the next host).
func (s *rcdSession) runOn(host, command, args string) int {
	if !strings.EqualFold(host, s.thisHost) {
		s.msg("Remote: HOST:" + host + " " + command + " " + args)

		return 0
	}

	line := command
	if args != "" {
		line = command + " " + args
	}

	if c, ok := rcdSimpleCommands[line]; ok {
		return s.run(host, c.run, rcdStages(c.run), rcdOutput{quiet: c.quiet, secret: c.secret})
	}

	c, ok := rcdCheckedCommands[command]
	if !ok {
		return s.fail(`Unknown command: "` + command + `"`)
	}

	program, _ := rcdWords(c.program)
	words, valid := rcdWords(args)

	if !valid || !c.re.MatchString(args) {
		return s.fail("args '" + args + "' not allowed for command '" + command + "'")
	}

	return s.run(host, c.program+" "+args, [][]string{append(program, words...)}, rcdOutput{quiet: c.quiet})
}

// rcdOutput says how the output of a command is relayed.
type rcdOutput struct {
	quiet  bool // no STARTCMD and ENDCMD lines
	secret bool // not in the log file
}

// run runs the stages of a command (a pipeline, usually one stage) and relays
// its output. shown is the command as STARTCMD and ENDCMD print it.
func (s *rcdSession) run(host, shown string, stages [][]string, o rcdOutput) int {
	r := &rcdRelay{s: s, o: o}

	if !o.quiet {
		r.start, r.end = "STARTCMD: "+host+" "+shown, "ENDCMD: "+host+" "+shown
	}

	status, err := s.pipeline(stages, r)
	if err != nil {
		return s.fail("Can't run " + shown + ": " + rcdErrText(err))
	}

	r.finish()

	return status
}

// rcdRelay copies the output of a command to the caller, a line at a time
// like the script did (so a last line without a newline gets one), between
// the STARTCMD and the ENDCMD lines. STARTCMD is printed once the command
// has started, which is when its first output comes or it is over.
type rcdRelay struct {
	s          *rcdSession
	o          rcdOutput
	start, end string

	mu      sync.Mutex
	begun   bool
	wrote   bool
	last    byte
	partial []byte
}

func (r *rcdRelay) begin() {
	if r.begun {
		return
	}

	r.begun = true

	if r.start != "" {
		r.s.msg(r.start)
	}
}

func (r *rcdRelay) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.begin()
	r.s.write(string(p))

	r.wrote, r.last = true, p[len(p)-1]

	if r.s.log != nil && !r.o.secret {
		r.partial = append(r.partial, p...)

		for {
			i := slices.Index(r.partial, '\n')
			if i < 0 {
				break
			}

			r.s.logLine(string(r.partial[:i]))
			r.partial = r.partial[i+1:]
		}
	}

	return len(p), nil
}

// finish ends the output: the last line gets its newline, then ENDCMD.
func (r *rcdRelay) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.begin()

	if r.wrote && r.last != '\n' {
		r.s.write("\n")
	}

	if len(r.partial) > 0 {
		r.s.logLine(string(r.partial))
	}

	if r.end != "" {
		r.s.msg(r.end)
	}
}

// pipeline runs the stages, each one's output the next one's input, and
// returns the exit status of the last, like a shell pipeline. An error is a
// stage that could not be started.
func (s *rcdSession) pipeline(stages [][]string, out io.Writer) (int, error) {
	var (
		wg      sync.WaitGroup
		stdin   io.Reader
		results = make([]rcdStage, len(stages))
	)

	for i, argv := range stages {
		stdout := out
		next := stdin

		var pw *io.PipeWriter

		if i < len(stages)-1 {
			var pr *io.PipeReader

			pr, pw = io.Pipe()
			stdout, stdin = pw, pr
		}

		wg.Go(func() { results[i] = s.stage(argv, next, stdout, pw) })
	}

	wg.Wait()

	for _, r := range results[:len(results)-1] {
		if r.startFailed() {
			return 0, r.err
		}
	}

	last := results[len(results)-1]
	if last.err != nil {
		return 0, last.err
	}

	return last.status, nil
}

// rcdStage is the outcome of one stage of a pipeline.
type rcdStage struct {
	status int
	err    error
}

// startFailed reports a stage that never ran, as opposed to one that ended
// before its reader (a closed pipe) or with an exit status.
func (r rcdStage) startFailed() bool { return r.err != nil && !errors.Is(r.err, io.ErrClosedPipe) }

// stage runs one command with stdin as its input (nil: /dev/null, the
// legacy script left the session's input to the child; no command reads
// it). The pipe ends of the stage are closed when it is over, so its
// neighbours see end of input or a closed pipe.
func (s *rcdSession) stage(argv []string, stdin io.Reader, stdout io.Writer, pw *io.PipeWriter) rcdStage {
	cmd := &sys.Cmd{Path: argv[0], Args: argv[1:], Stdin: stdin, Stdout: stdout, Stderr: s.errw}
	res, err := s.e.Sys.Exec.Run(s.e.Ctx, cmd)

	if pw != nil {
		_ = pw.Close()
	}

	if pr, ok := stdin.(*io.PipeReader); ok {
		_ = pr.Close()
	}

	if err == nil {
		return rcdStage{status: res.ExitCode}
	}

	if xe, ok := errors.AsType[*sys.ExitError](err); ok {
		return rcdStage{status: rcdExitCode(xe)}
	}

	return rcdStage{err: err}
}

// rcdExitCode is the status zmrcd exits with for a command that ended with
// xe: its exit status, or 128 plus the signal that killed it (the script
// exited 0 for a command that was killed).
func rcdExitCode(xe *sys.ExitError) int {
	if xe.Code >= 0 {
		return xe.Code
	}

	if ee, ok := errors.AsType[*exec.ExitError](xe); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}

	return 1
}

// rcdErrText is the text of perl's $! for a command that did not start.
func rcdErrText(err error) string {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return rcdNoEntry
	}

	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		text := errno.Error()

		return strings.ToUpper(text[:1]) + text[1:]
	}

	return err.Error()
}
