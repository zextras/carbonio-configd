// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestDistroID(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			f := systest.New()
			f.FS.Put("/etc/os-release", []byte(tt.content), 0o644)

			if got := distroID(f.FS, "/etc/os-release"); got != tt.want {
				t.Errorf("distroID = %q, want %q", got, tt.want)
			}
		})
	}

	if got := distroID(systest.New().FS, "/missing"); got != "" {
		t.Errorf("distroID(missing) = %q, want empty", got)
	}
}

func TestRun_PrintsRelease(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/cb")

	f := systest.New()
	f.FS.Put("/cb/.version", []byte("25.6.0\n"), 0o600)

	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), f.System(), &stdout, &stderr, &Options{}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if want := "Carbonio Release 25.6.0\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	if lines := f.Exec.Lines(); len(lines) != 0 {
		t.Errorf("unexpected commands without -V: %v", lines)
	}
}

// TestRun_MissingVersionFile_ExitsZero: legacy `zmcontrol -v` runs
// `cat .version` and exits 0 even when the file is missing.
func TestRun_MissingVersionFile_ExitsZero(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/cb")

	var stdout, stderr bytes.Buffer

	if code := Run(context.Background(), systest.New().System(), &stdout, &stderr, &Options{}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	if want := "Carbonio Release \n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRun_Packages(t *testing.T) {
	t.Setenv("CARBONIO_BASE_DIR", "/cb")

	tests := []struct {
		name     string
		osID     string
		out      string
		want     string
		wantExec string
	}{
		{
			"deb listed", "ID=ubuntu\n", "carbonio-core 1.2-3\n",
			"Carbonio Release \n\nInstalled packages:\ncarbonio-core 1.2-3\n",
			"dpkg-query -W -f ${Package} ${Version}\n carbonio*",
		},
		{
			"rpm listed", "ID=\"rocky\"\n", "carbonio-core 1.2-3\n",
			"Carbonio Release \n\nInstalled packages:\ncarbonio-core 1.2-3\n",
			"rpm -qa --qf %{NAME} %{VERSION}-%{RELEASE}\n carbonio*",
		},
		{"none installed", "ID=debian\n", "", "Carbonio Release \n", "dpkg-query -W -f ${Package} ${Version}\n carbonio*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := systest.New()
			f.FS.Put("/etc/os-release", []byte(tt.osID), 0o644)
			f.Exec.When(func(*sys.Cmd) bool { return true }, systest.Reply(tt.out))

			var stdout, stderr bytes.Buffer

			if code := Run(context.Background(), f.System(), &stdout, &stderr, &Options{Packages: true}); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}

			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}

			if lines := f.Exec.Lines(); len(lines) != 1 || lines[0] != tt.wantExec {
				t.Errorf("commands = %q, want [%q]", lines, tt.wantExec)
			}
		})
	}
}
