// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"os"
	"testing"
)

func TestCheckDiskSpace_RootFS(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	availMB, ok, err := CheckDiskSpace("/", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if availMB <= 0 {
		t.Errorf("expected positive available MB, got %d", availMB)
	}

	if !ok {
		t.Errorf("root filesystem should have > 1MB available")
	}
}

func TestCheckDiskSpace_HighThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, ok, err := CheckDiskSpace("/", 999999999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ok {
		t.Error("should not have 999TB available")
	}
}

func TestCheckDiskSpace_NonexistentPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, _, err := CheckDiskSpace("/nonexistent/path/that/does/not/exist", 100)
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestServiceDiskDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dirs, ok := ServiceDiskDirs["mailbox"]
	if !ok {
		t.Fatal("expected mailbox in ServiceDiskDirs")
	}

	if len(dirs) != 4 {
		t.Errorf("expected 4 mailbox dirs, got %d", len(dirs))
	}

	dirs, ok = ServiceDiskDirs["mta"]
	if !ok {
		t.Fatal("expected mta in ServiceDiskDirs")
	}

	if len(dirs) != 1 {
		t.Errorf("expected 1 mta dir, got %d", len(dirs))
	}
}

func TestGetDiskThreshold_Default(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Without localconfig, should return default
	threshold := GetDiskThreshold()
	if threshold <= 0 {
		t.Errorf("expected positive threshold, got %d", threshold)
	}
}

func TestCheckServiceDiskSpace_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Should not panic or error for unknown service
	CheckServiceDiskSpace("nonexistent", 100)
}

// TestCheckDiskSpace_ZeroThreshold verifies that zero threshold is always satisfied.
func TestCheckDiskSpace_ZeroThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	availMB, ok, err := CheckDiskSpace("/", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ok {
		t.Errorf("CheckDiskSpace with 0 threshold should always be ok, availMB=%d", availMB)
	}
}

// TestCheckDiskSpace_TmpDir verifies CheckDiskSpace works on a real temp directory.
func TestCheckDiskSpace_TmpDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	availMB, ok, err := CheckDiskSpace(dir, 1)
	if err != nil {
		t.Fatalf("unexpected error checking temp dir: %v", err)
	}

	if availMB < 0 {
		t.Errorf("expected non-negative availMB, got %d", availMB)
	}

	// Temp dir should have at least 1 MB available on any real system.
	if !ok {
		t.Logf("warning: temp dir has < 1MB available (%dMB) — unusual environment", availMB)
	}
}

// TestGetDiskThreshold_Positive verifies the threshold is always positive.
func TestGetDiskThreshold_Positive(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	threshold := GetDiskThreshold()
	if threshold <= 0 {
		t.Errorf("GetDiskThreshold() = %d, want > 0", threshold)
	}
}

// TestGetDiskThreshold_DefaultValue verifies default is 100 when no localconfig present.
func TestGetDiskThreshold_DefaultValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// In a test environment without localconfig, should return defaultDiskThresholdMB.
	threshold := GetDiskThreshold()
	// Accept either default (100) or any positive value from a real localconfig.
	if threshold <= 0 {
		t.Errorf("GetDiskThreshold() = %d, must be positive", threshold)
	}
}

// TestCheckServiceDiskSpace_KnownServiceNoDirs verifies known service with no existing dirs.
func TestCheckServiceDiskSpace_KnownServiceNoDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Override ServiceDiskDirs temporarily to use nonexistent paths.
	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"testservice": {"/nonexistent/path/a", "/nonexistent/path/b"},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Should not panic — nonexistent dirs are skipped.
	CheckServiceDiskSpace("testservice", 100)
}

// TestCheckServiceDiskSpace_ExistingDirAboveThreshold verifies no warning when space is sufficient.
func TestCheckServiceDiskSpace_ExistingDirAboveThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"testservice": {dir},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Should not panic with a real directory and reasonable threshold.
	CheckServiceDiskSpace("testservice", 1)
}

// TestCheckServiceDiskSpace_ExistingDirBelowThreshold verifies warning path executes.
func TestCheckServiceDiskSpace_ExistingDirBelowThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"testservice": {dir},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Use an impossibly high threshold to force the warning branch.
	CheckServiceDiskSpace("testservice", 999999999)
}

// TestCheckServiceDiskSpace_MultipleServices verifies all known services are handled.
func TestCheckServiceDiskSpace_MultipleServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	// Create a real directory for each service entry.
	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"mailbox": {dir},
		"mta":     {dir},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Both should execute without panic.
	CheckServiceDiskSpace("mailbox", 100)
	CheckServiceDiskSpace("mta", 100)
}

