// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const mysqlUsage = `Usage: zmstat-mysql [options]
Monitor MySQL status variables
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/mysql.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/mysql.csv
and a new file is created.
`

func mysqlSpec() *spec {
	return &spec{
		name:  procMySQL,
		files: fixedFiles("mysql.csv", "mysql.pid"),
		usage: mysqlUsage,
		build: func(env *Env, _ *options) (Collector, error) { return newMySQL(env) },
	}
}

// mysql is stat-mysql.pl: the variables of SHOW GLOBAL STATUS. The names are
// those the server has when the collector starts; a variable that is later
// missing reads empty.
type mysql struct {
	env   *Env
	names []string
}

func newMySQL(env *Env) (*mysql, error) {
	m := &mysql{env: env}

	rows, err := m.status()
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		m.names = append(m.names, r[0])
	}

	return m, nil
}

// Heading implements Collector.
func (m *mysql) Heading() string { return "timestamp, " + strings.Join(m.names, ", ") }

// Sample implements Collector. Values are joined with a bare comma, as the
// Perl does.
func (m *mysql) Sample(t Tick) ([]string, error) {
	rows, err := m.status()
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(rows))
	for _, r := range rows {
		values[r[0]] = r[1]
	}

	var line strings.Builder

	line.WriteString(t.Stamp)

	for _, n := range m.names {
		line.WriteString("," + values[n])
	}

	return []string{line.String()}, nil
}

// status runs SHOW GLOBAL STATUS through the mysql client, as the zimbra
// database user. The password goes in the environment, not on the command
// line where ps would show it.
func (m *mysql) status() ([][2]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	user := m.env.LC["zimbra_mysql_user"]
	if user == "" {
		user = defaultUser
	}

	bin := filepath.Join(m.env.Base, "bin", "mysql")

	res, err := m.env.Sys.Exec.Run(ctx, &sys.Cmd{
		Path:  bin,
		Args:  []string{"--user=" + user, "--database=zimbra", "--batch", "--skip-column-names"},
		Env:   []string{"MYSQL_PWD=" + m.env.LC["zimbra_mysql_password"]},
		Stdin: strings.NewReader("SHOW GLOBAL STATUS"),
	})
	if err != nil {
		return nil, fmt.Errorf("error while running %s: %w", bin, err)
	}

	var rows [][2]string

	for line := range strings.SplitSeq(string(res.Stdout), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}

		name, value, _ := strings.Cut(line, "\t")
		rows = append(rows, [2]string{name, value})
	}

	return rows, nil
}
