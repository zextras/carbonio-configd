// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const ldapUsage = `Usage: zmstat-ldap [options]
Monitor LDAP statistics
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/slapd.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/slapd.csv
and a new file is created.
`

const (
	attrOpCompleted = "monitorOpCompleted"
	attrCounter     = "monitorCounter"
)

// ldapStat is a column of slapd.csv: where cn=Monitor has it, and whether the
// column is the change since the previous sample (a counter) or the value.
type ldapStat struct {
	name, dn, attr string
	delta          bool
}

// ldapStats are the columns, sorted by name as the Perl sorts them.
var ldapStats = []ldapStat{
	{"abandon_ops", "cn=Abandon,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"add_ops", "cn=Add,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"bind_ops", "cn=Bind,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"bytes_sent", "cn=Bytes,cn=Statistics,cn=Monitor", attrCounter, true},
	{"compare_ops", "cn=Compare,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"completed_ops", "cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"connections", "cn=Total,cn=Connections,cn=Monitor", attrCounter, true},
	{"delete_ops", "cn=Delete,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"entries_sent", "cn=Entries,cn=Statistics,cn=Monitor", attrCounter, true},
	{"extended_ops", "cn=Extended,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"initiated_ops", "cn=Operations,cn=Monitor", "monitorOpInitiated", true},
	{"modify_ops", "cn=Modify,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"modrdn_ops", "cn=Modrdn,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"read_waiters", "cn=Read,cn=Waiters,cn=Monitor", attrCounter, false},
	{"referrals_sent", "cn=Referrals,cn=Statistics,cn=Monitor", attrCounter, true},
	{"search_ops", "cn=Search,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"unbind_ops", "cn=Unbind,cn=Operations,cn=Monitor", attrOpCompleted, true},
	{"write_waiters", "cn=Write,cn=Waiters,cn=Monitor", attrCounter, false},
}

func ldapSpec() *spec {
	return &spec{
		name:     procLDAP,
		files:    fixedFiles("slapd.csv", "ldap.pid"),
		usage:    ldapUsage,
		noSyslog: true,
		build: func(env *Env, _ *options) (Collector, error) {
			return &ldapCollector{env: env}, nil
		},
	}
}

// ldapCollector is stat-ldap.pl: the cn=Monitor counters of slapd. Counters
// are written as the change since the previous sample, so the first line (and
// the first after a connection failure) has them empty. A failure to reach
// slapd is reported once until it works again.
type ldapCollector struct {
	env       *Env
	prev      []int64
	havePrev  []bool
	seenError bool
}

// Heading implements Collector.
func (l *ldapCollector) Heading() string {
	names := make([]string, len(ldapStats))
	for i, s := range ldapStats {
		names[i] = s.name
	}

	return "timestamp, " + strings.Join(names, ", ")
}

// Sample implements Collector.
func (l *ldapCollector) Sample(t Tick) ([]string, error) {
	if l.env.Monitor == nil {
		return nil, errors.New("no way to reach slapd")
	}

	queries := make([]MonitorQuery, len(ldapStats))
	for i, s := range ldapStats {
		queries[i] = MonitorQuery{DN: s.dn, Attr: s.attr}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	vals, err := l.env.Monitor(ctx, queries)
	if err != nil {
		l.fail(t.Stamp, err)

		return nil, nil
	}

	l.seenError = false

	if l.prev == nil {
		l.prev, l.havePrev = make([]int64, len(ldapStats)), make([]bool, len(ldapStats))
	}

	out := make([]string, len(ldapStats))

	for i, s := range ldapStats {
		out[i] = l.column(i, s, vals[i])
	}

	return []string{t.Stamp + ", " + strings.Join(out, ", ")}, nil
}

// column is the value of one column; counters are differences.
func (l *ldapCollector) column(i int, s ldapStat, raw string) string {
	if !s.delta {
		return raw
	}

	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		l.havePrev[i] = false

		return ""
	}

	out := ""
	if l.havePrev[i] {
		out = strconv.FormatInt(n-l.prev[i], 10)
	}

	l.prev[i], l.havePrev[i] = n, true

	return out
}

// fail reports an unreachable slapd (once) and forgets the previous counters.
func (l *ldapCollector) fail(stamp string, err error) {
	l.prev, l.havePrev = nil, nil

	if l.seenError {
		return
	}

	l.seenError = true

	what := "connect"
	if errors.Is(err, ErrBind) {
		what = "bind"
	}

	_, _ = fmt.Fprintf(l.env.Stderr, "%s: ERROR: Could not %s to LDAP server: %s\n",
		stamp, what, l.env.LC["zimbra_server_hostname"])
}
