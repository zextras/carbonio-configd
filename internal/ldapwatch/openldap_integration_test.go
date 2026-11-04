// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package ldapwatch_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/ldapwatch"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/test"
)

// The watcher runs on the real clock against a slapd container seeded like the
// configmgr suite (same fixtures), through the same internal/ldap client the
// daemon reads its configuration with. A change made as zmprov would make it
// must reach Trigger within a few poll intervals; changes to entries a cycle
// does not read must not. Three directories are covered:
//
//   - a plain slapd, with no Subscriber: the poller;
//   - a plain slapd, with the sync Subscriber: no syncprov, so the watcher must
//     fall back to polling by itself, like a single-node Carbonio directory;
//   - a slapd with the syncprov overlay: the content-sync stream drives the
//     watcher, and nothing polls while it is live.
//
// Run it with `make test-integration-ldap`; it skips when no container runtime
// is reachable.

const (
	// olPoll and olDebounce keep the test fast; the daemon defaults are 5s and 2s.
	olPoll     = 200 * time.Millisecond
	olDebounce = 400 * time.Millisecond
	// olDeadline bounds how long a change may take to be reported: a poll to see
	// it, the quiet window, and slack for a loaded CI host.
	olDeadline = 10 * time.Second
	// olQuiet is how long the test watches for a trigger that must not come.
	olQuiet = 2 * time.Second
)

// olSetup selects the directory and the detection the watcher is built with.
type olSetup struct {
	syncprov   bool // load the syncprov overlay on the data database
	moduleOnly bool // load the syncprov module, but no overlay on the data database
	subscriber bool // give the watcher the content-sync Subscriber
}

// countingSource counts the fingerprint reads, which is the LDAP load the
// watcher generates.
type countingSource struct {
	ldapwatch.Source
	reads atomic.Int64
}

func (c *countingSource) Fingerprint(ctx context.Context) (ldapwatch.Fingerprint, error) {
	c.reads.Add(1)

	return c.Source.Fingerprint(ctx) //nolint:wrapcheck // a pass-through
}

type olWatch struct {
	t        *testing.T
	data     *goldap.Conn
	triggers chan []string
	modes    chan string
	source   *countingSource
}

// modify replaces attr of dn through the data connection.
func (w *olWatch) modify(dn, attr string, values ...string) {
	w.t.Helper()

	req := goldap.NewModifyRequest(dn, nil)
	req.Replace(attr, values)
	require.NoError(w.t, w.data.Modify(req))
}

func (w *olWatch) expectTrigger(want ...string) {
	w.t.Helper()

	select {
	case got := <-w.triggers:
		assert.Equal(w.t, want, got)
	case <-time.After(olDeadline):
		w.t.Fatalf("no trigger for %v within %s", want, olDeadline)
	}
}

func (w *olWatch) expectQuiet(msg string) {
	w.t.Helper()

	select {
	case got := <-w.triggers:
		w.t.Fatalf("%s: unexpected trigger %v", msg, got)
	case <-time.After(olQuiet):
	}
}

// expectMode waits for the watcher to report mode.
func (w *olWatch) expectMode(mode string) {
	w.t.Helper()

	deadline := time.After(olDeadline)

	for {
		select {
		case got := <-w.modes:
			if got == mode {
				return
			}
		case <-deadline:
			w.t.Fatalf("the watcher never reported mode %q within %s", mode, olDeadline)
		}
	}
}

