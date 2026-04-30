// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// ParseURLs splits a localconfig-style LDAP URL string on whitespace and
// returns the non-empty entries in their original order.
//
// Carbonio's localconfig stores HA LDAP URLs as a single space-separated
// string, e.g.:
//
//	ldap_url = "ldap://srv3.example.com:389 ldap://srv1.example.com:389"
//
// Legacy Perl tooling (control.pl) and the existing Go ldap_launcher both
// already split this form. Centralising the parser here lets every native
// LDAP construction site handle the multi-URL convention uniformly and
// prevents reintroduction of the CO-3565 regression where the raw string
// was passed straight into ldap.DialURL.
//
// An empty or whitespace-only input returns nil; callers MUST check for the
// empty result and surface a typed configuration error rather than dialing.
func ParseURLs(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}

	return fields
}

// urlDialError is the per-URL error wrapper produced by DialFirstReachable
// while iterating the URL list. It preserves both the URL that failed and
// the underlying go-ldap / network error so that callers can surface a
// per-URL diagnostic and operators can pinpoint the misconfigured entry.
type urlDialError struct {
	URL string
	Err error
}

func (e *urlDialError) Error() string {
	return fmt.Sprintf("dial %s: %v", e.URL, e.Err)
}

func (e *urlDialError) Unwrap() error { return e.Err }

// AggregateDialError is returned by DialFirstReachable when every URL in the
// supplied list fails to connect. It exposes the per-URL failures via Errors
// for structured logging and supports errors.Is/errors.As against the last
// underlying error so callers preserve the existing classification logic
// (e.g. errs.ErrLDAPUnhealthyConnection, ldap.LDAPResultServerDown).
type AggregateDialError struct {
	Errors []error
}

// Error returns a human-readable summary listing every attempted URL with
// its failure reason, matching the ordering of the input URL slice.
func (a *AggregateDialError) Error() string {
	if len(a.Errors) == 0 {
		return "ldap dial: no URLs attempted"
	}

	parts := make([]string, 0, len(a.Errors))
	for _, e := range a.Errors {
		parts = append(parts, e.Error())
	}

	return "ldap dial: all URLs failed: " + strings.Join(parts, "; ")
}

// Unwrap returns the last underlying error so errors.Is and errors.As keep
// working with the existing callers' error-classification helpers.
func (a *AggregateDialError) Unwrap() error {
	if len(a.Errors) == 0 {
		return nil
	}

	return a.Errors[len(a.Errors)-1]
}

// Is reports whether any wrapped error matches target. This makes
// AggregateDialError transparent to errors.Is checks done by callers
// against the original underlying causes (e.g. context.DeadlineExceeded).
func (a *AggregateDialError) Is(target error) bool {
	for _, e := range a.Errors {
		if errors.Is(e, target) {
			return true
		}
	}

	return false
}

// dialFn is the dialer signature used by DialFirstReachable. It exists so
// unit tests can stub out the real go-ldap dial call without standing up a
// network listener.
type dialFn func(url string, opts ...ldap.DialOpt) (*ldap.Conn, error)

// defaultDial is the production dialer; tests override dialURL via package
// vars in client.go to inject deterministic behaviour.
var defaultDial dialFn = ldap.DialURL

// DialFirstReachable iterates urls in order and returns the first
// successfully dialed connection together with the URL that produced it.
// When every URL fails it returns an AggregateDialError listing each
// per-URL failure.
//
// Failover is connect-time only: the selected URL is bound for the lifetime
// of the returned connection. Callers performing reconnects should call
// DialFirstReachable again so the full list is re-tried from the top — this
// matches legacy Perl Net::LDAP->new($master_ref, ...) semantics.
//
// The ctx is honoured between attempts: if it is cancelled, the function
// returns ctx.Err() wrapped in AggregateDialError together with the
// per-URL errors gathered so far.
//
//nolint:ireturn // returning the concrete *ldap.Conn from go-ldap is intended
func DialFirstReachable(
	ctx context.Context,
	urls []string,
	opts ...ldap.DialOpt,
) (*ldap.Conn, string, error) {
	if len(urls) == 0 {
		return nil, "", &AggregateDialError{Errors: []error{
			errors.New("no LDAP URLs configured"),
		}}
	}

	agg := &AggregateDialError{Errors: make([]error, 0, len(urls))}

	for _, u := range urls {
		if err := ctx.Err(); err != nil {
			agg.Errors = append(agg.Errors, err)

			return nil, "", agg
		}

		conn, err := defaultDial(u, opts...)
		if err == nil {
			return conn, u, nil
		}

		agg.Errors = append(agg.Errors, &urlDialError{URL: u, Err: err})
	}

	return nil, "", agg
}
