// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// setProcExe adds the /proc/<pid>/exe symlink the fake /proc tree lacks.
func setProcExe(t *testing.T, root string, pid int, target string) {
	t.Helper()

	require.NoError(t, os.Symlink(target, filepath.Join(root, strconv.Itoa(pid), "exe")))
}

// registerTestService adds def to the Registry for the duration of the test.
func registerTestService(t *testing.T, name string, def *ServiceDef) {
	t.Helper()

	Registry[name] = def

	t.Cleanup(func() { delete(Registry, name) })
}

func writePidFile(t *testing.T, pid int) string {
	t.Helper()

	pidFile := filepath.Join(t.TempDir(), "svc.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644))

	return pidFile
}

func TestProcEntryMatches(t *testing.T) {
	const (
		clamd   = "/opt/zextras/common/sbin/clamd"
		nginx   = "/opt/zextras/common/sbin/nginx"
		discovd = "/usr/bin/service-discoverd" //nolint:misspell // a binary name, for the comm-truncation case
	)

	for _, tc := range []struct {
		name  string
		entry procEntry
		ident string
		want  bool
	}{
		{"argv0 equals path", procEntry{argv: []string{clamd, "-c", "/etc/clamd.conf"}}, clamd, true},
		{"exe equals path, argv unrelated", procEntry{exe: clamd, argv: []string{"clamd-renamed"}}, clamd, true},
		{"script path as interpreter argument", procEntry{
			argv: []string{"/usr/bin/perl", "-w", "/opt/zextras/libexec/zmstat-proc"},
		}, "/opt/zextras/libexec/zmstat-*", true},
		{"tail of the log mentions the name", procEntry{
			argv: []string{"tail", "-f", "/opt/zextras/log/clamd.log"},
		}, clamd, false},
		{"less of slapd.log", procEntry{
			argv: []string{"less", "/opt/zextras/log/slapd.log"},
		}, "/opt/zextras/common/libexec/slapd", false},
		{"consul sidecar naming the service", procEntry{
			argv: []string{"consul", "connect", "envoy", "-sidecar-for", "carbonio-memcached"},
		}, "/opt/zextras/common/bin/memcached", false},
		{"glob never crosses a slash", procEntry{
			argv: []string{"tail", "-f", "/opt/zextras/log/zmstat-proc.out"},
		}, "/opt/zextras/libexec/zmstat-*", false},
		{"java main class", procEntry{
			argv: []string{
				"/opt/zextras/common/lib/jvm/java/bin/java", "-Xmx2g", "-cp",
				"/opt/zextras/lib/ext/*:/opt/zextras/jars/*", "com.zextras.mailbox.Mailbox",
			},
		}, "com.zextras.mailbox.Mailbox", true},
		{"java classpath mentioning the class is not the class", procEntry{
			argv: []string{"java", "-cp", "/opt/zextras/jars/com.zextras.mailbox.Mailbox.jar", "other.Main"},
		}, "com.zextras.mailbox.Mailbox", false},
		{"retitled nginx master carries the path", procEntry{
			argv: []string{"nginx:", "master", "process", nginx, "-c", "/opt/zextras/conf/nginx.conf"},
		}, nginx, true},
		{"retitled nginx worker is not the master path", procEntry{
			argv: []string{"nginx:", "worker", "process"},
		}, nginx, false},
		{"bare name matches title token", procEntry{argv: []string{"amavisd", "(master)"}}, "amavisd", true},
		{"bare name matches script basename", procEntry{
			argv: []string{"/usr/bin/perl", "/opt/zextras/common/sbin/amavisd", "-c", "/x"},
		}, "amavisd", true},
		{"bare name does not match a longer name", procEntry{argv: []string{"/bin/amavisd-status"}}, "amavisd", false},
		{"empty argv falls back to truncated comm", procEntry{comm: "service-discove"}, discovd, true},
		{"empty argv, different comm", procEntry{comm: "other"}, discovd, false},
		{"argv is authoritative over comm", procEntry{
			argv: []string{"/bin/bash", "/usr/bin/service-discover-wrapper.sh", "-sidecar-for", "carbonio-x"},
			comm: "service-discove",
		}, discovd, false},
		{"identity with required args: agent of the config dir", procEntry{
			argv: []string{"/usr/bin/consul", "agent", "-config-dir", "/etc/zextras/service-discover/"},
		}, "/usr/bin/consul agent /etc/zextras/service-discover", true},
		{"identity with required args: sidecar lacks them", procEntry{
			argv: []string{"/usr/bin/consul", "connect", "envoy", "-sidecar-for", "carbonio-mta"},
		}, "/usr/bin/consul agent /etc/zextras/service-discover", false},
		{"identity with required args: other config dir", procEntry{
			argv: []string{"/usr/bin/consul", "agent", "-config-dir", "/etc/other"},
		}, "/usr/bin/consul agent /etc/zextras/service-discover", false},
		{"identity with required args: empty argv", procEntry{comm: "consul"}, "/usr/bin/consul agent", false},
		{"empty identity matches nothing", procEntry{argv: []string{"x"}, comm: "x"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.entry.matches(tc.ident))
		})
	}
}

