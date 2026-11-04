// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package rununit launches the long-running processes of Carbonio services
// (nginx, mailboxd, clamd, postfix, ...) as systemd units of their own, so
// that they are not children, and not in the cgroup, of the process that
// started them: the configd daemon (carbonio-configd.service) or an admin's
// login session.
//
// The units are two static templates shipped with the package, never
// transient units:
//
//	carbonio-run-exec@<service>.service     Type=exec     foreground daemons
//	carbonio-run-forking@<service>.service  Type=forking  self-daemonizing
//	                                                      launchers/scripts
//
// both with User=zextras and Slice=carbonio.slice. Their ExecStart is
// `configd internal run-unit <kind> <service>` (see Main), which reads what to
// run from a spec file the caller wrote beforehand and runs it. A transient
// unit would let whoever may create units pick User=root and any command
// (polkit can only match the unit name and the verb, not the properties), so
// the command and user of a unit are not hers to choose: the polkit rule
// grants start/stop/reset-failed on the template instances only, and those
// can only ever run, as zextras, what a zextras-owned 0600 spec in a
// zextras-owned 0700 directory names.
//
// Everything is optional: without systemd as init, as root (the templates run
// as zextras), without the templates, or when polkit denies the request, the
// Runner logs one warning and runs the command as a plain child, which is how
// the daemon always behaved.
package rununit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	// DefaultDir holds the spec, result and output files of the launches. It
	// is created 0700 and must be owned by the user the units run as.
	DefaultDir = "/run/carbonio/units"

	// ExecTemplate is the prefix of the units of foreground daemons.
	ExecTemplate = "carbonio-run-exec@"
	// ForkingTemplate is the prefix of the units of launchers that fork.
	ForkingTemplate = "carbonio-run-forking@"

	systemctlBin = "/usr/bin/systemctl"
	systemdDir   = "/run/systemd/system"

	// stopTimeout bounds the stop of a unit that is still stopping.
	stopTimeout = 150 * time.Second
	// settleAttempts bounds the wait for a just-exited unit to leave the
	// active states before its result is read.
	settleAttempts = 20
	settleInterval = 100 * time.Millisecond
	// pollInterval paces the process-table fallback of waitPidExit.
	pollInterval = time.Second
)

// Kind selects the template a launch uses.
type Kind string

const (
	// KindExec runs a foreground daemon that stays the unit's main process.
	KindExec Kind = "exec"
	// KindForking runs a command to completion in the unit, the way systemd
	// runs the ExecStart of Type=forking: whatever it leaves running stays in
	// the unit's cgroup.
	KindForking Kind = "forking"
)

// template returns the template unit prefix of k.
func (k Kind) template() string {
	if k == KindForking {
		return ForkingTemplate
	}

	return ExecTemplate
}

// allowedEnv are the only environment variables a launch may add: what the
// daemon has always set on the children it starts.
var allowedEnv = map[string]struct{}{"NOTIFY_SOCKET": {}}

var instanceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// infraMarkers are the pieces of systemctl's diagnostics that say the request
// never reached a unit (no systemd, polkit said no, the template is missing)
// as opposed to the unit running and failing.
var infraMarkers = []string{
	"access denied",
	"authentication required",
	"authentication is required",
	"failed to connect",
	"not found",
	"not loaded",
	"not been booted",
}

// Runner launches commands as template units. The zero value is unusable;
// build one with New. A Runner is safe for concurrent use.
type Runner struct {
	env sys.System
	dir string

	bootOnce sync.Once
	booted   bool

	disabled atomic.Bool
	warnOnce sync.Once

	locks sync.Map // instance -> *sync.Mutex

	// waitExit blocks until pid has exited. Replaced in tests.
	waitExit func(pid int) error
}

// New returns a Runner acting through env.
func New(env sys.System) *Runner {
	r := &Runner{env: env, dir: DefaultDir}
	r.waitExit = r.waitPidExit

	return r
}

// Enabled reports whether launches currently go through units: systemd is the
// init system, the caller is not root and no launch has failed for want of the
// units yet.
func (r *Runner) Enabled() bool {
	if r.disabled.Load() {
		return false
	}

	r.bootOnce.Do(func() {
		st, err := r.env.FS.Stat(systemdDir)
		r.booted = err == nil && st.IsDir()
	})

	return r.booted && r.env.Proc.UID() != 0
}

// Unit returns the unit name of service for kind.
func Unit(kind Kind, service string) string {
	return kind.template() + service + ".service"
}

