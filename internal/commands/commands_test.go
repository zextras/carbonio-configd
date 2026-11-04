// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewCommand(t *testing.T) {
	tests := []struct {
		name        string
		desc        string
		cmdName     string
		fn          func(context.Context, ...string) (string, error)
		args        []string
		wantDesc    string
		wantCmdName string
	}{
		{
			name:        "binary command",
			desc:        "Test binary command",
			cmdName:     "test",
			fn:          nil,
			args:        []string{"arg1"},
			wantDesc:    "Test binary command",
			wantCmdName: "test",
		},
		{
			name:        "function command",
			desc:        "Test function command",
			cmdName:     "testfn",
			fn:          func(ctx context.Context, args ...string) (string, error) { return "result", nil },
			args:        []string{"arg1"},
			wantDesc:    "Test function command",
			wantCmdName: "testfn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(tt.desc, tt.cmdName, tt.fn, tt.args...)
			if cmd.Desc != tt.wantDesc {
				t.Errorf("NewCommand().Desc = %v, want %v", cmd.Desc, tt.wantDesc)
			}
			if cmd.Name != tt.wantCmdName {
				t.Errorf("NewCommand().Name = %v, want %v", cmd.Name, tt.wantCmdName)
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
	cmd := NewCommand("test", "test", nil)
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
			name: "binary command",
			cmd:  NewCommand("test", "echo", nil),
			want: "echo([])",
		},
		{
			name: "function command",
			cmd:  NewCommand("test", "testfn", func(ctx context.Context, args ...string) (string, error) { return "", nil }, "arg1"),
			want: "testfn([arg1])",
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
			cmd := NewCommand("test", "test", tt.fn)
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
	cmd := NewCommand("test", "test", func(ctx context.Context, args ...string) (string, error) {
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
	cmd := NewCommand("test", "test", func(ctx context.Context, args ...string) (string, error) {
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

// TestExeMap removed - tests now use per-instance Registry instead of global Exe map

// TestPostconfExec removed - postconfExec is a deprecated internal function

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

func TestBuildBackendURL(t *testing.T) {
	tests := []struct {
		name    string
		attrs   map[string]string
		wantURL string
		wantOK  bool
	}{
		{
			name: "https mode uses ssl port",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "TRUE",
				attrZimbraMailMode:                 "https",
				attrZimbraMailSSLPort:              "8443",
			},
			wantURL: "mail.example.com:8443",
			wantOK:  true,
		},
		{
			name: "https mode missing ssl port uses default 0",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "TRUE",
				attrZimbraMailMode:                 "https",
			},
			wantURL: "mail.example.com:0",
			wantOK:  true,
		},
		{
			name: "both mode uses plaintext mail port",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "true",
				attrZimbraMailMode:                 "both",
				attrZimbraMailPort:                 "8080",
				attrZimbraMailSSLPort:              "7443",
			},
			wantURL: "mail.example.com:8080",
			wantOK:  true,
		},
		{
			name: "http mode missing mail port uses default 80",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "TRUE",
				attrZimbraMailMode:                 "http",
			},
			wantURL: "mail.example.com:80",
			wantOK:  true,
		},
		{
			name:    "empty attrs",
			attrs:   map[string]string{},
			wantURL: "",
			wantOK:  false,
		},
		{
			name: "missing hostname",
			attrs: map[string]string{
				attrZimbraReverseProxyLookupTarget: "TRUE",
				attrZimbraMailMode:                 "https",
			},
			wantURL: "",
			wantOK:  false,
		},
		{
			name: "not a lookup target",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "FALSE",
				attrZimbraMailMode:                 "https",
			},
			wantURL: "",
			wantOK:  false,
		},
		{
			name: "missing lookup target attr",
			attrs: map[string]string{
				zimbraServiceHostname: "mail.example.com",
				attrZimbraMailMode:    "https",
			},
			wantURL: "",
			wantOK:  false,
		},
		{
			name: "missing mail mode",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "TRUE",
			},
			wantURL: "",
			wantOK:  false,
		},
		{
			name: "empty mail mode",
			attrs: map[string]string{
				zimbraServiceHostname:              "mail.example.com",
				attrZimbraReverseProxyLookupTarget: "TRUE",
				attrZimbraMailMode:                 "",
			},
			wantURL: "",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotOK := buildBackendURL(tt.attrs)
			if gotURL != tt.wantURL {
				t.Errorf("buildBackendURL() url = %q, want %q", gotURL, tt.wantURL)
			}
			if gotOK != tt.wantOK {
				t.Errorf("buildBackendURL() ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

// TestRegisterLDAPCommands verifies that all expected LDAP-provisioning
// commands are registered and wired to non-nil handler functions.
func TestRegisterLDAPCommands(t *testing.T) {
	r := &Registry{}
	e := &CommandExecutor{}
	r.RegisterLDAPCommands(e)

	wantCommands := []string{cmdGAMAU, cmdGARPB, cmdGARPU}
	for _, name := range wantCommands {
		cmd, ok := r.Commands[name]
		if !ok {
			t.Errorf("command %q not registered", name)
			continue
		}
		if cmd == nil {
			t.Errorf("command %q is nil", name)
			continue
		}
		if cmd.Func == nil {
			t.Errorf("command %q has nil Func", name)
		}
	}

	if r.ServerAttrs == nil || r.GlobalAttrs == nil {
		t.Error("typed LDAP attribute loaders not wired")
	}

	if len(r.Commands) != len(wantCommands) {
		t.Errorf("got %d registered commands, want %d", len(r.Commands), len(wantCommands))
	}
}

// TestNewCommandExecutor_NilClient verifies NewCommandExecutor(nil) returns a non-nil executor.
func TestNewCommandExecutor_NilClient(t *testing.T) {
	e := NewCommandExecutor(nil)
	if e == nil {
		t.Fatal("NewCommandExecutor(nil) returned nil")
	}
}

// TestCommandExecutor_requireLDAP verifies the shared nil-client guard used by
// every LDAP-backed CommandExecutor method.
func TestCommandExecutor_requireLDAP(t *testing.T) {
	if err := NewCommandExecutor(nil).requireLDAP(); err == nil {
		t.Fatal("requireLDAP() with nil client returned nil error")
	} else if !strings.Contains(err.Error(), errLDAPNotInitialized) {
		t.Errorf("requireLDAP() error = %v, want to contain %q", err, errLDAPNotInitialized)
	}

	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{}}
	if err := e.requireLDAP(); err != nil {
		t.Errorf("requireLDAP() with configured client = %v, want nil", err)
	}
}
