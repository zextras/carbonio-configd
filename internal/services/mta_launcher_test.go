// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// --- writePostfixLDAPConfig ---

func TestWritePostfixLDAPConfig_MissingLDAPURL(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	err := ctl.writePostfixLDAPConfig(context.Background(), map[string]string{})
	if err == nil {
		t.Fatal("expected error when ldap_url missing")
	}
}

func TestWritePostfixLDAPConfig_WritesFiles(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()

	ctl.p.conf = tmp

	lc := map[string]string{
		"ldap_url":  "ldap://test",
		"ldap_port": "389",
	}

	err := ctl.writePostfixLDAPConfig(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{
		"ldap-vmm.cf", "ldap-vmd.cf", "ldap-vam.cf", "ldap-vad.cf",
		"ldap-canonical.cf", "ldap-transport.cf", "ldap-slm.cf", "ldap-splitdomain.cf",
	} {
		path := filepath.Join(tmp, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}

		if !strings.Contains(string(data), "bind_pw =") {
			t.Errorf("%s missing bind_pw line", name)
		}
	}
}

func TestWritePostfixLDAPConfig_EnforcesMode0640OnExistingFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()

	ctl.p.conf = tmp

	existing := filepath.Join(tmp, "ldap-vmm.cf")
	if err := os.WriteFile(existing, []byte("old"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(existing, 0o666); err != nil {
		t.Fatal(err)
	}

	lc := map[string]string{"ldap_url": "ldap://test", "ldap_port": "389"}
	if err := ctl.writePostfixLDAPConfig(context.Background(), lc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o640 {
		t.Errorf("ldap-vmm.cf mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestWritePostfixLDAPConfig_StartTLS(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()

	ctl.p.conf = tmp

	lc := map[string]string{
		"ldap_url":                "ldap://test",
		"ldap_port":               "389",
		"ldap_starttls_supported": "1",
	}

	err := ctl.writePostfixLDAPConfig(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmp, "ldap-vmm.cf"))
	if !strings.Contains(string(data), "start_tls = yes") {
		t.Error("expected start_tls = yes when ldap_starttls_supported=1")
	}
}

func TestWritePostfixLDAPConfig_EmptyPortFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	lc := map[string]string{
		"ldap_url":  "ldap://localhost",
		"ldap_port": "",
	}

	err := ctl.writePostfixLDAPConfig(context.Background(), lc)
	if err == nil {
		t.Fatal("expected error when ldap_port is empty")
	}
}

func TestWritePostfixLDAPConfig_MkdirError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.p.conf = "/proc/nonexistent-conf-dir-test"

	lc := map[string]string{
		"ldap_url":  "ldap://localhost",
		"ldap_port": "389",
	}
	err := ctl.writePostfixLDAPConfig(context.Background(), lc)
	if err == nil {
		t.Fatal("expected error for inaccessible conf dir")
	}
}

// --- bootstrapPostfixMainCf ---

func TestBootstrapPostfixMainCf_AlreadyExists(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	mainCf := filepath.Join(tmp, "main.cf")
	if err := os.WriteFile(mainCf, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.mainCf = mainCf

	err := ctl.bootstrapPostfixMainCf(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(mainCf)
	if string(data) != "existing" {
		t.Error("main.cf should not have been modified")
	}
}

func TestBootstrapPostfixMainCf_MissingOwner(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	lc := map[string]string{
		"postfix_mail_owner":   "",
		"postfix_setgid_group": "",
	}

	err := ctl.bootstrapPostfixMainCf(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{sudoBin + " /usr/bin/touch " + ctl.p.mainCf}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want only the touch (postconf seed skipped): %q", got, want)
	}
}

func TestBootstrapPostfixMainCf_InvalidOwnerGroup(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ owner, group string }{
		{"-evil", "postdrop"},
		{"postfix", "a b"},
		{"postfix", "x=y"},
		{"Post", "postdrop"},
		{"postfix", "$(id)"},
	} {
		ctl, f := newFakeController(t)

		err := ctl.bootstrapPostfixMainCf(context.Background(), map[string]string{
			"postfix_mail_owner":   tc.owner,
			"postfix_setgid_group": tc.group,
		})

		if !errors.Is(err, errInvalidPostfixAccount) {
			t.Errorf("%+v: err = %v, want errInvalidPostfixAccount", tc, err)
		}

		if calls := f.Exec.Lines(); len(calls) != 0 {
			t.Errorf("%+v: sudo must not run for invalid names, got %q", tc, calls)
		}
	}
}

func TestBootstrapPostfixMainCf_NewFile(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	lc := map[string]string{
		"postfix_mail_owner":   "postfix",
		"postfix_setgid_group": "postdrop",
	}

	err := ctl.bootstrapPostfixMainCf(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		sudoBin + " /usr/bin/touch " + ctl.p.mainCf,
		sudoBin + " " + ctl.p.postconfBin + " -e mail_owner=postfix setgid_group=postdrop",
	}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestBootstrapPostfixMainCf_TouchFails(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath(sudoBin, systest.Fail(1, "sudo: a password is required"))

	err := ctl.bootstrapPostfixMainCf(context.Background(), map[string]string{
		"postfix_mail_owner":   "postfix",
		"postfix_setgid_group": "postdrop",
	})
	if err == nil || !strings.Contains(err.Error(), "touch "+ctl.p.mainCf) {
		t.Fatalf("err = %v, want touch failure", err)
	}

	if got := f.Exec.Lines(); len(got) != 1 {
		t.Errorf("postconf must not run after a failed touch, got %q", got)
	}
}

// --- chgrpPostfixLDAPFiles ---

func TestChgrpPostfixLDAPFiles_GroupNotFound(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.conf+"/ldap-vmm.cf", []byte("test"), 0o640)

	// The fake Proc knows no "postfix" group.
	ctl.chgrpPostfixLDAPFiles(context.Background())

	if got := f.Rec.Of("fs.chown"); len(got) != 0 {
		t.Errorf("no chown expected when the group is missing, got %q", got)
	}
}

func TestChgrpPostfixLDAPFiles_ChownsAllFiles(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Proc.SetGID("postfix", 4242)

	for _, name := range []string{"ldap-vmm.cf", "ldap-vmd.cf"} {
		f.FS.Put(ctl.p.conf+"/"+name, []byte("test"), 0o640)
	}

	ctl.chgrpPostfixLDAPFiles(context.Background())

	for _, name := range []string{"ldap-vmm.cf", "ldap-vmd.cf"} {
		if _, gid, ok := f.FS.Owner(ctl.p.conf + "/" + name); !ok || gid != 4242 {
			t.Errorf("%s: gid = %d (found=%v), want 4242", name, gid, ok)
		}
	}
}

func TestChgrpPostfixLDAPFiles_NoFiles(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Proc.SetGID("postfix", 4242)

	// Missing files only log a warning.
	ctl.chgrpPostfixLDAPFiles(context.Background())
}

// --- runPostalias ---

func TestRunPostalias_MissingFile(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	ctl.runPostalias(context.Background())

	if got := f.Exec.Lines(); len(got) != 0 {
		t.Errorf("postalias must not run without an aliases file, got %q", got)
	}
}

func TestRunPostalias_Exists(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.aliases, []byte("root: admin\n"), 0o644)

	ctl.runPostalias(context.Background())

	want := []string{sudoBin + " " + ctl.p.postaliasBin + " " + ctl.p.aliases}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestRunPostalias_FailureIsNonFatal(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.aliases, []byte("root: admin\n"), 0o644)
	f.Exec.OnPath(sudoBin, systest.Fail(1, "postalias: fatal"))

	ctl.runPostalias(context.Background())
}

// --- startPostfixDaemon / mtaIsRunning / sudoRun error paths ---

func TestStartPostfixDaemon_Error(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == sudoBin }, errors.New("sudo: not found"))

	if err := ctl.startPostfixDaemon(context.Background()); err == nil {
		t.Fatal("expected error when sudo is missing")
	}
}