// Run runs cmd to completion in a Type=forking unit of its own and returns
// what it printed (stdout and stderr merged, like sys.CombinedOutput) and its
// exit status, exactly as sys.Exec.Run would. service names the unit
// (carbonio-run-forking@<service>.service). A command the units cannot carry
// (no systemd, a stdin, an environment the daemon never sets, ...) runs as a
// child of the caller.
func (r *Runner) Run(ctx context.Context, service string, cmd *sys.Cmd) (sys.Result, error) {
	if !r.usable(service, cmd) {
		return r.env.Exec.Run(ctx, cmd)
	}

	res, ok, err := r.runUnit(ctx, service, cmd)
	if !ok {
		return r.env.Exec.Run(ctx, cmd)
	}

	return res, err
}

// Start starts cmd as the main process of a Type=exec unit of its own and
// returns without waiting, exactly as sys.Exec.Start would. service names the
// unit (carbonio-run-exec@<service>.service). Cancelling ctx does not stop
// the unit; the process outlives its caller. Falls back to a child of the
// caller like Run.
//
// unitArgs are appended to the arguments only when the command does run in a
// unit: a daemon that forks into the background by default has to be told to
// stay in the foreground there, or its unit ends with the parent and takes
// the daemon down.
func (r *Runner) Start(ctx context.Context, service string, cmd *sys.Cmd, unitArgs ...string) (sys.Process, error) {
	if !r.usable(service, cmd) {
		return r.env.Exec.Start(ctx, cmd)
	}

	unitCmd := *cmd
	unitCmd.Args = append(slices.Clone(cmd.Args), unitArgs...)

	proc, ok, err := r.startUnit(ctx, service, &unitCmd)
	if !ok {
		return r.env.Exec.Start(ctx, cmd)
	}

	return proc, err
}

// usable reports whether cmd can be carried by a unit.
func (r *Runner) usable(service string, cmd *sys.Cmd) bool {
	if !r.Enabled() || !instanceRe.MatchString(service) {
		return false
	}

	if cmd.Stdin != nil || !strings.HasPrefix(cmd.Path, "/") {
		return false
	}

	for _, kv := range cmd.Env {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := allowedEnv[name]; !ok {
			return false
		}
	}

	return fileOrNil(cmd.Stdout) && fileOrNil(cmd.Stderr)
}

// fileOrNil reports whether w is unset or an open file (whose path the unit can
// reopen).
func fileOrNil(w io.Writer) bool {
	return w == nil || fileName(w) != ""
}

// fileName returns the path behind w when it is an open file, "" otherwise.
func fileName(w io.Writer) string {
	if n, ok := w.(interface{ Name() string }); ok {
		return n.Name()
	}

	return ""
}

func (r *Runner) lock(kind Kind, service string) func() {
	m, _ := r.locks.LoadOrStore(string(kind)+"/"+service, &sync.Mutex{})
	mu := m.(*sync.Mutex) //nolint:forcetypeassert // only *sync.Mutex is stored

	mu.Lock()

	return mu.Unlock
}

func (r *Runner) specPaths(kind Kind, service string) (specPath, resultPath, outPath string) {
	base := r.dir + "/" + string(kind) + "-" + service

	return base + ".json", base + ".rc", base + ".out"
}

// prepare creates the spec directory and writes the spec of cmd. It returns
// the paths of the files and a cleanup. ok is false when the directory or the
// spec cannot be made, which makes the caller fall back.
func (r *Runner) prepare(kind Kind, service string, cmd *sys.Cmd) (paths [3]string, cleanup func(), err error) {
	specPath, resultPath, outPath := r.specPaths(kind, service)
	paths = [3]string{specPath, resultPath, outPath}

	if err = r.env.FS.MkdirAll(r.dir, 0o700); err != nil {
		return paths, nil, fmt.Errorf("create %s: %w", r.dir, err)
	}

	if err = r.env.FS.Chmod(r.dir, 0o700); err != nil {
		return paths, nil, fmt.Errorf("secure %s: %w", r.dir, err)
	}

	s := spec{Path: cmd.Path, Args: cmd.Args, Env: cmd.Env, Dir: cmd.Dir}

	if kind == KindForking {
		s.Result = resultPath
		s.Stdout = outPath // what the command prints, read back by Run
	}

	if name := fileName(cmd.Stdout); name != "" {
		s.Stdout = name
	}

	if name := fileName(cmd.Stderr); name != "" {
		s.Stderr = name
	}

	data, err := json.Marshal(s)
	if err != nil {
		return paths, nil, fmt.Errorf("encode spec: %w", err)
	}

	if err = r.env.FS.WriteFileAtomic(specPath, data, 0o600); err != nil {
		return paths, nil, fmt.Errorf("write %s: %w", specPath, err)
	}

	cleanup = func() {
		for _, p := range paths {
			_ = r.env.FS.Remove(p)
		}
	}

	return paths, cleanup, nil
}

