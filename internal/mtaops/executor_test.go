// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.InitStructuredLogging(&logger.Config{
		Format: logger.FormatText,
		Level:  logger.LogLevelInfo,
	})
	os.Exit(m.Run())
}

// testBaseDir is the fake installation root every fake-backed test uses.
const testBaseDir = "/opt/zextras"

// newFakeExecutor builds an executor over fresh fakes.
func newFakeExecutor(ldapMgr ldap.AttributeModifier) (*executor, *systest.Fakes) {
	fakes := systest.New()

	return NewExecutor(testBaseDir, ldapMgr, fakes.System()).(*executor), fakes
}

const postconfBin = testBaseDir + "/common/sbin/postconf"

func TestExecutor_Initialization(t *testing.T) {
	t.Parallel()

	mockLdap := &mockLdapManager{}
	baseDir := "/opt/zextras"

	executor := NewExecutor(baseDir, mockLdap, systest.New().System()).(*executor)

	if executor == nil {
		t.Fatal("NewExecutor should not return nil")
	}

	if executor.baseDir != baseDir {
		t.Errorf("baseDir = %v, want %v", executor.baseDir, baseDir)
	}

	expectedPostconfPath := filepath.Join(baseDir, "common", "sbin", "postconf")
	if executor.postconfPath != expectedPostconfPath {
		t.Errorf("postconfPath = %v, want %v", executor.postconfPath, expectedPostconfPath)
	}

	if executor.ldapManager != mockLdap {
		t.Error("ldapManager not set correctly")
	}

	if len(executor.mappedFiles) == 0 {
		t.Error("mappedFiles should be initialized")
	}

	// Check zimbraSSLDHParam mapping
	expectedDHParamPath := filepath.Join(baseDir, "conf", "dhparam.pem")
	if executor.mappedFiles[attrZimbraSSLDHParam] != expectedDHParamPath {
		t.Errorf("zimbraSSLDHParam mapping = %v, want %v",
			executor.mappedFiles[attrZimbraSSLDHParam], expectedDHParamPath)
	}
}

func TestExecutePostconf(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	op := PostconfOperation{
		Key:   "myhostname",
		Value: "mail.example.com",
	}

	err := executor.ExecutePostconf(context.Background(), op)
	if err != nil {
		t.Errorf("ExecutePostconf failed: %v", err)
	}

	want := []string{"exec " + postconfBin + " -e myhostname=mail.example.com"}
	if got := fakes.Rec.Strings(); !slices.Equal(got, want) {
		t.Errorf("recorded ops = %q, want %q", got, want)
	}
}

func TestExecutePostconfBatch_MultilineValues(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	// Test multi-line value normalization
	ops := []PostconfOperation{
		{
			Key:   "smtpd_restriction_classes",
			Value: "restriction_class1\nrestriction_class2\nrestriction_class3",
		},
	}

	err := executor.ExecutePostconfBatch(context.Background(), ops)
	if err != nil {
		t.Errorf("ExecutePostconfBatch failed: %v", err)
	}

	// Should have comma-separated format
	args := strings.Join(fakes.Exec.Calls()[0].Args, " ")
	if !strings.Contains(args, "restriction_class1, restriction_class2, restriction_class3") {
		t.Errorf("Multi-line value not normalized correctly: %s", args)
	}
}

func TestExecutePostconfBatch_EmptyBatch(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	err := executor.ExecutePostconfBatch(context.Background(), nil)
	if err != nil {
		t.Errorf("ExecutePostconfBatch with nil should not error: %v", err)
	}

	err = executor.ExecutePostconfBatch(context.Background(), []PostconfOperation{})
	if err != nil {
		t.Errorf("ExecutePostconfBatch with empty slice should not error: %v", err)
	}

	if got := fakes.Rec.Strings(); len(got) != 0 {
		t.Errorf("empty batch must not run anything, recorded %q", got)
	}
}

func TestExecutePostconfd(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	op := PostconfdOperation{
		Key: "deprecated_param",
	}

	err := executor.ExecutePostconfd(context.Background(), op)
	if err != nil {
		t.Errorf("ExecutePostconfd failed: %v", err)
	}

	want := []string{"exec " + postconfBin + " -X deprecated_param"}
	if got := fakes.Rec.Strings(); !slices.Equal(got, want) {
		t.Errorf("recorded ops = %q, want %q", got, want)
	}
}

func TestExecutePostconfdBatch_NonExistentKey(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})
	// postconf -X fails (key doesn't exist)
	fakes.Exec.OnPath(postconfBin, systest.Fail(1, "postconf: warning: unknown parameter: deprecated_param\n"))

	ops := []PostconfdOperation{
		{Key: "deprecated_param"},
	}

	// postconf -X's "unknown parameter" warning is expected and must not error.
	err := executor.ExecutePostconfdBatch(context.Background(), ops)
	if err != nil {
		t.Errorf("ExecutePostconfdBatch should not error on non-existent keys: %v", err)
	}
}

