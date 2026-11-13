// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	// rockyRsyslog is the stock rsyslog.conf of Rocky 9 (the relevant part).
	rockyRsyslog = "include(file=\"/etc/rsyslog.d/*.conf\" mode=\"optional\")\n" +
		"*.info;mail.none;authpriv.none;cron.none action(type=\"omfile\" file=\"/var/log/messages\")\n" +
		"authpriv.* action(type=\"omfile\" file=\"/var/log/secure\")\n" +
		"local7.* action(type=\"omfile\" file=\"/var/log/boot.log\")\n"
	rockySetUp = "include(file=\"/etc/rsyslog.d/*.conf\" mode=\"optional\")\n" +
		"*.info;local0.none;local1.none;mail.none;auth.none;authpriv.none;cron.none " +
		"action(type=\"omfile\" file=\"/var/log/messages\")\n" +
		"authpriv.* action(type=\"omfile\" file=\"/var/log/secure\")\n" +
		"local7.* action(type=\"omfile\" file=\"/var/log/boot.log\")\n" +
		"local0.*                -/var/log/carbonio.log\n" +
		"auth.info               -/var/log/carbonio.log\n" +
		"mail.*                -/var/log/carbonio.log\n"
	ubuntuDefault = "auth,authpriv.*\t\t\t/var/log/auth.log\n" +
		"*.*;auth,authpriv.none\t\t-/var/log/syslog\n" +
		"mail.err\t\t\t/var/log/mail.err\n"
	rotateTemplate = "/var/log/carbonio.log {\n\tcreate 0640 USER GROUP\n\t#su zextras zextras\n}\n"
)

func syslogEnv(t *testing.T, osID, loghost string) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes := svcEnv(t)
	e.Cfg.Host.Hostname = "mail.example.com"
	svcWithDir(t, &svcFakeDir{global: map[string]string{"zimbraLogHostname": loghost}})
	fakes.FS.Put("/etc/os-release", []byte("NAME=x\nID="+osID+"\n"), 0o644)
	fakes.Exec.OnPath("id", func(c *sys.Cmd) (sys.Result, error) {
		out := "999\n"
		if c.Args[0] == "-u" && c.Args[1] == "syslog" {
			out = "104\n"
		}

		_, _ = c.Stdout.Write([]byte(out))

		return sys.Result{}, nil
	})
	fakes.Exec.OnPath("getent", func(c *sys.Cmd) (sys.Result, error) {
		_, _ = c.Stdout.Write([]byte("adm:x:4:syslog\n"))

		return sys.Result{}, nil
	})

	return e, fakes
}

func syslogRocky(t *testing.T, loghost string) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes := syslogEnv(t, "rocky", loghost)
	fakes.FS.Put(syslogConf, []byte(rockyRsyslog), 0o644)
	fakes.FS.Put(syslogUnit, []byte("[Unit]"), 0o644)

	return e, fakes
}

func syslogRestarts(fakes *systest.Fakes) int {
	n := 0

	for _, l := range fakes.Exec.Lines() {
		if l == "systemctl restart rsyslog.service" {
			n++
		}
	}

	return n
}

func syslogRead(t *testing.T, fakes *systest.Fakes, name string) string {
	t.Helper()

	b, err := fakes.FS.ReadFile(name)
	require.NoError(t, err)

	return string(b)
}

func TestSyslog_RockyMatchesTheLegacyOutput(t *testing.T) {
	e, fakes := syslogRocky(t, "mail.example.com")

	require.NoError(t, StepSetupSyslog().Apply(e))

	assert.Equal(t, rockySetUp, syslogRead(t, fakes, syslogConf))
	assert.Equal(t, rockyRsyslog, syslogRead(t, fakes, syslogConf+".bak"))
	assert.Equal(t, 1, syslogRestarts(fakes))

	uid, gid, ok := fakes.FS.Owner(syslogLogFile)
	require.True(t, ok)
	assert.Equal(t, [2]int{999, 999}, [2]int{uid, gid})
}

func TestSyslog_SecondRunChangesNothing(t *testing.T) {
	for _, loghost := range []string{"", "mail.example.com", "log.example.com"} {
		t.Run("loghost="+loghost, func(t *testing.T) {
			e, fakes := syslogRocky(t, loghost)
			require.NoError(t, ConfigureSyslog(e))

			first := syslogRead(t, fakes, syslogConf)

			require.NoError(t, ConfigureSyslog(e))
			assert.Equal(t, first, syslogRead(t, fakes, syslogConf))
			assert.Equal(t, 1, syslogRestarts(fakes), "the second run must not restart rsyslog")
		})
	}
}

