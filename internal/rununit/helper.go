// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rununit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// spec is the file a launch leaves for the unit's ExecStart: the one thing the
// unit runs. It is written by the Runner, 0600, in a 0700 directory, both
// owned by the user the unit runs as.
type spec struct {
	// Path is the absolute program and Args its arguments after argv[0].
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
	// Env are NAME=value pairs added to the unit's environment; only the
	// names of allowedEnv are accepted.
	Env []string `json:"env,omitempty"`
	// Dir is the working directory ("" keeps the unit's).
	Dir string `json:"dir,omitempty"`
	// Stdout is a file appended to with the program's standard output and,
	// unless Stderr is set, its standard error. Unset, both stay the unit's
	// (the journal).
	Stdout string `json:"stdout,omitempty"`
	// Stderr is a file appended to with the program's standard error.
	Stderr string `json:"stderr,omitempty"`
	// Result, for a Type=forking launch, is the file receiving the program's
	// exit status once it ended (see result).
	Result string `json:"result,omitempty"`
}

// validate rejects a spec the helper must not run.
func (s *spec) validate(dir string) error {
	if !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != s.Path {
		return fmt.Errorf("program %q is not a clean absolute path", s.Path)
	}

	for _, kv := range s.Env {
		name, _, found := strings.Cut(kv, "=")
		if _, ok := allowedEnv[name]; !found || !ok {
			return fmt.Errorf("environment variable %q is not allowed", name)
		}
	}

	for _, p := range []string{s.Dir, s.Stdout, s.Stderr, s.Result} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return fmt.Errorf("path %q is not a clean absolute path", p)
		}
	}

	if s.Result != "" && filepath.Dir(s.Result) != filepath.Clean(dir) {
		return fmt.Errorf("result file %q is outside %s", s.Result, dir)
	}

	return nil
}

// specFile is the spec of kind/instance in dir.
func specFile(dir string, kind Kind, instance string) string {
	return filepath.Join(dir, string(kind)+"-"+instance+".json")
}

// loadSpec reads and validates the spec of a launch. The directory and the
// file must belong to the running user and be closed to everybody else, so
// nobody who could not already run a command as this user can make the unit
// run one.
func loadSpec(dir string, kind Kind, instance string) (*spec, error) {
	if !instanceRe.MatchString(instance) {
		return nil, fmt.Errorf("invalid unit instance %q", instance)
	}

	uid := uint32(os.Getuid()) //nolint:gosec // a uid is never negative

	if err := checkOwned(dir, uid, 0o077); err != nil {
		return nil, err
	}

	path := specFile(dir, kind, instance)

	if err := checkOwned(path, uid, 0o177); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is the checked spec file of this instance
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	var s spec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	if err := s.validate(dir); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &s, nil
}

// checkOwned fails unless path (not followed if a link) belongs to uid and has
// no permission bit of forbidden set.
func checkOwned(path string, uid uint32, forbidden os.FileMode) error {
	st, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != uid {
		return fmt.Errorf("%s is not owned by uid %d", path, uid)
	}

	if st.Mode()&forbidden != 0 || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s has mode %v", path, st.Mode())
	}

	return nil
}

// openLog opens a log file the way the daemon opened it: appended, created
// 0640.
func openLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec // validated absolute path
}

