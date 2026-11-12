// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package setup applies a bootstrap configuration to the node: the work of
// the legacy setup.pl applyConfig, as an ordered list of steps.
//
// Every step checks the node before it changes it (Done), so a run that
// stopped half way is completed by running again. Commands run without a
// shell, with their arguments as a list; secrets go through stdin or files,
// never through a command line.
package setup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Env is what every step works with.
type Env struct {
	Ctx   context.Context
	Sys   sys.System
	Cfg   *bootstrap.Config
	Facts *Facts
	Log   *Log
	// Ctl starts, stops and checks the services (systemd or legacy).
	Ctl *services.Controller
	// Flags carry what one step learns for the later ones.
	Flags Flags
	// Observe, if set, is told when each step of a run starts and ends.
	Observe Observer
	// Stop, if set, is polled between steps: once it reports true the run
	// ends with ErrStopped, never in the middle of a step.
	Stop func() bool

	// Paths of the node; Default paths outside tests.
	Paths Paths

	// perlLib caches the zextras PERL5LIB.
	perlLib string
	// lc caches the effective localconfig until the next write.
	lc map[string]string
}

// Flags are the cross-step state of setup.pl (its package globals).
type Flags struct {
	// NeedNewCert forces the certificate steps to redeploy (setup.pl
	// $needNewCert): set when the CA or the certificate was recreated.
	NeedNewCert bool
	// LDAPStarted is set once a step started the local LDAP server.
	LDAPStarted bool
	// CertType is "self" (generated) or "comm" (commercial, kept).
	CertType string
}

// Step is one unit of the bootstrap.
type Step struct {
	// Name is the setup.pl stage name when there is one (configSetupLdap,
	// ...): it is written to .install_history once the step has run.
	Name string
	// Title is what the operator reads while the step runs.
	Title string
	// When reports whether the step applies to this node (nil: always).
	When func(*Env) bool
	// Done reports whether the node is already in the state the step
	// produces (nil: the step runs every time).
	Done func(*Env) (bool, error)
	// Apply changes the node.
	Apply func(*Env) error
	// Optional steps only warn on failure, as setup.pl did ("failed." and
	// carry on); a failure of any other step ends the run.
	Optional bool
}

// Outcome is what happened to one step.
type Outcome uint8

// The outcomes of a step.
const (
	Skipped Outcome = iota // does not apply to the node
	AlreadyDone
	Applied
	WouldApply // dry run
	Warned     // optional step failed
	Failed     // the step failed and ended the run
)

var outcomeNames = [...]string{"skipped", "done", "applied", "would apply", "warning", "failed"}

func (o Outcome) String() string { return outcomeNames[o] }

// Result is the outcome of one step.
type Result struct {
	Step    *Step
	Outcome Outcome
	Err     error
	// Warnings are what the step reported through Log.Warn while it ran.
	Warnings []string
}

// ErrStepFailed wraps the failure that ended a run.
var ErrStepFailed = errors.New("bootstrap step failed")

// ErrStopped ends a run the operator asked to stop: the current step has
// finished, the later ones did not start.
var ErrStopped = errors.New("bootstrap stopped by the operator")

// EventKind tells what an Event reports.
type EventKind uint8

// The events of a run.
const (
	// EventStart: a step is about to change the node.
	EventStart EventKind = iota
	// EventFinish: a step ended; Outcome and Err say how.
	EventFinish
)

// Event reports the progress of a real run (not a dry run) to an Observer.
type Event struct {
	// Index is the position of the step in the list given to Run.
	Index int
	Step  *Step
	// Err is set on EventFinish.
	Err error
	// Elapsed is how long the step took (EventFinish).
	Elapsed time.Duration
	// Warnings are the warnings the step reported (EventFinish).
	Warnings []string
	Kind     EventKind
	// Outcome is set on EventFinish.
	Outcome Outcome
}

// Observer receives the events of a run, from the goroutine that runs it.
type Observer func(Event)

func (e *Env) emit(ev Event) {
	if e.Observe != nil {
		e.Observe(ev)
	}
}

// Run runs steps in order. In a dry run it only reports which steps would
// apply. It stops at the first failure of a non-optional step.
func Run(e *Env, steps []Step, dryRun bool) ([]Result, error) {
	results := make([]Result, 0, len(steps))

	if !dryRun {
		if err := e.history().sessionStart(); err != nil {
			return nil, err
		}
	}

	for i := range steps {
		if e.Stop != nil && e.Stop() {
			return results, ErrStopped
		}

		s := &steps[i]

		res, err := observedStep(e, i, s, dryRun)
		results = append(results, res)

		if err != nil {
			return results, err
		}
	}

	if !dryRun {
		if err := e.history().sessionComplete(); err != nil {
			return results, err
		}
	}

	return results, nil
}

// observedStep runs one step and reports its end to the observer.
func observedStep(e *Env, i int, s *Step, dryRun bool) (Result, error) {
	start := e.Sys.Clock.Now()

	res, err := runStep(e, i, s, dryRun)
	if dryRun {
		return res, err
	}

	elapsed := e.Sys.Clock.Now().Sub(start)

	ev := Event{Kind: EventFinish, Index: i, Step: s, Outcome: res.Outcome, Err: res.Err, Elapsed: elapsed,
		Warnings: res.Warnings}
	if ev.Err == nil {
		ev.Err = err
	}

	e.emit(ev)

	return res, err
}

func runStep(e *Env, i int, s *Step, dryRun bool) (Result, error) {
	res := Result{Step: s}

	if s.When != nil && !s.When(e) {
		return res, nil
	}

	if s.Done != nil {
		done, err := s.Done(e)

		switch {
		case err != nil && dryRun:
			// An earlier step, not run, would provide what the check needs
			// (LDAP, localconfig): report the step as pending.
			res.Outcome = WouldApply
			res.Err = fmt.Errorf("cannot check yet: %w", err)

			return res, nil
		case err != nil:
			return failed(e, s, res, fmt.Errorf("checking: %w", err))
		}

		if done {
			res.Outcome = AlreadyDone

			e.Log.Detail("%s: already done", s.Title)

			return res, recordDone(e, s, dryRun)
		}
	}

	if dryRun {
		res.Outcome = WouldApply

		return res, nil
	}

	e.emit(Event{Kind: EventStart, Index: i, Step: s})

	e.Log.Detail("%s: running", s.Title)
	e.Log.takeWarnings()

	err := s.Apply(e)
	res.Warnings = e.Log.takeWarnings()

	if err != nil {
		e.Log.Detail("%s: failed: %v", s.Title, err)

		return failed(e, s, res, err)
	}

	e.Log.Detail("%s: done", s.Title)

	res.Outcome = Applied

	if s.Name != "" {
		if err := e.history().configured(s.Name); err != nil {
			return res, err
		}
	}

	return res, nil
}

// recordDone records the stage of a step found already done: setup.pl
// records a stage whenever its sub ran, also when there was nothing left to
// do.
func recordDone(e *Env, s *Step, dryRun bool) error {
	if dryRun || s.Name == "" || e.Facts.Configured[s.Name] {
		return nil
	}

	return e.history().configured(s.Name)
}

func failed(e *Env, s *Step, res Result, err error) (Result, error) {
	res.Err = err

	res.Outcome = Failed

	if s.Optional {
		res.Outcome = Warned

		e.Log.Detail("%s: warning: %v", s.Title, err)

		return res, nil
	}

	return res, fmt.Errorf("%w: %s: %w", ErrStepFailed, s.Title, err)
}
