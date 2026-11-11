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
type Outcome int

// The outcomes of a step.
const (
	Skipped Outcome = iota // does not apply to the node
	AlreadyDone
	Applied
	WouldApply // dry run
	Warned     // optional step failed
)

var outcomeNames = [...]string{"skipped", "done", "applied", "would apply", "warning"}

func (o Outcome) String() string { return outcomeNames[o] }

// Result is the outcome of one step.
type Result struct {
	Step    *Step
	Outcome Outcome
	Err     error
}

// ErrStepFailed wraps the failure that ended a run.
var ErrStepFailed = errors.New("bootstrap step failed")

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
		s := &steps[i]

		res, err := runStep(e, s, dryRun)
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

func runStep(e *Env, s *Step, dryRun bool) (Result, error) {
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

			return res, nil
		}
	}

	if dryRun {
		res.Outcome = WouldApply

		return res, nil
	}

	end := e.Log.Begin(s.Title)

	err := s.Apply(e)
	end(err)

	if err != nil {
		return failed(e, s, res, err)
	}

	res.Outcome = Applied

	if s.Name != "" {
		if err := e.history().configured(s.Name); err != nil {
			return res, err
		}
	}

	return res, nil
}

func failed(e *Env, s *Step, res Result, err error) (Result, error) {
	res.Err = err

	if s.Optional {
		res.Outcome = Warned

		e.Log.Warn("%s: %v", s.Title, err)

		return res, nil
	}

	return res, fmt.Errorf("%w: %s: %w", ErrStepFailed, s.Title, err)
}