// command builds the process of a spec with its standard streams. The returned
// closers must be closed after the process started.
func (s *spec) command() (*exec.Cmd, []*os.File, error) {
	//nolint:gosec // G204: the program is whatever a launch of this user's daemon named
	cmd := exec.CommandContext(context.Background(), s.Path, s.Args...)
	cmd.Dir = s.Dir
	cmd.Env = mergeEnv(os.Environ(), s.Env)

	var files []*os.File

	if s.Stdout != "" {
		out, err := openLog(s.Stdout)
		if err != nil {
			return nil, nil, fmt.Errorf("open %s: %w", s.Stdout, err)
		}

		files = append(files, out)
		cmd.Stdout, cmd.Stderr = out, out
	}

	if s.Stderr != "" {
		errFile, err := openLog(s.Stderr)
		if err != nil {
			closeAll(files)

			return nil, nil, fmt.Errorf("open %s: %w", s.Stderr, err)
		}

		files = append(files, errFile)
		cmd.Stderr = errFile
	}

	return cmd, files, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func mergeEnv(base, extra []string) []string {
	names := make(map[string]struct{}, len(extra))

	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		names[name] = struct{}{}
	}

	env := make([]string, 0, len(base)+len(extra))

	for _, kv := range base {
		if name, _, _ := strings.Cut(kv, "="); !hasName(names, name) {
			env = append(env, kv)
		}
	}

	return append(env, extra...)
}

func hasName(names map[string]struct{}, name string) bool {
	_, ok := names[name]

	return ok
}

// Main is `configd internal run-unit <kind> <instance>`, the ExecStart of the
// template units. For KindExec it replaces itself with the program, so the
// program is the unit's main process. For KindForking it runs the program to
// completion, records its exit status for Run and exits 0: what the program
// left running stays in the unit's cgroup, which keeps the unit active.
func Main(dir string, kind Kind, instance string) error {
	s, err := loadSpec(dir, kind, instance)
	if err != nil {
		return err
	}

	if kind == KindExec {
		// "systemctl start" of a Type=exec unit returns once this process has
		// been exec'd, not once it has read the spec, so the caller cannot
		// remove the file: the reader does, now that it holds the content.
		_ = os.Remove(specFile(dir, kind, instance))

		return s.execReplace()
	}

	return s.runAndRecord()
}

// execReplace execs the program in place of the helper.
func (s *spec) execReplace() error {
	if s.Dir != "" {
		if err := os.Chdir(s.Dir); err != nil {
			return fmt.Errorf("chdir %s: %w", s.Dir, err)
		}
	}

	if err := s.redirect(); err != nil {
		return err
	}

	//nolint:gosec // G204: the program is whatever a launch of this user's daemon named
	return syscall.Exec(s.Path, append([]string{s.Path}, s.Args...), mergeEnv(os.Environ(), s.Env))
}

// redirect points the process's stdout (and stderr, to the same file unless the
// spec names another) at the log files of the spec.
func (s *spec) redirect() error {
	if s.Stdout == "" {
		return nil
	}

	out, err := openLog(s.Stdout)
	if err != nil {
		return fmt.Errorf("open %s: %w", s.Stdout, err)
	}

	if err = syscall.Dup2(int(out.Fd()), syscall.Stdout); err != nil { //nolint:gosec // fd fits an int
		return fmt.Errorf("redirect stdout: %w", err)
	}

	stderr := out

	if s.Stderr != "" {
		if stderr, err = openLog(s.Stderr); err != nil {
			return fmt.Errorf("open %s: %w", s.Stderr, err)
		}
	}

	if err = syscall.Dup2(int(stderr.Fd()), syscall.Stderr); err != nil { //nolint:gosec // fd fits an int
		return fmt.Errorf("redirect stderr: %w", err)
	}

	return nil
}

// runAndRecord runs the program, waits for it and writes its result file.
func (s *spec) runAndRecord() error {
	cmd, files, err := s.command()
	if err != nil {
		return s.record(result{Code: -1, Error: err.Error()})
	}

	defer closeAll(files)

	runErr := cmd.Run()

	var exit *exec.ExitError

	switch {
	case runErr == nil:
		return s.record(result{})
	case errors.As(runErr, &exit):
		return s.record(result{Code: exit.ExitCode()})
	default:
		return s.record(result{Code: -1, Error: runErr.Error()})
	}
}

func (s *spec) record(r result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}

	if err := os.WriteFile(s.Result, data, 0o600); err != nil {
		return fmt.Errorf("write result: %w", err)
	}

	return nil
}
