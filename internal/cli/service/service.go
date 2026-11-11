// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package service implements "configd service <action> [name]": listing,
// controlling and probing a single registered service.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Action is the "configd service" subcommand to run.
type Action string

// The service subcommands. StartSystemd, StopSystemd and ReloadSystemd are the
// hidden systemd ExecStart/ExecStop/ExecReload leaves: they act in-process and
// never call systemctl.
const (
	ActionList         Action = "list"
	ActionStart        Action = "start"
	ActionStop         Action = "stop"
	ActionRestart      Action = "restart"
	ActionReload       Action = "reload"
	ActionStatus       Action = "status"
	ActionStartSystemd Action = "start-systemd"
	ActionStopSystemd  Action = "stop-systemd"
	// ActionReloadSystemd is the ExecReload leaf: native reload in-process.
	ActionReloadSystemd Action = "reload-systemd"
	// ActionRestartSystemd is stop-systemd then start-systemd (legacy
	// zmstatctl restart-systemd).
	ActionRestartSystemd Action = "restart-systemd"
	// ActionKill is the legacy alias of stop. ActionRefresh and ActionRotate
	// are the signal verbs (milter refresh, stats rotate).
	ActionKill    Action = "kill"
	ActionRefresh Action = "refresh"
	ActionRotate  Action = "rotate"
	// ActionUsage is an empty or unknown verb: print the verb list, fail.
	ActionUsage Action = "usage"
)

// usageLine is what an empty or unknown verb prints, like the legacy
// `$0 start|stop|kill|restart|reload|status[|refresh|rotate]`.
const usageLine = "Usage: configd service start|stop|kill|restart|reload|status|refresh|rotate <service> [norewrite]"

// Options are the parsed arguments of "configd service".
type Options struct {
	Action Action
	// Name is the service to act on; unused by ActionList.
	Name string
	// NoRewrite skips config regeneration on start, restart and reload.
	NoRewrite bool
	// Extra are the arguments the legacy wrapper forwarded after the service
	// name; only "norewrite" is meaningful (NoRewrite is derived from it by the
	// caller), the rest is accepted and ignored.
	Extra []string
}

// mutation describes a service-mutating action: the verb used in the error
// message and the Controller method implementing it.
type mutation struct {
	verb string
	fn   func(context.Context, string) error
}

// runner carries what one "configd service" invocation drives: the Controller,
// the status reporter and the mutating-action table bound to that Controller.
type runner struct {
	ctl       *services.Controller
	reporter  *status.Reporter
	mutations map[Action]mutation
}

// noRewrite reports whether the invocation skips the REWRITE round-trip.
// `norewrite` is honored by start, restart and reload only (legacy scripts
// ignore it on every other verb).
func noRewrite(opts *Options) bool {
	switch opts.Action {
	case ActionStart, ActionRestart, ActionReload:
		return opts.NoRewrite
	default:
		return false
	}
}

func newRunner(env sys.System, opts *Options) *runner {
	ctl := services.NewController(env, services.WithNoRewrite(noRewrite(opts)))

	return &runner{
		ctl:      ctl,
		reporter: status.NewReporter(env, ctl),
		mutations: map[Action]mutation{
			ActionStart:         {"start", ctl.ServiceStart},
			ActionStop:          {"stop", ctl.ServiceStop},
			ActionKill:          {"kill", ctl.ServiceStop},
			ActionRestart:       {"restart", ctl.ServiceRestart},
			ActionReload:        {"reload", ctl.ServiceReload},
			ActionStartSystemd:  {"start", ctl.ServiceStartSystemd},
			ActionStopSystemd:   {"stop", ctl.ServiceStopSystemd},
			ActionReloadSystemd: {"reload", ctl.ServiceReloadSystemd},
			ActionRestartSystemd: {"restart", func(ctx context.Context, name string) error {
				if err := ctl.ServiceStopSystemd(ctx, name); err != nil {
					return err
				}

				return ctl.ServiceStartSystemd(ctx, name)
			}},
		},
	}
}