// TestExecutePostconfdBatch_GenericFailure verifies that a postconf -X failure
// unrelated to an unknown parameter (e.g. permission denied, crash) is reported
// to the caller instead of being silently swallowed.
func TestExecutePostconfdBatch_GenericFailure(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})
	fakes.Exec.OnPath(postconfBin, systest.Fail(1, "postconf: fatal: permission denied\n"))

	ops := []PostconfdOperation{
		{Key: "some_param"},
	}

	err := executor.ExecutePostconfdBatch(context.Background(), ops)
	if err == nil {
		t.Fatal("ExecutePostconfdBatch should return an error for non-unknown-parameter failures")
	}

	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should include the postconf output snippet, got: %v", err)
	}

	if !strings.Contains(err.Error(), "postconf -X batch failed: exit status 1, output: ") {
		t.Errorf("error text changed: %v", err)
	}
}

func TestExecuteMapfile_WriteBase64Data(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	// Set up mapped file path
	testFilePath := filepath.Join(testBaseDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	// Create test data
	testData := "-----BEGIN DH PARAMETERS-----\ntest data\n-----END DH PARAMETERS-----\n"
	base64Data := base64.StdEncoding.EncodeToString([]byte(testData))

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: base64Data,
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Fatalf("ExecuteMapfile failed: %v", err)
	}

	// Verify file was written
	writtenData, ok := fakes.FS.Data(testFilePath)
	if !ok {
		t.Fatal("mapped file was not written")
	}

	if string(writtenData) != testData {
		t.Errorf("Written data = %q, want %q", string(writtenData), testData)
	}

	// Test idempotency - writing same data again should succeed without a second write
	fakes.Rec.Reset()

	err = executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteMapfile should be idempotent: %v", err)
	}

	if got := fakes.Rec.Strings(); len(got) != 0 {
		t.Errorf("unchanged content must not be rewritten, recorded %q", got)
	}
}

func TestExecuteMapfile_InvalidBase64(t *testing.T) {
	t.Parallel()

	executor, _ := newFakeExecutor(&mockLdapManager{})

	testFilePath := filepath.Join(testBaseDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "invalid!@#$%base64",
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err == nil {
		t.Error("ExecuteMapfile should fail with invalid base64 data")
	}
}

func TestExecuteMapfile_UnmappedKey(t *testing.T) {
	t.Parallel()

	executor, _ := newFakeExecutor(&mockLdapManager{})

	op := MapfileOperation{
		Key:        "unmappedKey",
		IsLocal:    false,
		Base64Data: "dGVzdA==",
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err == nil {
		t.Fatal("ExecuteMapfile should fail with unmapped key")
	}

	if !strings.Contains(err.Error(), "unmapped MAPFILE key") {
		t.Errorf("Error should mention unmapped key, got: %v", err)
	}
}

func TestExecuteMapfile_MAPLOCAL_FileExists(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	testFilePath := filepath.Join(testBaseDir, "existing.crt")
	fakes.FS.Put(testFilePath, []byte("test cert"), 0o600)

	executor.mappedFiles["testCert"] = testFilePath

	op := MapfileOperation{
		Key:     "testCert",
		IsLocal: true, // MAPLOCAL
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("MAPLOCAL with existing file should succeed: %v", err)
	}
}

func TestExecuteMapfile_MAPLOCAL_FileNotExists(t *testing.T) {
	t.Parallel()

	executor, _ := newFakeExecutor(&mockLdapManager{})

	testFilePath := filepath.Join(testBaseDir, "nonexistent.crt")
	executor.mappedFiles["testCert"] = testFilePath

	op := MapfileOperation{
		Key:     "testCert",
		IsLocal: true, // MAPLOCAL
	}

	// Should not error, just log that file doesn't exist
	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("MAPLOCAL with non-existent file should not error: %v", err)
	}
}

func TestHandleEmptyMapfileData_RemovesStaleFile(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	testFilePath := filepath.Join(testBaseDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	// Existing mapped file that should be removed when the VAR is cleared,
	// mirroring jylibs/state.py os.remove(mapfile) (e.g. clearing zimbraSSLDHParam).
	fakes.FS.Put(testFilePath, []byte("stale content"), 0o600)

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "", // Empty data => VAR cleared
	}

	if err := executor.ExecuteMapfile(context.Background(), op); err != nil {
		t.Fatalf("ExecuteMapfile with empty data should remove the stale file: %v", err)
	}

	if fakes.FS.Exists(testFilePath) {
		t.Error("stale mapfile should have been removed")
	}
}

func TestHandleEmptyMapfileData_NoBackupNoFile(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})

	testFilePath := filepath.Join(testBaseDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "", // Empty data
	}

	// No backup, no existing file - should not error
	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteMapfile with no data and no file should not error: %v", err)
	}

	// File should not be created
	if fakes.FS.Exists(testFilePath) {
		t.Error("File should not be created when there's no data")
	}
}