func TestSyslog_KeepsCustomLines(t *testing.T) {
	e, fakes := syslogRocky(t, "log.example.com")
	custom := rockyRsyslog + "*.emerg action(type=\"omusrmsg\" users=\"*\")\nlocal3.* /var/log/mine.log\n"
	fakes.FS.Put(syslogConf, []byte(custom), 0o600)

	require.NoError(t, ConfigureSyslog(e))

	got := syslogRead(t, fakes, syslogConf)
	assert.Contains(t, got, "*.emerg action(type=\"omusrmsg\" users=\"*\")\n")
	assert.Contains(t, got, "local3.* /var/log/mine.log\n")

	mode, _ := fakes.FS.Mode(syslogConf)
	assert.EqualValues(t, 0o600, mode, "the mode of the file is kept")
}

func TestSyslog_RemoteHostForwardsOverUDP(t *testing.T) {
	e, fakes := syslogRocky(t, "log.example.com")

	require.NoError(t, ConfigureSyslog(e))

	got := syslogRead(t, fakes, syslogConf)
	assert.True(t, strings.HasSuffix(got, "local0.*                @log.example.com\n"+
		"local1.*                @log.example.com\n"+
		"auth.info               @log.example.com\n"+
		"local0.*                -/var/log/carbonio.log\n"+
		"auth.info               -/var/log/carbonio.log\n"+
		"mail.*                @log.example.com\n"+
		"mail.*                -/var/log/carbonio.log\n"), got)
}

func TestSyslog_LocalHostNameIsCaseInsensitive(t *testing.T) {
	assert.False(t, syslogTargetOf("MAIL.Example.com", "mail.example.com").remote)
	assert.False(t, syslogTargetOf("", "mail.example.com").remote)
	assert.True(t, syslogTargetOf("log.example.com", "mail.example.com").remote)
}

func TestSyslog_RewritesCatchAllRules(t *testing.T) {
	in := "*.=debug;\\\n\tauth,authpriv.none -/var/log/debug\n" +
		"*.=info;*.=notice;*.=warn;\\\n\tauth,authpriv.none /var/log/messages\n" +
		"*.*;auth,authpriv.none -/var/log/syslog\n" +
		"\tlocal0,local1.none;\\\n" +
		"auth.info /var/log/old\n" +
		"mail.* @old.zimbra.example.com\n"
	want := "*.=debug;\\\n\tlocal0,local1.none;\\\n\tauth,authpriv.none -/var/log/debug\n" +
		"*.=info;*.=notice;*.=warn;\\\n\tlocal0,local1.none;\\\n\tauth,authpriv.none /var/log/messages\n" +
		"*.*;auth,authpriv.none;local0.none;local1.none;mail.none -/var/log/syslog\n"

	got := syslogStrip(in, &syslogTarget{loghost: "log.example.com", remote: true}, true)
	assert.Equal(t, want, got)
	assert.Equal(t, got, syslogStrip(got, &syslogTarget{loghost: "log.example.com", remote: true}, true))
}

func TestSyslog_UbuntuDropInFile(t *testing.T) {
	e, fakes := syslogEnv(t, "ubuntu", "")
	fakes.FS.Put(syslogConf, []byte("# main\n"), 0o644)
	fakes.FS.PutDir(syslogDropInDir, 0o755)
	fakes.FS.Put(syslogDefaultConf, []byte(ubuntuDefault), 0o644)
	fakes.FS.Put(syslogZimbraOld, []byte("old"), 0o644)
	fakes.FS.Put(syslogLogrotate, []byte(rotateTemplate), 0o644)
	fakes.FS.Put(syslogUnit, []byte("[Unit]"), 0o644)

	require.NoError(t, ConfigureSyslog(e))

	assert.Equal(t, "# main\n", syslogRead(t, fakes, syslogConf), "rsyslog.conf is not touched")
	assert.Equal(t, "auth,authpriv.*\t\t\t/var/log/auth.log\n"+
		"*.*;auth,authpriv.none;local0.none;local1.none;mail.none\t\t-/var/log/syslog\n"+
		"mail.err\t\t\t/var/log/mail.err\n", syslogRead(t, fakes, syslogDefaultConf),
		"the mail rules of the stock file stay")
	assert.Equal(t, "local0.*                -/var/log/carbonio.log\n"+
		"auth.info               -/var/log/carbonio.log\n"+
		"mail.*                -/var/log/carbonio.log\n", syslogRead(t, fakes, syslogCarbonio))
	_, err := fakes.FS.Stat(syslogZimbraOld)
	require.Error(t, err)

	_, err = fakes.FS.Stat(syslogDefaultConf + ".bak")
	require.Error(t, err, "updateRsyslogd made no backup")

	uid, gid, _ := fakes.FS.Owner(syslogLogFile)
	assert.Equal(t, [2]int{104, 4}, [2]int{uid, gid}, "syslog:adm")
	assert.Equal(t, "/var/log/carbonio.log {\n\tcreate 0640 syslog adm\n\tsu zextras zextras\n}\n",
		syslogRead(t, fakes, syslogLogrotate))

	before := syslogRead(t, fakes, syslogDefaultConf)

	require.NoError(t, ConfigureSyslog(e))
	assert.Equal(t, before, syslogRead(t, fakes, syslogDefaultConf))
	assert.Equal(t, 1, syslogRestarts(fakes))
}

