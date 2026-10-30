// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestConsole() (c *Console, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}

	return NewConsole(stdout, stderr), stdout, stderr
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{150 * time.Millisecond, "150ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{1500 * time.Millisecond, "1.5s"},
		{12300 * time.Millisecond, "12.3s"},
	}

	for _, tt := range tests {
		if got := formatDuration(tt.d); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestVisibleLenAndPadRight(t *testing.T) {
	colored := "\x1b[32mRunning\x1b[0m"

	if got := visibleLen(colored); got != len("Running") {
		t.Errorf("visibleLen(colored) = %d, want %d", got, len("Running"))
	}

	if got := padRight(colored, 10); visibleLen(got) != 10 {
		t.Errorf("padRight width = %d, want 10", visibleLen(got))
	}

	if got := padRight("longer than width", 4); got != "longer than width" {
		t.Errorf("padRight must not truncate, got %q", got)
	}
}

func TestIsTTY(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if isTTY(f) {
		t.Error("a regular file is not a TTY")
	}

	if isTTY(&bytes.Buffer{}) {
		t.Error("a non-file writer is not a TTY")
	}
}

func TestNewConsole_NoColorsOffTerminal(t *testing.T) {
	c, _, _ := newTestConsole()

	if c.colors != (palette{}) {
		t.Errorf("colors = %+v, want none when stdout is not a terminal", c.colors)
	}
}

func TestConsoleStatus(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		detail  string
		want    string
	}{
		{"running", true, "", "\trunning-svc" + strings.Repeat(" ", colLabelWidth-len("running-svc")) + " Running\n"},
		{"stopped", false, "", "\tstopped-svc" + strings.Repeat(" ", colLabelWidth-len("stopped-svc")) + " Stopped\n"},
		{
			"detail", true, "(pid 1234)",
			"\tdetail-svc" + strings.Repeat(" ", colLabelWidth-len("detail-svc")) + " Running  (pid 1234)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, stdout, stderr := newTestConsole()
			c.Status(tt.name+"-svc", tt.running, tt.detail)

			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestConsoleWarn(t *testing.T) {
	c, stdout, _ := newTestConsole()
	c.Warn("disk space %dMB available", 42)

	if want := "\tWARNING: disk space 42MB available\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestConsoleHeaderPrintedOncePerConsole(t *testing.T) {
	c, stdout, _ := newTestConsole()
	c.Header()
	c.Header()

	host, _ := os.Hostname()
	if want := "Host " + host + "\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want one line %q", stdout.String(), want)
	}

	c2, stdout2, _ := newTestConsole()
	c2.Header()

	if stdout2.Len() == 0 {
		t.Error("a new console must print its own header")
	}
}

func TestConsoleProgress(t *testing.T) {
	c, stdout, _ := newTestConsole()
	c.Progress("Starting", "TestService")(nil)

	out := stdout.String()
	if !strings.HasPrefix(out, "\tStarting TestService") || !strings.Contains(out, " Done ") {
		t.Errorf("success row = %q", out)
	}

	c, stdout, stderr := newTestConsole()
	c.Progress("Stopping", "BadService")(errors.New("stop service bad: failed to stop bad: unit not found: exit status 5"))

	out = stdout.String()
	if !strings.Contains(out, " Failed ") {
		t.Errorf("failure row = %q, want Failed state", out)
	}

	if !strings.Contains(out, "\n\t   ▸ unit not found\n") {
		t.Errorf("failure row = %q, want the unwrapped error bullet", out)
	}

	if stderr.Len() != 0 {
		t.Errorf("progress must not use stderr, got %q", stderr.String())
	}
}

func TestConsoleErrorBullets(t *testing.T) {
	c, stdout, _ := newTestConsole()
	c.Error(nil)
	c.Error(errors.New("first\n\n  second  \n"))

	if want := "\t   ▸ first\n\t   ▸ second\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestConsoleHeadingAndNote(t *testing.T) {
	c, stdout, _ := newTestConsole()
	c.Heading("Carbonio Advanced")
	c.Note("module status unavailable")

	if want := "\n\tCarbonio Advanced\n\tmodule status unavailable\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestConsoleStderrHelpers(t *testing.T) {
	c, stdout, stderr := newTestConsole()
	c.Errorf("Error: %v\n", "boom")

	if stderr.String() != "Error: boom\n" || stdout.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}