func TestExecuteLdapWrite_Success(t *testing.T) {
	t.Parallel()

	mockLdap := &mockLdapManager{}
	executor := NewExecutor("/opt/zextras", mockLdap, systest.New().System())

	op := LdapOperation{
		Key:   "ldap_db_maxsize",
		Value: "1073741824",
	}

	err := executor.ExecuteLdapWrite(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteLdapWrite should succeed: %v", err)
	}

	if !mockLdap.modifyCalled {
		t.Error("LDAP ModifyAttribute should have been called")
	}

	if mockLdap.modifyKey != op.Key {
		t.Errorf("LDAP key = %v, want %v", mockLdap.modifyKey, op.Key)
	}

	if mockLdap.modifyValue != op.Value {
		t.Errorf("LDAP value = %v, want %v", mockLdap.modifyValue, op.Value)
	}
}

func TestExecuteLdapWrite_Failure(t *testing.T) {
	t.Parallel()

	mockLdap := &mockLdapManager{
		modifyErr: errors.New("ldap connection failed"),
	}
	executor := NewExecutor("/opt/zextras", mockLdap, systest.New().System())

	op := LdapOperation{
		Key:   "ldap_db_maxsize",
		Value: "1073741824",
	}

	err := executor.ExecuteLdapWrite(context.Background(), op)
	if err == nil {
		t.Fatal("ExecuteLdapWrite should fail when LDAP manager returns error")
	}

	if !strings.Contains(err.Error(), "LDAP write failed") {
		t.Errorf("Error should mention LDAP write failure, got: %v", err)
	}
}

// TestExecutePostconfdBatch_SuccessWithOutput covers the debug-output branch
// where postconf -X succeeds and produces stdout.
func TestExecutePostconfdBatch_SuccessWithOutput(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})
	fakes.Exec.OnPath(postconfBin, systest.Reply("postconf: deprecated: some_param\n"))

	ops := []PostconfdOperation{
		{Key: "some_param"},
	}

	err := executor.ExecutePostconfdBatch(context.Background(), ops)
	if err != nil {
		t.Errorf("ExecutePostconfdBatch with output should not error: %v", err)
	}
}