// Run executes "configd service" and returns the exit status. list and status
// have no permission gate; every mutating action requires the zextras user.
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	r := newRunner(env, opts)

	_, mutating := r.mutations[opts.Action]
	signaling := opts.Action == ActionRefresh || opts.Action == ActionRotate

	if (mutating || signaling) && !cliutil.RequireZextras(stderr) {
		return 1
	}

	return r.run(ctx, stdout, stderr, opts)
}

func (r *runner) run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	if r.ldapReplicaRefused(c, opts) {
		return 1
	}

	switch opts.Action {
	case ActionList:
		r.list(ctx, c)

		return 0
	case ActionUsage:
		_, _ = fmt.Fprintln(stderr, usageLine)

		return 1
	case ActionStatus:
		err := r.reporter.PrintServiceStatus(ctx, c, opts.Name)
		if errors.Is(err, status.ErrNotRunning) {
			// "<service> is not running." is already on stdout: legacy scripts
			// stop at the exit status.
			return 1
		}

		return report(stderr, err)
	case ActionRefresh:
		return report(stderr, r.signal(ctx, c, opts, r.ctl.ServiceRefresh))
	case ActionRotate:
		return report(stderr, r.signal(ctx, c, opts, r.ctl.ServiceRotate))
	default:
		return report(stderr, r.mutate(ctx, opts))
	}
}

// ldapReplicaRefused applies legacy ldap.sh's guard: on an ldap replica whose
// ldap_url is its ldap_master_url every verb but stop is refused.
func (r *runner) ldapReplicaRefused(c *cliutil.Console, opts *Options) bool {
	switch opts.Action {
	case ActionList, ActionUsage, ActionStop, ActionKill, ActionStopSystemd:
		return false
	}

	def := r.ctl.LookupService(opts.Name)
	if def == nil || def.Name != "ldap" {
		return false
	}

	if err := r.ctl.LDAPReplicaGuard(); err != nil {
		c.Println(err.Error())

		return true
	}

	return false
}

// report prints err as the "Error:" line main prints for a failing
// command and returns the matching exit status.
func report(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

	return 1
}

// signal runs the refresh or rotate verb and prints what it did, in the words
// of the legacy scripts.
func (r *runner) signal(
	ctx context.Context, c *cliutil.Console, opts *Options,
	fn func(context.Context, string) (services.SignalReport, error),
) error {
	report, err := fn(ctx, opts.Name)

	for _, pid := range report.Signaled {
		c.Printf("Sending %s to process %d\n", signalName(opts.Action), pid)
	}

	for _, pid := range report.NotRunning {
		c.Printf("PID %d was not running\n", pid)
	}

	if err != nil {
		return fmt.Errorf("failed to %s service %s: %w", opts.Action, opts.Name, err)
	}

	if opts.Action == ActionRefresh && len(report.Signaled) == 0 {
		def := r.ctl.LookupService(opts.Name)
		c.Printf("%s is not currently running\n", def.DisplayName)
	}

	return nil
}

// signalName is the signal a signal verb sends, as the legacy scripts name it.
func signalName(a Action) string {
	if a == ActionRotate {
		return "HUP"
	}

	return "CONT"
}

// mutate runs a service-mutating action, wrapping any error with the verb and
// service name to match each command's original user-facing message.
func (r *runner) mutate(ctx context.Context, opts *Options) error {
	m, ok := r.mutations[opts.Action]
	if !ok {
		return fmt.Errorf("unknown service action: %q", opts.Action)
	}

	if err := m.fn(ctx, opts.Name); err != nil {
		return fmt.Errorf("failed to %s service %s: %w", m.verb, opts.Name, err)
	}

	return nil
}

func (r *runner) list(ctx context.Context, c *cliutil.Console) {
	for _, info := range r.ctl.ServiceListStatus(ctx, true) {
		c.Status(info.DisplayName, info.Running, "")
	}
}
