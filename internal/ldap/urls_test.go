// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestParseURLs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single URL", "ldap://srv1:389", []string{"ldap://srv1:389"}},
		{
			"multi URL space separated",
			"ldap://srv3:389 ldap://srv1:389",
			[]string{"ldap://srv3:389", "ldap://srv1:389"},
		},
		{
			"multi URL multi-space",
			"ldap://srv3:389    ldap://srv1:389",
			[]string{"ldap://srv3:389", "ldap://srv1:389"},
		},
		{
			"surrounding whitespace preserved order",
			"  ldap://srv1:389\tldap://srv2:389  ",
			[]string{"ldap://srv1:389", "ldap://srv2:389"},
		},
		{
			"mixed schemes",
			"ldap://srv1:389 ldaps://srv2:636",
			[]string{"ldap://srv1:389", "ldaps://srv2:636"},
		},
		{"empty", "", nil},
		{"whitespace only", "   \t\n  ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseURLs(tt.in)
			if !slicesEqual(got, tt.want) {
				t.Errorf("ParseURLs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDialFirstReachable_NoURLs(t *testing.T) {
	conn, picked, err := DialFirstReachable(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty URL list, got nil")
	}

	if conn != nil {
		t.Errorf("expected nil conn, got %v", conn)
	}

	if picked != "" {
		t.Errorf("expected empty picked URL, got %q", picked)
	}

	var agg *AggregateDialError
	if !errors.As(err, &agg) {
		t.Fatalf("expected *AggregateDialError, got %T: %v", err, err)
	}

	if len(agg.Errors) == 0 {
		t.Error("expected at least one wrapped error")
	}
}

func TestDialFirstReachable_AllFail(t *testing.T) {
	prev := defaultDial
	t.Cleanup(func() { defaultDial = prev })

	defaultDial = func(url string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		return nil, errors.New("dial " + url + " refused")
	}

	urls := []string{"ldap://a:389", "ldap://b:389", "ldap://c:389"}

	conn, picked, err := DialFirstReachable(context.Background(), urls)
	if err == nil {
		t.Fatal("expected aggregate error, got nil")
	}

	if conn != nil {
		t.Errorf("expected nil conn, got %v", conn)
	}

	if picked != "" {
		t.Errorf("expected empty picked URL, got %q", picked)
	}

	var agg *AggregateDialError
	if !errors.As(err, &agg) {
		t.Fatalf("expected *AggregateDialError, got %T: %v", err, err)
	}

	if len(agg.Errors) != len(urls) {
		t.Errorf("expected %d wrapped errors, got %d (%v)", len(urls), len(agg.Errors), agg.Errors)
	}

	msg := agg.Error()
	for _, u := range urls {
		if !strings.Contains(msg, u) {
			t.Errorf("aggregate error missing URL %q: %s", u, msg)
		}
	}
}

func TestDialFirstReachable_CtxCancelled(t *testing.T) {
	prev := defaultDial
	t.Cleanup(func() { defaultDial = prev })

	defaultDial = func(_ string, _ ...ldap.DialOpt) (*ldap.Conn, error) {
		t.Fatal("dialer should not be called when ctx is already cancelled")
		return nil, errors.New("unreachable")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, picked, err := DialFirstReachable(ctx, []string{"ldap://a:389"})
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}

	if conn != nil {
		t.Errorf("expected nil conn, got %v", conn)
	}

	if picked != "" {
		t.Errorf("expected empty picked URL, got %q", picked)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected ctx.Canceled in error chain, got %v", err)
	}
}

func TestAggregateDialError_IsUnwrap(t *testing.T) {
	sentinel := errors.New("sentinel")
	agg := &AggregateDialError{
		Errors: []error{
			&urlDialError{URL: "ldap://a", Err: errors.New("first")},
			&urlDialError{URL: "ldap://b", Err: sentinel},
		},
	}

	if !errors.Is(agg, sentinel) {
		t.Error("errors.Is should match sentinel through Is()")
	}

	if u := errors.Unwrap(agg); u == nil || !strings.Contains(u.Error(), "ldap://b") {
		t.Errorf("Unwrap should return last per-URL error, got %v", u)
	}
}

func TestAggregateDialError_EmptyErrors(t *testing.T) {
	agg := &AggregateDialError{}
	if got := agg.Error(); got != "ldap dial: no URLs attempted" {
		t.Errorf("Error() on empty agg = %q, want %q", got, "ldap dial: no URLs attempted")
	}

	if got := agg.Unwrap(); got != nil {
		t.Errorf("Unwrap() on empty agg = %v, want nil", got)
	}

	if errors.Is(agg, errors.New("anything")) {
		t.Error("Is() on empty agg should not match arbitrary errors")
	}
}

func TestUrlDialError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("boom")
	e := &urlDialError{URL: "ldap://h:389", Err: cause}

	if got := e.Error(); got != "dial ldap://h:389: boom" {
		t.Errorf("Error() = %q", got)
	}

	if !errors.Is(e, cause) {
		t.Error("urlDialError should unwrap to its cause")
	}
}
