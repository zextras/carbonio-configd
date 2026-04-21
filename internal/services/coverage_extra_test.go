// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// safePID is a PID chosen to be higher than any realistic running process so
// signaling it is a no-op (ESRCH). NEVER use 1 or os.Getpid() in tests —
// syscall.Kill(-1, sig) broadcasts to every process the user owns and will
// tear down their graphical session, and signaling self kills the test binary.
const safePID = 999999990

func TestServiceStop_UnknownSvc(t *testing.T) {
	if err := ServiceStop(context.Background(), "nonexistent-service-xyz"); err == nil {
		t.Error("expected error for unknown service")
	}
}

func TestServiceReload_UnknownSvc(t *testing.T) {
	if err := ServiceReload(context.Background(), "nonexistent-service-xyz"); err == nil {
		t.Error("expected error for unknown service")
	}
}

func TestServiceReload_NoUnitsNoop(t *testing.T) {
	def := &ServiceDef{Name: "test-reload-empty", SystemdUnits: nil}
	Registry["test-reload-empty"] = def
	defer delete(Registry, "test-reload-empty")

	if err := ServiceReload(context.Background(), "test-reload-empty"); err != nil {
		t.Errorf("expected nil for service with no units, got %v", err)
	}
}

func TestServiceListStatus_LegacyMode(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	infos := ServiceListStatus(context.Background())
	if len(infos) == 0 {
		t.Error("expected non-empty list from registry")
	}
}

func TestServiceListStatusStream_CtxCancelled(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := ServiceListStatusStream(ctx)
	for range ch {
	}
}

func TestRewriteViaConfigd_NotReachable(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zmconfigd_listen_port": "1"}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := rewriteViaConfigd(context.Background(), []string{"proxy"})
	if err == nil {
		t.Error("expected error when configd is not listening on port 1")
	}
}

func TestRewriteViaConfigd_LCErrDefaultsToDefaultPort(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("no lc")
	}
	defer func() { loadConfig = oldLC }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := rewriteViaConfigd(ctx, []string{"mta"})
	if err == nil {
		t.Error("expected error dialing default port when configd not running")
	}
}

func TestRewriteViaConfigd_ServerReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	portStr := strings.Split(ln.Addr().String(), ":")
	port := portStr[len(portStr)-1]

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		buf := make([]byte, 128)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERROR: bad section\n"))
	}()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zmconfigd_listen_port": port}, nil
	}
	defer func() { loadConfig = oldLC }()

	err = rewriteViaConfigd(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "configd returned error") {
		t.Errorf("expected configd error response, got %v", err)
	}
}

func TestRewriteViaConfigd_ServerSucceeds(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	portStr := strings.Split(ln.Addr().String(), ":")
	port := portStr[len(portStr)-1]

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		buf := make([]byte, 128)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("OK\n"))
	}()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"zmconfigd_listen_port": port}, nil
	}
	defer func() { loadConfig = oldLC }()

	if err := rewriteViaConfigd(context.Background(), []string{"proxy"}); err != nil {
		t.Errorf("expected success, got %v", err)
	}
}

func TestStopService_LegacyNoProcessName(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	def := &ServiceDef{Name: "test-nostop", ProcessName: "", CustomStop: nil}
	err := stopService(context.Background(), "test-nostop", def)
	if err == nil || !strings.Contains(err.Error(), "no ProcessName") {
		t.Errorf("expected 'no ProcessName' error, got %v", err)
	}
}

func TestStopService_LegacyCustomStop(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	sentinel := fmt.Errorf("custom-stop-called")
	def := &ServiceDef{
		Name: "test-custom-stop",
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			return sentinel
		},
	}

	err := stopService(context.Background(), "test-custom-stop", def)
	if err != sentinel {
		t.Errorf("expected sentinel from CustomStop, got %v", err)
	}
}

func TestIsOwnedByCurrentUser_SelfProc(t *testing.T) {
	if !isOwnedByCurrentUser("/proc/self") {
		t.Error("expected /proc/self to be owned by current user")
	}
}

func TestIsOwnedByCurrentUser_UnreadableDefaultsTrue(t *testing.T) {
	// Nonexistent path means ReadFile errors; the function defaults to true.
	if !isOwnedByCurrentUser(filepath.Join(t.TempDir(), "nope")) {
		t.Error("expected true for unreadable proc dir")
	}
}

