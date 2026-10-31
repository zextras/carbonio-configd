// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Signal is one signal delivered through the fake.
type Signal struct {
	PID   int
	Sig   syscall.Signal
	Group bool
}

// Proc is a programmable sys.Proc: a process table the test fills, signals
// that are recorded and, by default, make the target disappear.
type Proc struct {
	rec *Recorder

	mu      sync.Mutex
	table   map[int]sys.ProcEntry
	zombies map[int]bool
	signals []Signal
	gids    map[string]int

	// SelfPID, ParentPID, UserID and UmaskValue describe the fake process.
	SelfPID, ParentPID, UserID int
	UmaskValue                 os.FileMode
	// SignalErr, when set, decides the error returned for a signal (nil
	// means delivered). When nil every signal is delivered.
	SignalErr func(pid int, sig syscall.Signal, group bool) error
	// SurviveTerm keeps a process alive after SIGTERM, to exercise
	// escalation; SIGKILL always removes it.
	SurviveTerm map[int]bool
}

// NewProc returns an empty process table. The fake process is pid 100, child
// of 99, running as uid 1000 with umask 0027.
func NewProc(rec *Recorder) *Proc {
	return &Proc{
		rec: rec, table: map[int]sys.ProcEntry{}, zombies: map[int]bool{}, gids: map[string]int{},
		SelfPID: 100, ParentPID: 99, UserID: 1000, UmaskValue: 0o027,
		SurviveTerm: map[int]bool{},
	}
}

// Add puts a process in the table.
func (p *Proc) Add(e sys.ProcEntry) {
	p.mu.Lock()
	p.table[e.PID] = e
	p.mu.Unlock()
}

// Remove drops a process from the table.
func (p *Proc) Remove(pid int) {
	p.mu.Lock()
	delete(p.table, pid)
	delete(p.zombies, pid)
	p.mu.Unlock()
}

// SetZombie marks a process as a zombie.
func (p *Proc) SetZombie(pid int, z bool) {
	p.mu.Lock()
	p.zombies[pid] = z
	p.mu.Unlock()
}

// SetGID registers a group for LookupGID.
func (p *Proc) SetGID(group string, gid int) {
	p.mu.Lock()
	p.gids[group] = gid
	p.mu.Unlock()
}

// Signals returns every signal delivered, in order.
func (p *Proc) Signals() []Signal {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]Signal(nil), p.signals...)
}

// Table implements sys.Proc.
func (p *Proc) Table() ([]sys.ProcEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]sys.ProcEntry, 0, len(p.table))
	for _, e := range p.table {
		out = append(out, e)
	}

	slices.SortFunc(out, func(a, b sys.ProcEntry) int { return a.PID - b.PID })

	return out, nil
}

// Entry implements sys.Proc.
func (p *Proc) Entry(pid int) (sys.ProcEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, ok := p.table[pid]

	return e, ok
}

// Alive implements sys.Proc.
func (p *Proc) Alive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, ok := p.table[pid]

	return ok && !p.zombies[pid]
}

// Zombie implements sys.Proc.
func (p *Proc) Zombie(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.zombies[pid]
}

func (p *Proc) deliver(pid int, sig syscall.Signal, group bool) error {
	kind := "proc.signal"
	if group {
		kind = "proc.signal-group"
	}

	p.rec.Recordf(kind, "%d %d", pid, int(sig))

	p.mu.Lock()
	defer p.mu.Unlock()

	p.signals = append(p.signals, Signal{PID: pid, Sig: sig, Group: group})

	if p.SignalErr != nil {
		if err := p.SignalErr(pid, sig, group); err != nil {
			return err
		}
	}

	if _, ok := p.table[pid]; !ok {
		return syscall.ESRCH
	}

	if sig == syscall.SIGTERM && p.SurviveTerm[pid] {
		return nil
	}

	if sig == syscall.SIGTERM || sig == syscall.SIGKILL {
		delete(p.table, pid)
	}

	return nil
}

// Signal implements sys.Proc.
func (p *Proc) Signal(pid int, sig syscall.Signal) error { return p.deliver(pid, sig, false) }

// SignalGroup implements sys.Proc.
func (p *Proc) SignalGroup(pgid int, sig syscall.Signal) error { return p.deliver(pgid, sig, true) }

// Pid implements sys.Proc.
func (p *Proc) Pid() int { return p.SelfPID }

// Ppid implements sys.Proc.
func (p *Proc) Ppid() int { return p.ParentPID }

// UID implements sys.Proc.
func (p *Proc) UID() int { return p.UserID }

// Umask implements sys.Proc.
func (p *Proc) Umask() os.FileMode { return p.UmaskValue }

// LookupGID implements sys.Proc.
func (p *Proc) LookupGID(group string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if gid, ok := p.gids[group]; ok {
		return gid, nil
	}

	return 0, fmt.Errorf("group: unknown group %s", group)
}
