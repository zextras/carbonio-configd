// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package ldapwatch_test

import (
	"context"
	"sync"
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
// does not read must not. Run it with `make test-integration-ldap`; it skips
// when no container runtime is reachable.

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

type olWatch struct {
	t        *testing.T
	data     *goldap.Conn
	triggers chan []string
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

func startOpenLDAPWatch(t *testing.T) *olWatch {
	t.Helper()

	ctx := context.Background()
	ol := test.StartOpenLDAP(ctx, t, test.OpenLDAPFixtures{
		Slapd:  "../configmgr/testdata/openldap/slapd.ldif",
		Schema: "../configmgr/testdata/openldap/zimbra-schema.ldif",
		Seed:   "../configmgr/testdata/openldap/seed.ldif",
	})

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

	w := &olWatch{t: t, data: data, triggers: make(chan []string, 16)}

	var wg sync.WaitGroup

	runCtx, cancel := context.WithCancel(ctx)
	watcher := ldapwatch.New(&ldapwatch.Options{
		Source:   ldapwatch.NewLDAPSource(client, client.BaseDN()),
		Clock:    sys.RealClock{},
		Interval: olPoll,
		Debounce: olDebounce,
		Trigger: func(_ context.Context, changed []string) {
			w.triggers <- changed
		},
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

func TestOpenLDAP_ChangeTriggersAWatcherReload(t *testing.T) {
	w := startOpenLDAPWatch(t)

	// The baseline is taken in the first poll and must not trigger.
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