// TestExecutePostconfdBatch_StartFailure verifies a postconf that cannot be
// started (missing binary) surfaces to the caller.
func TestExecutePostconfdBatch_StartFailure(t *testing.T) {
	t.Parallel()

	executor, fakes := newFakeExecutor(&mockLdapManager{})
	fakes.Exec.FailStart(func(*sys.Cmd) bool { return true }, os.ErrNotExist)

	err := executor.ExecutePostconfdBatch(context.Background(), []PostconfdOperation{{Key: "some_param"}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ExecutePostconfdBatch error = %v, want wrapped %v", err, os.ErrNotExist)
	}
}

// TestExecutePostconfdBatch_NilOps verifies ExecutePostconfdBatch returns nil for nil ops.
func TestExecutePostconfdBatch_NilOps(t *testing.T) {
	t.Parallel()

	e := NewExecutor("/opt/zextras", nil, systest.New().System())

	err := e.ExecutePostconfdBatch(context.Background(), nil)
	if err != nil {
		t.Errorf("ExecutePostconfdBatch(nil) = %v, want nil", err)
	}
}

// TestHandleEmptyMapfileData_RemoveError covers the Remove error branch in
// handleEmptyMapfileData: the file exists but cannot be removed because the
// parent directory is read-only. It runs on the real filesystem because
// directory permissions are what the test is about.
func TestHandleEmptyMapfileData_RemoveError(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}

	tmpDir := t.TempDir()

	// Create a sub-directory to hold the file; we'll make it read-only so
	// Remove inside it fails.
	subDir := filepath.Join(tmpDir, "protected")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testFilePath := filepath.Join(subDir, "stale.pem")
	if err := os.WriteFile(testFilePath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Make the parent dir read-only so Remove fails.
	if err := os.Chmod(subDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(subDir, 0o755) //nolint:errcheck // best-effort cleanup

	ex := NewExecutor(tmpDir, &mockLdapManager{}, sys.Real()).(*executor)
	ex.mappedFiles["staleKey"] = testFilePath

	op := MapfileOperation{
		Key:        "staleKey",
		IsLocal:    false,
		Base64Data: "", // empty => handleEmptyMapfileData
	}

	err := ex.ExecuteMapfile(context.Background(), op)
	if err == nil {
		t.Error("expected an error when Remove fails, got nil")
	}
}

func TestExecutePostconfBatch_SkipsInvalidKeys(t *testing.T) {
	t.Parallel()

	ex, fakes := newFakeExecutor(&mockLdapManager{})

	ops := []PostconfOperation{
		{Key: "-X", Value: "evil"},
		{Key: "lmtp_tls_CAfile", Value: "/ca"},
		{Key: "a=b", Value: "c"},
		{Key: "", Value: "c"},
		{Key: "myhostname", Value: "h"},
	}

	if err := ex.ExecutePostconfBatch(context.Background(), ops); err != nil {
		t.Fatalf("ExecutePostconfBatch: %v", err)
	}

	got := fakes.Exec.Calls()
	if len(got) != 1 {
		t.Fatalf("postconf ran %d times, want 1", len(got))
	}

	if want := []string{"-e", "lmtp_tls_CAfile=/ca", "myhostname=h"}; !slices.Equal(got[0].Args, want) {
		t.Errorf("postconf args = %q, want %q", got[0].Args, want)
	}
}

func TestExecutePostconfBatch_AllKeysInvalidRunsNothing(t *testing.T) {
	t.Parallel()

	ex, fakes := newFakeExecutor(&mockLdapManager{})

	err := ex.ExecutePostconfBatch(context.Background(), []PostconfOperation{{Key: "-X", Value: "v"}})
	if err != nil {
		t.Errorf("expected nil when every key is skipped, got %v", err)
	}

	if got := fakes.Rec.Strings(); len(got) != 0 {
		t.Errorf("no command may run when every key is skipped, recorded %q", got)
	}
}

func TestExecuteMapfile_NewFileHonoursUmask(t *testing.T) {
	t.Parallel()

	ex, fakes := newFakeExecutor(&mockLdapManager{})
	fakes.Proc.UmaskValue = 0o027

	path := filepath.Join(testBaseDir, "dhparam.pem")
	ex.mappedFiles["k"] = path

	op := MapfileOperation{Key: "k", Base64Data: base64.StdEncoding.EncodeToString([]byte("dh"))}
	if err := ex.ExecuteMapfile(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	mode, ok := fakes.FS.Mode(path)
	if !ok {
		t.Fatal("mapped file was not written")
	}

	if mode.Perm() != 0o640 {
		t.Errorf("new MAPFILE mode = %o, want 640 (legacy open(w) under umask 027)", mode.Perm())
	}

	want := []string{"fs.write-atomic " + path + " mode=0640"}
	if got := fakes.Rec.Strings(); !slices.Equal(got, want) {
		t.Errorf("recorded ops = %q, want %q", got, want)
	}
}

func TestExecuteMapfile_ExistingFileKeepsMode(t *testing.T) {
	t.Parallel()

	ex, fakes := newFakeExecutor(&mockLdapManager{})
	path := filepath.Join(testBaseDir, "dhparam.pem")
	fakes.FS.Put(path, []byte("old"), 0o600)

	ex.mappedFiles["k"] = path

	op := MapfileOperation{Key: "k", Base64Data: base64.StdEncoding.EncodeToString([]byte("new"))}
	if err := ex.ExecuteMapfile(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	if mode, _ := fakes.FS.Mode(path); mode.Perm() != 0o600 {
		t.Errorf("existing MAPFILE mode = %o, want 600 preserved", mode.Perm())
	}

	if b, _ := fakes.FS.Data(path); string(b) != "new" {
		t.Errorf("content = %q", b)
	}

	want := []string{"fs.write-atomic " + path + " mode=0600"}
	if got := fakes.Rec.Strings(); !slices.Equal(got, want) {
		t.Errorf("recorded ops = %q, want %q", got, want)
	}
}

// TestExecuteMapfile_RealFSWritesAtomically runs the real filesystem: temp +
// rename replaces the inode instead of truncating it, and the new file honours
// the process umask via the real Proc.
func TestExecuteMapfile_RealFSWritesAtomically(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ex := NewExecutor(tmpDir, &mockLdapManager{}, sys.Real()).(*executor)
	path := filepath.Join(tmpDir, "dhparam.pem")

	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	ex.mappedFiles["k"] = path

	op := MapfileOperation{Key: "k", Base64Data: base64.StdEncoding.EncodeToString([]byte("new"))}
	if err := ex.ExecuteMapfile(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if after.Mode().Perm() != 0o600 {
		t.Errorf("existing MAPFILE mode = %o, want 600 preserved", after.Mode().Perm())
	}

	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("content = %q", b)
	}

	if os.SameFile(before, after) {
		t.Error("MAPFILE was rewritten in place (same inode); want atomic temp+rename")
	}
}
