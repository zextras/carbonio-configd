// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Hook actions, the keys of a service entry in the hooks file.
const (
	hookStart   = "start"
	hookStop    = "stop"
	hookRestart = "restart"
	hookStatus  = "status"

	// hookWildcard is the service entry applying to every service without an
	// own list for an action.
	hookWildcard = "*"

	specSkip    = "skip"
	specProcess = "process"
	specSignal  = "signal:"
	specExec    = "exec:"

	// hookExecTimeout bounds one exec: hook.
	hookExecTimeout = 30 * time.Second
)

// ErrHooks is wrapped by every hooks file or hook specification error.
var ErrHooks = errors.New("service hooks")

// ErrHooksMissing is returned by HookBackend.Validate and lists what is not
// covered by the hooks file.
var ErrHooksMissing = errors.New("service hooks missing")

// HookActions maps an action (start, stop, restart, status) to the ordered
// hook specifications that implement it.
type HookActions map[string][]string

// Hooks is the content of the hooks file: service name (or "*") to its actions.
type Hooks map[string]HookActions

// hookSpec is one parsed hook specification.
type hookSpec struct {
	raw     string
	kind    string // specSkip, specProcess, specSignal, specExec
	sig     syscall.Signal
	service string   // signal: target service; "" is the service of the action
	argv    []string // exec: command line
}

// HookBackend implements Backend by signalling processes and running commands
// instead of managing them: for pods, where the service lives in another
// container sharing the process namespace and its lifecycle belongs to the
// kubelet. Host side effects go through the sys.System of the Controller.
type HookBackend struct {
	ctl   *Controller
	env   sys.System
	hooks map[string]map[string][]hookSpec
}

// signals are the signal names a signal: hook accepts.
var signals = map[string]syscall.Signal{
	"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT,
	"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
}

// ParseHooks decodes and checks the content of a hooks file.
func ParseHooks(data []byte) (Hooks, error) {
	var hooks Hooks
	if err := json.Unmarshal(data, &hooks); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHooks, err)
	}

	return hooks, nil
}

// NewHookBackend builds the backend for hooks; every specification is parsed
// here, so an invalid one fails at startup. ctl provides the registry (names,
// pid files, process identities) and the sys.System.
func NewHookBackend(ctl *Controller, hooks Hooks) (*HookBackend, error) {
	b := &HookBackend{ctl: ctl, env: ctl.env, hooks: map[string]map[string][]hookSpec{}}

	for svc, actions := range hooks {
		name := strings.ToLower(svc)
		if name != hookWildcard && ctl.LookupService(name) == nil {
			return nil, fmt.Errorf("%w: unknown service %q", ErrHooks, svc)
		}

		b.hooks[name] = map[string][]hookSpec{}

		for action, specs := range actions {
			if !slices.Contains([]string{hookStart, hookStop, hookRestart, hookStatus}, action) {
				return nil, fmt.Errorf("%w: service %q: unknown action %q", ErrHooks, svc, action)
			}

			if len(specs) == 0 {
				return nil, fmt.Errorf("%w: service %q: %s has no hook", ErrHooks, svc, action)
			}

			for _, raw := range specs {
				spec, err := parseHookSpec(ctl, action, raw)
				if err != nil {
					return nil, fmt.Errorf("%w: service %q %s: %w", ErrHooks, svc, action, err)
				}

				b.hooks[name][action] = append(b.hooks[name][action], spec)
			}
		}
	}

	return b, nil
}

// parseHookSpec parses one hook specification valid for action.
func parseHookSpec(ctl *Controller, action, raw string) (hookSpec, error) {
	spec := hookSpec{raw: raw}

	switch {
	case raw == specSkip:
		if action == hookStatus {
			return spec, fmt.Errorf("%q cannot answer a status", raw)
		}

		spec.kind = specSkip
	case raw == specProcess:
		if action != hookStatus {
			return spec, fmt.Errorf("%q is only valid for status", raw)
		}

		spec.kind = specProcess
	case strings.HasPrefix(raw, specSignal):
		if action == hookStatus {
			return spec, fmt.Errorf("%q cannot answer a status", raw)
		}

		return parseSignalSpec(ctl, raw)
	case strings.HasPrefix(raw, specExec):
		spec.kind = specExec

		spec.argv = strings.Fields(strings.TrimPrefix(raw, specExec))
		if len(spec.argv) == 0 {
			return spec, fmt.Errorf("%q has no command", raw)
		}
	default:
		return spec, fmt.Errorf("unknown hook %q (want skip, process, signal:<SIG>[@<service>] or exec:<argv>)", raw)
	}

	return spec, nil
}

// parseSignalSpec parses signal:<SIG>[@<service>].
func parseSignalSpec(ctl *Controller, raw string) (hookSpec, error) {
	spec := hookSpec{raw: raw, kind: specSignal}
	name, target, _ := strings.Cut(strings.TrimPrefix(raw, specSignal), "@")

	sig, ok := signals[strings.TrimPrefix(strings.ToUpper(name), "SIG")]
	if !ok {
		return spec, fmt.Errorf("%q: unknown signal %q", raw, name)
	}

	spec.sig = sig

	if target != "" {
		if ctl.LookupService(strings.ToLower(target)) == nil {
			return spec, fmt.Errorf("%q: unknown service %q", raw, target)
		}

		spec.service = strings.ToLower(target)
	}

	return spec, nil
}

