// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestInitializeConfig(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	cliutil.InitLogging(os.Stderr)

	mainCfg, appState, ldapClient := initializeConfig()

	if mainCfg == nil {
		t.Error("expected non-nil config")
	}
	if ldapClient == nil {
		t.Error("expected non-nil ldap client")
	}

	snap := appState.Snapshot()
	if port, _ := snap.Local("zmconfigd_listen_port"); port != "7171" {
		t.Errorf("expected listen port 7171, got %s", port)
	}
	if mode, _ := snap.Local("zimbraIPMode"); mode != rewrite.IPModeIPv4 {
		t.Errorf("expected IP mode %s, got %s", rewrite.IPModeIPv4, mode)
	}
}

func TestPerlArchname(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "standard perl output",
			output:   "archname='x86_64-linux-thread-multi';\n",
			expected: "x86_64-linux-thread-multi",
		},
		{name: "no quotes", output: "archname=\n", expected: ""},
		{name: "single quote only start", output: "archname='value\n", expected: ""},
		{name: "empty quotes", output: "archname='';\n", expected: ""},
		{
			name:     "multi segment",
			output:   "archname='aarch64-linux-thread-multi';\n",
			expected: "aarch64-linux-thread-multi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fakes := systest.New()
			fakes.Exec.OnPath("/usr/bin/perl", systest.Reply(tt.output))

			if got := perlArchname(fakes.System()); got != tt.expected {
				t.Errorf("perlArchname() = %q, want %q", got, tt.expected)
			}

			if want := []string{"exec /usr/bin/perl -V:archname"}; !slices.Equal(fakes.Rec.Strings(), want) {
				t.Errorf("ops = %v, want %v", fakes.Rec.Strings(), want)
			}
		})
	}
}

func TestPerlArchname_PerlFails(t *testing.T) {
	t.Parallel()

	fakes := systest.New()
	fakes.Exec.OnPath("/usr/bin/perl", systest.Fail(2, "boom"))

	if got := perlArchname(fakes.System()); got != "" {
		t.Errorf("perlArchname() = %q, want empty when perl fails", got)
	}
}

func TestEnsureZextrasPerlEnv_PerlAlreadySet(t *testing.T) {
	t.Setenv("PERL5LIB", "/existing/path")
	t.Setenv("PERLLIB", "")

	fakes := systest.New()
	ensureZextrasPerlEnv(fakes.System())

	if got := os.Getenv("PERLLIB"); got != "" {
		t.Errorf("PERLLIB = %q, want it untouched when PERL5LIB is already set", got)
	}

	if ops := fakes.Rec.Strings(); len(ops) != 0 {
		t.Errorf("perl must not be run when PERL5LIB is set, got %v", ops)
	}
}

func TestEnsureZextrasPerlEnv_PerlNotAvailable(t *testing.T) {
	unsetEnv(t, "PERL5LIB")
	unsetEnv(t, "PERLLIB")

	fakes := systest.New()
	fakes.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/usr/bin/perl" }, os.ErrNotExist)

	ensureZextrasPerlEnv(fakes.System())

	if got := os.Getenv("PERL5LIB"); got != "" {
		t.Errorf("PERL5LIB = %q, want it unset when perl is missing", got)
	}

	if got := os.Getenv("PERLLIB"); got != "" {
		t.Errorf("PERLLIB = %q, want it unset when perl is missing", got)
	}
}

func TestEnsureZextrasPerlEnv_PerlSetsVars(t *testing.T) {
	unsetEnv(t, "PERL5LIB")
	unsetEnv(t, "PERLLIB")

	fakes := systest.New()
	fakes.Exec.OnPath("/usr/bin/perl", systest.Reply("archname='x86_64-linux-thread-multi';\n"))

	ensureZextrasPerlEnv(fakes.System())

	want := cliutil.BasePath("common", "lib", "perl5", "x86_64-linux-thread-multi") + ":" +
		cliutil.BasePath("common", "lib", "perl5")

	if got := os.Getenv("PERL5LIB"); got != want {
		t.Errorf("PERL5LIB = %q, want %q", got, want)
	}

	if got := os.Getenv("PERLLIB"); got != want {
		t.Errorf("PERLLIB = %q, want %q", got, want)
	}
}

// unsetEnv removes key for the duration of the test and restores it after.
func unsetEnv(t *testing.T, key string) {
	t.Helper()

	t.Setenv(key, "")

	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func TestSetupProfilingAndTracing_TracingEnabled(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	cliutil.InitLogging(os.Stderr)

	args := &Args{
		EnableTracing: true,
		TracingOutput: t.TempDir() + "/trace.json",
	}

	pc, tc, err := setupProfilingAndTracing(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pc != nil {
		t.Error("expected nil ProfilingConfig when only tracing enabled")
	}
	if tc == nil {
		t.Error("expected non-nil TracingConfig when tracing enabled")
	}

	StopTracing(tc)
}

// TestSetupProfilingAndTracing_ValidationFails verifies that an unwritable
// profile path makes setupProfilingAndTracing return the validation error (the
// daemon then exits with status 1) when profiling is compiled in. Without the
// profiling tag validation is a no-op and nothing fails.
func TestSetupProfilingAndTracing_ValidationFails(t *testing.T) {
	cliutil.InitLogging(os.Stderr)

	badPath := "/nonexistent/path/cpu.prof"
	wantErr := ValidateProfilingConfig(&ProfilingConfig{CPUProfilePath: badPath}) != nil

	pc, tc, err := setupProfilingAndTracing(context.Background(), &Args{CPUProfile: badPath})

	if (err != nil) != wantErr {
		t.Fatalf("setupProfilingAndTracing error = %v, want error: %v", err, wantErr)
	}

	if wantErr && (pc != nil || tc != nil) {
		t.Errorf("configs on failure = (%v, %v), want nil", pc, tc)
	}
}
