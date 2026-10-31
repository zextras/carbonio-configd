// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package sys is the single side-effect boundary of configd: everything that
// touches the host — spawning processes, writing files, reading the process
// table, sending signals, waiting for time to pass — goes through one of four
// small interfaces (Exec, FS, Proc, Clock) bundled in System.
//
// Production wiring builds Real() once (cmd/configd) and injects it through
// constructors and struct fields; tests inject the recording fakes of
// internal/sys/systest instead of mutating package variables.
//
// Deliberately outside the boundary: network I/O (LDAP, the 7171 listener,
// the sd_notify unix datagram socket), environment variables, and wall-clock
// reads used only to log how long something took. They are not host
// mutations that tests need to intercept and ordering-assert.
package sys

// System bundles the four side-effect boundaries. The zero value is unusable;
// build one with Real or systest.New.
type System struct {
	Exec  Exec
	FS    FS
	Proc  Proc
	Clock Clock
}

// ProcRoot is the production process-table mount point.
const ProcRoot = "/proc/"

// Real returns the production implementations: os/exec, the os filesystem,
// the Linux /proc table and the system clock.
func Real() System {
	return System{
		Exec:  RealExec{},
		FS:    RealFS{},
		Proc:  NewRealProc(ProcRoot),
		Clock: RealClock{},
	}
}
