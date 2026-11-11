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
}

// DefaultPaths are the paths of a Carbonio node.
func DefaultPaths() Paths {
	base := config.BaseDir()

	return Paths{
		Base:        base,
		LocalConfig: filepath.Join(base, "conf", "localconfig.xml"),
		History:     filepath.Join(base, ".install_history"),
	}
}

// Path joins elem to the Carbonio home.
func (p Paths) Path(elem ...string) string {
	return filepath.Join(append([]string{p.Base}, elem...)...)
}

// Log writes progress to the operator and details to the log file. Every
// line goes through redact.Text, so a secret that reaches a message by
// mistake is still masked.
type Log struct {
	mu   sync.Mutex
	out  io.Writer
	file io.Writer
	now  func() time.Time
}

// NewLog returns a log that prints progress to out and writes everything,
// timestamped, to file (nil: no file).
func NewLog(out, file io.Writer) *Log {
	return &Log{out: out, file: file, now: time.Now}
}

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

	if l.file != nil {
		for line := range strings.SplitSeq(strings.TrimRight(msg, "\n"), "\n") {
			_, _ = fmt.Fprintf(l.file, "%s %s\n", l.now().Format(time.RFC3339), line)
		}
	}
}

// Info prints a line to the operator and the log file.
func (l *Log) Info(format string, args ...any) { l.write(true, format+"\n", args...) }

// Warn prints a warning to the operator and the log file.
func (l *Log) Warn(format string, args ...any) { l.write(true, "Warning: "+format+"\n", args...) }

// Detail writes a line to the log file only.
func (l *Log) Detail(format string, args ...any) { l.write(false, format+"\n", args...) }

// Begin prints "title..." and returns the function that ends the line with
// "done." or "failed.".
func (l *Log) Begin(title string) func(error) {
	l.write(true, "%s...", title)

	return func(err error) {
		if err != nil {
			l.write(true, "failed.\n")

			return
		}

		l.write(true, "done.\n")
	}
}

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
