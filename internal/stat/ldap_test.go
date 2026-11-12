// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// monitor answers the queries of the ldap collector from a counter value and
// the two waiter gauges, or fails.
type monitor struct {
	counter, read, write int
	err                  error
	queries              []MonitorQuery
}

func (m *monitor) query(_ context.Context, q []MonitorQuery) ([]string, error) {
	m.queries = q

	if m.err != nil {
		return nil, m.err
	}

	out := make([]string, len(q))

	for i, query := range q {
		switch {
		case strings.HasPrefix(query.DN, "cn=Read,cn=Waiters"):
			out[i] = fmt.Sprint(m.read)
		case strings.HasPrefix(query.DN, "cn=Write,cn=Waiters"):
			out[i] = fmt.Sprint(m.write)
		default:
			out[i] = fmt.Sprint(m.counter)
		}
	}

	return out, nil
}

func ldapRow(counter, read, write string) string {
	cols := make([]string, 18)
	for i := range cols {
		cols[i] = counter
	}

	cols[13], cols[17] = read, write

	return strings.Join(cols, ", ")
}

// stat-ldap.pl: cn=Monitor counters are logged as the change since the
// previous sample (empty on the first line), the waiters as they are.
func TestLDAPGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	m := &monitor{counter: 100, read: 2, write: 3}
	te.Monitor = m.query
	c := &ldapCollector{env: te.Env}

	assert.Equal(t, "timestamp, abandon_ops, add_ops, bind_ops, bytes_sent, compare_ops, completed_ops, connections, "+
		"delete_ops, entries_sent, extended_ops, initiated_ops, modify_ops, modrdn_ops, read_waiters, referrals_sent, "+
		"search_ops, unbind_ops, write_waiters", c.Heading())

	assert.Equal(t, []string{stamp(0) + ", " + ldapRow("", "2", "3")}, te.sample(t, c, 0))

	m.counter, m.read, m.write = 105, 4, 1
	assert.Equal(t, []string{stamp(1) + ", " + ldapRow("5", "4", "1")}, te.sample(t, c, 1))

	require.Len(t, m.queries, 18)
	assert.Equal(t, MonitorQuery{DN: "cn=Bytes,cn=Statistics,cn=Monitor", Attr: "monitorCounter"}, m.queries[3])
	assert.Equal(t, MonitorQuery{DN: "cn=Operations,cn=Monitor", Attr: "monitorOpInitiated"}, m.queries[10])
}

// A slapd that cannot be reached writes no line, is reported once until it
// answers again, and the counters start over.
func TestLDAPUnreachable(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	te.LC["zimbra_server_hostname"] = "ldap1.example.com"
	m := &monitor{counter: 100}
	te.Monitor = m.query
	c := &ldapCollector{env: te.Env}

	te.sample(t, c, 0)

	m.err = fmt.Errorf("refused: %w", ErrBind)
	assert.Empty(t, te.sample(t, c, 1))
	assert.Empty(t, te.sample(t, c, 2))
	assert.Equal(t, stamp(1)+": ERROR: Could not bind to LDAP server: ldap1.example.com\n", te.stderr.String())

	m.err = nil
	m.counter = 110
	assert.Equal(t, []string{stamp(3) + ", " + ldapRow("", "0", "0")}, te.sample(t, c, 3), "counters start over")

	m.err = errors.New("no socket")
	te.stderr.Reset()
	assert.Empty(t, te.sample(t, c, 4))
	assert.Equal(t, stamp(4)+": ERROR: Could not connect to LDAP server: ldap1.example.com\n", te.stderr.String())
}

func TestLDAPWithoutMonitor(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	_, err := (&ldapCollector{env: te.Env}).Sample(Tick{})

	require.Error(t, err)
}