func startOpenLDAPWatch(t *testing.T, setup olSetup) *olWatch {
	t.Helper()

	ctx := context.Background()
	fixtures := test.OpenLDAPFixtures{
		Slapd:  "../configmgr/testdata/openldap/slapd.ldif",
		Schema: "../configmgr/testdata/openldap/zimbra-schema.ldif",
		Seed:   "../configmgr/testdata/openldap/seed.ldif",
	}

	switch {
	case setup.syncprov:
		fixtures.ExtraConfig = "testdata/syncprov.ldif"
	case setup.moduleOnly:
		fixtures.ExtraConfig = "testdata/syncprov-module-only.ldif"
	}

	ol := test.StartOpenLDAP(ctx, t, fixtures)

	client, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      ol.URL,
		BindDN:   test.OpenLDAPDataBindDN,
		Password: test.OpenLDAPDataPassword,
		BaseDN:   "cn=zimbra",
		StartTLS: false,
		FS:       systest.New().FS,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	data, err := goldap.DialURL(ol.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = data.Close() })
	require.NoError(t, data.Bind(test.OpenLDAPDataBindDN, test.OpenLDAPDataPassword))

	w := &olWatch{
		t: t, data: data, triggers: make(chan []string, 16), modes: make(chan string, 16),
		source: &countingSource{Source: ldapwatch.NewLDAPSource(client, client.BaseDN())},
	}

	var subscriber ldapwatch.Subscriber

	if setup.subscriber {
		subscriber = ldapwatch.NewSyncSubscriber(func(ctx context.Context) (ldapwatch.SyncConn, error) {
			conn, err := client.NewStreamConn(ctx)
			if err != nil {
				return nil, err //nolint:wrapcheck // the subscriber wraps it
			}

			return conn, nil
		})
	}

	var wg sync.WaitGroup

	runCtx, cancel := context.WithCancel(ctx)
	watcher := ldapwatch.New(&ldapwatch.Options{
		Source:     w.source,
		Subscriber: subscriber,
		Clock:      sys.RealClock{},
		Interval:   olPoll,
		Debounce:   olDebounce,
		Trigger: func(_ context.Context, changed []string) {
			w.triggers <- changed
		},
		OnMode: func(mode string) { w.modes <- mode },
	})

	wg.Add(1)

	go func() {
		defer wg.Done()

		watcher.Run(runCtx)
	}()

	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	return w
}

// changeScenarios are the changes every detection must report, once each.
func changeScenarios(t *testing.T, w *olWatch) {
	t.Helper()

	// The baseline is taken at start and must not trigger.
	w.expectQuiet("baseline")

	t.Run("global_config", func(t *testing.T) {
		w.modify("cn=config,cn=zimbra", "zimbraMtaMaxMessageSize", "20480000")
		w.expectTrigger(ldapwatch.ComponentGlobal)
		w.expectQuiet("after the global change")
	})

	t.Run("server_entry", func(t *testing.T) {
		w.modify("cn=mail.test.example,cn=servers,cn=zimbra", "zimbraMilterServerEnabled", "TRUE")
		w.expectTrigger(ldapwatch.ComponentServers)
		w.expectQuiet("after the server change")
	})

	t.Run("domain_entry", func(t *testing.T) {
		w.modify("dc=test.example", "zimbraVirtualHostname", "mail.test.example", "web.test.example")
		w.expectTrigger(ldapwatch.ComponentDomains)
		w.expectQuiet("after the domain change")
	})

	t.Run("burst_is_one_trigger", func(t *testing.T) {
		for _, v := range []string{"1", "2", "3", "4", "5"} {
			w.modify("cn=config,cn=zimbra", "zimbraMtaMaxMessageSize", v+"0240000")
			time.Sleep(50 * time.Millisecond)
		}

		w.expectTrigger(ldapwatch.ComponentGlobal)
		w.expectQuiet("a burst makes one trigger")
	})

	t.Run("unwatched_entry_is_ignored", func(t *testing.T) {
		// cn=admins is not read by a cycle: nothing to reload.
		w.modify("cn=admins,cn=zimbra", "description", "changed")
		w.expectQuiet("an unwatched entry changed")
	})
}

func TestOpenLDAP_ChangeTriggersAWatcherReload(t *testing.T) {
	w := startOpenLDAPWatch(t, olSetup{})

	changeScenarios(t, w)
}

// A directory without the syncprov overlay does not advertise the control; the
// watcher must fall back to polling on its own and behave exactly as the poller.
func TestOpenLDAP_WithoutSyncprovTheSyncWatcherFallsBackToPolling(t *testing.T) {
	assertFallsBackToPolling(t, olSetup{subscriber: true})
}

