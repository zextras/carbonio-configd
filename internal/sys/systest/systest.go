// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package systest provides the test doubles for internal/sys: an in-memory
// filesystem, a scriptable command runner, a programmable process table and a
// manual clock, all writing to one shared Recorder so a test can assert the
// exact order of every side effect across the four boundaries. It is test
// support only; production code never imports it.
package systest

import (
	"fmt"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Op is one recorded side effect.
type Op struct {
	// Kind groups operations: "exec", "exec.start", "fs.write", "fs.rename",
	// "proc.signal", ... (tests may record their own kinds, e.g. "ldap").
	Kind string
	// Detail identifies the target, e.g. the command line or the path.
	Detail string
}

// String renders the op as "kind detail".
func (o Op) String() string { return o.Kind + " " + o.Detail }

// Recorder is an ordered, concurrency-safe log of side effects shared by all
// the fakes of one Fakes.
type Recorder struct {
	mu  sync.Mutex
	ops []Op
}

// Record appends an operation.
func (r *Recorder) Record(kind, detail string) {
	r.mu.Lock()
	r.ops = append(r.ops, Op{Kind: kind, Detail: detail})
	r.mu.Unlock()
}

// Recordf appends an operation with a formatted detail.
func (r *Recorder) Recordf(kind, format string, args ...any) {
	r.Record(kind, fmt.Sprintf(format, args...))
}

// Ops returns a copy of the recorded operations in order.
func (r *Recorder) Ops() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Op(nil), r.ops...)
}

// Strings returns the operations as "kind detail" lines, in order.
func (r *Recorder) Strings() []string {
	ops := r.Ops()
	out := make([]string, len(ops))

	for i, o := range ops {
		out[i] = o.String()
	}

	return out
}

// Of returns the details of the operations whose kind has the given prefix.
func (r *Recorder) Of(kindPrefix string) []string {
	var out []string

	for _, o := range r.Ops() {
		if strings.HasPrefix(o.Kind, kindPrefix) {
			out = append(out, o.Detail)
		}
	}

	return out
}

// Reset forgets everything recorded so far.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.ops = nil
	r.mu.Unlock()
}

// Fakes is a complete fake sys.System sharing one Recorder.
type Fakes struct {
	Rec   *Recorder
	Exec  *Exec
	FS    *FS
	Proc  *Proc
	Clock *Clock
}

// New returns fresh fakes: empty filesystem, empty process table, every
// command succeeding with no output, the clock at a fixed instant.
func New() *Fakes {
	rec := &Recorder{}

	return &Fakes{
		Rec:   rec,
		Exec:  NewExec(rec),
		FS:    NewFS(rec),
		Proc:  NewProc(rec),
		Clock: NewClock(rec),
	}
}

// System returns the fakes as a sys.System ready to inject.
func (f *Fakes) System() sys.System {
	return sys.System{Exec: f.Exec, FS: f.FS, Proc: f.Proc, Clock: f.Clock}
}
