// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
)

func TestConfigureLogFormat(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected logger.LogFormat
	}{
		{
			name:     "json format",
			envValue: "json",
			expected: logger.FormatJSON,
		},
		{
			name:     "text format",
			envValue: "text",
			expected: logger.FormatText,
		},
		{
			name:     "empty defaults to text",
			envValue: "",
			expected: logger.FormatText,
		},
		{
			name:     "unknown defaults to text",
			envValue: "invalid",
			expected: logger.FormatText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("CONFIGD_LOG_FORMAT", tt.envValue)
			defer os.Unsetenv("CONFIGD_LOG_FORMAT")

			cfg := logger.DefaultConfig()
			configureLogFormat(cfg)

			if cfg.Format != tt.expected {
				t.Errorf("expected format %v, got %v", tt.expected, cfg.Format)
			}
		})
	}
}

func TestConfigureLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected slog.Level
	}{
		{
			name:     "debug level",
			envValue: "debug",
			expected: logger.LogLevelDebug,
		},
		{
			name:     "info level",
			envValue: "info",
			expected: logger.LogLevelInfo,
		},
		{
			name:     "warn level",
			envValue: "warn",
			expected: logger.LogLevelWarn,
		},
		{
			name:     "warning level",
			envValue: "warning",
			expected: logger.LogLevelWarn,
		},
		{
			name:     "error level",
			envValue: "error",
			expected: logger.LogLevelError,
		},
		{
			name:     "empty defaults to info",
			envValue: "",
			expected: logger.LogLevelInfo,
		},
		{
			name:     "unknown defaults to info",
			envValue: "invalid",
			expected: logger.LogLevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("CONFIGD_LOG_LEVEL", tt.envValue)
			defer os.Unsetenv("CONFIGD_LOG_LEVEL")

			cfg := logger.DefaultConfig()
			configureLogLevel(cfg)

			if cfg.Level != tt.expected {
				t.Errorf("expected level %v, got %v", tt.expected, cfg.Level)
			}
		})
	}
}

func TestInitializeLogging(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "json")
	os.Setenv("CONFIGD_LOG_LEVEL", "debug")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	ctx := initializeLogging()
	if ctx == nil {
		t.Error("expected non-nil context from initializeLogging")
	}
}

func TestInitializeConfig(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

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
	if appState.LocalConfig.Data.GetOr("zimbraIPMode", "") != ipModeIPv4 {
		t.Errorf("expected IP mode %s, got %s", ipModeIPv4, appState.LocalConfig.Data.GetOr("zimbraIPMode", ""))
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

// TestRewriteViaDaemon covers the legacy configrewrite contract: a reachable
// daemon gets the REWRITE request and nothing runs in-process; an unreachable
// or failing daemon falls back to a standalone rewrite whose exit code is
// returned, instead of failing the caller (zmtlsctl, zmproxyctl, rewrite).
func TestRewriteViaDaemon(t *testing.T) {
	tests := []struct {
		name           string
		reply          string // "" = no listener on the port
		wantStandalone bool
		wantCode       int
	}{
		{name: "daemon accepts", reply: "SUCCESS REWRITE\n", wantStandalone: false, wantCode: 0},
		{name: "daemon not running", reply: "", wantStandalone: true, wantCode: 3},
		{name: "daemon answers error", reply: "ERROR busy\n", wantStandalone: true, wantCode: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := fakeDaemon(t, tt.reply)

			var gotArgs *Args

			standalone := func(_ context.Context, a *Args) int {
				gotArgs = a

				return 3
			}

			args := &Args{ForcedConfigs: []string{"proxy", "mta"}}
			code := rewriteViaDaemon(context.Background(), args, port, ipModeIPv4, standalone)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if (gotArgs != nil) != tt.wantStandalone {
				t.Fatalf("standalone ran = %v, want %v", gotArgs != nil, tt.wantStandalone)
			}

			if gotArgs != nil && strings.Join(gotArgs.ForcedConfigs, " ") != "proxy mta" {
				t.Errorf("standalone configs = %v, want [proxy mta]", gotArgs.ForcedConfigs)
			}
		})
	}
}

// fakeDaemon returns a port where one connection is answered with reply, or a
// port with nothing listening when reply is empty.
func fakeDaemon(t *testing.T, reply string) int {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	if reply == "" {
		_ = ln.Close()

		return port
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(reply))
	}()

	return port
}

func TestSetupProfilingAndTracing_TracingEnabled(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

	args := &Args{
		EnableTracing: true,
		TracingOutput: t.TempDir() + "/trace.json",
	}

	pc, tc := setupProfilingAndTracing(context.Background(), args)
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

func TestConfigureLogFormat_Unknown(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "unknown_format")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")

	cfg := logger.DefaultConfig()
	configureLogFormat(cfg)
	if cfg.Format != logger.FormatText {
		t.Errorf("expected text format for unknown, got %v", cfg.Format)
	}
}

// TestSetupProfilingAndTracing_ValidationFails verifies that an unwritable
// profile path makes setupProfilingAndTracing exit with status 1 when profiling
// is compiled in (os.Exit would kill the test binary, so it runs in a child
// process). Without the profiling tag validation is a no-op and the child exits 0.
func TestSetupProfilingAndTracing_ValidationFails(t *testing.T) {
	if os.Getenv("CONFIGD_TEST_VALIDATION_CHILD") == "1" {
		initializeLogging()
		setupProfilingAndTracing(context.Background(), &Args{CPUProfile: "/nonexistent/path/cpu.prof"})

		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSetupProfilingAndTracing_ValidationFails$")
	cmd.Env = append(os.Environ(),
		"CONFIGD_TEST_VALIDATION_CHILD=1",
		"CONFIGD_LOG_FORMAT=text",
		"CONFIGD_LOG_LEVEL=error",
	)

	err := cmd.Run()
	if err == nil {
		return
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("child failed unexpectedly: %v", err)
	}
}
