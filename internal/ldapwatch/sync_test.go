// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldapwatch"
)

// syncMsg is one message of a scripted sync response.
type syncMsg struct {
	entry    *goldap.Entry
	controls []goldap.Control
}

// scriptedResponse is a goldap.Response that plays msgs and then ends with err,
// or, when hold is set, blocks until its context is done like an open
// persistent search.
type scriptedResponse struct {
	ctx  context.Context
	msgs []syncMsg
	err  error
	hold bool
	cur  syncMsg
}

func (r *scriptedResponse) Entry() *goldap.Entry       { return r.cur.entry }
func (r *scriptedResponse) Referral() string           { return "" }
func (r *scriptedResponse) Controls() []goldap.Control { return r.cur.controls }
func (r *scriptedResponse) Err() error                 { return r.err }

func (r *scriptedResponse) Next() bool {
	if len(r.msgs) > 0 {
		r.cur, r.msgs = r.msgs[0], r.msgs[1:]

		return true
	}

	if r.hold {
		<-r.ctx.Done()
		r.err = nil
	}

	return false
}

// fakeSyncConn is a SyncConn over a scripted root DSE and sync response.
type fakeSyncConn struct {
	supported  []string // supportedControl values
	probeErr   error
	respMsgs   []syncMsg
	respErr    error
	respHold   bool
	closed     chan struct{}
	closeOnce  sync.Once
	searchReqs []*goldap.SearchRequest
	syncReq    *goldap.SearchRequest
	mode       goldap.ControlSyncRequestMode
	cookie     []byte
	timeouts   []time.Duration
}

func newFakeSyncConn() *fakeSyncConn { return &fakeSyncConn{closed: make(chan struct{})} }

func (c *fakeSyncConn) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	c.searchReqs = append(c.searchReqs, req)

	if c.probeErr != nil {
		return nil, c.probeErr
	}

	return &goldap.SearchResult{Entries: []*goldap.Entry{
		goldap.NewEntry("", map[string][]string{"supportedControl": c.supported}),
	}}, nil
}

func (c *fakeSyncConn) Syncrepl(ctx context.Context, req *goldap.SearchRequest, _ int,
	mode goldap.ControlSyncRequestMode, cookie []byte, _ bool,
) goldap.Response {
	c.syncReq, c.mode, c.cookie = req, mode, cookie

	return &scriptedResponse{ctx: ctx, msgs: c.respMsgs, err: c.respErr, hold: c.respHold}
}

func (c *fakeSyncConn) SetTimeout(d time.Duration) { c.timeouts = append(c.timeouts, d) }

func (c *fakeSyncConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })

	return nil
}

func entryMsg(dn string) syncMsg {
	return syncMsg{entry: goldap.NewEntry(dn, nil), controls: []goldap.Control{
		&goldap.ControlSyncState{State: goldap.SyncStateModify},
	}}
}

func refreshDoneMsg() syncMsg {
	return syncMsg{controls: []goldap.Control{&goldap.ControlSyncInfo{
		Value:          goldap.SyncInfoRefreshPresent,
		RefreshPresent: &goldap.ControlSyncInfoRefreshPresent{RefreshDone: true},
	}}}
}

func subscribe(t *testing.T, conn *fakeSyncConn) (lives, changes int, err error) {
	t.Helper()

	sub := ldapwatch.NewSyncSubscriber(func(context.Context) (ldapwatch.SyncConn, error) { return conn, nil })

	err = sub.Subscribe(context.Background(), func() { lives++ }, func() { changes++ })

	return lives, changes, err
}

const syncOID = "1.3.6.1.4.1.4203.1.9.1.1"

// The initial content is the baseline: entries before the refresh-done message
// are not changes, the stream is live once it ends, and every entry after it is
// a change.
func TestSyncSubscriber_RefreshThenChanges(t *testing.T) {
	conn := newFakeSyncConn()
	conn.supported = []string{"1.2.3", syncOID}
	conn.respMsgs = []syncMsg{
		entryMsg("cn=config,cn=zimbra"), entryMsg("dc=test.example"), refreshDoneMsg(),
		entryMsg("cn=config,cn=zimbra"),
		{controls: []goldap.Control{&goldap.ControlSyncInfo{ // newCookie is not a change
			Value: goldap.SyncInfoNewcookie, NewCookie: &goldap.ControlSyncInfoNewCookie{Cookie: []byte("c")},
		}}},
		{controls: []goldap.Control{&goldap.ControlSyncInfo{ // entries that left the result set
			Value: goldap.SyncInfoSyncIdSet, SyncIdSet: &goldap.ControlSyncInfoSyncIdSet{},
		}}},
	}

	lives, changes, err := subscribe(t, conn)

	require.Error(t, err, "a search the server ends is a lost stream")
	assert.NotErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	assert.Equal(t, 1, lives)
	assert.Equal(t, 2, changes, "one modified entry and one delete notice after the refresh")

	assert.Equal(t, goldap.SyncRequestModeRefreshAndPersist, conn.mode)
	assert.Empty(t, conn.cookie)
	assert.Equal(t, "", conn.syncReq.BaseDN)
	assert.Equal(t, goldap.ScopeWholeSubtree, conn.syncReq.Scope)
	assert.Equal(t, []string{"1.1"}, conn.syncReq.Attributes, "DNs only")
	assert.Contains(t, conn.syncReq.Filter, "zimbraGlobalConfig")
	assert.Contains(t, conn.syncReq.Filter, "zimbraServer")
	assert.Contains(t, conn.syncReq.Filter, "zimbraDomain")
	assert.Equal(t, []string{"supportedControl"}, conn.searchReqs[0].Attributes)

	require.Len(t, conn.timeouts, 2)
	assert.Positive(t, conn.timeouts[0], "the root DSE read is bounded")
	assert.Zero(t, conn.timeouts[1], "the persistent search has no request timeout")

	select {
	case <-conn.closed:
	default:
		t.Error("the dedicated connection must be closed when Subscribe returns")
	}
}

