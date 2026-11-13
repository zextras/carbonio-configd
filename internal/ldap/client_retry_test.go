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
