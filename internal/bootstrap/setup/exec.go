// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Cmd is one command of a step.
type Cmd struct {
	// AsZextras runs the command as the zextras user, with the environment
	// of its login shell (setup.pl "su - zextras -c").
	AsZextras bool
	Path      string
	Args      []string
	// Stdin feeds the command; secrets go here, never in Args.
	Stdin io.Reader
	// Env adds NAME=value pairs to the environment.
	Env []string
	// Quiet keeps the output out of the log file (it may hold secrets).
	Quiet bool
	// Brief keeps the output out of the log file unless the command fails
	// or the run is verbose: for tools that narrate every file they touch.
	Brief bool
}

// zextrasUmask is the umask of the zextras login shell (zextras.bashrc).
const zextrasUmask = "027"

// zextrasShim runs argv as given (no word splitting, no interpolation) after
// setting the umask: `sh -c 'umask 027; exec "$0" "$@"' argv...`.
const zextrasShim = `umask ` + zextrasUmask + `; exec "$0" "$@"`

// Run runs c, copying its output to the log file, and fails when it exits
// non-zero; the error carries the last lines of output.
func (e *Env) Run(c *Cmd) error {
	_, err := e.output(c, false)

	return err
}

// Output runs c and returns its standard output; standard error goes to the
// log file.
func (e *Env) Output(c *Cmd) ([]byte, error) {
	return e.output(c, true)
}

// Zextras runs name args... as zextras (see Run).
func (e *Env) Zextras(name string, args ...string) error {
	return e.Run(&Cmd{AsZextras: true, Path: name, Args: args})
}

// Root runs name args... as root (see Run).
func (e *Env) Root(name string, args ...string) error {
	return e.Run(&Cmd{Path: name, Args: args})
}

func (e *Env) output(c *Cmd, capture bool) ([]byte, error) {
	// Setsid: no step command gets the controlling terminal. A child that
	// opens /dev/tty (zmprov's JLine runs stty on it) would otherwise be
	// stopped by SIGTTOU from its background process group, hanging the run
	// whenever bootstrap is started from a terminal.
	cmd := e.sysCmd(c)

	shown := shownArgs(append([]string{c.Path}, c.Args...))
	who := "root"

	if c.AsZextras || e.IsZextras {
		who = config.ZextrasUser
	}

	e.Log.Detail("*** Running as %s: %s", who, shown)

	var stdout, tail bytes.Buffer

	brief := c.Brief && !e.Verbose
	logOut, lw := e.cmdLog(&tail, c.Quiet || brief)

	defer lw.Flush()

	cmd.Stderr = logOut
	cmd.Stdout = logOut

	if capture {
		cmd.Stdout = &stdout
	}

	res, err := e.Sys.Exec.Run(e.Ctx, cmd)

	// The real Exec reports a non-zero exit as *sys.ExitError next to the
	// Result: that is a command that ran and said no (a CmdError, which
	// callers inspect), not a failure to run it.
	if exitErr, ok := errors.AsType[*sys.ExitError](err); ok {
		err = nil

		if res.ExitCode == 0 {
			res.ExitCode = exitErr.Code
		}
	}

	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Path, err)
	}

	if res.ExitCode != 0 {
		if brief && !c.Quiet {
			e.Log.Detail("%s", strings.TrimRight(tail.String(), "\n"))
		}

		return stdout.Bytes(), &CmdError{Cmd: shown, Code: res.ExitCode, Output: tail.String(), quiet: c.Quiet}
	}

	return stdout.Bytes(), nil
}

// sysCmd is the process that runs c. Setsid: no step command gets the
// controlling terminal. An AsZextras command goes through the environment
// shim, under runuser unless the process already runs as zextras (runuser
// needs root).
func (e *Env) sysCmd(c *Cmd) *sys.Cmd {
	if !c.AsZextras {
		return &sys.Cmd{Path: c.Path, Args: c.Args, Stdin: c.Stdin, Env: c.Env, Setsid: true}
	}

	env := e.zextrasEnv()

	shim := append([]string{"-c", zextrasShim, c.Path}, c.Args...)
	cmd := &sys.Cmd{
		Path:   "runuser",
		Args:   append([]string{"-u", config.ZextrasUser, "--", "sh"}, shim...),
		Stdin:  c.Stdin,
		Env:    append(env, c.Env...),
		Setsid: true,
	}

	if e.IsZextras {
		cmd.Path, cmd.Args = "sh", shim
	}

	return cmd
}