func TestLoadProcEntry_ExeAndTitleTokens(t *testing.T) {
	root, add := fakeProcFS(t)
	add(4100, "nginx: master process /opt/zextras/common/sbin/nginx -c /conf\x00", "nginx", false)
	setProcExe(t, root, 4100, "/opt/zextras/common/sbin/nginx (deleted)")

	e := loadProcEntry(4100)
	assert.Equal(t, "/opt/zextras/common/sbin/nginx", e.exe, "the deleted-binary suffix is stripped")
	assert.Contains(t, e.argv, "/opt/zextras/common/sbin/nginx", "title tokens are split on whitespace")
	assert.Empty(t, e.comm, "comm is only read when argv is empty")
}

func TestReadPidFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

		return p
	}

	pid, ok := readPidFile(write("ok", " 123 \n"))
	assert.True(t, ok)
	assert.Equal(t, 123, pid)

	pid, ok = readPidFile(write("multi", "77\n78\n79\n"))
	assert.True(t, ok)
	assert.Equal(t, 77, pid, "only the first line is the primary PID")

	for _, bad := range []string{"", "abc\n", "0\n", "-5\n"} {
		_, ok = readPidFile(write("bad", bad))
		assert.False(t, ok, "%q", bad)
	}

	_, ok = readPidFile(filepath.Join(dir, "missing"))
	assert.False(t, ok)
}

// A pidfile whose PID was recycled by another binary must not make the
// service look running; neither may a process that only mentions the service.
func TestServiceStatus_StalePidfileRecycledPid(t *testing.T) {
	withMode(t, false)

	_, add := fakeProcFS(t)
	add(4242, "/usr/bin/vim\x00/opt/zextras/log/clamd.log\x00", "vim", false)
	add(4243, "tail\x00-f\x00/opt/zextras/log/clamd.log\x00", "tail", false)

	registerTestService(t, "test-recycled", &ServiceDef{
		Name:        "test-recycled",
		PidFile:     writePidFile(t, 4242),
		ProcessName: commonPath + "/sbin/clamd",
	})

	var logs bytes.Buffer

	ctx := logger.ContextWithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	running, err := ServiceStatus(ctx, "test-recycled")
	require.NoError(t, err)
	assert.False(t, running)
	assert.Empty(t, logs.String(), "a service that is not running is not an error (or any log)")
	assert.Zero(t, RunningPID(ctx, Registry["test-recycled"]))
}

func TestServiceStatus_NotRunningIsFalseNil(t *testing.T) {
	withMode(t, false)
	fakeProcFS(t)

	for _, name := range AllServiceNames() {
		def := LookupService(name)
		if def.UseSystemdForStatus {
			continue
		}

		// Registry pidfiles live in real directories: they must not exist on
		// a build host, but if one does, this service cannot be asserted.
		if _, err := os.Stat(def.PidFile); def.PidFile != "" && err == nil {
			continue
		}

		running, err := ServiceStatus(context.Background(), name)
		assert.NoError(t, err, name)
		assert.False(t, running, name)
	}
}

func TestServiceStatus_StalePidfileFallsThroughToScan(t *testing.T) {
	withMode(t, false)

	_, add := fakeProcFS(t)
	add(4242, "/usr/bin/vim\x00", "vim", false)
	add(4300, "/opt/zextras/common/sbin/clamd\x00-c\x00/x\x00", "clamd", false)

	def := &ServiceDef{
		Name:        "test-fallthrough",
		PidFile:     writePidFile(t, 4242),
		ProcessName: commonPath + "/sbin/clamd",
	}
	registerTestService(t, "test-fallthrough", def)

	running, err := ServiceStatus(context.Background(), "test-fallthrough")
	require.NoError(t, err)
	assert.True(t, running, "the pidfile is stale but the service is running")
	assert.Equal(t, 4300, RunningPID(context.Background(), def), "the PID shown is the one that made status true")
}

