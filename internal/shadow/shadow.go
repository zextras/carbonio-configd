// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package shadow runs the configd cycle (load, snapshot, plan) next to a real
// configd without any side effect, so the plans and the rendered files of this
// build can be compared with what the legacy daemon, or the previous Go
// release, actually does on the same node.
//
// Side-effect freedom is structural, not a matter of discipline:
//
//   - the cycle never calls the executor, the restart policy or the watchdog,
//     and the shadow command starts no listener and no sd_notify;
//   - the configuration manager runs on sys.ReadOnly, whose mutating methods
//     fail with sys.ErrReadOnly;
//   - the only thing written is the optional report directory, through a
//     separate sys.FS that only this package holds.
package shadow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Names inside the report directory.
const (
	// RenderedDir holds the would-be files, laid out like the install tree
	// (RenderedDir/conf/nginx.conf is what /opt/zextras/conf/nginx.conf would be).
	RenderedDir = "rendered"
	// PlansDir holds one JSON plan per cycle whose plan is not empty.
	PlansDir = "plans"
	// LatestPlan is the JSON plan of the last cycle, empty or not.
	LatestPlan = "plan-latest.json"
)

const (
	// retryDelay is how long a failed cycle waits before the next attempt, as
	// the daemon loop does.
	retryDelay = 60 * time.Second

	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// ErrUnsafeDir is returned by New for a report directory that would overlap
// the live install tree.
var ErrUnsafeDir = errors.New("shadow directory overlaps the install tree")

// Source is what a shadow cycle needs of the configuration manager.
type Source interface {
	// Load loads every configuration source and parses configd.cf.
	Load(ctx context.Context) error
	// Plan computes the plan of this cycle. It does not touch the host.
	Plan(ctx context.Context) (*plan.Plan, error)
	// RenderRewrite renders the template of r in memory.
	RenderRewrite(ctx context.Context, r *plan.Rewrite) ([]byte, os.FileMode, error)
	// CycleDone is called after every successfully planned cycle; the first
	// call ends the "first run" in which every enabled section is rendered.
	CycleDone()
}

// Options configure a Shadow.
type Options struct {
	// Source is the configuration manager.
	Source Source
	// Clock waits between cycles and stamps the plan files.
	Clock sys.Clock
	// Interval returns the poll interval; it is read after every cycle, as the
	// load may change it.
	Interval func() time.Duration
	// Reload wakes the loop early (SIGHUP and friends); nil disables it.
	Reload <-chan struct{}
	// Once runs a single cycle and returns its error.
	Once bool

	// Dir is the report directory ("" writes no files). It is created when
	// missing.
	Dir string
	// Out is the filesystem Dir is written through. It must be a writable
	// FS, deliberately distinct from the read-only one the Source runs on.
	Out sys.FS
	// BaseDir is the install tree (/opt/zextras) the plan is about; Dir must
	// not be, or contain, it.
	BaseDir string
}

// Shadow runs shadow cycles.
type Shadow struct {
	opts  Options
	dir   string
	cycle int
}

// New validates opts. The report directory is made absolute and must not
// overlap BaseDir, so a mistyped --shadow-dir can never send a rendered file
// over a live one.
func New(opts *Options) (*Shadow, error) {
	s := &Shadow{opts: *opts}

	if opts.Dir == "" {
		return s, nil
	}

	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolve shadow directory %q: %w", opts.Dir, err)
	}

	if base := filepath.Clean(opts.BaseDir); contains(dir, base) {
		return nil, fmt.Errorf("%w: %s contains or is %s", ErrUnsafeDir, dir, base)
	}

	s.dir = dir

	return s, nil
}

// contains reports whether path is dir or lies below it.
func contains(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Run runs cycles until ctx is done, or once when Options.Once is set. It
// returns the error of the last cycle in Once mode and nil otherwise: a failed
// cycle is logged and retried, like in the daemon.
func (s *Shadow) Run(ctx context.Context) error {
	ctx = logger.ContextWithComponent(ctx, "shadow")

	for {
		err := s.Cycle(ctx)

		if s.opts.Once {
			return err
		}

		wait := s.opts.Interval()
		if err != nil {
			wait = retryDelay
		}

		if !s.wait(ctx, wait) {
			return nil
		}
	}
}

// wait sleeps for d and reports whether the loop should go on (false once ctx
// is done).
func (s *Shadow) wait(ctx context.Context, d time.Duration) bool {
	timer := s.opts.Clock.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-s.opts.Reload:
		logger.InfoContext(ctx, "Received reload signal, running the next shadow cycle now")
	case <-timer.C():
	}

	// select picks at random among ready cases: a stop requested while the
	// timer fired must still win, so no cycle starts after shutdown.
	return ctx.Err() == nil
}

