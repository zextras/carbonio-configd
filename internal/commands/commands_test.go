// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Initialize commands before running tests
	Initialize()
	os.Exit(m.Run())
}

func TestNewCommand(t *testing.T) {
	tests := []struct {
		name        string
		desc        string
		cmdName     string
		cmd         string
		fn          func(context.Context, ...string) (string, error)
		args        []string
		wantDesc    string
		wantCmdName string
		wantCmd     string
	}{
		{
			name:        "external command",
			desc:        "Test external command",
			cmdName:     "test",
			cmd:         "echo %s",
			fn:          nil,
			args:        []string{"arg1"},
			wantDesc:    "Test external command",
			wantCmdName: "test",
			wantCmd:     "echo %s",
		},
		{
			name:        "function command",
			desc:        "Test function command",
			cmdName:     "testfn",
			cmd:         "",
			fn:          func(ctx context.Context, args ...string) (string, error) { return "result", nil },
			args:        []string{"arg1"},
			wantDesc:    "Test function command",
			wantCmdName: "testfn",
			wantCmd:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(tt.desc, tt.cmdName, tt.cmd, tt.fn, tt.args...)
			if cmd.Desc != tt.wantDesc {
				t.Errorf("NewCommand().Desc = %v, want %v", cmd.Desc, tt.wantDesc)
			}
			if cmd.Name != tt.wantCmdName {
				t.Errorf("NewCommand().Name = %v, want %v", cmd.Name, tt.wantCmdName)
			}
			if cmd.Cmd != tt.wantCmd {
				t.Errorf("NewCommand().Cmd = %v, want %v", cmd.Cmd, tt.wantCmd)
			}
			if cmd.Status != 0 {
				t.Errorf("NewCommand().Status = %v, want %v", cmd.Status, 0)
			}
			if cmd.Output != "" {
				t.Errorf("NewCommand().Output = %v, want empty", cmd.Output)
			}
			if cmd.Error != "" {
				t.Errorf("NewCommand().Error = %v, want empty", cmd.Error)
			}
		})
	}
}

func TestCommand_resetState(t *testing.T) {
	cmd := NewCommand("test", "test", "echo test", nil)
	cmd.Status = 1
	cmd.Output = "some output"
	cmd.Error = "some error"

	cmd.resetState()

	if cmd.Status != 0 {
		t.Errorf("resetState().Status = %v, want %v", cmd.Status, 0)
	}
	if cmd.Output != "" {
		t.Errorf("resetState().Output = %v, want empty", cmd.Output)
	}
	if cmd.Error != "" {
		t.Errorf("resetState().Error = %v, want empty", cmd.Error)
	}
}

func TestCommand_String(t *testing.T) {
	tests := []struct {
		name string
		cmd  *Command
		want string
	}{
		{
			name: "external command",
			cmd:  NewCommand("test", "echo", "echo %s", nil),
			want: "echo echo %s",
		},
		{
			name: "function command",
			cmd:  NewCommand("test", "testfn", "", func(ctx context.Context, args ...string) (string, error) { return "", nil }, "arg1"),
			want: "testfn testfn([arg1])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cmd.String(); got != tt.want {
				t.Errorf("Command.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCommand_Execute_ExternalCommand(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantStatus int
		wantOutput string
		wantError  string
	}{
		{
			name:       "successful echo command",
			cmd:        "echo %s",
			args:       []string{"hello"},
			wantStatus: 0,
			wantOutput: "hello\n",
			wantError:  "OK",
		},
		{
			name:       "command with no args",
			cmd:        "echo hello",
			args:       []string{},
			wantStatus: 0,
			wantOutput: "hello\n",
			wantError:  "OK",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand("test", "test", tt.cmd, nil)
			status, output, err := cmd.Execute(context.Background(), tt.args...)

			if status != tt.wantStatus {
				t.Errorf("Execute() status = %v, want %v", status, tt.wantStatus)
			}
			if output != tt.wantOutput {
				t.Errorf("Execute() output = %v, want %v", output, tt.wantOutput)
			}
			if !strings.Contains(err, tt.wantError) {
				t.Errorf("Execute() error = %v, want to contain %v", err, tt.wantError)
			}
		})
	}
}

func TestCommand_Execute_FunctionCommand(t *testing.T) {
	tests := []struct {
		name       string
		fn         func(context.Context, ...string) (string, error)
		args       []string
		wantStatus int
		wantOutput string
		wantError  string
	}{
		{
			name: "successful function",
			fn: func(ctx context.Context, args ...string) (string, error) {
				return "function result", nil
			},
			args:       []string{"arg1"},
			wantStatus: 0,
			wantOutput: "function result",
			wantError:  "OK",
		},
		{
			name: "function error",
			fn: func(ctx context.Context, args ...string) (string, error) {
				return "", fmt.Errorf("function error")
			},
			args:       []string{"arg1"},
			wantStatus: 1,
			wantOutput: "",
			wantError:  "function error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand("test", "test", "", tt.fn)
			status, output, err := cmd.Execute(context.Background(), tt.args...)

			if status != tt.wantStatus {
				t.Errorf("Execute() status = %v, want %v", status, tt.wantStatus)
			}
			if output != tt.wantOutput {
				t.Errorf("Execute() output = %v, want %v", output, tt.wantOutput)
			}
			if !strings.Contains(err, tt.wantError) {
				t.Errorf("Execute() error = %v, want to contain %v", err, tt.wantError)
			}
		})
	}
}

