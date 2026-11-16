// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
)

// retryClient is a client over a server that answers, with three retries.
func retryClient(t *testing.T) *Client {
	t.Helper()

	srv := ldapfake.New(t, ldapfake.Options{})

	c, err := NewClient(&ClientConfig{
		URLs:        []string{srv.URL("ldap", "127.0.0.1")},
		BindDN:      "uid=zimbra,cn=admins,cn=zimbra",
		Password:    "secret",
		MaxRetries:  3,
		RetryDelay:  time.Millisecond,
		DialTimeout: 2 * time.Second,
		BindTimeout: 2 * time.Second,
		OpTimeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// An answer of the server to the request itself (an attribute type it does not
// define, a value it refuses) is the same every time: the operation is made
// once and the server's error comes back as it is, not as "failed after 3
// retries".
func TestClient_ExecuteWithRetry_PermanentResultsAreNotRetried(t *testing.T) {
	t.Parallel()

	for name, code := range map[string]uint16{
		"undefined attribute type": ldap.LDAPResultUndefinedAttributeType,
		"invalid attribute syntax": ldap.LDAPResultInvalidAttributeSyntax,
		"constraint violation":     ldap.LDAPResultConstraintViolation,
		"object class violation":   ldap.LDAPResultObjectClassViolation,
		"unwilling to perform":     ldap.LDAPResultUnwillingToPerform,
		"naming violation":         ldap.LDAPResultNamingViolation,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := retryClient(t)
			want := ldap.NewError(code, errors.New("zimbraNoSuchAttr: attribute type undefined"))
			attempts := 0

			err := c.executeWithRetry(t.Context(), func(*ldap.Conn) error {
				attempts++

				return want
			})

			if attempts != 1 {
				t.Errorf("attempts = %d, want 1", attempts)
			}

			if !errors.Is(err, want) {
				t.Errorf("err = %v, want the error of the server", err)
			}
		})
	}
}

// The failures of the server are retried, up to 1+maxRetries attempts.
func TestClient_ExecuteWithRetry_ServerFailuresAreRetried(t *testing.T) {
	t.Parallel()

	for name, code := range map[string]uint16{
		"busy":        ldap.LDAPResultBusy,
		"unavailable": ldap.LDAPResultUnavailable,
		"timeout":     ldap.LDAPResultTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := retryClient(t)
			attempts := 0

			err := c.executeWithRetry(t.Context(), func(*ldap.Conn) error {
				attempts++

				return ldap.NewError(code, errors.New("try later"))
			})

			if attempts != 4 {
				t.Errorf("attempts = %d, want 4 (1 + maxRetries)", attempts)
			}

			if err == nil {
				t.Error("expected failure after the retries")
			}
		})
	}
}

// A bind the server refuses is refused again on every attempt: it is made once
// per URL, then the error comes back, so a wrong password cannot trip a
// password-policy lockout by being retried.
func TestClient_ExecuteWithRetry_RefusedBindIsNotRetried(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{RefuseBind: true})

	c, err := NewClient(&ClientConfig{
		URLs:        []string{srv.URL("ldap", "127.0.0.1")},
		BindDN:      "uid=zimbra,cn=admins,cn=zimbra",
		Password:    "wrong",
		MaxRetries:  3,
		RetryDelay:  time.Millisecond,
		DialTimeout: 2 * time.Second,
		BindTimeout: 2 * time.Second,
		OpTimeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	err = c.executeWithRetry(t.Context(), func(*ldap.Conn) error { return nil })
	if !ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
		t.Fatalf("err = %v, want invalid credentials", err)
	}

	if got := srv.Binds(); got != 1 {
		t.Errorf("binds = %d, want 1", got)
	}
}

// Reconfigure makes the next operation connect with the new URLs and
// credentials: the idle connection made with the old ones is dropped, and an
// unchanged configuration leaves the pool alone.
func TestClient_Reconfigure_AppliesNewIdentity(t *testing.T) {
	t.Parallel()

	oldSrv := ldapfake.New(t, ldapfake.Options{})
	newSrv := ldapfake.New(t, ldapfake.Options{})

	cfg := &ClientConfig{
		URLs:        []string{oldSrv.URL("ldap", "127.0.0.1")},
		BindDN:      "uid=zimbra,cn=admins,cn=zimbra",
		Password:    "old",
		DialTimeout: 2 * time.Second,
		BindTimeout: 2 * time.Second,
		OpTimeout:   2 * time.Second,
	}

	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	noop := func(*ldap.Conn) error { return nil }

	if err := c.executeWithRetry(t.Context(), noop); err != nil {
		t.Fatalf("first operation: %v", err)
	}

	same := *cfg

	changed, err := c.Reconfigure(&same)
	if err != nil || changed {
		t.Fatalf("Reconfigure(same) = %v, %v; want unchanged", changed, err)
	}

	if err := c.executeWithRetry(t.Context(), noop); err != nil {
		t.Fatalf("second operation: %v", err)
	}

	if got := oldSrv.Binds(); got != 1 {
		t.Fatalf("old server binds = %d, want 1 (the pooled connection is reused)", got)
	}

	next := *cfg
	next.URLs = []string{newSrv.URL("ldap", "127.0.0.1")}
	next.Password = "new"

	changed, err = c.Reconfigure(&next)
	if err != nil || !changed {
		t.Fatalf("Reconfigure(new) = %v, %v; want changed", changed, err)
	}

	if err := c.executeWithRetry(t.Context(), noop); err != nil {
		t.Fatalf("operation after Reconfigure: %v", err)
	}

	if got := newSrv.Binds(); got != 1 {
		t.Errorf("new server binds = %d, want 1", got)
	}

	if got := oldSrv.Binds(); got != 1 {
		t.Errorf("old server binds = %d, want still 1", got)
	}
}

// A connection that was in flight while the client was reconfigured is closed
// when it comes back, not pooled with the old credentials.
func TestClient_Reconfigure_DoesNotPoolStaleConnection(t *testing.T) {
	t.Parallel()

	c := retryClient(t)

	gen := c.generation()

	conn, err := c.getConnection(t.Context())
	if err != nil {
		t.Fatalf("getConnection: %v", err)
	}

	changed, err := c.Reconfigure(&ClientConfig{
		URLs: c.identity().urls, BindDN: "uid=other", Password: "rotated",
	})
	if err != nil || !changed {
		t.Fatalf("Reconfigure = %v, %v; want changed", changed, err)
	}

	c.returnConnection(conn, gen)

	if len(c.pool) != 0 {
		t.Errorf("pool holds %d connections, want 0", len(c.pool))
	}

	if !conn.IsClosing() {
		t.Error("the stale connection was not closed")
	}
}

func TestClient_Reconfigure_RejectsEmptyURL(t *testing.T) {
	t.Parallel()

	c := retryClient(t)

	if _, err := c.Reconfigure(&ClientConfig{BindDN: "x", Password: "y"}); err == nil {
		t.Error("expected an error for a configuration without URL")
	}
}