// Cycle runs one shadow cycle: load, plan, report. It never changes the host.
// The returned error joins everything that went wrong; the cycle still reports
// whatever it could.
func (s *Shadow) Cycle(ctx context.Context) error {
	s.cycle++
	start := time.Now()

	ctx = logger.ContextWithComponent(ctx, "shadow")

	if err := s.opts.Source.Load(ctx); err != nil {
		if ctx.Err() == nil {
			logger.ErrorContext(ctx, "Shadow cycle failed", "cycle", s.cycle, "phase", "load", "error", err)
		}

		return fmt.Errorf("load: %w", err)
	}

	pl, err := s.opts.Source.Plan(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Shadow cycle failed", "cycle", s.cycle, "phase", "plan", "error", err)

		return fmt.Errorf("plan: %w", err)
	}

	errs := s.report(ctx, pl)

	s.opts.Source.CycleDone()

	logger.InfoContext(ctx, "Shadow cycle completed",
		"cycle", s.cycle,
		"first_run", pl.FirstRun,
		"actions", actionCount(pl),
		"report_dir", s.dir,
		"report_errors", len(errs),
		"duration_seconds", time.Since(start).Seconds())

	return errors.Join(errs...)
}

// actionCount is the number of "Shadow plan" lines the plan produces.
func actionCount(pl *plan.Plan) int {
	n := len(pl.Mapfiles) + len(pl.Rewrites) + len(pl.Postconf) + len(pl.Postconfd) +
		len(pl.Ldap) + len(pl.Restarts)
	if pl.Proxygen != nil {
		n++
	}

	return n
}

// report logs the plan and, when a report directory is set, writes the plan
// JSON and the rendered files. It returns what it failed to write.
func (s *Shadow) report(ctx context.Context, pl *plan.Plan) []error {
	red := pl.Redacted()

	s.logPlan(ctx, &red)

	if s.dir == "" {
		return nil
	}

	errs := make([]error, 0, 1+len(red.Rewrites))

	if err := s.writePlan(&red); err != nil {
		logger.ErrorContext(ctx, "Shadow report failed", "cycle", s.cycle, "what", "plan", "error", err)

		errs = append(errs, err)
	}

	for i := range red.Rewrites {
		if err := s.writeRewrite(ctx, &red.Rewrites[i]); err != nil {
			logger.ErrorContext(ctx, "Shadow report failed", "cycle", s.cycle,
				"what", "rewrite", "source", red.Rewrites[i].Source, "error", err)

			errs = append(errs, err)
		}
	}

	return errs
}

// logPlan logs one "Shadow plan" line per action.
func (s *Shadow) logPlan(ctx context.Context, pl *plan.Plan) {
	line := func(action, subject string, sections []string, reasons []plan.Reason) {
		logger.InfoContext(ctx, "Shadow plan",
			"cycle", s.cycle,
			"action", action,
			"subject", subject,
			"sections", sections,
			"reasons", plan.Strings(reasons))
	}

	for i := range pl.Mapfiles {
		m := &pl.Mapfiles[i]
		line("mapfile", m.Key, m.Sections, m.Reasons)
	}

	for i := range pl.Rewrites {
		r := &pl.Rewrites[i]
		line("rewrite", r.Source+" -> "+r.Target, r.Sections, r.Reasons)
	}

	for i := range pl.Postconf {
		c := &pl.Postconf[i]
		line("postconf", c.Key+"="+c.Value, c.Sections, c.Reasons)
	}

	for i := range pl.Postconfd {
		c := &pl.Postconfd[i]
		line("postconfd", c.Key, c.Sections, c.Reasons)
	}

	for i := range pl.Ldap {
		l := &pl.Ldap[i]
		line("ldap", l.Key+"="+l.Value, l.Sections, l.Reasons)
	}

	if pl.Proxygen != nil {
		line("proxygen", "regenerate proxy configuration", pl.Proxygen.Sections, pl.Proxygen.Reasons)
	}

	for i := range pl.Restarts {
		r := &pl.Restarts[i]
		line(string(r.Action), r.Service, r.Sections, r.Reasons)
	}

	logger.DebugContext(ctx, "Shadow mapped files kept in sync", "cycle", s.cycle, "count", len(pl.Heal))
}

// writePlan writes plan-latest.json and, when the plan is not empty, a copy
// of it under plans/ named after the cycle time. Empty plans are the steady
// state; keeping each of them would grow the directory without bound.
func (s *Shadow) writePlan(pl *plan.Plan) error {
	data, err := pl.JSON()
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}

	if err := s.write(filepath.Join(s.dir, LatestPlan), data); err != nil {
		return err
	}

	if pl.IsEmpty() {
		return nil
	}

	name := fmt.Sprintf("plan-%s-%06d.json", s.opts.Clock.Now().UTC().Format("20060102T150405Z"), s.cycle)

	return s.write(filepath.Join(s.dir, PlansDir, name), data)
}

// writeRewrite renders r and writes it under rendered/, mirroring the install
// tree. The target is cleaned as an absolute path first, so no ".." in
// configd.cf can leave the report directory.
func (s *Shadow) writeRewrite(ctx context.Context, r *plan.Rewrite) error {
	content, _, err := s.opts.Source.RenderRewrite(ctx, r)
	if err != nil {
		return fmt.Errorf("render %s: %w", r.Source, err)
	}

	return s.write(filepath.Join(s.dir, RenderedDir, filepath.Clean("/"+r.Target)), content)
}

// write creates the parent directory and atomically replaces name, owner-only:
// rendered files may carry credentials.
func (s *Shadow) write(name string, data []byte) error {
	if err := s.opts.Out.MkdirAll(filepath.Dir(name), dirMode); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(name), err)
	}

	if err := s.opts.Out.WriteFileAtomic(name, data, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return nil
}