func TestCommand_Execute_UnknownOutput(t *testing.T) {
	cmd := NewCommand("test", "test", "", func(ctx context.Context, args ...string) (string, error) {
		return "", nil // Empty output
	})

	status, output, err := cmd.Execute(context.Background())

	if status != 0 {
		t.Errorf("Execute() status = %v, want %v", status, 0)
	}
	if output != "UNKNOWN OUTPUT" {
		t.Errorf("Execute() output = %v, want %v", output, "UNKNOWN OUTPUT")
	}
	if err != "OK" {
		t.Errorf("Execute() error = %v, want %v", err, "OK")
	}
}

func TestCommand_Execute_Timing(t *testing.T) {
	cmd := NewCommand("test", "test", "", func(ctx context.Context, args ...string) (string, error) {
		time.Sleep(10 * time.Millisecond) // Small delay
		return "result", nil
	})

	before := time.Now()
	cmd.Execute(context.Background())
	after := time.Now()

	if cmd.LastChecked.Before(before) || cmd.LastChecked.After(after) {
		t.Errorf("LastChecked not set correctly: got %v, want between %v and %v",
			cmd.LastChecked, before, after)
	}
}

func TestDummyFunctions(t *testing.T) {
	// Create executor with nil LDAP client for testing error paths
	executor := NewCommandExecutor(nil)

	tests := []struct {
		name     string
		fn       func(context.Context, ...string) (string, error)
		args     []string
		wantErr  bool
		contains string
	}{
		{
			name:     "getserver - no args",
			fn:       executor.getserver,
			args:     []string{},
			wantErr:  true, // Should error without hostname
			contains: "hostname required",
		},
		{
			name:     "getserver - with hostname",
			fn:       executor.getserver,
			args:     []string{"localhost"},
			wantErr:  true, // Will fail since LDAP client is nil
			contains: "",
		},
		{
			name:     "getglobal",
			fn:       executor.getglobal,
			args:     []string{},
			wantErr:  true, // Will fail since LDAP client is nil
			contains: "",
		},
		{
			name:     "getlocal",
			fn:       getlocal,
			args:     []string{},
			wantErr:  true, // Will fail since zmlocalconfig doesn't exist in test env
			contains: "",
		},
		{
			name:     "gamau",
			fn:       executor.gamau,
			args:     []string{},
			wantErr:  true, // Will fail since LDAP client is nil
			contains: "",
		},
		{
			name:     "garpu",
			fn:       executor.garpu,
			args:     []string{},
			wantErr:  true, // Will fail since LDAP client is nil
			contains: "",
		},
		{
			name:     "garpb",
			fn:       executor.garpb,
			args:     []string{},
			wantErr:  true, // Will fail since LDAP client is nil
			contains: "",
		},
		{
			name:     "proxygen",
			fn:       proxygen,
			args:     []string{"test.example.com"},
			wantErr:  true, // Will fail since templates don't exist in test env
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.fn(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("%s() error = %v, wantErr %v", tt.name, err, tt.wantErr)
				return
			}
			if tt.contains != "" && err != nil && !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("%s() error = %v, want to contain %v", tt.name, err, tt.contains)
			}
		})
	}
}

