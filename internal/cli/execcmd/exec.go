// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package execcmd implements "configd exec": a launcher for systemd units that
// loads localconfig, exports the derived launch variables and execs a program
// with its arguments expanded against them.
package execcmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// Exit statuses of a failed launch, as a shell reports them.
const (
	exitLoad     = 1
	exitNotFound = 127
	exitNoExec   = 126
)

// Options is one "configd exec" run. The zero value of every hook selects the
// real one; tests inject them.
type Options struct {
	// Args is the program followed by its arguments, unexpanded.
	Args []string
	// Stderr receives the failure messages.
	Stderr io.Writer
	// Load returns the effective localconfig (default: localconfig.LoadEffective).
	Load func() (map[string]string, error)
	// Environ returns the inherited environment (default: os.Environ).
	Environ func() []string
	// LookPath resolves a program name (default: exec.LookPath).
	LookPath func(string) (string, error)
	// Exec replaces the process image (default: syscall.Exec). It only returns
	// on failure.
	Exec func(path string, argv, env []string) error
}

// Run launches the program. It only returns when the launch failed, with the
// exit status to use.
func Run(o *Options) int {
	if len(o.Args) == 0 {
		_, _ = fmt.Fprintln(o.Stderr, "Error: exec: no program given")

		return exitLoad
	}

	load := o.Load
	if load == nil {
		load = localconfig.LoadEffective
	}

	lc, err := load()
	if err != nil {
		_, _ = fmt.Fprintf(o.Stderr, "Error: exec: failed to load localconfig: %v\n", err)

		return exitLoad
	}

	vars := localconfig.LaunchEnv(lc)

	environ := os.Environ
	if o.Environ != nil {
		environ = o.Environ
	}

	lookPath := exec.LookPath
	if o.LookPath != nil {
		lookPath = o.LookPath
	}

	execFn := syscall.Exec
	if o.Exec != nil {
		execFn = o.Exec
	}

	prog := o.Args[0]
	if !strings.Contains(prog, "/") {
		if prog, err = lookPath(prog); err != nil {
			_, _ = fmt.Fprintf(o.Stderr, "Error: exec: %s: %v\n", o.Args[0], err)

			return statusFor(err)
		}
	}

	argv := append([]string{o.Args[0]}, ExpandArgs(o.Args[1:], vars)...)

	if err := execFn(prog, argv, mergeEnv(environ(), vars)); err != nil {
		_, _ = fmt.Fprintf(o.Stderr, "Error: exec: %s: %v\n", prog, err)

		return statusFor(err)
	}

	return 0
}

// statusFor maps an exec failure to 127 (program not found) or 126.
func statusFor(err error) int {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
		return exitNotFound
	}

	return exitNoExec
}

// mergeEnv returns environ with vars set, replacing inherited values of the
// same names.
func mergeEnv(environ []string, vars map[string]string) []string {
	env := make([]string, 0, len(environ)+len(vars))

	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); !hasKey(vars, name) {
			env = append(env, kv)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(vars)) {
		env = append(env, name+"="+vars[name])
	}

	return env
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]

	return ok
}

// ExpandArgs expands args with systemd ExecStart semantics over vars only:
// "${name}" is replaced in place; a word that is exactly "$name" is split on
// whitespace into separate arguments (dropped when empty); "$$" is a literal
// "$". Unknown names expand to nothing; any other "$" stays as it is.
func ExpandArgs(args []string, vars map[string]string) []string {
	out := make([]string, 0, len(args))

	for _, arg := range args {
		if name, ok := strings.CutPrefix(arg, "$"); ok && isName(name) {
			out = append(out, strings.Fields(vars[name])...)

			continue
		}

		out = append(out, expandWord(arg, vars))
	}

	return out
}

// expandWord expands "${name}" and "$$" inside one word.
func expandWord(word string, vars map[string]string) string {
	if !strings.Contains(word, "$") {
		return word
	}

	var sb strings.Builder

	for i := 0; i < len(word); i++ {
		if word[i] != '$' || i+1 >= len(word) {
			sb.WriteByte(word[i])

			continue
		}

		switch {
		case word[i+1] == '$':
			sb.WriteByte('$')

			i++
		case word[i+1] == '{':
			end := strings.IndexByte(word[i+2:], '}')
			if end >= 0 && isName(word[i+2:i+2+end]) {
				sb.WriteString(vars[word[i+2:i+2+end]])

				i += end + 2

				continue
			}

			sb.WriteByte('$')
		default:
			sb.WriteByte('$')
		}
	}

	return sb.String()
}

// isName reports whether s is a variable name: [A-Za-z_][A-Za-z0-9_]*.
func isName(s string) bool {
	if s == "" {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}

		return false
	}

	return true
}