// A directory that lists the control but does not serve it for the data
// database (the module is loaded, the overlay is not) must not fool the watcher:
// the sync search is not answered with a refresh, and the watcher polls.
func TestOpenLDAP_AdvertisedButUnservedSyncFallsBackToPolling(t *testing.T) {
	assertFallsBackToPolling(t, olSetup{moduleOnly: true, subscriber: true})
}

func assertFallsBackToPolling(t *testing.T, setup olSetup) {
	t.Helper()

	w := startOpenLDAPWatch(t, setup)

	changeScenarios(t, w)

	select {
	case m := <-w.modes:
		assert.Equal(t, ldapwatch.ModePoll, m)
	default:
		t.Fatal("the watcher reports its initial mode")
	}

	select {
	case m := <-w.modes:
		t.Fatalf("a directory without syncprov must never go to mode %q", m)
	default:
	}
}

// With the syncprov overlay the content-sync stream drives the watcher: the same
// changes are reported, a burst of writes is one trigger and one fingerprint
// read, and the directory is not polled while the stream is live.
func TestOpenLDAP_SyncStreamTriggersAWatcherReload(t *testing.T) {
	w := startOpenLDAPWatch(t, olSetup{syncprov: true, subscriber: true})

	w.expectMode(ldapwatch.ModeSync)

	// Idle: the poll interval is 200ms, so a poller would have read the
	// directory about ten times in the quiet period. The stream reads it once
	// (the catch-up read after it went live) and then not at all.
	w.expectQuiet("baseline")
	assert.LessOrEqual(t, w.source.reads.Load(), int64(2), "no polling while the stream is live")

	idle := w.source.reads.Load()

	t.Run("global_config", func(t *testing.T) {
		w.modify("cn=config,cn=zimbra", "zimbraMtaMaxMessageSize", "20480000")
		w.expectTrigger(ldapwatch.ComponentGlobal)
		w.expectQuiet("after the global change")
		assert.Equal(t, idle+1, w.source.reads.Load(), "one fingerprint read for one change")
	})

	t.Run("server_entry", func(t *testing.T) {
		w.modify("cn=mail.test.example,cn=servers,cn=zimbra", "zimbraMilterServerEnabled", "TRUE")
		w.expectTrigger(ldapwatch.ComponentServers)
		w.expectQuiet("after the server change")
	})

	t.Run("domain_entry", func(t *testing.T) {
		w.modify("dc=test.example", "zimbraVirtualHostname", "mail.test.example", "web.test.example")
		w.expectTrigger(ldapwatch.ComponentDomains)
		w.expectQuiet("after the domain change")
	})

	t.Run("burst_is_one_trigger_and_one_read", func(t *testing.T) {
		before := w.source.reads.Load()

		for _, v := range []string{"1", "2", "3", "4", "5"} {
			w.modify("cn=config,cn=zimbra", "zimbraMtaMaxMessageSize", v+"0240000")
			time.Sleep(50 * time.Millisecond)
		}

		w.expectTrigger(ldapwatch.ComponentGlobal)
		w.expectQuiet("a burst makes one trigger")
		assert.Equal(t, before+1, w.source.reads.Load(), "five writes, one fingerprint read")
	})

	t.Run("unwatched_entry_is_ignored", func(t *testing.T) {
		before := w.source.reads.Load()

		// cn=admins is not selected by the sync search: not even a read.
		w.modify("cn=admins,cn=zimbra", "description", "changed")
		w.expectQuiet("an unwatched entry changed")
		assert.Equal(t, before, w.source.reads.Load(), "the stream does not report entries outside the watched classes")
	})

	t.Run("still_in_sync_mode", func(t *testing.T) {
		for {
			select {
			case m := <-w.modes:
				assert.Equal(t, ldapwatch.ModeSync, m, "the stream never dropped")
			default:
				return
			}
		}
	})
}