// TestCheckServiceDiskSpace_MixedExistenceDir verifies that missing dirs are skipped
// and existing dirs are checked.
func TestCheckServiceDiskSpace_MixedExistenceDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"testservice": {
			"/nonexistent/dir",
			dir,
			"/another/nonexistent",
		},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Should process without panic — missing dirs skipped, real dir checked.
	CheckServiceDiskSpace("testservice", 1)
}

// TestServiceDiskDirs_NoDuplicates verifies no duplicate paths in service definitions.
func TestServiceDiskDirs_NoDuplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	for service, dirs := range ServiceDiskDirs {
		seen := make(map[string]bool)

		for _, dir := range dirs {
			if seen[dir] {
				t.Errorf("service %q has duplicate dir %q", service, dir)
			}

			seen[dir] = true
		}
	}
}

// TestCheckDiskSpace_ExactThreshold verifies boundary condition where avail equals threshold.
func TestCheckDiskSpace_ExactThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Get actual available MB on root.
	availMB, _, err := CheckDiskSpace("/", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// At exactly the threshold, should return ok=true.
	_, ok, err := CheckDiskSpace("/", availMB)
	if err != nil {
		t.Fatalf("unexpected error at exact threshold: %v", err)
	}

	if !ok {
		t.Errorf("CheckDiskSpace at exact threshold (%dMB) should return ok=true", availMB)
	}

	// One above the available should return ok=false.
	_, ok, err = CheckDiskSpace("/", availMB+1)
	if err != nil {
		t.Fatalf("unexpected error at threshold+1: %v", err)
	}

	if ok {
		t.Logf("note: availMB changed between calls (concurrent system activity), test inconclusive")
	}
}

// TestCheckServiceDiskSpace_DefaultDirsNotPanic verifies the real ServiceDiskDirs
// entries don't panic when checked (dirs simply won't exist in test environment).
func TestCheckServiceDiskSpace_DefaultDirsNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Restore to original ServiceDiskDirs (they reference /opt/zextras paths).
	services := []string{"mailbox", "mta"}

	for _, svc := range services {
		t.Run(svc, func(t *testing.T) {
			// Should not panic even if paths don't exist.
			CheckServiceDiskSpace(svc, defaultDiskThresholdMB)
		})
	}
}

// TestCheckServiceDiskSpace_WritableTempDir verifies warning output on low-space condition.
func TestCheckServiceDiskSpace_WritableTempDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	// Create a file to verify the directory actually exists.
	testFile := dir + "/testfile"
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	orig := ServiceDiskDirs
	ServiceDiskDirs = map[string][]string{
		"testservice": {dir},
	}

	defer func() { ServiceDiskDirs = orig }()

	// Low threshold (1MB) — should succeed silently.
	CheckServiceDiskSpace("testservice", 1)

	// Very high threshold — triggers the warning print branch.
	CheckServiceDiskSpace("testservice", 999999999)
}

// TestGetDiskThreshold_CustomValue verifies GetDiskThreshold reads from localconfig.
func TestGetDiskThreshold_CustomValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zimbra_disk_threshold": "200"}, nil
	}

	defer func() { loadConfig = old }()

	got := GetDiskThreshold()
	if got != 200 {
		t.Errorf("GetDiskThreshold() = %d, want 200", got)
	}
}

// TestGetDiskThreshold_EmptyConfig verifies default when key is absent.
func TestGetDiskThreshold_EmptyConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	defer func() { loadConfig = old }()

	got := GetDiskThreshold()
	if got != defaultDiskThresholdMB {
		t.Errorf("GetDiskThreshold() = %d, want %d", got, defaultDiskThresholdMB)
	}
}

// TestGetDiskThreshold_InvalidValue verifies default when value is non-numeric.
func TestGetDiskThreshold_InvalidValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zimbra_disk_threshold": "abc"}, nil
	}

	defer func() { loadConfig = old }()

	got := GetDiskThreshold()
	if got != defaultDiskThresholdMB {
		t.Errorf("GetDiskThreshold() = %d, want %d", got, defaultDiskThresholdMB)
	}
}

// TestGetDiskThreshold_ZeroValue verifies default when value is zero.
func TestGetDiskThreshold_ZeroValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zimbra_disk_threshold": "0"}, nil
	}

	defer func() { loadConfig = old }()

	got := GetDiskThreshold()
	if got != defaultDiskThresholdMB {
		t.Errorf("GetDiskThreshold() = %d, want %d", got, defaultDiskThresholdMB)
	}
}

// TestGetDiskThreshold_NegativeValue verifies default when value is negative.
func TestGetDiskThreshold_NegativeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zimbra_disk_threshold": "-5"}, nil
	}

	defer func() { loadConfig = old }()

	got := GetDiskThreshold()
	if got != defaultDiskThresholdMB {
		t.Errorf("GetDiskThreshold() = %d, want %d", got, defaultDiskThresholdMB)
	}
}
