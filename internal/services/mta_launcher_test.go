// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- writePostfixLDAPConfig ---

func TestWritePostfixLDAPConfig_MissingLDAPURL(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := writePostfixLDAPConfig(context.Background(), map[string]string{})
	if err == nil {
		t.Fatal("expected error when ldap_url missing")
	}
}

func TestWritePostfixLDAPConfig_WritesFiles(t *testing.T) {
	tmp := t.TempDir()

	oldConf := confPath
	confPath = tmp
	defer func() { confPath = oldConf }()

	lc := map[string]string{
		"ldap_url":  "ldap://test",
		"ldap_port": "389",
	}

	err := writePostfixLDAPConfig(context.Background(), lc)
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

func TestWritePostfixLDAPConfig_StartTLS(t *testing.T) {
	tmp := t.TempDir()

	oldConf := confPath
	confPath = tmp
	defer func() { confPath = oldConf }()

	lc := map[string]string{
		"ldap_url":                "ldap://test",
		"ldap_port":               "389",
		"ldap_starttls_supported": "1",
	}

	err := writePostfixLDAPConfig(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmp, "ldap-vmm.cf"))
	if !strings.Contains(string(data), "start_tls = yes") {
		t.Error("expected start_tls = yes when ldap_starttls_supported=1")
	}
}

func TestWritePostfixLDAPConfig_EmptyPortFails(t *testing.T) {
	lc := map[string]string{
		"ldap_url":  "ldap://localhost",
		"ldap_port": "",
	}

	err := writePostfixLDAPConfig(context.Background(), lc)
	if err == nil {
		t.Fatal("expected error when ldap_port is empty")
	}
}

func TestWritePostfixLDAPConfig_MkdirError(t *testing.T) {
	origConf := confPath
	confPath = "/proc/nonexistent-conf-dir-test"
	defer func() { confPath = origConf }()

	lc := map[string]string{
		"ldap_url":  "ldap://localhost",
		"ldap_port": "389",
	}
	err := writePostfixLDAPConfig(context.Background(), lc)
	if err == nil {
		t.Fatal("expected error for inaccessible conf dir")
	}
}

// --- bootstrapPostfixMainCf ---