func TestResetProvisioning(t *testing.T) {
	// Just test that it doesn't panic
	ResetProvisioning(context.Background(), "test")
	// No assertion needed as it's a void function for now
}

// TestProxygen tests the proxygen function specifically
func TestProxygen(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no hostname provided",
			args:    []string{},
			wantErr: true,
			errMsg:  "hostname required",
		},
		{
			name:    "hostname provided",
			args:    []string{"mail.example.com"},
			wantErr: true, // Will fail in test env (no templates)
			errMsg:  "proxy configuration generation failed",
		},
		{
			name:    "multiple args - use first as hostname",
			args:    []string{"mail.example.com", "extra"},
			wantErr: true, // Will fail in test env
			errMsg:  "proxy configuration generation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := proxygen(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("proxygen() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("proxygen() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func TestExeMap(t *testing.T) {
	// Test that all expected keys exist
	expectedKeys := []string{
		"POSTCONF", "POSTCONFD", "PROXY", "STATS",
		"ARCHIVING", "MEMCACHED", "MTA", "ANTISPAM", "AMAVIS", "ANTIVIRUS",
		"SASL", "MAILBOXD", "SERVICE", "LDAP", "MAILBOX", "CBPOLICYD",
		"OPENDKIM",
		// NOTE: PROXYGEN removed - using pure Go implementation
		// NOTE: ZMPROV and ZMLOCALCONFIG removed - replaced with native Go LDAP client
	}

	for _, key := range expectedKeys {
		if _, exists := Exe[key]; !exists {
			t.Errorf("Exe map missing key: %s", key)
		}
	}
}

func TestCommandsMap(t *testing.T) {
	// Register LDAP commands so they appear in the Commands map
	executor := NewCommandExecutor(nil)
	RegisterLDAPCommands(executor)

	// Test that all expected commands exist
	expectedCommands := []string{
		"gs:enabled", "gs", "localconfig", "gacf", "gamau", "garpu", "garpb",
		"postconf", "postconfd", "proxygen", "proxy", "stats", "archiving", "memcached",
		"mta", "antispam", "antivirus", "amavis", "opendkim", "cbpolicyd",
		"sasl", "mailboxd", "service", "ldap", "mailbox",
	}

	for _, cmd := range expectedCommands {
		if _, exists := Commands[cmd]; !exists {
			t.Errorf("Commands map missing command: %s", cmd)
		}
	}

	// Test that a specific command has expected properties
	if gsCmd := Commands["gs"]; gsCmd != nil {
		if gsCmd.Name != "gs" {
			t.Errorf("Commands[\"gs\"].Name = %v, want %v", gsCmd.Name, "gs")
		}
		if gsCmd.Func == nil {
			t.Errorf("Commands[\"gs\"].Func = nil, want non-nil")
		}
	}
}

// TestCommand_runCmd_ErrorCases tests error paths in runCmdWithContext
func TestCommand_runCmd_ErrorCases(t *testing.T) {
	tests := []struct {
		name       string
		cmdStr     string
		wantStatus int
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "empty command string",
			cmdStr:     "",
			wantStatus: 1,
			wantErr:    true,
			errMsg:     "empty command",
		},
		{
			name:       "command not found",
			cmdStr:     "nonexistent_command_xyz",
			wantStatus: 1,
			wantErr:    true,
			errMsg:     "execute",
		},
		{
			name:       "command with non-zero exit",
			cmdStr:     "false", // false command always returns exit code 1
			wantStatus: 1,
			wantErr:    true,
			errMsg:     "execute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand("test", "test", "", nil)
			ctx := context.Background()
			exitCode, _, err := cmd.runCmdWithContext(ctx, tt.cmdStr)

			if exitCode != tt.wantStatus {
				t.Errorf("runCmdWithContext() exitCode = %v, want %v", exitCode, tt.wantStatus)
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("runCmdWithContext() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("runCmdWithContext() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

// TestPostconfExec tests the postconfExec function
func TestPostconfExec(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
			errMsg:  "postconf requires arguments",
		},
		{
			name:    "with arguments but binary not found",
			args:    []string{"key=value"},
			wantErr: true,
			errMsg:  "postconf command failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := postconfExec(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("postconfExec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("postconfExec() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

// TestResetProvisioning_AllTypes tests all switch cases in ResetProvisioning
func TestResetProvisioning_AllTypes(t *testing.T) {
	tests := []struct {
		name     string
		provType string
	}{
		{
			name:     "local type",
			provType: "local",
		},
		{
			name:     "config type",
			provType: "config",
		},
		{
			name:     "server type",
			provType: "server",
		},
		{
			name:     "unknown type",
			provType: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify it doesn't panic
			ResetProvisioning(context.Background(), tt.provType)
		})
	}
}

// TestProxygen_ArgumentParsing tests proxygen argument parsing in detail
func TestProxygen_ArgumentParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "hostname with -s flag",
			args:    []string{"-s", "mail.example.com"},
			wantErr: true, // Will fail in test env (no templates)
			errMsg:  "",
		},
		{
			name:    "hostname with --dry-run",
			args:    []string{"--dry-run", "mail.example.com"},
			wantErr: true,
			errMsg:  "",
		},
		{
			name:    "hostname with -d flag",
			args:    []string{"-d", "mail.example.com"},
			wantErr: true,
			errMsg:  "",
		},
		{
			name:    "hostname with -v flag",
			args:    []string{"-v", "mail.example.com"},
			wantErr: true,
			errMsg:  "",
		},
		{
			name:    "hostname with --verbose",
			args:    []string{"--verbose", "mail.example.com"},
			wantErr: true,
			errMsg:  "",
		},
		{
			name:    "multiple flags combined",
			args:    []string{"-v", "-d", "-s", "mail.example.com"},
			wantErr: true,
			errMsg:  "",
		},
		{
			name:    "hostname without flag",
			args:    []string{"mail.example.com"},
			wantErr: true, // Will fail in test env
			errMsg:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := proxygen(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("proxygen() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

// TestGetserver_ErrorCases tests getserver error handling
func TestGetserver_ErrorCases(t *testing.T) {
	executor := NewCommandExecutor(nil)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no hostname provided",
			args:    []string{},
			wantErr: true,
			errMsg:  "hostname required",
		},
		{
			name:    "native LDAP client not initialized",
			args:    []string{"mail.example.com"},
			wantErr: true,
			errMsg:  "native LDAP client not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executor.getserver(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("getserver() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("getserver() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func TestSplitCommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:  "simple command",
			input: "echo hello",
			want:  []string{"echo", "hello"},
		},
		{
			name:  "command with quoted argument",
			input: `echo "hello world"`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "command with single quotes",
			input: `echo 'hello world'`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "command with escaped space",
			input: `echo hello\ world`,
			want:  []string{"echo", "hello world"},
		},
		{
			name:  "command with escaped quote",
			input: `echo "hello \"world\""`,
			want:  []string{"echo", `hello "world"`},
		},
		{
			name:  "multiple arguments",
			input: `cmd arg1 "arg 2" arg3`,
			want:  []string{"cmd", "arg1", "arg 2", "arg3"},
		},
		{
			name:  "empty quotes",
			input: `cmd ""`,
			want:  []string{"cmd", ""},
		},
		{
			name:  "mixed quotes",
			input: `cmd "double" 'single'`,
			want:  []string{"cmd", "double", "single"},
		},
		{
			name:    "unterminated double quote",
			input:   `echo "hello`,
			wantErr: true,
			errMsg:  "unterminated quote",
		},
		{
			name:    "unterminated single quote",
			input:   `echo 'hello`,
			wantErr: true,
			errMsg:  "unterminated quote",
		},
		{
			name:    "trailing escape",
			input:   `echo hello\`,
			wantErr: true,
			errMsg:  "trailing escape",
		},
		{
			name:  "tabs as whitespace",
			input: "echo\thello\tworld",
			want:  []string{"echo", "hello", "world"},
		},
		{
			name:  "multiple spaces",
			input: "echo  hello   world",
			want:  []string{"echo", "hello", "world"},
		},
		{
			name:  "quote within different quote type",
			input: `echo "it's working"`,
			want:  []string{"echo", "it's working"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandArgs(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("splitCommandArgs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("splitCommandArgs() error = %v, want error containing %v", err, tt.errMsg)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("splitCommandArgs() got %d args, want %d args\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
					return
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("splitCommandArgs() arg[%d] = %q, want %q", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

// TestRunBinaryWithContext tests the runBinaryWithContext method directly.
func TestRunBinaryWithContext(t *testing.T) {
	tests := []struct {
		name       string
		binary     string
		cmdArgs    []string
		args       []string
		wantStatus int
		wantErr    bool
	}{
		{
			name:       "successful binary execution",
			binary:     "echo",
			cmdArgs:    []string{"hello"},
			args:       []string{},
			wantStatus: 0,
			wantErr:    false,
		},
		{
			name:       "binary with runtime args",
			binary:     "echo",
			cmdArgs:    []string{},
			args:       []string{"world"},
			wantStatus: 0,
			wantErr:    false,
		},
		{
			name:       "binary with non-zero exit",
			binary:     "false",
			cmdArgs:    []string{},
			args:       []string{},
			wantStatus: 1,
			wantErr:    true,
		},
		{
			name:       "binary not found",
			binary:     "/nonexistent/binary/xyz",
			cmdArgs:    []string{},
			args:       []string{},
			wantStatus: 1,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &Command{
				Name:    "test",
				Binary:  tt.binary,
				CmdArgs: tt.cmdArgs,
			}
			exitCode, _, err := cmd.runBinaryWithContext(context.Background(), tt.args)
			if exitCode != tt.wantStatus {
				t.Errorf("runBinaryWithContext() exitCode = %v, want %v", exitCode, tt.wantStatus)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("runBinaryWithContext() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestExecuteWithContext_BinaryPath tests ExecuteWithContext when Binary field is set.
func TestExecuteWithContext_BinaryPath(t *testing.T) {
	cmd := &Command{
		Name:    "echo-test",
		Binary:  "echo",
		CmdArgs: []string{"hello"},
	}
	status, output, errStr := cmd.ExecuteWithContext(context.Background())
	if status != 0 {
		t.Errorf("ExecuteWithContext() status = %v, want 0", status)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("ExecuteWithContext() output = %q, want to contain 'hello'", output)
	}
	if errStr != "OK" {
		t.Errorf("ExecuteWithContext() error = %q, want 'OK'", errStr)
	}
}

// TestGetserverenabled_ErrorCases tests getserverenabled error paths.
func TestGetserverenabled_ErrorCases(t *testing.T) {
	executor := NewCommandExecutor(nil)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no hostname provided",
			args:    []string{},
			wantErr: true,
			errMsg:  "hostname required for getserverenabled",
		},
		{
			name:    "nil LDAP client",
			args:    []string{"mail.example.com"},
			wantErr: true,
			errMsg:  errLDAPNotInitialized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executor.getserverenabled(context.Background(), tt.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("getserverenabled() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("getserverenabled() error = %v, want to contain %q", err, tt.errMsg)
			}
		})
	}
}

// TestGetglobal_NilLDAP tests getglobal with nil LDAP client.
func TestGetglobal_NilLDAP(t *testing.T) {
	executor := NewCommandExecutor(nil)
	_, err := executor.getglobal(context.Background())
	if err == nil {
		t.Fatal("getglobal() expected error with nil LDAP client, got nil")
	}
	if !strings.Contains(err.Error(), errLDAPNotInitialized) {
		t.Errorf("getglobal() error = %v, want to contain %q", err, errLDAPNotInitialized)
	}
}

// TestGetAllServersWithAttribute_NilLDAP tests getAllServersWithAttribute with nil LDAP client.
func TestGetAllServersWithAttribute_NilLDAP(t *testing.T) {
	executor := NewCommandExecutor(nil)
	_, err := executor.getAllServersWithAttribute(context.Background(), "someAttr", "http://%s:1234", "testcmd")
	if err == nil {
		t.Fatal("getAllServersWithAttribute() expected error with nil LDAP client, got nil")
	}
	if !strings.Contains(err.Error(), errLDAPNotInitialized) {
		t.Errorf("getAllServersWithAttribute() error = %v, want to contain %q", err, errLDAPNotInitialized)
	}
}

// TestGarpb_NilLDAP tests garpb with nil LDAP client.
func TestGarpb_NilLDAP(t *testing.T) {
	executor := NewCommandExecutor(nil)
	_, err := executor.garpb(context.Background())
	if err == nil {
		t.Fatal("garpb() expected error with nil LDAP client, got nil")
	}
	if !strings.Contains(err.Error(), errLDAPNotInitialized) {
		t.Errorf("garpb() error = %v, want to contain %q", err, errLDAPNotInitialized)
	}
}

// TestParseProxygenArgs tests all branches of parseProxygenArgs.
func TestParseProxygenArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantHost    string
		wantDryRun  bool
		wantVerbose bool
	}{
		{
			name:        "no args",
			args:        []string{},
			wantHost:    "",
			wantDryRun:  false,
			wantVerbose: false,
		},
		{
			name:        "-s flag",
			args:        []string{"-s", "mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  false,
			wantVerbose: false,
		},
		{
			name:        "-d flag",
			args:        []string{"-d", "mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  true,
			wantVerbose: false,
		},
		{
			name:        "--dry-run flag",
			args:        []string{"--dry-run", "mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  true,
			wantVerbose: false,
		},
		{
			name:        "-v flag",
			args:        []string{"-v", "mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  false,
			wantVerbose: true,
		},
		{
			name:        "--verbose flag",
			args:        []string{"--verbose", "mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  false,
			wantVerbose: true,
		},
		{
			name:        "positional hostname",
			args:        []string{"mail.example.com"},
			wantHost:    "mail.example.com",
			wantDryRun:  false,
			wantVerbose: false,
		},
		{
			name:        "all flags combined",
			args:        []string{"-d", "-v", "-s", "combo.example.com"},
			wantHost:    "combo.example.com",
			wantDryRun:  true,
			wantVerbose: true,
		},
		{
			name:        "-s without following arg",
			args:        []string{"-s"},
			wantHost:    "",
			wantDryRun:  false,
			wantVerbose: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, dryRun, verbose := parseProxygenArgs(tt.args)
			if host != tt.wantHost {
				t.Errorf("parseProxygenArgs() host = %q, want %q", host, tt.wantHost)
			}
			if dryRun != tt.wantDryRun {
				t.Errorf("parseProxygenArgs() dryRun = %v, want %v", dryRun, tt.wantDryRun)
			}
			if verbose != tt.wantVerbose {
				t.Errorf("parseProxygenArgs() verbose = %v, want %v", verbose, tt.wantVerbose)
			}
		})
	}
}

// TestRegisterLDAPCommands_NilCommandsMap tests RegisterLDAPCommands when Commands is nil.
func TestRegisterLDAPCommands_NilCommandsMap(t *testing.T) {
	// Save and restore Commands map after the test.
	saved := Commands
	Commands = nil
	defer func() { Commands = saved }()

	executor := NewCommandExecutor(nil)
	RegisterLDAPCommands(executor)

	expectedCmds := []string{"gacf", "gamau", "garpb", "garpu", "gs", "gs:enabled"}
	for _, name := range expectedCmds {
		if _, ok := Commands[name]; !ok {
			t.Errorf("RegisterLDAPCommands() missing command %q", name)
		}
	}
}

func TestCommand_Execute_SafeFormatting(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantStatus int
		wantErr    bool
	}{
		{
			name:       "command with format specifier and arg",
			cmd:        "echo %s",
			args:       []string{"hello"},
			wantStatus: 0,
			wantErr:    false,
		},
		{
			name:       "command with format specifier but no args",
			cmd:        "echo %s",
			args:       []string{},
			wantStatus: 0,
			wantErr:    false,
		},
		{
			name:       "command without format specifier with args",
			cmd:        "echo hello",
			args:       []string{"ignored"},
			wantStatus: 0,
			wantErr:    false,
		},
		{
			name:       "command without format specifier without args",
			cmd:        "echo hello",
			args:       []string{},
			wantStatus: 0,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand("test", "test", tt.cmd, nil)
			status, _, errStr := cmd.Execute(context.Background(), tt.args...)

			if status != tt.wantStatus {
				t.Errorf("Execute() status = %v, want %v", status, tt.wantStatus)
			}

			hasErr := errStr != "OK"
			if hasErr != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", errStr, tt.wantErr)
			}
		})
	}
}