func TestRunningPID_ValidPidfileSkipsProcessTable(t *testing.T) {
	root, add := fakeProcFS(t)
	add(4400, "/opt/zextras/common/sbin/clamd\x00", "clamd", false)
	add(4401, "/opt/zextras/common/sbin/clamd\x00", "clamd", false)

	def := &ServiceDef{PidFile: writePidFile(t, 4401), ProcessName: commonPath + "/sbin/clamd"}

	// Without a snapshot only /proc/4401 is read, never the directory listing:
	// make the listing itself unreadable to prove it.
	require.NoError(t, os.Chmod(root, 0o311))

	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	assert.Equal(t, 4401, RunningPID(context.Background(), def), "the validated pidfile PID wins over the lower scan hit")
}

func TestRunningPID_ExeTierAndSnapshotLookup(t *testing.T) {
	root, add := fakeProcFS(t)
	add(4500, "daemonized-title\x00", "daemon", false)
	setProcExe(t, root, 4500, commonPath+"/sbin/opendkim")

	def := &ServiceDef{PidFile: writePidFile(t, 4500), ProcessName: commonPath + "/sbin/opendkim"}

	assert.Equal(t, 4500, RunningPID(context.Background(), def), "exe matches although argv does not")

	// Same verdict from a snapshot, which also serves the pidfile validation.
	ctx := WithProcessSnapshot(context.Background())
	assert.Equal(t, 4500, RunningPID(ctx, def))
}

// Java-launched services (mailboxd) are recognised by their main class.
func TestServiceStatus_JavaMailboxd(t *testing.T) {
	withMode(t, false)

	_, add := fakeProcFS(t)
	add(4600, "tail\x00-f\x00/opt/zextras/log/mailbox.log\x00", "tail", false)

	running, err := ServiceStatus(context.Background(), svcMailbox)
	require.NoError(t, err)
	assert.False(t, running, "a tail of mailbox.log is not mailboxd")

	add(4601, "/opt/zextras/common/lib/jvm/java/bin/java\x00-Xmx4g\x00-cp\x00/opt/zextras/jars/*\x00"+
		mailboxMainClass+"\x00", "java", false)

	running, err = ServiceStatus(context.Background(), svcMailbox)
	require.NoError(t, err)
	assert.True(t, running)
	assert.Equal(t, 4601, RunningPID(context.Background(), LookupService(svcMailbox)))
}

// Status and the PID displayed beside it come from one snapshot: a process
// that exits right after the snapshot was taken is still reported by both.
func TestServiceStatus_SnapshotConsistentWithRunningPID(t *testing.T) {
	withMode(t, false)

	root, add := fakeProcFS(t)
	add(4700, "/opt/zextras/common/sbin/saslauthd\x00-a\x00ldap\x00", "saslauthd", false)

	ctx := WithProcessSnapshot(context.Background())

	running, err := ServiceStatus(ctx, svcSaslauthd)
	require.NoError(t, err)
	require.True(t, running)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "4700")))

	running, err = ServiceStatus(ctx, svcSaslauthd)
	require.NoError(t, err)
	assert.True(t, running, "the same snapshot answers every probe of the batch")
	assert.Equal(t, 4700, RunningPID(ctx, LookupService(svcSaslauthd)))

	running, err = ServiceStatus(context.Background(), svcSaslauthd)
	require.NoError(t, err)
	assert.False(t, running, "a fresh probe sees the live state")
}

func TestServiceStatus_SystemdModeUnaffectedByProcessIdentity(t *testing.T) {
	withMode(t, true)

	_, add := fakeProcFS(t)
	add(4800, "/opt/zextras/common/sbin/clamd\x00", "clamd", false)

	falseBin, err := exec.LookPath("false")
	require.NoError(t, err)

	orig := systemctlBin
	systemctlBin = falseBin // every `systemctl is-active` exits 1: inactive

	t.Cleanup(func() { systemctlBin = orig })

	running, err := ServiceStatus(context.Background(), svcAntivirus)
	require.NoError(t, err)
	assert.False(t, running, "systemd stays authoritative: /proc is not consulted")
}

