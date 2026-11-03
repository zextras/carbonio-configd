// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	dbPID      = 500
	mailboxPID = 600
	dbPassword = "s3cret"
)

// addDB puts a running mailbox mariadbd (matching the appserver-db identity)
// in the fake process table.
func addDB(ctl *Controller, f *systest.Fakes, pid int) {
	f.Proc.Add(sys.ProcEntry{
		PID:  pid,
		Argv: []string{ctl.p.common + "/sbin/mariadbd", "--defaults-file=" + ctl.p.conf + "/my.cnf"},
	})
}

func sigsOf(f *systest.Fakes) []syscall.Signal {
	var out []syscall.Signal
	for _, s := range f.Proc.Signals() {
		out = append(out, s.Sig)
	}

	return out
}

// mysqlRecorder answers the mysql client: engines for "show engines;", and
// status(n) for each "show engine innodb status" call (n counts from 1).
type mysqlRecorder struct {
	mu      sync.Mutex
	stmts   []string
	engines string
	status  func(n int) string
}

func (m *mysqlRecorder) install(ctl *Controller, f *systest.Fakes) {
	f.Exec.OnPath(ctl.p.bin+"/mysql", func(c *sys.Cmd) (sys.Result, error) {
		stmt := c.Args[len(c.Args)-1]

		m.mu.Lock()
		defer m.mu.Unlock()

		m.stmts = append(m.stmts, stmt)

		switch {
		case stmt == "show engines;":
			return sys.Result{Stdout: []byte(m.engines)}, nil
		case strings.HasPrefix(stmt, "show engine innodb status"):
			n := 0

			for _, s := range m.stmts {
				if strings.HasPrefix(s, "show engine innodb status") {
					n++
				}
			}

			return sys.Result{Stdout: []byte(m.status(n))}, nil
		}

		return sys.Result{}, nil
	})
}

func (m *mysqlRecorder) count(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for _, s := range m.stmts {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}

	return n
}

const enginesEnabled = "Engine\tSupport\nInnoDB\tDEFAULT\tSupports transactions\n"

func flushController(t *testing.T) (*Controller, *systest.Fakes) {
	t.Helper()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"zimbra_mysql_password": dbPassword}, nil
	}))

	return ctl, f
}

func TestStopMysqld_SIGTERMOnlyAndPidfileRemoved(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	addDB(ctl, f, dbPID)
	f.FS.Put(ctl.p.appserverDBPidFile, []byte("500\n"), 0o644)

	require.NoError(t, ctl.stopAppserverDB(context.Background(), ctl.registry[svcAppserverDB]))

	assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, sigsOf(f))
	assert.False(t, f.Proc.Alive(dbPID))
	assert.False(t, f.FS.Exists(ctl.p.appserverDBPidFile), "pidfile removed after exit")
}

func TestStopMysqld_NotRunningIsNil(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.appserverDBPidFile, []byte("500\n"), 0o644) // stale

	require.NoError(t, ctl.stopAppserverDB(context.Background(), ctl.registry[svcAppserverDB]))

	assert.Empty(t, f.Proc.Signals())
}

func TestStopMysqld_TimeoutLeavesProcessAliveWithoutKill(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	addDB(ctl, f, dbPID)
	f.Proc.SurviveTerm[dbPID] = true
	f.FS.Put(ctl.p.appserverDBPidFile, []byte("500\n"), 0o644)

	pumpClock(t, f.Clock, time.Second)

	start := f.Clock.Now()
	err := ctl.stopAppserverDB(context.Background(), ctl.registry[svcAppserverDB])

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not forcing termination")
	assert.GreaterOrEqual(t, f.Clock.Now().Sub(start), mysqlShutdownTimeoutDefault, "default wait is 60s")
	assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, sigsOf(f), "never SIGKILL")
	assert.True(t, f.Proc.Alive(dbPID), "process left alive")
	assert.True(t, f.FS.Exists(ctl.p.appserverDBPidFile), "pidfile kept while alive")
}