// runUnit implements Run. ok is false when units cannot be used: the caller
// then runs the command itself.
func (r *Runner) runUnit(ctx context.Context, service string, cmd *sys.Cmd) (res sys.Result, ok bool, err error) {
	unit := Unit(KindForking, service)

	unlock := r.lock(KindForking, service)
	defer unlock()

	joined, claimOK, claimErr := r.claim(ctx, unit)
	if !claimOK {
		r.disable(ctx, unit, claimErr)

		return res, false, nil
	}

	if joined {
		// Someone else is launching or has launched this service: wait for
		// that launch (a failed one fails this start too) and leave the spec
		// and the result, which are theirs, alone.
		if start, startErr := r.systemctl(ctx, "start", unit); startErr != nil {
			if infraFailure(start, startErr) {
				r.disable(ctx, unit, startErr)

				return res, false, nil
			}

			return sys.Result{ExitCode: -1}, true, fmt.Errorf("start %s: %w", unit, describe(start, startErr))
		}

		return res, true, nil
	}

	paths, cleanup, prepErr := r.prepare(KindForking, service, cmd)
	if prepErr != nil {
		r.disable(ctx, unit, prepErr)

		return res, false, nil
	}

	defer cleanup()

	start, startErr := r.systemctl(ctx, "start", unit)

	rc, haveRC := readResult(r.env.FS, paths[1])
	if !haveRC {
		if startErr != nil && infraFailure(start, startErr) {
			r.disable(ctx, unit, startErr)

			return res, false, nil
		}

		return sys.Result{ExitCode: -1}, true, fmt.Errorf("start %s: %w", unit, describe(start, startErr))
	}

	out, _ := r.env.FS.ReadFile(paths[2])
	res = sys.Result{Stdout: out, ExitCode: rc.Code}

	if rc.Error != "" {
		return sys.Result{Stdout: out, ExitCode: -1}, true, errors.New(rc.Error)
	}

	if rc.Code != 0 {
		return res, true, &sys.ExitError{Code: rc.Code, Err: fmt.Errorf("exit status %d", rc.Code)}
	}

	return res, true, nil
}

// startUnit implements Start.
func (r *Runner) startUnit(ctx context.Context, service string, cmd *sys.Cmd) (proc sys.Process, ok bool, err error) {
	unit := Unit(KindExec, service)

	unlock := r.lock(KindExec, service)
	defer unlock()

	joined, claimOK, claimErr := r.claim(ctx, unit)
	if !claimOK {
		r.disable(ctx, unit, claimErr)

		return nil, false, nil
	}

	cleanup := func() {}

	if !joined {
		var prepErr error

		_, cleanup, prepErr = r.prepare(KindExec, service, cmd)
		if prepErr != nil {
			r.disable(ctx, unit, prepErr)

			return nil, false, nil
		}
	}

	// No cleanup on success: the unit's helper removes the spec when it has
	// read it (Main), which happens after "systemctl start" returned.
	start, startErr := r.systemctl(ctx, "start", unit)
	if startErr != nil {
		cleanup()

		if infraFailure(start, startErr) {
			r.disable(ctx, unit, startErr)

			return nil, false, nil
		}

		return nil, true, fmt.Errorf("start %s: %w", unit, describe(start, startErr))
	}

	pid := r.mainPID(ctx, unit)

	logger.InfoContext(ctx, "Started service unit", "unit", unit, "pid", pid)

	return &unitProcess{r: r, unit: unit, pid: pid}, true, nil
}

// claim decides what a start of unit has to do about what an earlier start
// left. An active (or activating) unit is joined: its process is the service
// and starting it again would only replace a running service; this is what a
// start that races with another one (the daemon's watchdog and zmcontrol, the
// dependency start of a service and its own) finds, and the legacy second
// start would just have failed to bind. A failed unit is reset and a stopping
// one waited for, so that the name can be used again. ok is false when systemd
// could not be asked (the caller falls back); err is then the reason.
func (r *Runner) claim(ctx context.Context, unit string) (joined, ok bool, err error) {
	res, askErr := r.systemctl(ctx, "is-active", unit)
	if askErr != nil {
		if _, isExit := errors.AsType[*sys.ExitError](askErr); !isExit {
			return false, false, askErr
		}
	}

	state := strings.TrimSpace(string(res.Stdout))

	var verb string

	switch state {
	case "active", "activating", "reloading", "refreshing":
		logger.InfoContext(ctx, "Service unit already running, not starting it again", "unit", unit, "state", state)

		return true, true, nil
	case "deactivating":
		verb = "stop"
	case "failed":
		verb = "reset-failed"
	default:
		return false, true, nil
	}

	logger.InfoContext(ctx, "Clearing stale service unit", "unit", unit, "state", state)

	if out, verbErr := r.systemctl(ctx, verb, unit); verbErr != nil {
		if infraFailure(out, verbErr) {
			return false, false, verbErr
		}

		logger.WarnContext(ctx, "Could not clear the service unit",
			"unit", unit, "verb", verb, "error", describe(out, verbErr))
	}

	return false, true, nil
}

