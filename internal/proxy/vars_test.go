// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/proxy"
)

// TestVariableResolutionFromGlobalConfig tests that variables are resolved from GlobalConfig
func TestVariableResolutionFromGlobalConfig(t *testing.T) {
	// Setup: Create configs with test data
	cfg := &config.Config{
		BaseDir:  "/tmp/test-proxy",
		Hostname: "proxy.example.com",
	}

	localCfg := &config.LocalConfig{
		Data: map[string]string{
			"zimbraIPMode": "both",
		},
	}

	globalCfg := &config.GlobalConfig{
		Data: map[string]string{
			"zimbraReverseProxyWorkerProcesses":   "8",
			"zimbraReverseProxyWorkerConnections": "20480",
			"zimbraReverseProxyLogLevel":          "warn",
			"zimbraMailProxyPort":                 "8080",
			"zimbraMailSSLProxyPort":              "8443",
			"zimbraReverseProxySSLCiphers":        "HIGH:!aNULL:!MD5",
			"zimbraReverseProxySSLProtocols":      "TLSv1.2 TLSv1.3",
		},
	}

	serverCfg := &config.ServerConfig{
		Data:          make(map[string]string),
		ServiceConfig: make(map[string]string),
	}

	// Create generator
	gen, err := proxy.LoadConfiguration(context.Background(), cfg, localCfg, globalCfg, serverCfg, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	// Test: Verify variables are resolved from GlobalConfig
	tests := []struct {
		varName  string
		expected any
	}{
		{"main.workers", 8},
		{"main.workerConnections", 20480},
		{"main.logLevel", "warn"},
		{"web.http.port", 8080},
		{"web.https.port", 8443},
		{"ssl.ciphers", "HIGH:!aNULL:!MD5"},
		{"ssl.protocols", "TLSv1.2 TLSv1.3"},
	}

	for _, tt := range tests {
		t.Run(tt.varName, func(t *testing.T) {
			v, err := gen.GetVariable(tt.varName)
			if err != nil {
				t.Fatalf("Failed to get variable %s: %v", tt.varName, err)
			}

			if v.Value != tt.expected {
				t.Errorf("Variable %s: expected %v, got %v", tt.varName, tt.expected, v.Value)
			}
		})
	}
}

// TestVariableResolutionDefaults tests that default values are used when no config is provided
func TestVariableResolutionDefaults(t *testing.T) {
	// Setup: Create configs with minimal data
	cfg := &config.Config{
		BaseDir:  "/tmp/test-proxy",
		Hostname: "proxy.example.com",
	}

	localCfg := &config.LocalConfig{Data: make(map[string]string)}
	globalCfg := &config.GlobalConfig{Data: make(map[string]string)}
	serverCfg := &config.ServerConfig{
		Data:          make(map[string]string),
		ServiceConfig: make(map[string]string),
	}

	// Create generator
	gen, err := proxy.LoadConfiguration(context.Background(), cfg, localCfg, globalCfg, serverCfg, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	// Test: Verify default values are used
	tests := []struct {
		varName  string
		expected any
	}{
		{"main.workers", 4},               // Default
		{"main.workerConnections", 10240}, // Default
		{"main.logLevel", "info"},         // Default
		{"web.http.port", 0},              // Changed to 0 to match Java
		{"web.https.port", 0},             // Changed to 0 to match Java
	}

	for _, tt := range tests {
		t.Run(tt.varName, func(t *testing.T) {
			v, err := gen.GetVariable(tt.varName)
			if err != nil {
				t.Fatalf("Failed to get variable %s: %v", tt.varName, err)
			}

			if v.Value != tt.expected {
				t.Errorf("Variable %s: expected default %v, got %v", tt.varName, tt.expected, v.Value)
			}
		})
	}
}
