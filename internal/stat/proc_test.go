// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addProc adds a process of a user (by uid) with its command line, ticks and
// statm line.
func (f files) addProc(pid, uid int, name, cmdline string, utime, stime int, statm string) {
	f.procEntry(pid, name, 1, statLine(pid, name, 1, utime, stime, 1, 0), "")
	f["proc/"+itoa(pid)+"/status"] += "Uid:\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\n"
	f["proc/"+itoa(pid)+"/cmdline"] = cmdline
	f["proc/"+itoa(pid)+"/statm"] = statm + "\n"
}

func procFixture(step int) files {
	f := files{}

	if step == 1 {
		f["proc/stat"] = "cpu  1000 0 500 8000 500 0 0 0\n"
		f.addProc(10, 1000, "slapd", "/opt/zextras/libexec/slapd\x00-h\x00ldapi:///\x00", 100, 50, "50000 12800 2560 0 0 0 0")
		f.addProc(20, 1000, "mysqld", "/opt/zextras/common/libexec/mysqld\x00--defaults-file=/opt/zextras/conf/my.cnf\x00",
			7, 7, "2048 1024 512 0 0 0 0")
		f.addProc(30, 101, "qmgr", "qmgr\x00-l\x00", 4, 2, "1000 500 100 0 0 0 0")
		f.addProc(31, 101, "pickup", "pickup\x00", 1, 0, "500 250 50 0 0 0 0")
	} else {
		f["proc/stat"] = "cpu  1200 0 600 8600 600 0 0 0\n"
		f.addProc(10, 1000, "slapd", "/opt/zextras/libexec/slapd\x00-h\x00ldapi:///\x00", 150, 70, "50000 12800 2560 0 0 0 0")
		f.addProc(20, 1000, "mysqld", "/opt/zextras/common/libexec/mysqld\x00--defaults-file=/opt/zextras/conf/my.cnf\x00",
			7, 7, "2048 1024 512 0 0 0 0")
		f.addProc(30, 101, "qmgr", "qmgr\x00-l\x00", 10, 4, "1000 500 100 0 0 0 0")
		f.addProc(31, 101, "pickup", "pickup\x00", 3, 1, "500 250 50 0 0 0 0")
	}

	f.addProc(40, 0, "bash", "bash\x00", 999, 999, "1 1 1 0 0 0 0")

	return f
}

func userNames(uid int) string {
	return map[int]string{0: "root", 101: "postfix", 1000: "zextras"}[uid]
}

// stat-proc.pl: the process groups named on the command line (the %ps_subs
// filters of the owner and the command), the CPU share of each over the
// interval (computeDelta, getProcessStat), the memory of the last process of
// the group and the system CPU split; the first line has no interval.
func TestProcGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, procFixture(1))
	te.UserName = userNames
	c := newProc(te.Env, []string{"ldap", "mysql", "postfix", "zmstat", "bogus"})

	assert.Equal(t, "Warning: Not possible to monitor process bogus\n", te.stderr.String())
	assert.Equal(t, "timestamp, system, user, sys, idle, iowait, "+
		"ldap, ldap-total-cpu, ldap-utime, ldap-stime, ldap-totalMB, ldap-rssMB, ldap-sharedMB, ldap-process-count, "+
		"mysql, mysql-total-cpu, mysql-utime, mysql-stime, mysql-totalMB, mysql-rssMB, mysql-sharedMB, mysql-process-count, "+
		"postfix, postfix-total-cpu, postfix-utime, postfix-stime, postfix-totalMB, postfix-rssMB, postfix-sharedMB, "+
		"postfix-process-count, "+
		"zmstat, zmstat-total-cpu, zmstat-utime, zmstat-stime, zmstat-totalMB, zmstat-rssMB, zmstat-sharedMB, "+
		"zmstat-process-count", c.Heading())

	assert.Equal(t, []string{stamp(0) + ", system, 0, 0, 0, 0, " +
		"ldap, 0.0, 0.0, 0.0, 195.3, 50.0, 10.0, 1, " +
		"mysql, 0.0, 0.0, 0.0, 8.0, 4.0, 2.0, 1, " +
		"postfix, 0.0, 0.0, 0.0, 2.0, 1.0, 0.2, 2, " +
		"zmstat, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0"}, te.sample(t, c, 0))

	te.setFiles(procFixture(2))
	assert.Equal(t, []string{stamp(1) + ", system, 20.0, 10.0, 60.0, 10.0, " +
		"ldap, 7.0, 5.0, 2.0, 195.3, 50.0, 10.0, 1, " +
		"mysql, 0.0, 0.0, 0.0, 8.0, 4.0, 2.0, 1, " +
		"postfix, 1.1, 0.8, 0.3, 2.0, 1.0, 0.2, 2, " +
		"zmstat, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0"}, te.sample(t, c, 1))
}

// getEnabledProcs: the groups follow the services of the server; when LDAP
// cannot be read every group is monitored.
func TestEnabledProcs(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		services []string
		err      error
		want     []string
		stderr   string
	}{
		"mailbox host": {services: []string{"mailbox"}, want: []string{"mailbox", "mysql", procStat}},
		"mta with antivirus": {
			services: []string{"mta", "antivirus", "ldap"},
			want:     []string{"ldap", "postfix", "amavis", "clam", procStat},
		},
		"nothing": {want: []string{procStat}},
		"ldap down": {
			err: errors.New("down"), want: defaultProcs,
			stderr: "stat proc: cannot read the enabled services: down\n",
		},
	} {
		te := newTestEnv(t, files{})
		te.Enabled = func(context.Context, string) ([]string, error) { return tc.services, tc.err }

		got := te.enabledProcs()

		assert.Equal(t, tc.want, got, name)
		assert.Equal(t, tc.stderr, te.stderr.String(), name)
	}
}

func TestProcMissingProcessIsReported(t *testing.T) {
	t.Parallel()

	tree := procFixture(1)
	te := newTestEnv(t, tree)
	te.UserName = userNames
	c := newProc(te.Env, []string{"ldap"})

	delete(tree, "proc/10/statm")
	te.setFiles(tree)

	lines := te.sample(t, c, 0)
	require.Len(t, lines, 1)
	assert.Contains(t, te.stderr.String(), "01/01/2026 00:00:00: ERROR: No such process: '10'\n")
	assert.Contains(t, lines[0], "ldap, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 1")
}
