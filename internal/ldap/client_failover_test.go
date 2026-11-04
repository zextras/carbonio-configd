// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestClient_Connect_AggregatesPerURLFailure(t *testing.T) {
	t.Parallel()

	c := &Client{
		dial: func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
			return nil, errors.New("dial " + url + " refused")
		},
		urls:     []string{"ldap://a:389", "ldap://b:389", "ldaps://c:636"},
		bindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		password: "secret",
	}

	conn, err := c.connect(t.Context())
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
	t.Parallel()

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

	dial := func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
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
	}

	c := &Client{
		dial:     dial,
		urls:     []string{"ldap://a:389", "ldap://b:389"},
		bindDN:   "uid=zimbra,cn=admins,cn=zimbra",
		password: "secret",
	}

	conn, err := c.connect(t.Context())
	if err != nil {
		t.Fatalf("connect() did not fail over to the next URL: %v", err)
	}

	if conn == nil {
		t.Fatal("expected a connection from the URL that bound successfully")
	}

	_ = conn.Close()
}

// A cancelled context must abort a dial that never returns instead of waiting
// for the dial timeout.
func TestClient_Connect_ContextCancelAbortsDial(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	c := &Client{
		dial: func(string, ...ldap.DialOpt) (*ldap.Conn, error) {
			<-release

			return nil, errors.New("late")
		},
		urls: []string{"ldap://a:389"},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		_, err := c.connect(ctx)
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("connect = %v, want context.Canceled in the chain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connect did not return after the context was cancelled")
	}
}

// executeWithRetry is the only retry layer: with a permanently failing dial it
// makes exactly 1+maxRetries connection attempts.
func TestClient_ExecuteWithRetry_AttemptBudget(t *testing.T) {
	t.Parallel()

	var dials atomic.Int32

	c := &Client{
		dial: func(string, ...ldap.DialOpt) (*ldap.Conn, error) {
			dials.Add(1)

			return nil, errors.New("refused")
		},
		urls:          []string{"ldap://a:389"},
		maxRetries:    3,
		retryDelay:    time.Millisecond,
		maxRetryDelay: time.Millisecond,
	}

	err := c.executeWithRetry(t.Context(), func(*ldap.Conn) error { return nil })
	if err == nil {
		t.Fatal("expected failure")
	}

	if got := dials.Load(); got != 4 {
		t.Errorf("dials = %d, want 4 (1 + maxRetries)", got)
	}
}
