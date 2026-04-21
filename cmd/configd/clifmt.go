// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// Shared column widths for cliProgress + cliStatus. Picked so that
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

// CLI color codes — only used when output is a TTY.
var (
	colorReset  = ""
	colorGreen  = ""
	colorRed    = ""
	colorYellow = ""
	colorCyan   = ""
	colorDim    = ""
)

func initCLIColors() {
	if isTTY(os.Stdout) {
		colorReset = "\033[0m"
		colorGreen = "\033[32m"
		colorRed = "\033[31m"
		colorYellow = "\033[33m"
		colorCyan = "\033[36m"
		colorDim = "\033[2m"
	}
}

// initCLILogging configures structured logging for CLI subcommands.
//
// Default is error-only + /dev/null output so a normal `zmcontrol stop`
// stays quiet and leaves the stdout surface exclusively for the user-facing
// cliProgress / cliStatus lines.
//
// Operators can raise verbosity on demand via CONFIGD_LOG_LEVEL (one of
// debug, info, warn, error) and CONFIGD_LOG_FORMAT (text or json) — the
// same knobs the daemon honors. When a level is set the output is routed to
// stderr so the logs appear alongside the CLI's normal output without
// interleaving them on stdout. This makes otherwise-hidden messages
// (e.g. the sd_notify "Graceful shutdown acknowledged" info log, the
// watchdog's restart decisions, legacy-mode diagnostics) visible without
// requiring code changes.
func initCLILogging() {
	cfg := logger.DefaultConfig()
	cfg.AddSource = false

	if _, override := os.LookupEnv("CONFIGD_LOG_LEVEL"); override {
		configureLogLevel(cfg)
		configureLogFormat(cfg)
		cfg.Output = os.Stderr
	} else {
		cfg.Level = logger.LogLevelError

		if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			cfg.Output = devNull
		}
	}

	logger.InitStructuredLogging(cfg)
	initCLIColors()
}

// cliProgress prints the label column (action + name) and returns a
// callback that fills the state + timing columns when the operation ends.
// Output aligns with cliStatus on the state/detail columns so start/stop
// rows line up with status rows.
func cliProgress(action, name string) func(err error) {
	label := name
	if action != "" {
		label = action + " " + name
	}

	fmt.Printf("\t%s", padRight(label, colLabelWidth))

	start := time.Now()

	return func(err error) {
		timing := fmt.Sprintf("(%s)", formatDuration(time.Since(start)))

		if err != nil {
			state := fmt.Sprintf("%sFailed%s", colorRed, colorReset)
			fmt.Printf(" %s %s%s%s\n", padRight(state, colStateWidth), colorDim, timing, colorReset)
			cliError(err)

			return
		}

		state := fmt.Sprintf("%sDone%s", colorGreen, colorReset)
		fmt.Printf(" %s %s%s%s\n", padRight(state, colStateWidth), colorDim, timing, colorReset)
	}
}

// cliStatus prints a service status row with the same columns as cliProgress
// (name in colLabelWidth, state in colStateWidth, optional dim detail).
func cliStatus(name string, running bool, detail string) {
	state := fmt.Sprintf("%sStopped%s", colorRed, colorReset)
	if running {
		state = fmt.Sprintf("%sRunning%s", colorGreen, colorReset)
	}

	if detail != "" {
		fmt.Printf("\t%s %s %s%s%s\n",
			padRight(name, colLabelWidth), padRight(state, colStateWidth),
			colorDim, detail, colorReset)

		return
	}

	fmt.Printf("\t%s %s\n", padRight(name, colLabelWidth), state)
}

// cliError prints a failure's details indented under the failing row.
// Each line of the error message is emitted as a separate dim bullet so a
// long multi-line systemctl error stays readable and visually scoped to
// its row. The message is light-parsed to strip the Go wrapping prefixes
// that are already redundant with the name column (e.g. "start service
// stats: failed to start stats (carbonio-stats.service):") and the
// trailing "exit status N" suffix that adds no operator-visible signal.
func cliError(err error) {
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

		fmt.Printf("\t   %s▸ %s%s\n", colorDim, line, colorReset)
	}
}

// cliWarn prints a warning message.
func cliWarn(format string, args ...any) {
	fmt.Printf("\t%sWARNING:%s ", colorYellow, colorReset)
	fmt.Printf(format, args...)
	fmt.Println()
}

// cliHeaderPrinted tracks whether the host header has already been emitted
// in this CLI invocation. `zmcontrol restart` runs controlStop then
// controlStart in sequence; each called cliHeader() unconditionally, which
// duplicated the "Host ..." line in the output.
var cliHeaderPrinted bool

// cliHeader prints the host header exactly once per CLI invocation.
func cliHeader() {
	if cliHeaderPrinted {
		return
	}

	cliHeaderPrinted = true

	hostname, _ := os.Hostname()
	fmt.Printf("Host %s%s%s\n", colorCyan, hostname, colorReset)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	return fmt.Sprintf("%.1fs", d.Seconds())
}