// specsFor returns the hooks of action for service: its own list, else the
// wildcard's.
func (b *HookBackend) specsFor(service, action string) []hookSpec {
	if specs := b.hooks[service][action]; len(specs) > 0 {
		return specs
	}

	return b.hooks[hookWildcard][action]
}

// Validate reports, as one ErrHooksMissing error, every service of restart
// that has no restart hook and every service of status that has no status
// hook. Names are resolved through the registry, so aliases work.
func (b *HookBackend) Validate(restart, status []string) error {
	var missing []string

	for _, svc := range restart {
		if len(b.specsFor(b.canonical(svc), hookRestart)) == 0 {
			missing = append(missing, "restart:"+svc)
		}
	}

	for _, svc := range status {
		if len(b.specsFor(b.canonical(svc), hookStatus)) == 0 {
			missing = append(missing, "status:"+svc)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	slices.Sort(missing)

	return fmt.Errorf("%w: %s", ErrHooksMissing, strings.Join(slices.Compact(missing), ", "))
}

// canonical resolves service names and aliases to the registry name; unknown
// names are returned lower-cased.
func (b *HookBackend) canonical(name string) string {
	name = strings.ToLower(name)
	if def := b.ctl.LookupService(name); def != nil {
		return def.Name
	}

	return name
}

// LookupService implements Backend.
func (b *HookBackend) LookupService(name string) *ServiceDef { return b.ctl.LookupService(name) }

// ServiceStart implements Backend.
func (b *HookBackend) ServiceStart(ctx context.Context, name string) error {
	return b.run(ctx, name, hookStart)
}

// ServiceStop implements Backend.
func (b *HookBackend) ServiceStop(ctx context.Context, name string) error {
	return b.run(ctx, name, hookStop)
}

// ServiceRestart implements Backend.
func (b *HookBackend) ServiceRestart(ctx context.Context, name string) error {
	return b.run(ctx, name, hookRestart)
}

// ServiceStatus implements Backend: the service is running when every status
// hook says so.
func (b *HookBackend) ServiceStatus(ctx context.Context, name string) (bool, error) {
	def := b.ctl.LookupService(strings.ToLower(name))
	if def == nil {
		return false, fmt.Errorf(errUnknownService, name)
	}

	specs := b.specsFor(def.Name, hookStatus)
	if len(specs) == 0 {
		return false, fmt.Errorf("%w: no status hook for service %s", ErrHooksMissing, def.Name)
	}

	for _, spec := range specs {
		running, err := b.status(ctx, def, &spec)
		if err != nil || !running {
			return false, err
		}
	}

	return true, nil
}

// status evaluates one status hook.
func (b *HookBackend) status(ctx context.Context, def *ServiceDef, spec *hookSpec) (bool, error) {
	if spec.kind == specProcess {
		return b.ctl.runningPID(ctx, def) > 0, nil
	}

	err := b.exec(ctx, spec)

	if _, ok := errors.AsType[*sys.ExitError](err); ok {
		return false, nil
	}

	return err == nil, err
}

// run executes the hooks of action for service in order, stopping at the first failure.
func (b *HookBackend) run(ctx context.Context, name, action string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")

	def := b.ctl.LookupService(strings.ToLower(name))
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	specs := b.specsFor(def.Name, action)
	if len(specs) == 0 {
		return fmt.Errorf("%w: no %s hook for service %s", ErrHooksMissing, action, def.Name)
	}

	for _, spec := range specs {
		if err := b.apply(ctx, def, action, &spec); err != nil {
			return fmt.Errorf("%s %s: hook %q: %w", action, def.Name, spec.raw, err)
		}
	}

	return nil
}

// apply executes one hook.
func (b *HookBackend) apply(ctx context.Context, def *ServiceDef, action string, spec *hookSpec) error {
	switch spec.kind {
	case specSkip:
		logger.InfoContext(ctx, "service action not managed by this process",
			"service", def.Name, "action", action)

		return nil
	case specSignal:
		return b.signal(ctx, def, spec)
	default:
		return b.exec(ctx, spec)
	}
}

// signal sends the signal of spec to the process of the service it targets.
func (b *HookBackend) signal(ctx context.Context, def *ServiceDef, spec *hookSpec) error {
	if spec.service != "" {
		def = b.ctl.LookupService(spec.service)
	}

	pid := b.ctl.runningPID(ctx, def)
	if pid <= 0 {
		return fmt.Errorf("%w: %s", errNoProcess, def.Name)
	}

	logger.InfoContext(ctx, "Signalling service process", "service", def.Name, "pid", pid, "signal", spec.sig.String())

	if err := b.env.Proc.Signal(pid, spec.sig); err != nil {
		return fmt.Errorf("signal %s to %s (pid %d): %w", spec.sig, def.Name, pid, err)
	}

	return nil
}

// errNoProcess is returned when a signal hook finds no process to signal.
var errNoProcess = errors.New("no running process")

// exec runs the command of spec with a timeout.
func (b *HookBackend) exec(ctx context.Context, spec *hookSpec) error {
	ctx, cancel := context.WithTimeout(ctx, hookExecTimeout)
	defer cancel()

	_, err := b.env.Exec.Run(ctx, &sys.Cmd{Path: spec.argv[0], Args: spec.argv[1:], Combined: true})
	if err != nil {
		if _, ok := errors.AsType[*sys.ExitError](err); ok {
			return err
		}

		return fmt.Errorf("run %s: %w", spec.argv[0], err)
	}

	return nil
}