// startIdentified runs `sh -c "sleep 30; :" <tag>`: a process whose argv
// carries tag as an exact token. The returned PID is reaped on cleanup.
func startIdentified(t *testing.T, tag string) int {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30; :", tag)
	require.NoError(t, cmd.Start())

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	return cmd.Process.Pid
}

// The graceful stop must never SIGTERM a PID that merely re-uses a stale
// pidfile's number: the unrelated process survives, the stale file goes away.
func TestGracefulStop_StalePidfileDoesNotSignalRecycledPid(t *testing.T) {
	const identity = "/opt/zextras/common/libexec/slapd-ident-test"

	unrelated := startIdentified(t, "/usr/bin/unrelated-ident-test")
	pidFile := writePidFile(t, unrelated)

	require.NoError(t, gracefulStopViaPidfile(context.Background(), pidFile, "svc", identity, time.Second))

	assert.True(t, processAlive(unrelated), "an unrelated process must not be signaled")
	assert.NoFileExists(t, pidFile)
}

func TestGracefulStop_ValidatedPidfileStopsProcess(t *testing.T) {
	const identity = "/opt/zextras/common/libexec/slapd-ident-test"

	target := startIdentified(t, identity)
	pidFile := writePidFile(t, target)

	require.NoError(t, gracefulStopViaPidfile(context.Background(), pidFile, "svc", identity, 5*time.Second))

	assert.False(t, processAlive(target))
	assert.NoFileExists(t, pidFile)
}

// A stale pidfile does not hide a running service: it is found by identity
// and stopped, while the process owning the stale PID is left alone.
func TestGracefulStop_StalePidfileStillStopsRunningService(t *testing.T) {
	const identity = "/opt/zextras/common/libexec/slapd-ident-test"

	unrelated := startIdentified(t, "/usr/bin/unrelated-ident-test")
	target := startIdentified(t, identity)
	pidFile := writePidFile(t, unrelated)

	require.NoError(t, gracefulStopViaPidfile(context.Background(), pidFile, "svc", identity, 5*time.Second))

	assert.False(t, processAlive(target))
	assert.True(t, processAlive(unrelated))
	assert.NoFileExists(t, pidFile)
}

