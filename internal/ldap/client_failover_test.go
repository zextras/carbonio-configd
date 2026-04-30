// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func withClientDial(t *testing.T, fn dialFn) {
	t.Helper()

	prev := clientDial
	t.Cleanup(func() { clientDial = prev })

	clientDial = fn
}

func TestClient_Connect_AggregatesPerURLFailure(t *testing.T) {
	withClientDial(t, func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		return nil, errors.New("dial " + url + " refused")
	})

	c := &Client{
		urls:     []string{"ldap://a:389", "ldap://b:389", "ldaps://c:636"},
		bindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		password: "secret",
	}

	conn, err := c.connect()
	if err == nil {
		t.Fatal("expected aggregate error")
	}

	if conn != nil {
		t.Errorf("expected nil conn, got %v", conn)
	}

	var agg *AggregateDialError
	if !errors.As(err, &agg) {
		t.Fatalf("expected *AggregateDialError in chain, got %T: %v", err, err)
	}

	if len(agg.Errors) != len(c.urls) {
		t.Errorf("expected %d wrapped errors, got %d", len(c.urls), len(agg.Errors))
	}

	for _, u := range c.urls {
		if !strings.Contains(agg.Error(), u) {
			t.Errorf("aggregate %q missing URL %q", agg.Error(), u)
		}
	}
}

func TestClient_DialAndBind_UnsupportedScheme(t *testing.T) {
	c := &Client{}

	_, err := c.dialAndBind("http://nope:80")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}

	if !strings.Contains(err.Error(), "unsupported LDAP URL scheme") {
		t.Errorf("error = %q, want substring %q", err.Error(), "unsupported LDAP URL scheme")
	}
}

func TestClient_DialAndBind_DialError(t *testing.T) {
	withClientDial(t, func(_ string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		return nil, errors.New("connection refused")
	})

	c := &Client{}

	_, err := c.dialAndBind("ldap://srv:389")
	if err == nil {
		t.Fatal("expected dial error")
	}

	if !strings.Contains(err.Error(), "dial:") {
		t.Errorf("error = %q, want substring %q", err.Error(), "dial:")
	}
}

func TestClient_DialAndBind_LDAPSPath(t *testing.T) {
	var got string

	withClientDial(t, func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		got = url
		return nil, errors.New("forced")
	})

	c := &Client{}
	_, _ = c.dialAndBind("ldaps://srv:636")

	if got != "ldaps://srv:636" {
		t.Errorf("ldaps path did not reach dialer with expected URL: got %q", got)
	}
}