func TestSyslog_UbuntuWithoutDropInDirEditsMainFile(t *testing.T) {
	e, fakes := syslogEnv(t, "ubuntu", "log.example.com")
	fakes.FS.Put(syslogConf, []byte("*.info;mail.none /var/log/messages\n"), 0o644)
	fakes.FS.Put(syslogUnit, []byte("[Unit]"), 0o644)

	require.NoError(t, ConfigureSyslog(e))

	assert.True(t, strings.HasPrefix(syslogRead(t, fakes, syslogConf), "*.info /var/log/messages\nlocal0.*"),
		"the *.info rule is left alone on Ubuntu")
}

func TestSyslog_UbuntuDropInWithoutDefaultFileFails(t *testing.T) {
	e, fakes := syslogEnv(t, "ubuntu", "")
	fakes.FS.Put(syslogConf, []byte("# main\n"), 0o644)
	fakes.FS.PutDir(syslogDropInDir, 0o755)

	require.Error(t, ConfigureSyslog(e))
}

func TestSyslog_LogrotateOnRocky(t *testing.T) {
	e, fakes := syslogRocky(t, "mail.example.com")
	fakes.FS.Put(syslogLogrotate, []byte(rotateTemplate), 0o644)

	require.NoError(t, ConfigureSyslog(e))
	assert.Equal(t, "/var/log/carbonio.log {\n\tcreate 0640 zextras zextras\n\t#su zextras zextras\n}\n",
		syslogRead(t, fakes, syslogLogrotate))
}

func TestSyslog_ExistingLogFileIsKept(t *testing.T) {
	e, fakes := syslogRocky(t, "mail.example.com")
	fakes.FS.Put(syslogLogFile, []byte("history\n"), 0o640)

	require.NoError(t, ConfigureSyslog(e))
	assert.Equal(t, "history\n", syslogRead(t, fakes, syslogLogFile))
}

func TestSyslog_MissingRsyslogConfFails(t *testing.T) {
	e, _ := syslogEnv(t, "rocky", "")

	require.ErrorContains(t, ConfigureSyslog(e), "/etc/rsyslog.conf")
}

func TestSyslog_MissingServiceUnitFailsOnlyWhenARestartIsDue(t *testing.T) {
	e, fakes := syslogRocky(t, "mail.example.com")
	require.NoError(t, fakes.FS.Remove(syslogUnit))

	require.ErrorContains(t, ConfigureSyslog(e), "rsyslog")
	assert.Zero(t, syslogRestarts(fakes))

	// The configuration is in place now: nothing to restart.
	require.NoError(t, ConfigureSyslog(e))
}

func TestSyslog_FailedRestartIsAWarning(t *testing.T) {
	e, fakes := syslogRocky(t, "mail.example.com")
	fakes.Exec.OnCmdline("systemctl restart", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: 1}, errors.New("unit failed")
	})

	require.NoError(t, ConfigureSyslog(e))
	assert.NotEmpty(t, e.Log.takeWarnings())
}

func TestSyslog_DirectoryFailureIsAnError(t *testing.T) {
	e, fakes := syslogRocky(t, "")
	old := svcOpenDir
	svcOpenDir = func(*Env) (svcDir, error) { return nil, errors.New("ldap down") }

	t.Cleanup(func() { svcOpenDir = old })

	require.ErrorContains(t, ConfigureSyslog(e), "zimbraLogHostname")
	assert.Equal(t, rockyRsyslog, syslogRead(t, fakes, syslogConf))
}

func TestSyslog_NodeNameWithoutAConfiguration(t *testing.T) {
	e, fakes := syslogRocky(t, "log.example.com")
	e.Cfg = nil

	require.NoError(t, ConfigureSyslog(e), "the command runs without a bootstrap configuration")
	assert.Contains(t, syslogRead(t, fakes, syslogConf), "@log.example.com")
}

func TestSyslog_ReportsTheChange(t *testing.T) {
	e, _ := syslogRocky(t, "mail.example.com")
	out := &strings.Builder{}
	e.Log = NewLog(out, nil)

	require.NoError(t, ConfigureSyslog(e))
	assert.Equal(t, "updateSyslog: Updating /etc/rsyslog.conf...done.\n", out.String())
}
