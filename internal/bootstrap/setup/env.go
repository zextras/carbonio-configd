// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Paths are the files and directories the bootstrap uses.
type Paths struct {
	// Base is the Carbonio home, /opt/zextras.
	Base string
	// LocalConfig is conf/localconfig.xml.
	LocalConfig string
	// History is .install_history.
	History string
	// LicenseNotice is the licence notice carbonio-core installs (.mini_alue_ce);
	// it may be absent.
	LicenseNotice string
}

// DefaultPaths are the paths of a Carbonio node.
func DefaultPaths() Paths {
	base := config.BaseDir()

	return Paths{
		Base:        base,
		LocalConfig: filepath.Join(base, "conf", "localconfig.xml"),
		History:     filepath.Join(base, ".install_history"),

		LicenseNotice: filepath.Join(base, ".mini_alue_ce"),
	}
}

// Path joins elem to the Carbonio home.
func (p Paths) Path(elem ...string) string {
	return filepath.Join(append([]string{p.Base}, elem...)...)
}

// Note is a message a step printed for the operator.
type Note struct {
	// Warn is set for a warning, clear for plain progress.
	Warn bool
	Text string
}

// Log writes progress to the operator and details to the log file. Every
// line goes through redact.Text, so a secret that reaches a message by
// mistake is still masked.
type Log struct {
	mu   sync.Mutex
	out  io.Writer
	file io.Writer
	now  func() time.Time
	// notes, if set, receives the messages instead of out.
	notes func(Note)
	// warnings are the warnings of the step in progress.
	warnings []string
}

// NewLog returns a log that prints progress to out and writes everything,
// timestamped, to file (nil: no file).
func NewLog(out, file io.Writer) *Log {
	return &Log{out: out, file: file, now: time.Now}
}

// OnNote sends the messages to f, a message at a time, instead of the output
// writer; the log file gets them all the same.
func (l *Log) OnNote(f func(Note)) { l.notes = f }

// OpenLogFile creates the log file of a run in dir, mode 0600 from the
// start: bootstrap.<YYYYMMDD-HHMMSS>.log.
func OpenLogFile(dir string, now time.Time) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	name := filepath.Join(dir, "bootstrap."+now.Format("20060102-150405")+".log")

	return os.OpenFile(filepath.Clean(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0o600)
}

func (l *Log) write(toOut bool, format string, args ...any) {
	msg, _ := redact.Text(fmt.Sprintf(format, args...))

	l.mu.Lock()
	defer l.mu.Unlock()

	if toOut && l.out != nil {
		_, _ = fmt.Fprint(l.out, msg)
	}

	l.toFile(msg)
}

func (l *Log) toFile(msg string) {
	if l.file == nil {
		return
	}

	for line := range strings.SplitSeq(strings.TrimRight(msg, "\n"), "\n") {
		_, _ = fmt.Fprintf(l.file, "%s %s\n", l.now().Format(time.RFC3339), line)
	}
}

// note delivers a message to the operator: to the note handler when there is
// one, else as a line of the output writer.
func (l *Log) note(warn bool, format string, args ...any) {
	text, _ := redact.Text(fmt.Sprintf(format, args...))

	l.mu.Lock()
	defer l.mu.Unlock()

	if warn {
		l.warnings = append(l.warnings, text)
		l.toFile("Warning: " + text)
	} else {
		l.toFile(text)
	}

	switch {
	case l.notes != nil:
		l.notes(Note{Warn: warn, Text: text})
	case l.out != nil && warn:
		_, _ = fmt.Fprintf(l.out, "Warning: %s\n", text)
	case l.out != nil:
		_, _ = fmt.Fprintf(l.out, "%s\n", text)
	}
}

// takeWarnings returns the warnings since the last call.
func (l *Log) takeWarnings() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	w := l.warnings
	l.warnings = nil

	return w
}

// Info prints a line to the operator and the log file.
func (l *Log) Info(format string, args ...any) { l.note(false, format, args...) }

// Warn prints a warning to the operator and the log file; the step in
// progress ends with a warning.
func (l *Log) Warn(format string, args ...any) { l.note(true, format, args...) }

// Detail writes a line to the log file only.
func (l *Log) Detail(format string, args ...any) { l.write(false, format+"\n", args...) }

// Writer returns a writer that copies command output into the log file.
func (l *Log) Writer() io.Writer { return logWriter{l} }

type logWriter struct{ l *Log }

func (w logWriter) Write(p []byte) (int, error) {
	if s := strings.TrimRight(string(p), "\n"); s != "" {
		w.l.write(false, "%s\n", s)
	}

	return len(p), nil
}

// history appends to .install_history in the setup.pl format, which the
// package scripts and carbonio-core tools read: "<epoch>: CONFIGURED <stage>"
// and the session markers.
type history struct{ e *Env }

func (e *Env) history() history { return history{e} }

func (h history) append(line string) error {
	f, err := h.e.Sys.FS.OpenFile(h.e.Paths.History, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", h.e.Paths.History, err)
	}

	_, err = fmt.Fprintf(f, "%d: %s\n", h.e.Sys.Clock.Now().Unix(), line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}

func (h history) sessionStart() error {
	if err := h.append("CONFIG SESSION START"); err != nil {
		return err
	}

	return h.configured("BEGIN")
}

func (h history) sessionComplete() error {
	if err := h.configured("END"); err != nil {
		return err
	}

	return h.append("CONFIG SESSION COMPLETE")
}

func (h history) configured(stage string) error { return h.append("CONFIGURED " + stage) }
