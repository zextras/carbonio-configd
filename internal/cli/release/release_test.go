// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDistroID(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"quoted", "NAME=\"Rocky\"\nID=\"rocky\"\nID_LIKE=\"rhel\"\n", "rocky"},
		{"plain", "NAME=Ubuntu\nID=ubuntu\n", "ubuntu"},
		{"no id", "NAME=Thing\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			if got := distroID(path); got != tt.want {
				t.Errorf("distroID = %q, want %q", got, tt.want)
			}
		})
	}

	if got := distroID(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("distroID(missing) = %q, want empty", got)
	}
}

func TestRun_PrintsRelease(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	if err := os.WriteFile(filepath.Join(base, ".version"), []byte("25.6.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), &stdout, &stderr, &Options{}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if want := "Carbonio Release 25.6.0\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

// TestRun_MissingVersionFile_ExitsZero: legacy `zmcontrol -v` runs
// `cat .version` and exits 0 even when the file is missing.
func TestRun_MissingVersionFile_ExitsZero(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), &stdout, &stderr, &Options{}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if want := "Carbonio Release \n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRun_Packages(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	tests := []struct {
		name string
		out  string
		want string
	}{
		{"listed", "carbonio-core 1.2-3\n", "Carbonio Release \n\nInstalled packages:\ncarbonio-core 1.2-3\n"},
		{"none installed", "", "Carbonio Release \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := installedPackages
			installedPackages = func(context.Context, string) []byte { return []byte(tt.out) }

			t.Cleanup(func() { installedPackages = prev })

			var stdout, stderr bytes.Buffer

			if code := Run(context.Background(), &stdout, &stderr, &Options{Packages: true}); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}

			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
		})
	}
}