// Real argv forms captured from a running carbonio container (ps -eo args,
// NUL-separated here): every registry identity must find its own process and
// ignore the sidecar noise around it, in particular the consul agent of
// service-discover versus the "consul connect envoy" sidecar machinery.
func TestRunningPID_RealContainerArgvForms(t *testing.T) {
	_, add := fakeProcFS(t)

	argv := func(args ...string) string { return strings.Join(args, "\x00") + "\x00" }

	// Sidecar noise that mentions services and shares binaries.
	add(14296, argv("/bin/bash", "/usr/bin/service-discover-wrapper.sh", "-token-file",
		"/etc/carbonio/mta/service-discover/token", "-admin-bind", "localhost:0", "-sidecar-for", "carbonio-mta"),
		"service-discove", false)
	add(14298, argv("/usr/bin/envoy", "--config-path", "/tmp/envoy-1-bootstrap.json", "--disable-hot-restart"),
		"envoy", false)
	add(14319, "", "consul", false) // zombie consul sidecar: empty argv, comm "consul"
	add(14320, argv("/usr/bin/consul", "connect", "envoy", "-sidecar-for", "carbonio-memcached",
		"-config-dir", "/etc/zextras/service-discover/"), "consul", false)
	add(14201, argv("/usr/bin/carbonio-prometheus-openldap-exporter"), "carbonio-prometh", false)

	// The services.
	add(13799, argv("/usr/bin/consul", "agent", "-bootstrap-expect", "1", "-config-dir",
		"/etc/zextras/service-discover/", "-server", "-retry-join=carbonio.carbonio-system.svc.cluster.local"),
		"consul", false)
	add(114005, argv("/opt/zextras/common/libexec/slapd", "-l", "LOCAL0", "-h",
		"ldap://carbonio.carbonio-system.svc.cluster.local:389 ldapi:///", "-F", "/opt/zextras/data/ldap/config"),
		"slapd", false)
	add(116048, argv("/opt/zextras/common/bin/java", "--enable-preview", "-Dzimbra.config=/opt/zextras/conf/localconfig.xml",
		"-cp", "/opt/zextras/mailbox/jars/mailbox.jar:/opt/zextras/mailbox/jars/*", "com.zextras.mailbox.Mailbox"),
		"java", false)
	add(116337, argv("/opt/zextras/common/bin/memcached", "-d", "-U", "0", "-l", "127.0.1.1,127.0.0.1", "-p", "11211"),
		"memcached", false)
	add(116364, "nginx: master process /opt/zextras/common/sbin/nginx -c /opt/zextras/conf/nginx.conf\x00",
		"nginx", false)
	add(116365, "nginx: worker process\x00", "nginx", false)
	add(116403, argv("/usr/bin/perl", "-T", "/opt/zextras/common/sbin/amavis-mc"), "amavis-mc", false)
	add(116422, "/opt/zextras/common/sbin/amavisd (master)\x00", "amavisd", false)
	add(116423, "/opt/zextras/common/sbin/amavisd (virgin child)\x00", "amavisd", false)
	add(116496, argv("/opt/zextras/common/sbin/clamd", "--config-file=/opt/zextras/conf/clamd.conf"), "clamd", false)
	add(116534, argv("/opt/zextras/common/bin/freshclam", "--config-file=/opt/zextras/conf/freshclam.conf",
		"--quiet", "--daemon", "--checks=12"), "freshclam", false)
	add(116538, argv("/opt/zextras/common/sbin/opendkim", "-x", "/opt/zextras/conf/opendkim.conf", "-u", "zextras"),
		"opendkim", false)
	add(116562, argv("/opt/zextras/common/lib/jvm/java/bin/java", "-client", "-Dzimbra.home=/opt/zextras",
		"-classpath", "/opt/zextras/lib/ext/*", "com.zimbra.cs.milter.MilterServer"), "java", false)
	add(116657, argv("/opt/zextras/common/sbin/saslauthd", "-r", "-a", "zimbra"), "saslauthd", false)
	add(116775, argv("/opt/zextras/common/libexec/master", "-w"), "master", false)
	add(116776, argv("pickup", "-l", "-t", "unix", "-u"), "pickup", false)
	add(116824, argv("/usr/bin/perl", "-w", "/opt/zextras/libexec/zmstat-proc"), "zmstat-proc", false)
	add(117319, argv("sudo", "/opt/zextras/libexec/zmstat-fd"), "sudo", false)
	add(117557, argv("/usr/bin/iostat", "-d", "-k", "-x", "30"), "iostat", false)

	for _, tc := range []struct {
		service string
		pid     int
	}{
		{svcServiceDiscover, 13799},
		{svcLdap, 114005},
		{svcMailbox, 116048},
		{svcMemcached, 116337},
		{svcProxy, 116364},
		{svcAmavis, 116422},
		{svcAntispam, 116422},
		{svcAntivirus, 116496},
		{svcFreshclam, 116534},
		{svcOpendkim, 116538},
		{svcMilter, 116562},
		{svcSaslauthd, 116657},
		{serviceMTA, 116775},
		{svcStats, 116824},
		{svcCbpolicyd, 0}, // not running in the capture
	} {
		def := *LookupService(tc.service)
		def.PidFile = "" // identity only; pidfile validation is covered separately

		assert.Equal(t, tc.pid, RunningPID(context.Background(), &def), tc.service)
	}
}

// The consul sidecar and its zombies must not be taken for the service-discover
// agent, even though they share its binary and config dir.
func TestRunningPID_ServiceDiscoverIgnoresConsulSidecars(t *testing.T) {
	_, add := fakeProcFS(t)
	add(14319, "", "consul", false)
	add(14320, "/usr/bin/consul\x00connect\x00envoy\x00-sidecar-for\x00carbonio-mta\x00"+
		"-config-dir\x00/etc/zextras/service-discover/\x00", "consul", false)
	add(14321, "/bin/bash\x00/usr/bin/service-discover-wrapper.sh\x00-sidecar-for\x00carbonio-mta\x00",
		"service-discove", false)

	def := LookupService(svcServiceDiscover)
	assert.Zero(t, RunningPID(context.Background(), def))

	add(13799, "/usr/bin/consul\x00agent\x00-config-dir=/etc/zextras/service-discover\x00-server\x00", "consul", false)
	assert.Equal(t, 13799, RunningPID(context.Background(), def), "-flag=value form, no trailing slash")
}
