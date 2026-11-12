// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"fmt"
	"log/syslog"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	// syslogIdent is the ident of Zextras::Mon::Logger.
	syslogIdent = "zimbramon"
	// syslogChunk is the longest message sent in one piece.
	syslogChunk = 800
)

// dialSyslog opens the connection of a facility; a seam for the tests.
var dialSyslog = syslog.New

// SystemSyslog is Zextras::Mon::Logger over the local syslog socket: Log is
// facility local0, Stats is local1. A syslog that cannot be reached is not an
// error (the Perl opens it with nofatal).
type SystemSyslog struct {
	pid int

	mu      sync.Mutex
	writers map[syslog.Priority]*syslog.Writer
}

// NewSystemSyslog returns the logger of a process with the given pid.
func NewSystemSyslog(pid int) *SystemSyslog {
	return &SystemSyslog{pid: pid, writers: map[syslog.Priority]*syslog.Writer{}}
}

// Log implements Syslog.
func (s *SystemSyslog) Log(level, msg string) { s.send(syslog.LOG_LOCAL0, level, msg) }

// Stats implements Syslog.
func (s *SystemSyslog) Stats(level, msg string) { s.send(syslog.LOG_LOCAL1, level, msg) }

func (s *SystemSyslog) send(facility syslog.Priority, level, msg string) {
	w := s.writer(facility)
	if w == nil {
		return
	}

	emit(func(m string) { writeLevel(w, level, m) }, s.pid, level, msg)
}

// writer is the connection of a facility, opened on first use.
func (s *SystemSyslog) writer(facility syslog.Priority) *syslog.Writer {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w := s.writers[facility]; w != nil {
		return w
	}

	w, err := dialSyslog(facility|syslog.LOG_INFO, syslogIdent)
	if err != nil {
		return nil // not cached: the next send retries
	}

	s.writers[facility] = w

	return w
}

func writeLevel(w *syslog.Writer, level, msg string) {
	switch level {
	case "debug":
		_ = w.Debug(msg)
	case "err":
		_ = w.Err(msg)
	case "crit":
		_ = w.Crit(msg)
	default:
		_ = w.Info(msg)
	}
}

// emit sends msg through send as Logger.pm does: "<pid>:<level>: <msg>", and a
// message over 800 characters in pieces chained by :::<uuid>::: markers.
func emit(send func(string), pid int, level, msg string) {
	if len(msg) <= syslogChunk {
		send(fmt.Sprintf("%d:%s: %s", pid, level, msg))

		return
	}

	var last string

	m := msg

	for {
		piece := m[:syslogChunk]
		m = m[syslogChunk:]

		if last != "" {
			piece = marker(last) + piece
		}

		last = strings.ToUpper(uuid.NewString())
		send(fmt.Sprintf("%d:%s: %s:::%s:::", pid, level, piece, last))

		if len(m) <= syslogChunk {
			break
		}
	}

	send(marker(last) + m)
}

// marker is the ":::<uuid>:::" that chains the pieces of a long message.
func marker(id string) string { return ":::" + id + ":::" }