func TestStartPostfixDaemon_RunsStart(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	if err := ctl.startPostfixDaemon(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{sudoBin + " " + ctl.p.postfixBin + " start"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestMtaIsRunning_Error(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == sudoBin }, errors.New("sudo: not found"))

	if ctl.mtaIsRunning(context.Background()) {
		t.Error("expected false when sudo is missing")
	}
}

func TestMtaIsRunning_Running(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	if !ctl.mtaIsRunning(context.Background()) {
		t.Error("expected true when `postfix status` succeeds")
	}

	want := []string{sudoBin + " " + ctl.p.postfixBin + " status"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestSudoRun_Error(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == sudoBin }, errors.New("sudo: not found"))

	if err := ctl.sudoRun(context.Background(), "/bin/true"); err == nil {
		t.Fatal("expected error when sudo is missing")
	}
}

func TestSudoRun_WithArgs(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	if err := ctl.sudoRun(context.Background(), "/bin/true", "arg1", "arg2"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}

	want := []string{sudoBin + " /bin/true arg1 arg2"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

// --- mtaCustomStart error path ---

func TestMtaCustomStart_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}

	err := ctl.mtaCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
}

func TestMtaCustomStart_RunningSkip(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"ldap_url": "ldap://localhost", "ldap_port": "389"}, nil
	}))

	// `postfix status` succeeds (fake default): the start is skipped.
	if err := ctl.mtaCustomStart(context.Background(), nil); err != nil {
		t.Fatalf("mtaCustomStart (running skip): %v", err)
	}

	want := []string{sudoBin + " " + ctl.p.postfixBin + " status"}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want only the status probe %q", got, want)
	}
}

