// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"os"
	"os/exec"
	"testing"
)

// BenchmarkOldApproach_Subprocess benchmarks the old zmlocalconfig subprocess approach.
// Requires zmlocalconfig binary to be available.
func BenchmarkOldApproach_Subprocess(b *testing.B) {
	// Check if zmlocalconfig exists
	_, err := exec.LookPath("/opt/zextras/bin/zmlocalconfig")
	if err != nil {
		b.Skip("zmlocalconfig binary not found - skipping subprocess benchmark")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("/opt/zextras/bin/zmlocalconfig", "-s")
		output, err := cmd.CombinedOutput()
		if err != nil {
			b.Fatalf("zmlocalconfig command failed: %v", err)
		}
		_ = output
	}
}

// BenchmarkNewApproach_DirectParsing benchmarks the new direct XML parsing approach.
func BenchmarkNewApproach_DirectParsing(b *testing.B) {
	// Check if localconfig.xml exists
	if _, err := os.Stat(DefaultConfigPath); os.IsNotExist(err) {
		b.Skip("localconfig.xml not found - skipping direct parsing benchmark")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		config, err := LoadLocalConfig()
		if err != nil {
			b.Fatalf("LoadLocalConfig failed: %v", err)
		}
		_ = config
	}
}

// BenchmarkNewApproach_WithFormatting benchmarks direct parsing + formatting to key=value.
func BenchmarkNewApproach_WithFormatting(b *testing.B) {
	if _, err := os.Stat(DefaultConfigPath); os.IsNotExist(err) {
		b.Skip("localconfig.xml not found - skipping benchmark")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		config, err := LoadLocalConfig()
		if err != nil {
			b.Fatalf("LoadLocalConfig failed: %v", err)
		}
		output := FormatAsKeyValue(config)
		_ = output
	}
}
