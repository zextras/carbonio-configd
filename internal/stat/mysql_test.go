// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// stat-mysql.pl: SHOW GLOBAL STATUS, the names fixed at start and the values
// joined with a bare comma. The password reaches the client through its
// environment, never its command line.
func TestMySQLGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.LC["zimbra_mysql_user"] = "zimbra"
	te.LC["zimbra_mysql_password"] = "s3cr3t"

	bin := filepath.Join(te.Base, "bin", "mysql")
	te.fakes.Exec.OnPath(bin, systest.Reply("Aborted_clients\t1\nQuestions\t100\nUptime\t5\n"))

	c, err := newMySQL(te.Env)
	require.NoError(t, err)

	assert.Equal(t, "timestamp, Aborted_clients, Questions, Uptime", c.Heading())

	// Uptime is gone and a new variable appeared: the columns stay.
	te.fakes.Exec.OnPath(bin, systest.Reply("Aborted_clients\t2\nQuestions\t150\nNew_var\t9\n"))
	assert.Equal(t, []string{stamp(0) + ",2,150,"}, te.sample(t, c, 0))

	for _, call := range te.fakes.Exec.Calls() {
		assert.Equal(t, bin, call.Path)
		assert.Equal(t, []string{"--user=zimbra", "--database=zimbra", "--batch", "--skip-column-names"}, call.Args)
		assert.Equal(t, []string{"MYSQL_PWD=s3cr3t"}, call.Env)
	}
}

func TestMySQLDefaultUser(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath(filepath.Join(te.Base, "bin", "mysql"), systest.Reply("A\t1\n"))

	_, err := newMySQL(te.Env)
	require.NoError(t, err)
	assert.Equal(t, "--user=zextras", te.fakes.Exec.Calls()[0].Args[0])
}

func TestMySQLClientFails(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.fakes.Exec.OnPath(filepath.Join(te.Base, "bin", "mysql"), systest.Fail(1, "ERROR 2002"))

	_, err := newMySQL(te.Env)
	require.ErrorContains(t, err, "error while running")
}