func TestKillStatsPidFile_BadPath(t *testing.T) {
	if killStatsPidFile(context.Background(), filepath.Join(t.TempDir(), "nope.pid")) {
		t.Error("expected false for missing pidfile")
	}
}

func TestKillStatsPidFile_GarbagePID(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.pid")
	if err := os.WriteFile(p, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if killStatsPidFile(context.Background(), p) {
		t.Error("expected false for garbage pid")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("expected pidfile to be removed after parse failure")
	}
}

func TestKillStatsPidFile_SafeNonexistentPID(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "safe.pid")
	if err := os.WriteFile(p, []byte(strconv.Itoa(safePID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// safePID does not exist, so killByPIDWithGroupAndSudo hits ESRCH and
	// reports success without signaling any real process.
	if !killStatsPidFile(context.Background(), p) {
		t.Error("expected true for nonexistent PID (ESRCH is success)")
	}
}

func TestStatsCustomStart_LoadConfigFails_Covg(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("no lc")
	}
	defer func() { loadConfig = oldLC }()

	if err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"}); err == nil {
		t.Error("expected error when loadConfig fails")
	}
}

func TestStatsCustomStart_NoBinaries(t *testing.T) {
	tmp := t.TempDir()

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = tmp
	defer func() { logPath = oldLog }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil || !strings.Contains(err.Error(), "no stats collectors started") {
		t.Errorf("expected 'no stats collectors started', got %v", err)
	}
}

func TestStatsCustomStop_MissingDir(t *testing.T) {
	oldDir := statsPidDir
	statsPidDir = filepath.Join(t.TempDir(), "does-not-exist")
	defer func() { statsPidDir = oldDir }()

	if err := statsCustomStop(context.Background(), &ServiceDef{Name: "stats"}); err != nil {
		t.Errorf("expected nil for missing dir, got %v", err)
	}
}

func TestStatsCustomStop_EmptyDir_Covg(t *testing.T) {
	tmp := t.TempDir()
	oldDir := statsPidDir
	statsPidDir = tmp
	defer func() { statsPidDir = oldDir }()

	if err := statsCustomStop(context.Background(), &ServiceDef{Name: "stats"}); err != nil {
		t.Errorf("expected nil for empty dir, got %v", err)
	}
}

func TestStatsCustomStop_WithSafePidFile(t *testing.T) {
	tmp := t.TempDir()
	oldDir := statsPidDir
	statsPidDir = tmp
	defer func() { statsPidDir = oldDir }()

	p := filepath.Join(tmp, "zmstat-proc.pid")
	if err := os.WriteFile(p, []byte(strconv.Itoa(safePID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := statsCustomStop(context.Background(), &ServiceDef{Name: "stats"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestMailboxCustomStart_LoadConfigFails_Covg(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("lc boom")
	}
	defer func() { loadConfig = oldLC }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err == nil || !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("expected load error, got %v", err)
	}
}

func TestMailboxCustomStart_JavaMissing(t *testing.T) {
	tmp := t.TempDir()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": filepath.Join(tmp, "nonexistent-jdk"),
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err == nil || !strings.Contains(err.Error(), "java binary not found") {
		t.Errorf("expected java-not-found error, got %v", err)
	}
}

func TestWaitForSDNotify_CtxCancelled(t *testing.T) {
	dir := t.TempDir()
	addr := &net.UnixAddr{Name: filepath.Join(dir, "s.sock"), Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := waitForSDNotify(ctx, conn, "svc"); err == nil {
		t.Error("expected error from cancelled ctx")
	}
}

func TestWaitForSDNotify_ReadyReceived(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "r.sock")
	addr := &net.UnixAddr{Name: sockPath, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	defer func() { _ = conn.Close() }()

	go func() {
		time.Sleep(50 * time.Millisecond)
		client, dialErr := net.DialUnix("unixgram", nil, addr)
		if dialErr != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = client.Write([]byte("READY=1\n"))
	}()

	if err := waitForSDNotify(context.Background(), conn, "svc"); err != nil {
		t.Errorf("expected READY=1 to succeed, got %v", err)
	}
}
