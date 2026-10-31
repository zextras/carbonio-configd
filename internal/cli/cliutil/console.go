// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package cliutil holds what every "configd <command>" implementation under
// internal/cli shares: the console renderer for the zmcontrol-style tables,
// logging set-up for one-shot commands, the installation layout and the
// permission check. Commands write to the stdout/stderr they are given, so the
// bytes they print are plain values a test can compare.
package cliutil

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Shared column widths for Progress + Status. Picked so that
// "Starting directory server" (25 chars) fits comfortably and "Running"/
// "Stopped"/"Failed" all align identically across start/stop/status output.
const (
	colLabelWidth = 32
	colStateWidth = 8
)

// visibleLen returns the display width of s, ignoring ANSI CSI sequences
// (e.g. "\x1b[32m ... \x1b[0m"). Needed because Go's fmt '%-Ns' padding
// counts bytes, so a colored string passes through padding unchanged.
func visibleLen(s string) int {
	n, inEsc := 0, false

	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		default:
			n++
		}
	}

	return n
}

// padRight appends spaces to s until its visible width reaches width.
// No-op if s is already at or beyond width.
func padRight(s string, width int) string {
	if diff := width - visibleLen(s); diff > 0 {
		return s + strings.Repeat(" ", diff)
	}

	return s
}

// isTTY returns true if the writer is an interactive terminal.
func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}

		return fi.Mode()&os.ModeCharDevice != 0
	}

	return false
}

// palette holds the ANSI codes of one console; every field is empty unless the
// console's stdout is a terminal.
type palette struct {
	reset, green, red, yellow, cyan, dim string
}

func newPalette(stdout io.Writer) palette {
	if !isTTY(stdout) {
		return palette{}
	}

	return palette{
		reset:  "\033[0m",
		green:  "\033[32m",
		red:    "\033[31m",
		yellow: "\033[33m",
		cyan:   "\033[36m",
		dim:    "\033[2m",
	}
}

// Console renders the user-facing lines of a CLI command (start/stop progress,
// status rows, warnings) on the stdout and stderr of one invocation. Colors are
// used only when stdout is a terminal.
type Console struct {
	out, err      io.Writer
	colors        palette
	headerPrinted bool
}

// NewConsole returns a console writing to stdout and stderr.
func NewConsole(stdout, stderr io.Writer) *Console {
	return &Console{out: stdout, err: stderr, colors: newPalette(stdout)}
}

// Printf writes formatted text to stdout. Write errors are ignored, like the
// legacy tools' print statements.
func (c *Console) Printf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.out, format, a...)
}

// Println writes a line to stdout.
func (c *Console) Println(a ...any) {
	_, _ = fmt.Fprintln(c.out, a...)
}

// Errorf writes formatted text to stderr.
func (c *Console) Errorf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.err, format, a...)
}

// Progress prints the label column (action + name) and returns a
// callback that fills the state + timing columns when the operation ends.
// Output aligns with Status on the state/detail columns so start/stop
// rows line up with status rows.
func (c *Console) Progress(action, name string) func(err error) {
	label := name
	if action != "" {
		label = action + " " + name
	}

	c.Printf("\t%s", padRight(label, colLabelWidth))

	start := time.Now()

	return func(err error) {
		timing := fmt.Sprintf("(%s)", formatDuration(time.Since(start)))

		if err != nil {
			state := fmt.Sprintf("%sFailed%s", c.colors.red, c.colors.reset)
			c.Printf(" %s %s%s%s\n", padRight(state, colStateWidth), c.colors.dim, timing, c.colors.reset)
			c.Error(err)

			return
		}

		state := fmt.Sprintf("%sDone%s", c.colors.green, c.colors.reset)
		c.Printf(" %s %s%s%s\n", padRight(state, colStateWidth), c.colors.dim, timing, c.colors.reset)
	}
}

// Status prints a service status row with the same columns as Progress
// (name in colLabelWidth, state in colStateWidth, optional dim detail).
func (c *Console) Status(name string, running bool, detail string) {
	state := fmt.Sprintf("%sStopped%s", c.colors.red, c.colors.reset)
	if running {
		state = fmt.Sprintf("%sRunning%s", c.colors.green, c.colors.reset)
	}

	if detail != "" {
		c.Printf("\t%s %s %s%s%s\n",
			padRight(name, colLabelWidth), padRight(state, colStateWidth),
			c.colors.dim, detail, c.colors.reset)

		return
	}

	c.Printf("\t%s %s\n", padRight(name, colLabelWidth), state)
}

// Error prints a failure's details indented under the failing row.
// Each line of the error message is emitted as a separate dim bullet so a
// long multi-line systemctl error stays readable and visually scoped to
// its row. The message is light-parsed to strip the Go wrapping prefixes
// that are already redundant with the name column (e.g. "start service
// stats: failed to start stats (carbonio-stats.service):") and the
// trailing "exit status N" suffix that adds no operator-visible signal.
func (c *Console) Error(err error) {
	if err == nil {
		return
	}

	msg := err.Error()

	for _, pfx := range []string{
		"start service ",
		"stop service ",
		"failed to start ",
		"failed to stop ",
	} {
		if strings.HasPrefix(msg, pfx) {
			if idx := strings.Index(msg, ": "); idx != -1 {
				msg = msg[idx+2:]
			}
		}
	}

	if idx := strings.LastIndex(msg, ": exit status "); idx != -1 {
		msg = msg[:idx]
	}

	for raw := range strings.SplitSeq(msg, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		c.Printf("\t   %s▸ %s%s\n", c.colors.dim, line, c.colors.reset)
	}
}

// Warn prints a warning message.
func (c *Console) Warn(format string, args ...any) {
	c.Printf("\t%sWARNING:%s ", c.colors.yellow, c.colors.reset)
	c.Printf(format, args...)
	c.Println()
}

// Header prints the host header exactly once per console. `zmcontrol restart`
// runs a stop then a start in sequence and each of them asks for the header;
// printing it twice would duplicate the "Host ..." line.
func (c *Console) Header() {
	if c.headerPrinted {
		return
	}

	c.headerPrinted = true

	hostname, _ := os.Hostname()
	c.Printf("Host %s%s%s\n", c.colors.cyan, hostname, c.colors.reset)
}

// Heading prints a section title: a blank line, then the title in cyan.
func (c *Console) Heading(title string) {
	c.Printf("\n\t%s%s%s\n", c.colors.cyan, title, c.colors.reset)
}

// Note prints an indented dim line.
func (c *Console) Note(text string) {
	c.Printf("\t%s%s%s\n", c.colors.dim, text, c.colors.reset)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	return fmt.Sprintf("%.1fs", d.Seconds())
}
