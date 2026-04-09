// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package fileutil_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/fileutil"
)

// skipIfRoot skips the test when running as root.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("skipping permission-based test: running as root")
	}
}

func TestCopyFile(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	t.Run("successful copy", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "src.txt")
		dstPath := filepath.Join(tmpDir, "dst.txt")

		content := "test content for file copy"
		if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err != nil {
			t.Errorf("CopyFile failed: %v", err)
		}

		destContent, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("Failed to read destination file: %v", err)
		}

		if string(destContent) != content {
			t.Errorf("Content mismatch: got %q, expected %q", string(destContent), content)
		}
	})

	t.Run("source file does not exist", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "nonexistent.txt")
		dstPath := filepath.Join(tmpDir, "dst2.txt")

		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err == nil {
			t.Error("Expected error when source file doesn't exist, got nil")
		}
	})

	t.Run("destination directory does not exist", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "src2.txt")
		dstPath := filepath.Join(tmpDir, "nonexistent_dir", "dst.txt")

		if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err == nil {
			t.Error("Expected error when destination directory doesn't exist, got nil")
		}
	})

	t.Run("empty file copy", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "empty.txt")
		dstPath := filepath.Join(tmpDir, "empty_dst.txt")

		if err := os.WriteFile(srcPath, []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create empty source file: %v", err)
		}

		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err != nil {
			t.Errorf("CopyFile failed for empty file: %v", err)
		}

		destContent, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("Failed to read destination file: %v", err)
		}

		if len(destContent) != 0 {
			t.Errorf("Expected empty destination, got %d bytes", len(destContent))
		}
	})

	t.Run("overwrite read-only destination", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "ro_src.txt")
		dstPath := filepath.Join(tmpDir, "ro_dst.txt")

		newContent := "updated content"
		if err := os.WriteFile(srcPath, []byte(newContent), 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		// Create read-only destination file (mode 0440, like Carbonio config files)
		oldContent := "old content"
		if err := os.WriteFile(dstPath, []byte(oldContent), 0440); err != nil {
			t.Fatalf("Failed to create read-only destination file: %v", err)
		}

		// Copy should succeed despite read-only destination (removes then creates)
		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err != nil {
			t.Errorf("CopyFile failed on read-only destination: %v", err)
		}

		destContent, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("Failed to read destination file: %v", err)
		}

		if string(destContent) != newContent {
			t.Errorf("Content mismatch: got %q, expected %q", string(destContent), newContent)
		}
	})

	t.Run("destination does not exist yet", func(t *testing.T) {
		srcPath := filepath.Join(tmpDir, "new_src.txt")
		dstPath := filepath.Join(tmpDir, "new_dst.txt")

		content := "brand new file"
		if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create source file: %v", err)
		}

		if err := fileutil.CopyFile(ctx, srcPath, dstPath); err != nil {
			t.Errorf("CopyFile failed for new destination: %v", err)
		}

		destContent, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("Failed to read destination file: %v", err)
		}

		if string(destContent) != content {
			t.Errorf("Content mismatch: got %q, expected %q", string(destContent), content)
		}
	})
}

// TestCopyFile_RemoveExistingDestFails exercises the "failed to remove existing
// destination" error path. We create a destination file inside a directory that
// has been made read-only so os.Remove fails.
func TestCopyFile_RemoveExistingDestFails(t *testing.T) {
	skipIfRoot(t)

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create source file.
	srcPath := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup src: %v", err)
	}

	// Create a subdirectory that will hold the destination.
	subDir := filepath.Join(tmpDir, "locked")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("setup subdir: %v", err)
	}

	// Create the destination file first.
	dstPath := filepath.Join(subDir, "dst.txt")
	if err := os.WriteFile(dstPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("setup dst: %v", err)
	}

	// Remove write permission from the parent directory so os.Remove(dstPath) fails.
	if err := os.Chmod(subDir, 0o555); err != nil {
		t.Fatalf("chmod subdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subDir, 0o755) })

	err := fileutil.CopyFile(ctx, srcPath, dstPath)
	if err == nil {
		t.Fatal("expected error when destination cannot be removed, got nil")
	}
}

// TestCopyFile_LargeFileCopy verifies io.Copy with a ~1 MB source (exercises
// multi-chunk copy path).
func TestCopyFile_LargeFileCopy(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	srcPath := filepath.Join(tmpDir, "large_src.bin")
	dstPath := filepath.Join(tmpDir, "large_dst.bin")
	if err := os.WriteFile(srcPath, data, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := fileutil.CopyFile(ctx, srcPath, dstPath); err != nil {
		t.Fatalf("CopyFile large file: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("size mismatch: got %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Errorf("data mismatch at byte %d", i)
			break
		}
	}
}

// TestCopyFile_DestCreateFails exercises the os.Create failure path by making
// the target directory read-only.
func TestCopyFile_DestCreateFails(t *testing.T) {
	skipIfRoot(t)

	ctx := context.Background()
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("setup src: %v", err)
	}

	roDir := filepath.Join(tmpDir, "rodir")
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	dstPath := filepath.Join(roDir, "dst.txt")
	err := fileutil.CopyFile(ctx, srcPath, dstPath)
	if err == nil {
		t.Fatal("expected error when dest directory is read-only, got nil")
	}
}