// systemctl runs one systemctl verb on unit.
func (r *Runner) systemctl(ctx context.Context, verb, unit string) (sys.Result, error) {
	if verb == "stop" {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, stopTimeout)
		defer cancel()
	}

	return r.env.Exec.Run(ctx, &sys.Cmd{
		Path: systemctlBin,
		Args: []string{"--no-ask-password", verb, unit},
	})
}

// mainPID asks for the main process of unit, 0 when it has none.
func (r *Runner) mainPID(ctx context.Context, unit string) int {
	res, err := r.env.Exec.Run(ctx, &sys.Cmd{
		Path: systemctlBin,
		Args: []string{"show", "--property=MainPID", "--value", unit},
	})
	if err != nil {
		return 0
	}

	pid, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))

	return pid
}

// infraFailure reports whether a failed systemctl request never reached a
// unit: systemctl itself could not run, or its diagnostics say there is no
// systemd, no permission or no such unit.
func infraFailure(res sys.Result, err error) bool {
	if _, exited := errors.AsType[*sys.ExitError](err); !exited {
		return true
	}

	msg := strings.ToLower(string(res.Stderr) + string(res.Stdout))

	for _, marker := range infraMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}

	return false
}

// describe renders a failed systemctl call for an error message.
func describe(res sys.Result, err error) error {
	if msg := strings.TrimSpace(string(bytes.TrimSpace(res.Stderr))); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}

	return err
}

// disable turns the Runner off for good and says why, once.
func (r *Runner) disable(ctx context.Context, unit string, cause error) {
	r.disabled.Store(true)

	r.warnOnce.Do(func() {
		logger.WarnContext(ctx,
			"Cannot start services as systemd units; starting them as children of this process",
			"unit", unit, "error", cause)
	})
}

// result is what the forking wrapper leaves for Run to read.
type result struct {
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

func readResult(fsys sys.FS, path string) (result, bool) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return result{}, false
	}

	var rc result
	if json.Unmarshal(data, &rc) != nil {
		return result{}, false
	}

	return rc, true
}

// unitProcess is the sys.Process of a unit's main process.
type unitProcess struct {
	r    *Runner
	unit string
	pid  int
}

// Pid implements sys.Process.
func (p *unitProcess) Pid() int { return p.pid }

// Release implements sys.Process: the unit, not the caller, owns the process.
func (p *unitProcess) Release() error { return nil }

// Wait implements sys.Process: it blocks until the main process has exited and
// reports, as *sys.ExitError, a unit that did not end successfully.
func (p *unitProcess) Wait() error {
	if p.pid > 0 {
		if err := p.r.waitExit(p.pid); err != nil {
			return err
		}
	}

	return p.r.unitResult(context.Background(), p.unit)
}

// unitResult reads how a unit ended: nil when it succeeded or is gone.
func (r *Runner) unitResult(ctx context.Context, unit string) error {
	var props map[string]string

	for range settleAttempts {
		props = r.show(ctx, unit, "ActiveState", "Result", "ExecMainStatus")

		switch props["ActiveState"] {
		case "active", "activating", "deactivating", "reloading", "refreshing":
			<-r.env.Clock.After(settleInterval)

			continue
		}

		break
	}

	if props["Result"] == "" || props["Result"] == "success" {
		return nil
	}

	code, _ := strconv.Atoi(props["ExecMainStatus"])

	return &sys.ExitError{Code: code, Err: fmt.Errorf("unit %s: %s (exit status %d)", unit, props["Result"], code)}
}

func (r *Runner) show(ctx context.Context, unit string, names ...string) map[string]string {
	args := []string{"show"}
	for _, n := range names {
		args = append(args, "--property="+n)
	}

	res, err := r.env.Exec.Run(ctx, &sys.Cmd{Path: systemctlBin, Args: append(args, unit)})
	if err != nil {
		return nil
	}

	props := map[string]string{}

	for line := range strings.SplitSeq(string(res.Stdout), "\n") {
		if k, v, found := strings.Cut(line, "="); found {
			props[k] = v
		}
	}

	return props
}
