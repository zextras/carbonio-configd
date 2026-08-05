// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// ldapBindSuccessPacket is a hand-encoded LDAPMessage carrying a successful
// (resultCode 0) BindResponse for messageID 1 — always the first message ID
// issued by a freshly started *ldap.Conn. It lets a fake net.Conn answer a
// real Conn.Bind() call without a live LDAP server.
var ldapBindSuccessPacket = []byte{
	0x30, 0x0c, // LDAPMessage SEQUENCE, len 12
	0x02, 0x01, 0x01, // messageID INTEGER 1
	0x61, 0x07, // [APPLICATION 1] BindResponse, len 7
	0x02, 0x01, 0x00, // resultCode INTEGER 0 (success)
	0x04, 0x00, // matchedDN OCTET STRING ""
	0x04, 0x00, // diagnosticMessage OCTET STRING ""
}

// withDefaultDial stubs the DialFirstReachable dialer for the duration of a
// test, restoring the production dialer on cleanup.
func withDefaultDial(t *testing.T, fn dialFn) {
	t.Helper()

	prev := defaultDial
	t.Cleanup(func() { defaultDial = prev })

	defaultDial = fn
}

func TestClient_Connect_AggregatesPerURLFailure(t *testing.T) {
	withDefaultDial(t, func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
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

// TestClient_Connect_FailsOverBindFailureToNextURL verifies that a URL which
// dials successfully but fails bind (e.g. a broken cert or a server in
// maintenance mode) does not abort connect(): the next URL is tried and, if
// it binds successfully, connect() returns its connection.
func TestClient_Connect_FailsOverBindFailureToNextURL(t *testing.T) {
	// url1: the "server" end of the pipe closes immediately, so the real
	// *ldap.Conn's Bind() write fails and connect() must fail over.
	badClient, badServer := net.Pipe()
	_ = badServer.Close()

	// url2: the "server" end answers with a valid resultCode-0 BindResponse,
	// so Bind() succeeds against a real *ldap.Conn.
	goodClient, goodServer := net.Pipe()

	go func() {
		buf := make([]byte, 4096)
		_, _ = goodServer.Read(buf)
		_, _ = goodServer.Write(ldapBindSuccessPacket)
	}()

	withDefaultDial(t, func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		switch url {
		case "ldap://a:389":
			conn := ldap.NewConn(badClient, false)
			conn.Start()

			return conn, nil
		case "ldap://b:389":
			conn := ldap.NewConn(goodClient, false)
			conn.Start()

			return conn, nil
		default:
			return nil, errors.New("unexpected url " + url)
		}
	})

	c := &Client{
		urls:     []string{"ldap://a:389", "ldap://b:389"},
		bindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		password: "secret",
	}

	conn, err := c.connect()
	if err != nil {
		t.Fatalf("connect() did not fail over to the next URL: %v", err)
	}

	if conn == nil {
		t.Fatal("expected a connection from the URL that bound successfully")
	}

	_ = conn.Close()
}
