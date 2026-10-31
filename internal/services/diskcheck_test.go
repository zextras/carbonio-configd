// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const mib = 1024 * 1024

func TestCheckDiskSpace_Thresholds(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Avail = 500 * mib

	tests := []struct {
		name      string
		threshold int
		wantOK    bool
	}{
		{"zero threshold is always satisfied", 0, true},
		{"below available", 499, true},
		{"exactly available", 500, true},
		{"one above available", 501, false},
		{"far above available", 999999999, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			availMB, ok, err := ctl.CheckDiskSpace("/data", tc.threshold)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if availMB != 500 {
				t.Errorf("availMB = %d, want 500", availMB)
			}

			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}

func TestCheckDiskSpace_ErrorDoesNotBlock(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Fail = func(op, _ string) error {
		if op == "AvailBytes" {
			return errors.New("statfs: no such file or directory")
		}

		return nil
	}

	availMB, ok, err := ctl.CheckDiskSpace("/nonexistent/path/that/does/not/exist", 100)
	if err == nil {
		t.Error("expected error for nonexistent path")
	}

	if availMB != 0 || !ok {
		t.Errorf("on error got (%d, %v), want (0, true): an unreadable path must not block", availMB, ok)
	}
}

func TestCheckDiskSpace_RealFS(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	availMB, ok, err := ctl.CheckDiskSpace(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if availMB < 0 || !ok {
		t.Errorf("CheckDiskSpace(0) = (%d, %v), want non-negative and ok", availMB, ok)
	}
}

func TestServiceDiskDirs(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	dirs := ctl.serviceDiskDirs("mailbox")
	if len(dirs) != 4 {
		t.Errorf("expected 4 mailbox dirs, got %d: %q", len(dirs), dirs)
	}

	if dirs := ctl.serviceDiskDirs("mta"); len(dirs) != 1 {
		t.Errorf("expected 1 mta dir, got %d: %q", len(dirs), dirs)
	}

	if dirs := ctl.serviceDiskDirs("nonexistent"); dirs != nil {
		t.Errorf("unknown service must have no dirs, got %q", dirs)
	}

	for _, svc := range []string{"mailbox", "mta"} {
		seen := make(map[string]bool)

		for _, dir := range ctl.serviceDiskDirs(svc) {
			if seen[dir] {
				t.Errorf("service %q has duplicate dir %q", svc, dir)
			}

			seen[dir] = true
		}
	}
}

func TestGetDiskThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lc   map[string]string
		err  error
		want int
	}{
		{"no key", map[string]string{}, nil, defaultDiskThresholdMB},
		{"configured", map[string]string{"zimbra_disk_threshold": "250"}, nil, 250},
		{"not a number", map[string]string{"zimbra_disk_threshold": "lots"}, nil, defaultDiskThresholdMB},
		{"not positive", map[string]string{"zimbra_disk_threshold": "0"}, nil, defaultDiskThresholdMB},
		{"loader error", nil, errors.New("boom"), defaultDiskThresholdMB},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctl, _ := newFakeController(t, WithConfigLoader(func() (map[string]string, error) { return tc.lc, tc.err }))

			if got := ctl.GetDiskThreshold(); got != tc.want {
				t.Errorf("GetDiskThreshold() = %d, want %d", got, tc.want)
			}
		})
	}
}

// availBytesQueries counts the AvailBytes queries made through f.
func availBytesQueries(f *systest.Fakes) *[]string {
	var queried []string

	f.FS.Fail = func(op, path string) error {
		if op == "AvailBytes" {
			queried = append(queried, path)
		}

		return nil
	}

	return &queried
}

func TestCheckServiceDiskSpace_UnknownService(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	queried := availBytesQueries(f)

	ctl.CheckServiceDiskSpace("nonexistent", 100)

	if len(*queried) != 0 {
		t.Errorf("unknown service must not query any directory, got %q", *queried)
	}
}

// TestCheckServiceDiskSpace_SkipsMissingDirs verifies directories that do not
// exist are skipped and the existing ones are checked.
func TestCheckServiceDiskSpace_SkipsMissingDirs(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	queried := availBytesQueries(f)

	f.FS.PutDir(ctl.p.store, 0o755)
	f.FS.PutDir(ctl.p.base+"/redolog", 0o755)

	ctl.CheckServiceDiskSpace("mailbox", 100)

	want := []string{ctl.p.store, ctl.p.base + "/redolog"}
	if !slices.Equal(*queried, want) {
		t.Errorf("queried dirs = %q, want %q", *queried, want)
	}
}

func TestCheckServiceDiskSpace_NoExistingDirs(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	queried := availBytesQueries(f)

	ctl.CheckServiceDiskSpace("mta", 100)

	if len(*queried) != 0 {
		t.Errorf("no directory exists, nothing may be queried: %q", *queried)
	}
}

// TestCheckServiceDiskSpace_WarnsBelowThreshold runs the warning branch
// (printed to stdout) on a real temp dir used as the MTA spool.
func TestCheckServiceDiskSpace_WarnsBelowThreshold(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/testfile", []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	ctl.p.mtaSpool = dir

	// Low threshold: silent. Impossibly high threshold: the warning branch.
	ctl.CheckServiceDiskSpace("mta", 1)
	ctl.CheckServiceDiskSpace("mta", 999999999)
}
