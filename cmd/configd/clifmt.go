// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

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

// initCLILogging suppresses structured logs for CLI subcommands.
// Only errors are shown; info/debug/warn go to /dev/null.
func initCLILogging() {
	cfg := logger.DefaultConfig()
	cfg.Level = logger.LogLevelError
	cfg.AddSource = false

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		cfg.Output = devNull
	}

	logger.InitStructuredLogging(cfg)
	initCLIColors()
}

// cliProgress prints "Starting <name>..." and returns a function to print the result.
func cliProgress(action, name string) func(err error) {
	fmt.Printf("\t%s %s...", action, name)

	start := time.Now()

	return func(err error) {
		elapsed := time.Since(start)
		timing := fmt.Sprintf(" %s(%s)%s", colorDim, formatDuration(elapsed), colorReset)

		if err != nil {
			fmt.Printf("%sFailed.%s%s\n", colorRed, colorReset, timing)
			fmt.Printf("\t\t%v\n", err)
		} else {
			fmt.Printf("%sDone.%s%s\n", colorGreen, colorReset, timing)
		}
	}
}

// cliStatus prints a service status line with alignment and optional detail.
func cliStatus(name string, running bool, detail string) {
	status := fmt.Sprintf("%sStopped%s", colorRed, colorReset)
	if running {
		status = fmt.Sprintf("%sRunning%s", colorGreen, colorReset)
	}

	if detail != "" {
		fmt.Printf("\t%-20s %-10s %s%s%s\n", name, status, colorDim, detail, colorReset)
	} else {
		fmt.Printf("\t%-20s %s\n", name, status)
	}
}

// cliWarn prints a warning message.
func cliWarn(format string, args ...any) {
	fmt.Printf("\t%sWARNING:%s ", colorYellow, colorReset)
	fmt.Printf(format, args...)
	fmt.Println()
}

// cliHeader prints the host header.
func cliHeader() {
	hostname, _ := os.Hostname()
	fmt.Printf("Host %s%s%s\n", colorCyan, hostname, colorReset)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	return fmt.Sprintf("%.1fs", d.Seconds())
}
