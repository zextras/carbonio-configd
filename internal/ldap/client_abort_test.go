// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
)

// hungSearchClient returns a client bound to a server that accepts the bind and
// then never answers a search, with an operation timeout far beyond the test so
// only an explicit abort can end the request. parent is the client's lifecycle
// context (the daemon's, in production).
func hungSearchClient(t *testing.T, parent context.Context) (*Client, *ldapfake.Server) {
	t.Helper()

	srv := ldapfake.New(t, ldapfake.Options{HangOn: "search"})

	c, err := NewClient(&ClientConfig{
		URLs:        []string{srv.URL("ldap", "127.0.0.1")},
		BindDN:      "uid=zimbra,cn=admins,cn=zimbra",
		Password:    "secret",
		MaxRetries:  3,
		RetryDelay:  time.Millisecond,
		DialTimeout: 2 * time.Second,
		BindTimeout: 2 * time.Second,
		OpTimeout:   time.Minute,
		Context:     parent,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c, srv
}

// searchAbortedBy runs a search against a server that never answers, calls
// abort once the request is in flight, and fails unless the search returns with
// an error promptly. A search that waited for opTimeout would hold up the
// shutdown of the daemon past the systemd stop timeout.
func searchAbortedBy(t *testing.T, c *Client, srv *ldapfake.Server, abort func()) {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		_, err := c.Search("cn=zimbra", ldapFilterAllObjects, []string{"cn"}, ldap.ScopeBaseObject)
		done <- err
	}()

	deadline := time.Now().Add(testWait)
	for srv.HungSearches() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the search never reached the server")
		}

		time.Sleep(time.Millisecond)
	}

	start := time.Now()

	abort()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("search to a hung server succeeded")
		}
	case <-time.After(testWait):
		t.Fatalf("the in-flight search was not aborted within %s", testWait)
	}

	if d := time.Since(start); d > testWait {
		t.Errorf("abort took %s", d)
	}

	if n := srv.HungSearches(); n != 1 {
		t.Errorf("server saw %d searches, want 1 (an aborted search must not be retried)", n)
	}
}

const testWait = 5 * time.Second

// TestClient_SearchAbortedByContext verifies that cancelling the context the
// client was built with (the daemon's, cancelled by SIGTERM) ends a search the
// server never answers.
func TestClient_SearchAbortedByContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, srv := hungSearchClient(t, ctx)

	searchAbortedBy(t, c, srv, cancel)
}

// TestClient_SearchAbortedByClose verifies Close ends a search the server never
// answers.
func TestClient_SearchAbortedByClose(t *testing.T) {
	t.Parallel()

	c, srv := hungSearchClient(t, context.Background())

	searchAbortedBy(t, c, srv, func() { _ = c.Close() })
}

// TestClient_ExecuteWithRetryAbortedByCallerContext verifies a caller context
// other than the client's lifecycle also ends an in-flight operation.
func TestClient_ExecuteWithRetryAbortedByCallerContext(t *testing.T) {
	t.Parallel()

	c, srv := hungSearchClient(t, context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- c.executeWithRetry(ctx, func(conn *ldap.Conn) error {
			_, err := conn.Search(ldap.NewSearchRequest("cn=zimbra", ldap.ScopeBaseObject,
				ldap.NeverDerefAliases, 0, 0, false, ldapFilterAllObjects, []string{"cn"}, nil))

			return err
		})
	}()

	deadline := time.Now().Add(testWait)
	for srv.HungSearches() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the search never reached the server")
		}

		time.Sleep(time.Millisecond)
	}

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("operation on a hung server succeeded")
		}
	case <-time.After(testWait):
		t.Fatalf("the in-flight operation was not aborted within %s", testWait)
	}
}