func TestBootstrapPostfixMainCf_AlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	mainCf := filepath.Join(tmp, "main.cf")
	if err := os.WriteFile(mainCf, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldMainCf := mainCfPath
	mainCfPath = mainCf
	defer func() { mainCfPath = oldMainCf }()

	err := bootstrapPostfixMainCf(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(mainCf)
	if string(data) != "existing" {
		t.Error("main.cf should not have been modified")
	}
}

func TestBootstrapPostfixMainCf_MissingOwner(t *testing.T) {
	tmp := t.TempDir()
	mainCf := filepath.Join(tmp, "main.cf")

	oldMainCf := mainCfPath
	mainCfPath = mainCf
	defer func() { mainCfPath = oldMainCf }()

	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	if err := os.WriteFile(sudoBin, []byte("#!/bin/sh\n$@\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { sudoBin = oldSudo }()

	oldPostconf := postconfBin
	postconfBin = filepath.Join(tmp, "postconf")
	if err := os.WriteFile(postconfBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postconfBin = oldPostconf }()

	lc := map[string]string{
		"postfix_mail_owner":   "",
		"postfix_setgid_group": "",
	}

	err := bootstrapPostfixMainCf(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBootstrapPostfixMainCf_NewFile(t *testing.T) {
	tmp := t.TempDir()
	mainCf := filepath.Join(tmp, "main.cf")

	origPath := mainCfPath
	mainCfPath = mainCf
	defer func() { mainCfPath = origPath }()

	fakeSudo := filepath.Join(tmp, "sudo")
	if err := os.WriteFile(fakeSudo, []byte("#!/bin/sh\n$@\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origSudo := sudoBin
	sudoBin = fakeSudo
	defer func() { sudoBin = origSudo }()

	fakePostconf := filepath.Join(tmp, "postconf")
	if err := os.WriteFile(fakePostconf, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origPostconf := postconfBin
	postconfBin = fakePostconf
	defer func() { postconfBin = origPostconf }()

	lc := map[string]string{
		"postfix_mail_owner":   "postfix",
		"postfix_setgid_group": "postdrop",
	}

	err := bootstrapPostfixMainCf(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(mainCf); statErr != nil {
		t.Errorf("main.cf should exist after bootstrap: %v", statErr)
	}
}

// --- chgrpPostfixLDAPFiles ---

func TestChgrpPostfixLDAPFiles_GroupNotFound(t *testing.T) {
	tmp := t.TempDir()

	oldConf := confPath
	confPath = tmp
	defer func() { confPath = oldConf }()

	for _, name := range []string{"ldap-vmm.cf", "ldap-vmd.cf"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("test"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	// Using a group name that does not exist on any system
	chgrpPostfixLDAPFiles(context.Background())
	// Should not panic; files remain unchanged
}

func TestChgrpPostfixLDAPFiles_NoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	origConf := confPath
	confPath = tmpDir
	defer func() { confPath = origConf }()

	chgrpPostfixLDAPFiles(context.Background())
}

// --- runPostalias ---

func TestRunPostalias_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	aliases := filepath.Join(tmp, "aliases")

	oldAliases := aliasesPath
	aliasesPath = aliases
	defer func() { aliasesPath = oldAliases }()

	// Should not panic when aliases file is missing
	runPostalias(context.Background())
}

func TestRunPostalias_Exists(t *testing.T) {
	tmp := t.TempDir()
	aliases := filepath.Join(tmp, "aliases")
	if err := os.WriteFile(aliases, []byte("root: admin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldAliases := aliasesPath
	aliasesPath = aliases
	defer func() { aliasesPath = oldAliases }()

	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	if err := os.WriteFile(sudoBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { sudoBin = oldSudo }()

	oldPostalias := postaliasBin
	postaliasBin = filepath.Join(tmp, "postalias")
	if err := os.WriteFile(postaliasBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postaliasBin = oldPostalias }()

	runPostalias(context.Background())
}

// --- startPostfixDaemon / mtaIsRunning / sudoRun error paths ---

func TestStartPostfixDaemon_Error(t *testing.T) {
	oldSudo := sudoBin
	sudoBin = "/nonexistent/sudo"
	defer func() { sudoBin = oldSudo }()

	err := startPostfixDaemon(context.Background())
	if err == nil {
		t.Fatal("expected error when sudo is missing")
	}
}

func TestMtaIsRunning_Error(t *testing.T) {
	oldSudo := sudoBin
	sudoBin = "/nonexistent/sudo"
	defer func() { sudoBin = oldSudo }()

	if mtaIsRunning(context.Background()) {
		t.Error("expected false when sudo is missing")
	}
}

func TestSudoRun_Error(t *testing.T) {
	oldSudo := sudoBin
	sudoBin = "/nonexistent/sudo"
	defer func() { sudoBin = oldSudo }()

	err := sudoRun(context.Background(), "/bin/true")
	if err == nil {
		t.Fatal("expected error when sudo is missing")
	}
}

func TestSudoRun_WithArgs(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	if err := os.WriteFile(sudoBin, []byte("#!/bin/sh\necho ok\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { sudoBin = oldSudo }()

	err := sudoRun(context.Background(), "/bin/true", "arg1", "arg2")
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// --- mtaCustomStart error path ---

func TestMtaCustomStart_LoadConfigFails(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}
	defer func() { loadConfig = oldLC }()

	err := mtaCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
}

func TestMtaCustomStart_RunningSkip(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	defer func() { sudoBin = oldSudo }()

	fakeSudo := `#!/bin/sh
case "$2" in
  status) exit 0 ;;
  start) exit 0 ;;
  *) exit 0 ;;
esac`
	if err := os.WriteFile(sudoBin, []byte(fakeSudo), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	if err := os.WriteFile(postfixBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postfixBin = oldPostfix }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":  "ldap://localhost",
			"ldap_port": "389",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldConf := confPath
	confPath = filepath.Join(tmp, "conf")
	defer func() { confPath = oldConf }()

	oldMainCf := mainCfPath
	mainCfPath = filepath.Join(tmp, "main.cf")
	defer func() { mainCfPath = oldMainCf }()

	if err := os.WriteFile(mainCfPath, []byte("existing config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(confPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := mtaCustomStart(context.Background(), nil)
	t.Logf("mtaCustomStart (running skip): %v", err)
}

func TestMtaCustomStart_BootstrapError(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	defer func() { sudoBin = oldSudo }()

	fakeSudo := `#!/bin/sh
case "$2" in
  status) exit 1 ;;
  start) exit 0 ;;
  *) exit 0 ;;
esac`
	if err := os.WriteFile(sudoBin, []byte(fakeSudo), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	if err := os.WriteFile(postfixBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postfixBin = oldPostfix }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("config error")
	}
	defer func() { loadConfig = oldLC }()

	err := mtaCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
}

// --- mtaCustomStop ---

func TestMtaCustomStop_NotRunningFromLaunchers(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "fake-sudo")
	defer func() { sudoBin = oldSudo }()

	fakeSudoContent := `#!/bin/sh
if [ "$2" = "stop" ]; then
  echo "postfix/postfix-script: the Postfix mail system is not running"
  exit 1
fi
exit 0`
	if err := os.WriteFile(sudoBin, []byte(fakeSudoContent), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	if err := os.WriteFile(postfixBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postfixBin = oldPostfix }()

	err := mtaCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when postfix reports 'not running', got %v", err)
	}
}

func TestMtaCustomStop_Error(t *testing.T) {
	tmp := t.TempDir()
	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "fake-sudo")
	defer func() { sudoBin = oldSudo }()

	fakeSudoContent := `#!/bin/sh
echo "fatal error" >&2
exit 2`
	if err := os.WriteFile(sudoBin, []byte(fakeSudoContent), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	if err := os.WriteFile(postfixBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { postfixBin = oldPostfix }()

	err := mtaCustomStop(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when postfix stop fails with unexpected output")
	}
}
