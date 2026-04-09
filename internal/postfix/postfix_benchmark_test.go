// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkPostconfSequential benchmarks the old sequential execution pattern.
// This simulates the old behavior for comparison.
func BenchmarkPostconfSequential(b *testing.B) {
	// Create a fast mock postconf script
	tmpDir := b.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		b.Fatalf("Failed to create mock script: %v", err)
	}

	// Benchmark with 139 parameters (realistic production count)
	paramCount := 139

	for b.Loop() {
		pm := NewPostfixManager(scriptPath)

		// Add parameters
		for j := range paramCount {
			key := fmt.Sprintf("param_%d", j)
			value := fmt.Sprintf("value_%d", j)
			pm.AddPostconf(context.Background(), key, value)
		}

		// Flush (batched execution)
		if err := pm.FlushPostconf(context.Background()); err != nil {
			b.Fatalf("FlushPostconf failed: %v", err)
		}
	}
}

// BenchmarkPostconfBatched benchmarks the new batched execution.
func BenchmarkPostconfBatched(b *testing.B) {
	// Create a fast mock postconf script
	tmpDir := b.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		b.Fatalf("Failed to create mock script: %v", err)
	}

	// Benchmark with 139 parameters (realistic production count)
	paramCount := 139

	for b.Loop() {
		pm := NewPostfixManager(scriptPath)

		// Add parameters
		for j := range paramCount {
			key := fmt.Sprintf("param_%d", j)
			value := fmt.Sprintf("value_%d", j)
			pm.AddPostconf(context.Background(), key, value)
		}

		// Flush (batched execution)
		if err := pm.FlushPostconf(context.Background()); err != nil {
			b.Fatalf("FlushPostconf failed: %v", err)
		}
	}
}

// BenchmarkPostconfdBatched benchmarks postconfd deletion batching.
func BenchmarkPostconfdBatched(b *testing.B) {
	// Create a fast mock postconf script
	tmpDir := b.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		b.Fatalf("Failed to create mock script: %v", err)
	}

	// Benchmark with 50 deletions
	deleteCount := 50

	for b.Loop() {
		pm := NewPostfixManager(scriptPath)

		// Add deletions
		for j := range deleteCount {
			key := fmt.Sprintf("param_%d", j)
			pm.AddPostconfd(context.Background(), key)
		}

		// Flush (batched execution)
		if err := pm.FlushPostconfd(context.Background()); err != nil {
			b.Fatalf("FlushPostconfd failed: %v", err)
		}
	}
}

// BenchmarkPostconfSmallBatch benchmarks with realistic small batch (10 params).
func BenchmarkPostconfSmallBatch(b *testing.B) {
	tmpDir := b.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		b.Fatalf("Failed to create mock script: %v", err)
	}

	for b.Loop() {
		pm := NewPostfixManager(scriptPath)

		for j := range 10 {
			pm.AddPostconf(context.Background(), fmt.Sprintf("param_%d", j), fmt.Sprintf("value_%d", j))
		}

		if err := pm.FlushPostconf(context.Background()); err != nil {
			b.Fatalf("FlushPostconf failed: %v", err)
		}
	}
}

// BenchmarkPostconfLargeBatch benchmarks with large batch (500 params).
func BenchmarkPostconfLargeBatch(b *testing.B) {
	tmpDir := b.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		b.Fatalf("Failed to create mock script: %v", err)
	}

	for b.Loop() {
		pm := NewPostfixManager(scriptPath)

		for j := range 500 {
			pm.AddPostconf(context.Background(), fmt.Sprintf("param_%d", j), fmt.Sprintf("value_%d", j))
		}

		if err := pm.FlushPostconf(context.Background()); err != nil {
			b.Fatalf("FlushPostconf failed: %v", err)
		}
	}
}