func TestSyncSubscriber_RefreshDeleteDoneAlsoEndsTheRefresh(t *testing.T) {
	conn := newFakeSyncConn()
	conn.supported = []string{syncOID}
	conn.respMsgs = []syncMsg{
		entryMsg("dc=a"),
		{controls: []goldap.Control{&goldap.ControlSyncInfo{
			Value:         goldap.SyncInfoRefreshDelete,
			RefreshDelete: &goldap.ControlSyncInfoRefreshDelete{RefreshDone: true},
		}}},
	}

	lives, changes, _ := subscribe(t, conn)

	assert.Equal(t, 1, lives)
	assert.Zero(t, changes)
}

func TestSyncSubscriber_UnsupportedWhenTheControlIsNotAdvertised(t *testing.T) {
	conn := newFakeSyncConn()
	conn.supported = []string{"1.2.840.113556.1.4.319"} // paged results only

	lives, _, err := subscribe(t, conn)

	require.ErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	assert.Zero(t, lives)
	assert.Nil(t, conn.syncReq, "no sync search is sent to a server without the control")
}

// A server that lists the control but does not apply it to this database, or an
// OpenLDAP without syncprov that ignores it, answers the search like a plain one.
func TestSyncSubscriber_UnsupportedWhenTheSearchEndsWithoutARefresh(t *testing.T) {
	conn := newFakeSyncConn()
	conn.supported = []string{syncOID}
	conn.respMsgs = []syncMsg{{entry: goldap.NewEntry("dc=a", nil)}}

	lives, changes, err := subscribe(t, conn)

	require.ErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	assert.Zero(t, lives)
	assert.Zero(t, changes)
}

func TestSyncSubscriber_ServerRejectionBeforeTheRefreshIsUnsupported(t *testing.T) {
	for _, code := range []uint16{
		goldap.LDAPResultUnavailableCriticalExtension, goldap.LDAPResultProtocolError, goldap.LDAPResultUnwillingToPerform,
	} {
		conn := newFakeSyncConn()
		conn.supported = []string{syncOID}
		conn.respErr = goldap.NewError(code, errors.New("no"))

		_, _, err := subscribe(t, conn)

		require.ErrorIs(t, err, ldapwatch.ErrSyncUnsupported, "code %d", code)
	}
}

// Anything else is a failed stream the watcher retries, not a verdict on the
// directory.
func TestSyncSubscriber_OtherErrorsAreNotUnsupported(t *testing.T) {
	t.Run("busy before the refresh", func(t *testing.T) {
		conn := newFakeSyncConn()
		conn.supported = []string{syncOID}
		conn.respErr = goldap.NewError(goldap.LDAPResultBusy, errors.New("busy"))

		_, _, err := subscribe(t, conn)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	})

	t.Run("rejection after the refresh", func(t *testing.T) {
		conn := newFakeSyncConn()
		conn.supported = []string{syncOID}
		conn.respMsgs = []syncMsg{refreshDoneMsg()}
		conn.respErr = goldap.NewError(goldap.LDAPResultUnwillingToPerform, errors.New("no"))

		lives, _, err := subscribe(t, conn)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ldapwatch.ErrSyncUnsupported, "it worked before: a transient failure")
		assert.Equal(t, 1, lives)
	})

	t.Run("root DSE read fails", func(t *testing.T) {
		conn := newFakeSyncConn()
		conn.probeErr = errors.New("connection reset")

		_, _, err := subscribe(t, conn)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	})

	t.Run("dial fails", func(t *testing.T) {
		sub := ldapwatch.NewSyncSubscriber(func(context.Context) (ldapwatch.SyncConn, error) {
			return nil, errors.New("connection refused")
		})

		err := sub.Subscribe(context.Background(), func() {}, func() {})

		require.Error(t, err)
		assert.NotErrorIs(t, err, ldapwatch.ErrSyncUnsupported)
	})
}

// Cancelling ends an open stream with the context error and closes the
// connection, which is what interrupts a blocked read.
func TestSyncSubscriber_CancelEndsAnOpenStream(t *testing.T) {
	conn := newFakeSyncConn()
	conn.supported = []string{syncOID}
	conn.respMsgs = []syncMsg{refreshDoneMsg()}
	conn.respHold = true

	sub := ldapwatch.NewSyncSubscriber(func(context.Context) (ldapwatch.SyncConn, error) { return conn, nil })

	ctx, cancel := context.WithCancel(context.Background())
	live := make(chan struct{})
	result := make(chan error, 1)

	go func() { result <- sub.Subscribe(ctx, func() { close(live) }, func() {}) }()

	<-live
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not return after cancel")
	}

	<-conn.closed
}
