// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldapwatch"
)

const (
	baseDN      = "cn=zimbra"
	globalBase  = "cn=config,cn=zimbra"
	serversBase = "cn=servers,cn=zimbra"
)

type searchCall struct {
	baseDN string
	filter string
	attrs  []string
	scope  int
}

// fakeDirectory answers each search from a per-base-DN entry list.
type fakeDirectory struct {
	mu      sync.Mutex
	entries map[string][]*goldap.Entry
	errs    map[string]error
	calls   []searchCall
	block   chan struct{} // when non-nil, Search waits for it to be closed
}

func newDirectory() *fakeDirectory {
	return &fakeDirectory{entries: map[string][]*goldap.Entry{}, errs: map[string]error{}}
}

func (d *fakeDirectory) Search(base, filter string, attrs []string, scope int) (*goldap.SearchResult, error) {
	d.mu.Lock()
	d.calls = append(d.calls, searchCall{base, filter, attrs, scope})
	block, err, entries := d.block, d.errs[base], d.entries[base]
	d.mu.Unlock()

	if block != nil {
		<-block
	}

	if err != nil {
		return nil, err
	}

	return &goldap.SearchResult{Entries: entries}, nil
}

func (d *fakeDirectory) put(base string, entries ...*goldap.Entry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries[base] = entries
}

func entry(dn, csn, modified string) *goldap.Entry {
	return goldap.NewEntry(dn, map[string][]string{"entryCSN": {csn}, "modifyTimestamp": {modified}})
}

func seededDirectory() *fakeDirectory {
	d := newDirectory()
	d.put(globalBase, entry(globalBase, "csn-g1", "20260101000000Z"))
	d.put(serversBase,
		entry("cn=mbox1,"+serversBase, "csn-s1", "20260101000000Z"),
		entry("cn=proxy1,"+serversBase, "csn-s2", "20260101000000Z"))
	d.put("", entry("dc=example,dc=com", "csn-d1", "20260101000000Z"))

	return d
}

func fingerprint(t *testing.T, d *fakeDirectory) ldapwatch.Fingerprint {
	t.Helper()

	got, err := ldapwatch.NewLDAPSource(d, baseDN).Fingerprint(context.Background())
	require.NoError(t, err)

	return got
}

func TestLDAPSourceQueriesTheEntriesACycleReads(t *testing.T) {
	d := seededDirectory()
	fingerprint(t, d)

	attrs := []string{"entryCSN", "modifyTimestamp"}
	assert.Equal(t, []searchCall{
		{globalBase, "(objectClass=*)", attrs, goldap.ScopeBaseObject},
		{serversBase, "(objectClass=zimbraServer)", attrs, goldap.ScopeSingleLevel},
		{"", "(objectClass=zimbraDomain)", attrs, goldap.ScopeWholeSubtree},
	}, d.calls, "global base, servers one level, domains from the root, operational attributes only")
}

func TestLDAPSourceFingerprintIsStableAndOrderIndependent(t *testing.T) {
	d := seededDirectory()
	first := fingerprint(t, d)

	assert.Equal(t, first, fingerprint(t, d), "no write, same fingerprint")
	assert.ElementsMatch(t,
		[]string{ldapwatch.ComponentGlobal, ldapwatch.ComponentServers, ldapwatch.ComponentDomains},
		keys(first))

	d.put(serversBase,
		entry("cn=proxy1,"+serversBase, "csn-s2", "20260101000000Z"),
		entry("cn=mbox1,"+serversBase, "csn-s1", "20260101000000Z"))

	assert.Equal(t, first, fingerprint(t, d), "the order the server returns entries in is irrelevant")
}

func TestLDAPSourceDetectsEveryKindOfChange(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(d *fakeDirectory)
		component string
	}{
		{"global modified", func(d *fakeDirectory) {
			d.put(globalBase, entry(globalBase, "csn-g2", "20260102000000Z"))
		}, ldapwatch.ComponentGlobal},
		{"global modified within the same second", func(d *fakeDirectory) {
			d.put(globalBase, entry(globalBase, "csn-g1b", "20260101000000Z"))
		}, ldapwatch.ComponentGlobal},
		{"server modified", func(d *fakeDirectory) {
			d.put(serversBase,
				entry("cn=mbox1,"+serversBase, "csn-s1-new", "20260102000000Z"),
				entry("cn=proxy1,"+serversBase, "csn-s2", "20260101000000Z"))
		}, ldapwatch.ComponentServers},
		{"server added", func(d *fakeDirectory) {
			d.put(serversBase,
				entry("cn=mbox1,"+serversBase, "csn-s1", "20260101000000Z"),
				entry("cn=proxy1,"+serversBase, "csn-s2", "20260101000000Z"),
				entry("cn=mbox2,"+serversBase, "csn-s3", "20260101000000Z"))
		}, ldapwatch.ComponentServers},
		{"server deleted", func(d *fakeDirectory) {
			d.put(serversBase, entry("cn=mbox1,"+serversBase, "csn-s1", "20260101000000Z"))
		}, ldapwatch.ComponentServers},
		{"domain modified", func(d *fakeDirectory) {
			d.put("", entry("dc=example,dc=com", "csn-d2", "20260102000000Z"))
		}, ldapwatch.ComponentDomains},
		{"domain added", func(d *fakeDirectory) {
			d.put("",
				entry("dc=example,dc=com", "csn-d1", "20260101000000Z"),
				entry("dc=other,dc=com", "csn-d9", "20260101000000Z"))
		}, ldapwatch.ComponentDomains},
		{"last domain deleted", func(d *fakeDirectory) {
			d.put("")
		}, ldapwatch.ComponentDomains},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := seededDirectory()
			before := fingerprint(t, d)

			tt.mutate(d)

			after := fingerprint(t, d)

			for _, c := range keys(before) {
				if c == tt.component {
					assert.NotEqual(t, before[c], after[c], "%s must change", c)
				} else {
					assert.Equal(t, before[c], after[c], "%s must not change", c)
				}
			}
		})
	}
}

func TestLDAPSourceNamesTheFailingComponent(t *testing.T) {
	d := seededDirectory()
	boom := errors.New("ldap: connection reset")
	d.errs[serversBase] = boom

	_, err := ldapwatch.NewLDAPSource(d, baseDN).Fingerprint(context.Background())

	require.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, ldapwatch.ComponentServers)
}

func TestLDAPSourceReturnsPromptlyWhenContextIsCancelled(t *testing.T) {
	d := seededDirectory()
	d.block = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		_, err := ldapwatch.NewLDAPSource(d, baseDN).Fingerprint(ctx)
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()

		return len(d.calls) == 1
	}, 5*time.Second, time.Millisecond, "the first search is in flight")

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Fingerprint did not return after cancel while a search was in flight")
	}

	close(d.block) // let the in-flight search finish; its goroutine must end on its own

	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()

		return len(d.calls) == 1 // collect saw the cancelled context and issued no further search
	}, 5*time.Second, time.Millisecond)
	requireNoWatcherGoroutine(t)
}

func keys(fp ldapwatch.Fingerprint) []string {
	out := make([]string, 0, len(fp))
	for k := range fp {
		out = append(out, k)
	}

	return out
}
