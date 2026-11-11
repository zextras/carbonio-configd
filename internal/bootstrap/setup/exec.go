// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"fmt"
	"io"
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
	cmd := &sys.Cmd{Path: c.Path, Args: c.Args, Stdin: c.Stdin, Env: c.Env}

	if c.AsZextras {
		env, err := e.zextrasEnv()
		if err != nil {
			return nil, err
		}

		cmd = &sys.Cmd{
			Path:  "runuser",
			Args:  append([]string{"-u", config.ZextrasUser, "--", "sh", "-c", zextrasShim, c.Path}, c.Args...),
			Stdin: c.Stdin,
			Env:   append(env, c.Env...),
		}
	}

	shown := strings.Join(redact.Args(append([]string{c.Path}, c.Args...)), " ")
	who := "root"

	if c.AsZextras {
		who = config.ZextrasUser
	}

	e.Log.Detail("*** Running as %s: %s", who, shown)

	var stdout, tail bytes.Buffer

	logOut := io.Writer(&tail)
	if !c.Quiet {
		logOut = io.MultiWriter(&tail, e.Log.Writer())
	}

	cmd.Stderr = logOut
	cmd.Stdout = logOut

	if capture {
		cmd.Stdout = &stdout
	}

	res, err := e.Sys.Exec.Run(e.Ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Path, err)
	}

	if res.ExitCode != 0 {
		return stdout.Bytes(), &CmdError{Cmd: shown, Code: res.ExitCode, Output: tail.String(), quiet: c.Quiet}
	}

	return stdout.Bytes(), nil
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

// zextrasEnv is the environment zextras.bashrc sets up for a login shell.
func (e *Env) zextrasEnv() ([]string, error) {
	if e.perlLib == "" {
		out, err := sys.Output(e.Ctx, e.Sys.Exec, "perl", "-V:archname")
		if err != nil {
			return nil, fmt.Errorf("perl -V:archname: %w", err)
		}

		arch := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(out)), "archname="), ";"), "'")
		lib := e.Paths.Path("common", "lib", "perl5")
		e.perlLib = lib + "/" + arch + ":" + lib
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
	}, nil
}