func TestMtaCustomStart_StartsWhenNotRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"ldap_url": "ldap://localhost", "ldap_port": "389"}, nil
	}))

	f.Exec.When(func(c *sys.Cmd) bool { return slices.Contains(c.Args, "status") }, systest.Fail(1, ""))
	f.FS.Put(ctl.p.mainCf, []byte("existing config\n"), 0o644) // bootstrap is a no-op
	f.FS.PutDir(ctl.p.conf, 0o755)

	if err := ctl.mtaCustomStart(context.Background(), nil); err != nil {
		t.Fatalf("mtaCustomStart: %v", err)
	}

	want := []string{
		sudoBin + " " + ctl.p.postfixBin + " status",
		sudoBin + " " + ctl.p.postfixBin + " start",
	}
	if got := f.Exec.Lines(); !slices.Equal(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}

	if !f.FS.Exists(ctl.p.conf + "/ldap-vmm.cf") {
		t.Error("expected the postfix ldap-*.cf tables to be written before the start")
	}
}

func TestMtaCustomStart_BootstrapError(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return nil, fmt.Errorf("config error")
	}))
	f.Exec.When(func(c *sys.Cmd) bool { return slices.Contains(c.Args, "status") }, systest.Fail(1, ""))

	if err := ctl.mtaCustomStart(context.Background(), nil); err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
}

// --- mtaCustomStop ---

func TestMtaCustomStop_NotRunningFromLaunchers(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath(sudoBin, systest.Fail(1, "postfix/postfix-script: the Postfix mail system is not running"))

	if err := ctl.mtaCustomStop(context.Background(), nil); err != nil {
		t.Errorf("expected nil when postfix reports 'not running', got %v", err)
	}
}

func TestMtaCustomStop_Error(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath(sudoBin, systest.Fail(2, "fatal error"))

	if err := ctl.mtaCustomStop(context.Background(), nil); err == nil {
		t.Fatal("expected error when postfix stop fails with unexpected output")
	}
}
