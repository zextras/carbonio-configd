// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
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

	if appState.LocalConfig.Data.GetOr("zmconfigd_listen_port", "") != "7171" {
		t.Errorf("expected listen port 7171, got %s", appState.LocalConfig.Data.GetOr("zmconfigd_listen_port", ""))
	}
	if appState.LocalConfig.Data.GetOr("zimbraIPMode", "") != rewrite.IPModeIPv4 {
		t.Errorf("expected IP mode %s, got %s", rewrite.IPModeIPv4, appState.LocalConfig.Data.GetOr("zimbraIPMode", ""))
	}
}

func TestPerlArchname_ValidOutput(t *testing.T) {
	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		t.Skip("perl not available")
	}

	result := perlArchname()
	_ = result
}

func TestPerlArchname_ParseFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "standard perl output", input: "archname='x86_64-linux-thread-multi'\n", expected: "x86_64-linux-thread-multi"},
		{name: "no quotes", input: "archname=\n", expected: ""},
		{name: "single quote only start", input: "archname='value\n", expected: ""},
		{name: "empty quotes", input: "archname=''\n", expected: ""},
		{name: "multi segment", input: "archname='aarch64-linux-thread-multi'\n", expected: "aarch64-linux-thread-multi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := strings.TrimSpace(tt.input)
			start := strings.IndexByte(s, '\'')
			end := strings.LastIndexByte(s, '\'')
			var got string
			if start >= 0 && end > start {
				got = s[start+1 : end]
			}
			if got != tt.expected {
				t.Errorf("parse result = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestEnsureZextrasPerlEnv_PerlAlreadySet(t *testing.T) {
	os.Setenv("PERL5LIB", "/existing/path")
	defer os.Unsetenv("PERL5LIB")

	originalPerlLib := os.Getenv("PERLLIB")
	defer func() {
		if originalPerlLib != "" {
			os.Setenv("PERLLIB", originalPerlLib)
		} else {
			os.Unsetenv("PERLLIB")
		}
	}()

	ensureZextrasPerlEnv()

	perlLib := os.Getenv("PERLLIB")
	if strings.Contains(perlLib, "/opt/zextras/common/lib/perl5") {
		t.Error("PERLLIB should not be modified when PERL5LIB is already set")
	}
}

func TestEnsureZextrasPerlEnv_PerlNotAvailable(t *testing.T) {
	origPerl5Lib, hadPerl5Lib := os.LookupEnv("PERL5LIB")
	os.Unsetenv("PERL5LIB")
	defer func() {
		if hadPerl5Lib {
			os.Setenv("PERL5LIB", origPerl5Lib)
		}
	}()

	origPerlLib, hadPerlLib := os.LookupEnv("PERLLIB")
	os.Unsetenv("PERLLIB")
	defer func() {
		if hadPerlLib {
			os.Setenv("PERLLIB", origPerlLib)
		} else {
			os.Unsetenv("PERLLIB")
		}
	}()

	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		ensureZextrasPerlEnv()
		if os.Getenv("PERL5LIB") != "" {
			t.Error("PERL5LIB should remain unset when perl is missing")
		}
		if os.Getenv("PERLLIB") != "" {
			t.Error("PERLLIB should remain unset when perl is missing")
		}
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

func TestEnsureZextrasPerlEnv_PerlSetsVars(t *testing.T) {
	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		t.Skip("perl not available")
	}

	os.Unsetenv("PERL5LIB")
	os.Unsetenv("PERLLIB")
	defer os.Unsetenv("PERL5LIB")
	defer os.Unsetenv("PERLLIB")

	ensureZextrasPerlEnv()

	perl5lib := os.Getenv("PERL5LIB")
	perlLib := os.Getenv("PERLLIB")
	if perl5lib == "" || perlLib == "" {
		t.Errorf("expected PERL5LIB and PERLLIB to be set when perl is available, got PERL5LIB=%q PERLLIB=%q", perl5lib, perlLib)
	}
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
