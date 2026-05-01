// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/commands"
	"strings"
	"testing"
)

// TestFetchMiscCommand_Success tests successful command execution
func TestFetchMiscCommand_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	mockOutput := "test output data"
	commands.Commands["testcmd"] = commands.NewCommand(
		"Test command",
		"testcmd",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "testcmd")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if output != mockOutput {
		t.Errorf("Expected output '%s', got: '%s'", mockOutput, output)
	}
}

// TestFetchMiscCommand_CommandNotAvailable tests behavior when command unavailable
func TestFetchMiscCommand_CommandNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands = make(map[string]*commands.Command)

	output, err := cm.fetchMiscCommand(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("Expected no error when command unavailable, got: %v", err)
	}

	if output != "" {
		t.Errorf("Expected empty output when command unavailable, got: %s", output)
	}
}

// TestFetchMiscCommand_CommandFails tests behavior when command fails
func TestFetchMiscCommand_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["failcmd"] = commands.NewCommand(
		"Failing command",
		"failcmd",
		func(_ context.Context, args ...string) (string, error) {
			return "", fmt.Errorf("command failed")
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "failcmd")
	if err != nil {
		t.Errorf("Expected no error (returns empty on failure), got: %v", err)
	}

	if output != "" {
		t.Errorf("Expected empty output when command fails, got: %s", output)
	}
}

// TestFetchMiscCommand_EmptyOutput tests behavior with empty output
func TestFetchMiscCommand_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	commands.Commands["emptycmd"] = commands.NewCommand(
		"Empty command",
		"emptycmd",
		func(_ context.Context, args ...string) (string, error) {
			return "   \n   ", nil
		},
	)

	output, err := cm.fetchMiscCommand(context.Background(), "emptycmd")
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Whitespace-only output is returned as-is (not filtered)
	if strings.TrimSpace(output) != "" {
		t.Errorf("Expected only whitespace output, got: %s", output)
	}
}

// TestLoadMiscConfig_Success tests successful misc config loading
func TestLoadMiscConfig_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	// Mock all 4 misc commands
	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		func(_ context.Context, args ...string) (string, error) {
			return "https://proxy1.example.com", nil
		},
	)

	commands.Commands["garpb"] = commands.NewCommand(
		"Get all reverse proxy backends",
		"garpb",
		func(_ context.Context, args ...string) (string, error) {
			return "backend1.example.com", nil
		},
	)

	commands.Commands["gamau"] = commands.NewCommand(
		"Get all MTA auth URLs",
		"gamau",
		func(_ context.Context, args ...string) (string, error) {
			return "ldap://mta-auth.example.com", nil
		},
	)

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify all commands were executed and stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy1.example.com" {
		t.Errorf("Expected garpu output to be stored, got: %s", cm.State.MiscConfig.Data["garpu"])
	}

	if cm.State.MiscConfig.Data["garpb"] != "backend1.example.com" {
		t.Errorf("Expected garpb output to be stored, got: %s", cm.State.MiscConfig.Data["garpb"])
	}

	if cm.State.MiscConfig.Data["gamau"] != "ldap://mta-auth.example.com" {
		t.Errorf("Expected gamau output to be stored, got: %s", cm.State.MiscConfig.Data["gamau"])
	}
}

// TestLoadMiscConfig_NoCache tests misc config loading without cache
func TestLoadMiscConfig_NoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)
	cm.Cache = nil // Disable cache

	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		func(_ context.Context, args ...string) (string, error) {
			return "https://proxy.example.com", nil
		},
	)

	commands.Commands["garpb"] = commands.NewCommand("desc", "garpb", func(_ context.Context, args ...string) (string, error) { return "", nil })
	commands.Commands["gamau"] = commands.NewCommand("desc", "gamau", func(_ context.Context, args ...string) (string, error) { return "", nil })

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify at least one command output was stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy.example.com" {
		t.Errorf("Expected garpu output to be stored without cache, got: %s", cm.State.MiscConfig.Data["garpu"])
	}
}

// TestLoadMiscConfig_PartialFailure tests misc config when some commands fail
func TestLoadMiscConfig_PartialFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}
	commands.Initialize()

	cm := newTestConfigManager(t)

	successCount := 0
	commands.Commands["garpu"] = commands.NewCommand(
		"Get all reverse proxy URLs",
		"garpu",
		func(_ context.Context, args ...string) (string, error) {
			successCount++
			return "https://proxy.example.com", nil
		},
	)

	// These commands will fail or return empty
	commands.Commands["garpb"] = commands.NewCommand(
		"desc",
		"garpb",
		func(_ context.Context, args ...string) (string, error) {
			return "", fmt.Errorf("command failed")
		},
	)

	commands.Commands["gamau"] = commands.NewCommand("desc", "gamau", func(_ context.Context, args ...string) (string, error) { return "", nil })

	err := cm.LoadMiscConfig(context.Background())
	if err != nil {
		t.Fatalf("Expected no error (partial failures are non-fatal), got: %v", err)
	}

	// Verify successful command output was stored
	if cm.State.MiscConfig.Data["garpu"] != "https://proxy.example.com" {
		t.Errorf("Expected garpu output to be stored, got: %s", cm.State.MiscConfig.Data["garpu"])
	}

	// Verify failed commands are not in the data map or are empty
	if val, ok := cm.State.MiscConfig.Data["garpb"]; ok && val != "" {
		t.Errorf("Expected garpb to not be stored or be empty, got: %s", val)
	}
}