func TestStopMysqld_HonorsLocalconfigTimeout(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"zimbra_mysql_shutdown_timeout": "7"}, nil
	}))
	addDB(ctl, f, dbPID)
	f.Proc.SurviveTerm[dbPID] = true

	pumpClock(t, f.Clock, time.Second)

	start := f.Clock.Now()
	err := ctl.stopAppserverDB(context.Background(), ctl.registry[svcAppserverDB])

	require.Error(t, err)
	assert.Contains(t, err.Error(), "7s")

	elapsed := f.Clock.Now().Sub(start)
	assert.GreaterOrEqual(t, elapsed, 7*time.Second)
	assert.Less(t, elapsed, 30*time.Second, "LC value, not the 60s default")
}

func TestStopMysqld_ExitsWithinTimeout(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	addDB(ctl, f, dbPID)
	f.Proc.SurviveTerm[dbPID] = true
	f.FS.Put(ctl.p.appserverDBPidFile, []byte("500\n"), 0o644)

	pumpClock(t, f.Clock, time.Second)

	go func() {
		// the server takes a few seconds to flush and exits on its own
		for f.Clock.Now().Sub(systest.Epoch) < 5*time.Second {
			time.Sleep(time.Millisecond)
		}

		f.Proc.Remove(dbPID)
	}()

	require.NoError(t, ctl.stopAppserverDB(context.Background(), ctl.registry[svcAppserverDB]))
	assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, sigsOf(f))
	assert.False(t, f.FS.Exists(ctl.p.appserverDBPidFile))
}

func TestFlushDirtyPages_SkippedWhenNotRunning(t *testing.T) {
	t.Parallel()

	ctl, f := flushController(t)
	m := &mysqlRecorder{engines: enginesEnabled, status: func(int) string { return "" }}
	m.install(ctl, f)

	ctl.flushAppserverDBDirtyPages(context.Background())

	assert.Empty(t, m.stmts, "no mysql call when the server is not running")
}

func TestFlushDirtyPages_SkippedWhenInnoDBDisabled(t *testing.T) {
	t.Parallel()

	ctl, f := flushController(t)
	addDB(ctl, f, dbPID)

	m := &mysqlRecorder{
		engines: "Engine\tSupport\nInnoDB\tDISABLED\tx\nMyISAM\tYES\ty\n",
		status:  func(int) string { return "" },
	}
	m.install(ctl, f)

	ctl.flushAppserverDBDirtyPages(context.Background())

	assert.Equal(t, 0, m.count("set global"))
	assert.Equal(t, 0, m.count("show engine innodb status"))
}

func TestFlushDirtyPages_SetsPctThenPollsUntilZero(t *testing.T) {
	t.Parallel()

	ctl, f := flushController(t)
	addDB(ctl, f, dbPID)

	// Two buffer pool instances: dirty for two polls, then both clean.
	m := &mysqlRecorder{engines: enginesEnabled, status: func(n int) string {
		if n < 3 {
			return "Modified db pages  4\nModified db pages  0\n"
		}

		return "Modified db pages  0\nModified db pages  0\n"
	}}
	m.install(ctl, f)

	pumpClock(t, f.Clock, time.Second)

	ctl.flushAppserverDBDirtyPages(context.Background())

	assert.Equal(t, 1, m.count("set global innodb_max_dirty_pages_pct=0;"))
	assert.Equal(t, 3, m.count("show engine innodb status"))

	// the password reaches mysql and the pct statement precedes the polling
	var setIdx, statusIdx = -1, -1

	for i, s := range m.stmts {
		if strings.HasPrefix(s, "set global") && setIdx < 0 {
			setIdx = i
		}

		if strings.HasPrefix(s, "show engine innodb status") && statusIdx < 0 {
			statusIdx = i
		}
	}

	assert.Less(t, setIdx, statusIdx)
	assert.Contains(t, strings.Join(f.Exec.Lines(), "\n"), "--password="+dbPassword)
	assert.Empty(t, f.Proc.Signals(), "the flush never signals the server")
}

func TestFlushDirtyPages_BoundedBy600s(t *testing.T) {
	t.Parallel()

	ctl, f := flushController(t)
	addDB(ctl, f, dbPID)

	m := &mysqlRecorder{engines: enginesEnabled, status: func(int) string { return "Modified db pages  9\n" }}
	m.install(ctl, f)

	pumpClock(t, f.Clock, time.Second)

	start := f.Clock.Now()

	ctl.flushAppserverDBDirtyPages(context.Background())

	elapsed := f.Clock.Now().Sub(start)
	assert.GreaterOrEqual(t, elapsed, innodbFlushTimeout)
	assert.Less(t, elapsed, innodbFlushTimeout+10*time.Second)
	assert.Positive(t, m.count("show engine innodb status"))
}