// cmdLog is where a command's output goes: always tail (for the error), and
// the log file a line at a time unless hidden (then lw is nil). lw.Flush
// ends the last line.
func (e *Env) cmdLog(tail io.Writer, hidden bool) (out io.Writer, lw *LineWriter) {
	if hidden {
		return tail, nil
	}

	lw = e.Log.Writer()

	return io.MultiWriter(tail, lw), lw
}

// shownArgs is argv as logged: secrets masked, and an argument with spaces
// or control characters quoted so the command stays on one line.
func shownArgs(argv []string) string {
	out := redact.Args(argv)

	for i, a := range out {
		if a == "" || strings.ContainsAny(a, " \t\n\r\"'") {
			out[i] = strconv.Quote(a)
		}
	}

	return strings.Join(out, " ")
}

// CmdError is a command that exited non-zero.
type CmdError struct {
	// Cmd is the command line, secrets masked.
	Cmd  string
	Code int
	// Output is what the command printed on stderr (and stdout, unless it
	// was captured).
	Output string
	quiet  bool
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("%s exited with status %d%s", e.Cmd, e.Code, lastLines(e.Output, e.quiet))
}

// lastLines returns ": <the last 3 lines>" of out, or "" when out is empty or
// quiet.
func lastLines(out string, quiet bool) string {
	out = strings.TrimSpace(out)
	if out == "" || quiet {
		return ""
	}

	lines := strings.Split(out, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	masked, _ := redact.Text(strings.Join(lines, "; "))

	return ": " + masked
}

// perlGoArch maps GOARCH to the leading word of Perl's archname.
var perlGoArch = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}

// perlLib is the PERL5LIB zextras.bashrc builds with `perl -V:archname`:
// "<lib>/<archname>:<lib>". Perl is not run; the archname directory is the
// one under lib named like an archname (arch-os[-flags], e.g.
// x86_64-linux-thread-multi). When several match, the one for goarch wins,
// then the first by name; when none exists, the value is just lib.
func perlLib(fsys sys.FS, lib, goarch string) string {
	entries, err := fsys.ReadDir(lib)
	if err != nil {
		return lib
	}

	best := ""

	for _, ent := range entries {
		name := ent.Name()
		if !ent.IsDir() || !strings.Contains(name, "-linux") {
			continue
		}

		prefer := strings.HasPrefix(name, perlGoArch[goarch]+"-")
		bestPrefer := strings.HasPrefix(best, perlGoArch[goarch]+"-")

		if best == "" || (prefer && !bestPrefer) || (prefer == bestPrefer && name < best) {
			best = name
		}
	}

	if best == "" {
		return lib
	}

	return lib + "/" + best + ":" + lib
}

// zextrasEnv is the environment zextras.bashrc sets up for a login shell.
func (e *Env) zextrasEnv() []string {
	if e.perlLib == "" {
		e.perlLib = perlLib(e.Sys.FS, e.Paths.Path("common", "lib", "perl5"), runtime.GOARCH)
	}

	javaHome := e.Paths.Path("common", "lib", "jvm", "java")
	path := strings.Join([]string{
		e.Paths.Path("bin"), javaHome + "/bin", e.Paths.Path("common", "bin"), e.Paths.Path("common", "sbin"),
		"/usr/sbin", "/usr/bin", "/sbin", "/bin",
	}, ":")

	return []string{
		"HOME=" + e.Paths.Base,
		"USER=" + config.ZextrasUser,
		"LOGNAME=" + config.ZextrasUser,
		"USERNAME=" + config.ZextrasUser,
		"SHELL=/bin/bash",
		"PATH=" + path,
		"JAVA_HOME=" + javaHome,
		"PERLLIB=" + e.perlLib,
		"PERL5LIB=" + e.perlLib,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"CLASSPATH=",
		"LD_LIBRARY_PATH=",
	}
}
