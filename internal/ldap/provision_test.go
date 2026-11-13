// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const passwordModifyOID = "1.3.6.1.4.1.4203.1.11.1"

// writeServer is a minimal LDAP server for the write operations: it accepts
// any bind and answers every add, delete and password modify with the result
// code its script gives for that request, recording what the client sent as
// "add dn|attr=v1,v2;...", "del dn" and "passwd dn|password".
type writeServer struct {
	url string

	mu       sync.Mutex
	requests []string
	script   func(n int, request string) uint8
}

func newWriteServer(t *testing.T, script func(n int, request string) uint8) *writeServer {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &writeServer{url: "ldap://" + lis.Addr().String(), script: script}

	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}

			go s.serve(conn)
		}
	}()

	return s
}

func (s *writeServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.requests...)
}

func (s *writeServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		packet, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}

		id := packet.Children[0].Value.(int64)
		op := packet.Children[1]

		var (
			request string
			reply   ber.Tag
		)

		switch op.Tag {
		case goldap.ApplicationBindRequest:
			s.reply(conn, id, goldap.ApplicationBindResponse, goldap.LDAPResultSuccess)

			continue
		case goldap.ApplicationAddRequest:
			request, reply = describeAdd(op), goldap.ApplicationAddResponse
		case goldap.ApplicationDelRequest:
			request, reply = "del "+op.Data.String(), goldap.ApplicationDelResponse
		case goldap.ApplicationExtendedRequest:
			request, reply = describePasswd(op), goldap.ApplicationExtendedResponse
		default:
			return
		}

		s.mu.Lock()
		s.requests = append(s.requests, request)
		code := s.script(len(s.requests), request)
		s.mu.Unlock()

		s.reply(conn, id, reply, code)
	}
}

func (s *writeServer) reply(conn net.Conn, id int64, app ber.Tag, code uint8) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "messageID"))

	resp := ber.Encode(ber.ClassApplication, ber.TypeConstructed, app, nil, "response")
	resp.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, uint64(code), "resultCode"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(resp)

	_, _ = conn.Write(msg.Bytes())
}

func describeAdd(op *ber.Packet) string {
	var attrs []string

	for _, a := range op.Children[1].Children {
		var values []string
		for _, v := range a.Children[1].Children {
			values = append(values, v.Data.String())
		}

		attrs = append(attrs, a.Children[0].Data.String()+"="+strings.Join(values, ","))
	}

	return "add " + op.Children[0].Data.String() + "|" + strings.Join(attrs, ";")
}

func describePasswd(op *ber.Packet) string {
	if op.Children[0].Data.String() != passwordModifyOID {
		return "extended " + op.Children[0].Data.String()
	}

	inner, _ := ber.DecodePacketErr(op.Children[1].Data.Bytes())

	var user, password string

	for _, c := range inner.Children {
		switch c.Tag {
		case 0:
			user = c.Data.String()
		case 2:
			password = c.Data.String()
		}
	}

	return "passwd " + user + "|" + password
}

func newClientFor(t *testing.T, url string) *Client {
	t.Helper()

	return newServerClient(t, &modifyServer{url: url})
}

func succeed(int, string) uint8 { return goldap.LDAPResultSuccess }

func refuse(code uint8) func(int, string) uint8 {
	return func(int, string) uint8 { return code }
}

func TestAddEntry_SendsTheAttributesInNameOrder(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, succeed)
	c := newClientFor(t, s.url)

	err := c.AddEntry("uid=u,ou=people,dc=x", map[string][]string{
		"uid":         {"u"},
		"objectClass": {"top", "person"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"add uid=u,ou=people,dc=x|objectClass=top,person;uid=u"}, s.seen())
}

func TestAddEntry_ATakenDNIsReportedAndSentOnce(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, refuse(goldap.LDAPResultEntryAlreadyExists))
	c := newClientFor(t, s.url)

	err := c.AddEntry("uid=u,dc=x", map[string][]string{"uid": {"u"}})

	require.Error(t, err)
	assert.True(t, IsEntryExists(err))
	assert.Len(t, s.seen(), 1, "an entry that exists is not retried")
}

func TestAddEntry_OtherRefusalsAreNotEntryExists(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, refuse(goldap.LDAPResultInsufficientAccessRights))
	c := newClientFor(t, s.url)

	err := c.AddEntry("uid=u,dc=x", map[string][]string{"uid": {"u"}})

	require.Error(t, err)
	assert.False(t, IsEntryExists(err))
}

func TestDeleteEntry_SendsTheDN(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, succeed)
	c := newClientFor(t, s.url)

	require.NoError(t, c.DeleteEntry("uid=u,dc=x"))
	assert.Equal(t, []string{"del uid=u,dc=x"}, s.seen())
}

func TestDeleteEntry_RefusalIsReturned(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, refuse(goldap.LDAPResultNoSuchObject))
	c := newClientFor(t, s.url)

	err := c.DeleteEntry("uid=gone,dc=x")

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject))
	assert.Len(t, s.seen(), 1)
}

func TestSetPassword_UsesThePasswordModifyOperationWithoutTheOldPassword(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, succeed)
	c := newClientFor(t, s.url)

	require.NoError(t, c.SetPassword("uid=u,dc=x", "s3cret"))
	assert.Equal(t, []string{"passwd uid=u,dc=x|s3cret"}, s.seen())
}

func TestSetPassword_APolicyRefusalIsFinalAndDoesNotLeakThePassword(t *testing.T) {
	t.Parallel()

	s := newWriteServer(t, refuse(goldap.LDAPResultConstraintViolation))
	c := newClientFor(t, s.url)

	err := c.SetPassword("uid=u,dc=x", "weak-password")

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultConstraintViolation))
	assert.NotContains(t, err.Error(), "weak-password")
	assert.Len(t, s.seen(), 1, "a constraint violation is not retried")
}

func TestIsEntryExists(t *testing.T) {
	t.Parallel()

	assert.True(t, IsEntryExists(goldap.NewError(goldap.LDAPResultEntryAlreadyExists, errors.New("exists"))))
	assert.False(t, IsEntryExists(goldap.NewError(goldap.LDAPResultNoSuchObject, errors.New("absent"))))
	assert.False(t, IsEntryExists(errors.New("plain")))
	assert.False(t, IsEntryExists(nil))
}

func TestIsLDAPErrorRetryable_AddRefusalsAreFinal(t *testing.T) {
	t.Parallel()

	assert.False(t, isLDAPErrorRetryable(goldap.NewError(goldap.LDAPResultEntryAlreadyExists, errors.New("exists"))))
	assert.False(t, isLDAPErrorRetryable(goldap.NewError(goldap.LDAPResultConstraintViolation, errors.New("policy"))))
}