func TestFlushDirtyPages_NoPasswordSkips(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	addDB(ctl, f, dbPID)

	ctl.flushAppserverDBDirtyPages(context.Background())

	for _, l := range f.Exec.Lines() {
		assert.NotContains(t, l, "mysql -u")
	}
}

func TestServiceStop_AppserverDBFlushesBeforeStop(t *testing.T) {
	t.Parallel()

	ctl, f := flushController(t)
	addDB(ctl, f, dbPID)
	f.FS.Put(ctl.p.appserverDBPidFile, []byte("500\n"), 0o644)

	m := &mysqlRecorder{engines: enginesEnabled, status: func(int) string { return "Modified db pages  0\n" }}
	m.install(ctl, f)

	require.NoError(t, ctl.ServiceStop(context.Background(), svcAppserverDB))

	lines := f.Rec.Strings()
	setIdx, termIdx := -1, -1

	for i, l := range lines {
		if strings.Contains(l, "set global innodb_max_dirty_pages_pct=0") && setIdx < 0 {
			setIdx = i
		}

		if strings.HasPrefix(l, "proc.signal 500 15") && termIdx < 0 {
			termIdx = i
		}
	}

	require.GreaterOrEqual(t, setIdx, 0, "flush ran: %v", lines)
	require.GreaterOrEqual(t, termIdx, 0, "SIGTERM sent: %v", lines)
	assert.Less(t, setIdx, termIdx, "flush precedes the stop")
	assert.Equal(t, []syscall.Signal{syscall.SIGTERM}, sigsOf(f))
}

func TestMailboxCustomStop_OnlyKillsJVM(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	addDB(ctl, f, dbPID)
	f.Proc.Add(sys.ProcEntry{
		PID:  mailboxPID,
		Argv: []string{"/opt/zextras/common/lib/jvm/java/bin/java", "-cp", "x", mailboxMainClass},
	})

	m := &mysqlRecorder{engines: enginesEnabled, status: func(int) string { return "" }}
	m.install(ctl, f)

	require.NoError(t, ctl.mailboxCustomStop(context.Background(), ctl.registry[svcMailbox]))

	assert.False(t, f.Proc.Alive(mailboxPID))
	assert.True(t, f.Proc.Alive(dbPID), "database untouched")

	for _, s := range f.Proc.Signals() {
		assert.Equal(t, mailboxPID, s.PID)
	}

	assert.Empty(t, m.stmts, "no mysql call from the JVM stop")
}

func mailboxStartController(t *testing.T) (*Controller, *systest.Fakes) {
	t.Helper()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.common+"/lib/jvm/java/bin/java", []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	return ctl, f
}

func TestMailboxCustomStart_JVMOnlyThenReady(t *testing.T) {
	t.Parallel()

	ctl, f := mailboxStartController(t)

	var readyCalls int

	ctl.mailboxReady = func(context.Context, map[string]string) error {
		readyCalls++

		// by the time readiness is checked the JVM has been launched
		assert.Contains(t, strings.Join(f.Rec.Strings(), "\n"), "exec.start "+ctl.p.common+"/lib/jvm/java/bin/java")

		return nil
	}

	require.NoError(t, ctl.mailboxCustomStart(context.Background(), ctl.registry[svcMailbox]))

	assert.Equal(t, 1, readyCalls)

	for _, l := range f.Rec.Strings() {
		assert.NotContains(t, l, "mysqld_safe", "mailbox start does not start the DB")
		assert.NotContains(t, l, "mysql.server")
	}
}

func TestMailboxCustomStart_ReadyFailureIsReturned(t *testing.T) {
	t.Parallel()

	ctl, _ := mailboxStartController(t)
	boom := errors.New("port never opened")
	ctl.mailboxReady = func(context.Context, map[string]string) error { return boom }

	err := ctl.mailboxCustomStart(context.Background(), ctl.registry[svcMailbox])

	require.ErrorIs(t, err, boom)
}
