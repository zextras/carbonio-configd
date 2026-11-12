// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// logFile is the CSV a collector appends to: Stat.pm openLogFile and
// rotateLogFile. The console (path "" or "-") is standard output and is never
// rotated.
type logFile struct {
	env     *Env
	path    string
	heading string
	f       *os.File
}

// openLog is openLogFile: it creates the directory, rotates a file whose last
// timestamp is not today, appends and writes the heading (again on every
// open, as the Perl does).
func openLog(env *Env, path, heading string, now func() string) (*logFile, error) {
	l := &logFile{env: env, path: path, heading: heading}

	if path == "" || path == "-" {
		l.path = ""
		_, err := fmt.Fprintln(env.Stdout, heading)

		return l, err
	}

	if err := l.ensureDir(); err != nil {
		return nil, err
	}

	if err := l.rotateStale(now()); err != nil {
		return nil, err
	}

	//nolint:gosec // the log named on the command line
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return nil, fmt.Errorf("unable to open log file %s: %w", path, err)
	}

	l.f = f

	if _, err := fmt.Fprintln(f, heading); err != nil {
		_ = f.Close()

		return nil, err
	}

	return l, nil
}

func (l *logFile) ensureDir() error {
	dir := filepath.Dir(l.path)
	if _, err := os.Stat(dir); err == nil {
		return nil
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("unable to create log directory %s: %w", dir, err)
	}

	l.chown(dir)

	return nil
}

// chown hands path to zextras, as the Perl does; a failure is not an error.
func (l *logFile) chown(path string) {
	if l.env.Chown != nil {
		_ = l.env.Chown(path)
	}
}

// rotateStale pre-rotates a file that still holds the data of another day
// into <dir>/<that day>/. A file without a timestamp line (empty, or only a
// heading) is not stale: the Perl would gzip it onto itself.
func (l *logFile) rotateStale(today string) error {
	st, err := os.Stat(l.path)
	if err != nil || !st.Mode().IsRegular() {
		return nil //nolint:nilerr // no regular file, nothing to rotate
	}

	date, err := lastDate(l.path)
	if err != nil {
		return fmt.Errorf("unable to read existing logfile: %w", err)
	}

	if date == "" || date == today {
		return nil
	}

	_, _ = fmt.Fprintf(l.env.Stderr, "%s was stale (%s) pre-rotating\n", l.path, date)

	return l.rotateFile(date)
}

// lastDate is the YYYY-MM-DD of the last line that starts with MM/DD/YYYY.
func lastDate(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the log named on the command line
	if err != nil {
		return "", err //nolint:wrapcheck // the caller names the file
	}

	defer func() { _ = f.Close() }()

	var date string

	r := bufio.NewReader(f)

	for {
		line, err := r.ReadString('\n')
		if d, ok := lineDate(line); ok {
			date = d
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return date, nil
			}

			return "", err //nolint:wrapcheck // the caller names the file
		}
	}
}

// lineDate parses the MM/DD/YYYY a CSV line starts with into YYYY-MM-DD.
func lineDate(line string) (string, bool) {
	if len(line) < 10 || line[2] != '/' || line[5] != '/' {
		return "", false
	}

	for _, i := range []int{0, 1, 3, 4, 6, 7, 8, 9} {
		if line[i] < '0' || line[i] > '9' {
			return "", false
		}
	}

	return line[6:10] + "-" + line[0:2] + "-" + line[3:5], true
}

// write appends the lines (each followed by a newline) to the file.
func (l *logFile) write(lines []string) error {
	text := strings.Join(lines, "\n") + "\n"

	if l.f == nil {
		_, err := io.WriteString(l.env.Stdout, text)

		return err //nolint:wrapcheck // plain write error
	}

	_, err := l.f.WriteString(text)

	return err //nolint:wrapcheck // plain write error
}

// rotate is rotateLogFile followed by the reopen of openLogFile: the file
// goes to <dir>/<date>/<name>.gz (appended to an existing one) and a new file
// starts with the heading. The console is not rotated.
func (l *logFile) rotate(date string) error {
	if l.f == nil {
		return nil
	}

	_ = l.f.Close()
	l.f = nil

	if err := l.rotateFile(date); err != nil {
		return err
	}

	n, err := openLog(l.env, l.path, l.heading, func() string { return date })
	if err != nil {
		return err
	}

	l.f = n.f

	return nil
}

func (l *logFile) close() {
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
}

// rotateFile moves the log to <dir>/<date>/<name>.gz. When that gz (or a bare
// copy) is already there, the old content comes first, as `cat old new | gzip`.
func (l *logFile) rotateFile(date string) error {
	dir, name := filepath.Split(l.path)
	rdir := filepath.Join(dir, date)

	_ = os.Mkdir(rdir, 0o755)

	if st, err := os.Stat(rdir); err != nil || !st.IsDir() {
		return fmt.Errorf("unable to create log rotation directory %s", rdir)
	}

	l.chown(rdir)

	bare := filepath.Join(rdir, name)
	gz := bare + ".gz"

	tmp := gz + ".tmp"
	if err := l.writeGz(tmp, gz, bare); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("unable to move %s to %s: %w", l.path, gz, err)
	}

	if err := os.Rename(tmp, gz); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("unable to move %s to %s: %w", l.path, gz, err)
	}

	l.chown(gz)

	_ = os.Remove(bare)

	return os.Remove(l.path)
}

// writeGz writes tmp as the gzip of the old rotated content (gz, else the
// bare file) followed by the log.
func (l *logFile) writeGz(tmp, gz, bare string) error {
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666) //nolint:gosec // beside the rotated log
	if err != nil {
		return err //nolint:wrapcheck // the caller names the files
	}

	zw := gzip.NewWriter(out)

	if err := copyOld(zw, gz, bare); err != nil {
		_ = out.Close()

		return err
	}

	if err := appendFile(zw, l.path); err != nil {
		_ = out.Close()

		return err
	}

	if err := zw.Close(); err != nil {
		_ = out.Close()

		return err //nolint:wrapcheck // the caller names the files
	}

	return out.Close() //nolint:wrapcheck // the caller names the files
}

// copyOld streams the earlier rotation of the day into w: the gz when there is
// one (an unreadable gz is dropped, as gzip -d failing is ignored), else the
// bare file.
func copyOld(w io.Writer, gz, bare string) error {
	if f, err := os.Open(gz); err == nil { //nolint:gosec // our own rotated log
		defer func() { _ = f.Close() }()

		if zr, zerr := gzip.NewReader(f); zerr == nil {
			_, _ = io.Copy(w, zr) //nolint:gosec // our own rotated log
		}

		return nil
	}

	if _, err := os.Stat(bare); err != nil {
		return nil //nolint:nilerr // nothing rotated before
	}

	return appendFile(w, bare)
}

func appendFile(w io.Writer, path string) error {
	f, err := os.Open(path) //nolint:gosec // the log named on the command line
	if err != nil {
		return err //nolint:wrapcheck // the caller names the files
	}

	defer func() { _ = f.Close() }()

	_, err = io.Copy(w, f)

	return err //nolint:wrapcheck // the caller names the files
}
