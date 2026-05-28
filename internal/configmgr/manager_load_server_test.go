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

// TestLoadServerConfigWithRetry_Success tests successful server config loading
func TestLoadServerConfigWithRetry_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	// Mock gs command output (LDAP attribute format)
	mockOutput := `zimbraServiceEnabled: mta mailbox
zimbraMailboxdSSLProtocols: TLSv1.2 TLSv1.3
zimbraMtaMyNetworks: 127.0.0.0/8 10.0.0.0/8
zimbraSSLExcludeCipherSuites: ECDHE-RSA-DES-CBC3-SHA ECDHE-ECDSA-DES-CBC3-SHA
zimbraMtaHeaderChecks: pcre:/opt/zextras/conf/postfix_header_checks`

	cm.CommandRegistry.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		func(_ context.Context, args ...string) (string, error) {
			if len(args) == 0 || args[0] != "testhost" {
				return "", fmt.Errorf("expected hostname argument")
			}
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was parsed
	if cm.State.ServerConfig.Data.GetOr("zimbraServiceEnabled", "") != "mta mailbox" {
		t.Errorf("Expected zimbraServiceEnabled to be 'mta mailbox', got: %s",
			cm.State.ServerConfig.Data.GetOr("zimbraServiceEnabled", ""))
	}

	// Verify ServiceConfig was populated
	if cm.State.ServerConfig.ServiceConfig.GetOr("mta", "") != "zimbraServiceEnabled" {
		t.Error("Expected mta service to be enabled")
	}

	// Verify SSL protocols were processed (should be sorted)
	if _, ok := cm.State.ServerConfig.Data.Get("zimbraMailboxdSSLProtocols"); !ok {
		t.Error("Expected zimbraMailboxdSSLProtocols to be processed")
	}

	// Verify zimbraMtaMyNetworksPerLine was generated
	if networks, ok := cm.State.ServerConfig.Data.Get("zimbraMtaMyNetworksPerLine"); !ok {
		t.Error("Expected zimbraMtaMyNetworksPerLine to be generated")
	} else if !strings.Contains(networks, "\n") {
		t.Error("Expected zimbraMtaMyNetworksPerLine to contain newlines")
	}

	// Verify comma-separated conversion
	if headers, ok := cm.State.ServerConfig.Data.Get("zimbraMtaHeaderChecks"); !ok {
		t.Error("Expected zimbraMtaHeaderChecks to be processed")
	} else if headers != "pcre:/opt/zextras/conf/postfix_header_checks" {
		t.Errorf("Expected single header check, got: %s", headers)
	}
}

// TestLoadServerConfigWithRetry_NoHostname tests error when hostname is missing
func TestLoadServerConfigWithRetry_NoHostname(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)
	cm.mainConfig.Hostname = "" // Clear hostname

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when hostname is missing")
	}

	if !strings.Contains(err.Error(), "hostname required") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadServerConfigWithRetry_CommandNotAvailable tests error when gs command unavailable
func TestLoadServerConfigWithRetry_CommandNotAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	cm.CommandRegistry.Commands = make(map[string]*commands.Command)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command not available")
	}

	if !strings.Contains(err.Error(), "gs command not available") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestLoadServerConfigWithRetry_CommandFails tests retry logic
func TestLoadServerConfigWithRetry_CommandFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	attempts := 0
	cm.CommandRegistry.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		func(_ context.Context, args ...string) (string, error) {
			attempts++
			return "", fmt.Errorf("command failed")
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err == nil {
		t.Fatal("Expected error when command fails")
	}

	if attempts != 3 {
		t.Errorf("Expected 3 retry attempts, got: %d", attempts)
	}
}

// TestLoadServerConfigWithRetry_EmptyOutput tests behavior with empty output
func TestLoadServerConfigWithRetry_EmptyOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	cm.CommandRegistry.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		func(_ context.Context, args ...string) (string, error) {
			return "   \n   \n   ", nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 2)
	if err != nil {
		t.Fatalf("Expected no error with whitespace output, got: %v", err)
	}

	// Verify the data map exists but is empty (or has only post-processed defaults)
	if cm.State.ServerConfig.Data.Len() > 5 {
		t.Errorf("Expected minimal or empty config data, got %d entries", cm.State.ServerConfig.Data.Len())
	}
}

// TestLoadServerConfigWithRetry_NoCache tests loading without cache
func TestLoadServerConfigWithRetry_NoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)
	cm.Cache = nil // Disable cache

	mockOutput := `zimbraServiceEnabled: mailbox
zimbraMailboxdSSLProtocols: TLSv1.3`

	cm.CommandRegistry.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify data was loaded even without cache
	if cm.State.ServerConfig.Data.GetOr("zimbraServiceEnabled", "") != "mailbox" {
		t.Errorf("Expected zimbraServiceEnabled to be 'mailbox', got: %s",
			cm.State.ServerConfig.Data.GetOr("zimbraServiceEnabled", ""))
	}
}

// TestLoadServerConfigWithRetry_ServiceMapping tests service config mapping
func TestLoadServerConfigWithRetry_ServiceMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: has retry delays")
	}

	cm := newTestConfigManager(t)

	// Test mailbox -> mailboxd mapping and mta -> sasl mapping
	mockOutput := `zimbraServiceEnabled: mailbox mta ldap`

	cm.CommandRegistry.Commands["gs"] = commands.NewCommand(
		"Get server test",
		"gs",
		func(_ context.Context, args ...string) (string, error) {
			return mockOutput, nil
		},
	)

	err := cm.loadServerConfigWithRetry(context.Background(), 3)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify service mappings
	if cm.State.ServerConfig.ServiceConfig.GetOr("mailbox", "") != "zimbraServiceEnabled" {
		t.Error("Expected mailbox service to be enabled")
	}

	if cm.State.ServerConfig.ServiceConfig.GetOr("mailboxd", "") != "zimbraServiceEnabled" {
		t.Error("Expected mailboxd service to be mapped from mailbox")
	}

	if cm.State.ServerConfig.ServiceConfig.GetOr("mta", "") != "zimbraServiceEnabled" {
		t.Error("Expected mta service to be enabled")
	}

	if cm.State.ServerConfig.ServiceConfig.GetOr("sasl", "") != "zimbraServiceEnabled" {
		t.Error("Expected sasl service to be mapped from mta")
	}

	if cm.State.ServerConfig.ServiceConfig.GetOr("ldap", "") != "zimbraServiceEnabled" {
		t.Error("Expected ldap service to be enabled")
	}
}
