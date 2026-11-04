// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// ErrSyncUnsupported is returned by a Subscriber when the directory cannot
// stream changes: the server does not advertise the content-sync control, or it
// rejected or ignored the sync search. The watcher falls back to polling.
var ErrSyncUnsupported = errors.New("ldap content sync (RFC 4533) not available")

const (
	// syncControlOID is the Sync Request control (RFC 4533) a server lists in
	// the root DSE supportedControl attribute when the syncprov overlay is
	// loaded.
	syncControlOID = goldap.ControlTypeSyncRequest

	// syncFilter selects the entries a cycle reads: the global config, the
	// server entries and the domains.
	syncFilter = "(|(objectClass=zimbraGlobalConfig)(objectClass=zimbraServer)(objectClass=zimbraDomain))"

	// syncBuffer is the depth of the go-ldap response channel; it only absorbs
	// bursts, the reader never does anything slow.
	syncBuffer = 16

	// probeTimeout bounds the root DSE read. The streaming search itself has no
	// timeout: it is idle for as long as nothing changes.
	probeTimeout = 30 * time.Second
)

// SyncConn is the part of an LDAP connection the subscriber uses.
// *ldap.Conn satisfies it.
type SyncConn interface {
	Search(req *goldap.SearchRequest) (*goldap.SearchResult, error)
	Syncrepl(ctx context.Context, req *goldap.SearchRequest, bufferSize int,
		mode goldap.ControlSyncRequestMode, cookie []byte, reloadHint bool) goldap.Response
	SetTimeout(d time.Duration)
	Close() error
}

// SyncSubscriber is a Subscriber over an RFC 4533 refreshAndPersist search: the
// server keeps the search open and sends every change to a matching entry as it
// is committed. It needs the syncprov overlay on the provider.
type SyncSubscriber struct {
	dial func(ctx context.Context) (SyncConn, error)
}

// NewSyncSubscriber returns a SyncSubscriber that opens its dedicated,
// unpooled, bound connection with dial. Subscribe closes it before returning.
func NewSyncSubscriber(dial func(ctx context.Context) (SyncConn, error)) *SyncSubscriber {
	return &SyncSubscriber{dial: dial}
}

// Subscribe implements Subscriber. It connects, checks that the server
// advertises the content-sync control, opens one persistent search over the
// whole directory restricted to the watched object classes and, once the initial
// refresh is over (the initial content is the baseline, not a change), calls
// live and then changed for every later change.
//
// It returns ErrSyncUnsupported (wrapped) when the server lacks syncprov, which
// it detects three ways: the control is absent from supportedControl, the server
// answers the critical control with unavailableCriticalExtension, protocolError
// or unwillingToPerform, or the search ends without ever completing a refresh.
// Every other return is a lost or failed stream.
func (s *SyncSubscriber) Subscribe(ctx context.Context, live, changed func()) error {
	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("content sync connect: %w", err)
	}

	defer func() { _ = conn.Close() }()

	// The go-ldap calls below do not all take a context; closing the connection
	// is what interrupts them.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	conn.SetTimeout(probeTimeout)

	if err := probeContentSync(conn); err != nil {
		return interrupted(ctx, err)
	}

	conn.SetTimeout(0)

	return interrupted(ctx, streamChanges(ctx, conn, live, changed))
}

// interrupted prefers the context error to the connection error that closing
// the connection caused.
func interrupted(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil && err != nil {
		return cerr //nolint:wrapcheck // callers want the bare context error
	}

	return err
}

// probeContentSync reads the root DSE and reports ErrSyncUnsupported when the
// server does not list the content-sync control.
func probeContentSync(conn SyncConn) error {
	res, err := conn.Search(goldap.NewSearchRequest("", goldap.ScopeBaseObject, goldap.NeverDerefAliases,
		0, 0, false, "(objectClass=*)", []string{"supportedControl"}, nil))
	if err != nil {
		return fmt.Errorf("read root DSE: %w", err)
	}

	if res == nil || len(res.Entries) == 0 ||
		!slices.Contains(res.Entries[0].GetAttributeValues("supportedControl"), syncControlOID) {
		return fmt.Errorf("%w: control %s is not in the root DSE supportedControl (syncprov overlay not loaded)",
			ErrSyncUnsupported, syncControlOID)
	}

	return nil
}

// streamChanges runs the persistent search until it ends.
func streamChanges(ctx context.Context, conn SyncConn, live, changed func()) error {
	// No attributes: the entry content is not needed, the fingerprint source
	// reads what a change is about. "1.1" asks for the DN only.
	req := goldap.NewSearchRequest("", goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		0, 0, false, syncFilter, []string{"1.1"}, nil)

	resp := conn.Syncrepl(ctx, req, syncBuffer, goldap.SyncRequestModeRefreshAndPersist, nil, false)

	refreshed := false

	for resp.Next() {
		switch {
		case !refreshed:
			// Initial content: entries with a Sync State control, then a Sync
			// Info message saying the refresh is done.
			if refreshDone(resp.Controls()) {
				refreshed = true

				live()
			}
		case resp.Entry() != nil || deletesReported(resp.Controls()):
			changed()
		}
	}

	return streamEnd(resp.Err(), refreshed)
}

// streamEnd classifies how the search ended.
func streamEnd(err error, refreshed bool) error {
	switch {
	case err != nil && !refreshed && goldap.IsErrorAnyOf(err,
		goldap.LDAPResultUnavailableCriticalExtension, goldap.LDAPResultProtocolError, goldap.LDAPResultUnwillingToPerform):
		return fmt.Errorf("%w: server rejected the sync search: %w", ErrSyncUnsupported, err)
	case err != nil:
		return fmt.Errorf("content sync search: %w", err)
	case !refreshed:
		// A server without syncprov ignores the control and just answers the
		// search: the entries come without Sync State controls and the search
		// completes, instead of staying open.
		return fmt.Errorf("%w: the sync search completed without a refresh phase", ErrSyncUnsupported)
	default:
		return errors.New("content sync search closed by the server")
	}
}

// refreshDone reports whether the controls of a message contain a Sync Info
// that ends the refresh phase.
func refreshDone(controls []goldap.Control) bool {
	for _, c := range controls {
		info, ok := c.(*goldap.ControlSyncInfo)
		if !ok {
			continue
		}

		if (info.RefreshDelete != nil && info.RefreshDelete.RefreshDone) ||
			(info.RefreshPresent != nil && info.RefreshPresent.RefreshDone) {
			return true
		}
	}

	return false
}

// deletesReported reports whether the controls carry a Sync Info listing
// entries that left the result set.
func deletesReported(controls []goldap.Control) bool {
	for _, c := range controls {
		if info, ok := c.(*goldap.ControlSyncInfo); ok && info.SyncIdSet != nil {
			return true
		}
	}

	return false
}
