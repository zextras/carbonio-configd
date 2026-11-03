// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modifyServer is a minimal LDAP server: it accepts any bind and answers every
// modify request with the result code its script gives for that request. It
// records each modify as "dn|op attr=v1,v2;..." so a test sees exactly what
// the client put on the wire.
type modifyServer struct {
	url string

	mu       sync.Mutex
	requests []string
	script   func(n int, request string) uint8
}

func newModifyServer(t *testing.T, script func(n int, request string) uint8) *modifyServer {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &modifyServer{url: "ldap://" + lis.Addr().String(), script: script}

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

func (s *modifyServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.requests...)
}

func (s *modifyServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		packet, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}

		id := packet.Children[0].Value.(int64)
		op := packet.Children[1]

		switch op.Tag {
		case goldap.ApplicationBindRequest:
			s.reply(conn, id, goldap.ApplicationBindResponse, goldap.LDAPResultSuccess)
		case goldap.ApplicationModifyRequest:
			s.mu.Lock()
			request := describeModify(op)
			s.requests = append(s.requests, request)
			code := s.script(len(s.requests), request)
			s.mu.Unlock()

			s.reply(conn, id, goldap.ApplicationModifyResponse, code)
		default:
			return
		}
	}
}

func (s *modifyServer) reply(conn net.Conn, id int64, app ber.Tag, code uint8) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "messageID"))

	resp := ber.Encode(ber.ClassApplication, ber.TypeConstructed, app, nil, "response")
	resp.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, uint64(code), "resultCode"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(resp)

	_, _ = conn.Write(msg.Bytes())
}

// describeModify renders a ModifyRequest as "dn|add a=1,2;delete b=;replace c=x".
func describeModify(op *ber.Packet) string {
	dn := op.Children[0].Value.(string)

	var changes []string

	for _, change := range op.Children[1].Children {
		kind := [...]string{"add", "delete", "replace"}[change.Children[0].Value.(int64)]
		attr := change.Children[1].Children[0].Value.(string)

		var values []string
		for _, v := range change.Children[1].Children[1].Children {
			values = append(values, v.Value.(string))
		}

		changes = append(changes, kind+" "+attr+"="+strings.Join(values, ","))
	}

	return dn + "|" + strings.Join(changes, ";")
}

func newServerClient(t *testing.T, s *modifyServer) *Client {
	t.Helper()

	c, err := NewClient(&ClientConfig{URLs: []string{s.url}, BindDN: "uid=zimbra", Password: "secret", MaxRetries: 1})
	require.NoError(t, err)

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func TestModifyValues_SendsOneRequestDeletesThenAddsThenReplaces(t *testing.T) {
	t.Parallel()

	s := newModifyServer(t, func(int, string) uint8 { return goldap.LDAPResultSuccess })
	c := newServerClient(t, s)

	err := c.ModifyValues("cn=config,cn=zimbra",
		map[string][]string{"zimbraMtaRestriction": {"a", "b"}},
		map[string][]string{"zimbraGone": nil},
		map[string][]string{"zimbraMailMode": {"https"}})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"cn=config,cn=zimbra|delete zimbraGone=;add zimbraMtaRestriction=a,b;replace zimbraMailMode=https",
	}, s.seen())
}

func TestModifyValues_ReappliesOneValueAtATimeWhenTheEntryAlreadyConverged(t *testing.T) {
	t.Parallel()

	// The whole request is refused: value "a" already exists. The values are
	// then sent one by one and the existing one is not an error.
	s := newModifyServer(t, func(n int, request string) uint8 {
		switch {
		case n == 1:
			return goldap.LDAPResultAttributeOrValueExists
		case strings.Contains(request, "add attr=a"):
			return goldap.LDAPResultAttributeOrValueExists
		case strings.Contains(request, "delete other=x"):
			return goldap.LDAPResultNoSuchAttribute
		default:
			return goldap.LDAPResultSuccess
		}
	})
	c := newServerClient(t, s)

	err := c.ModifyValues("cn=config,cn=zimbra",
		map[string][]string{"attr": {"a", "b"}},
		map[string][]string{"other": {"x", "y"}},
		map[string][]string{"mode": {"1", "2"}})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"cn=config,cn=zimbra|delete other=x,y;add attr=a,b;replace mode=1,2",
		"cn=config,cn=zimbra|delete other=x",
		"cn=config,cn=zimbra|delete other=y",
		"cn=config,cn=zimbra|add attr=a",
		"cn=config,cn=zimbra|add attr=b",
		"cn=config,cn=zimbra|replace mode=1,2",
	}, s.seen(), "a replace stays whole: it cannot conflict")
}

func TestModifyValues_OtherRefusalsAreReturnedAndStopTheReapply(t *testing.T) {
	t.Parallel()

	s := newModifyServer(t, func(n int, _ string) uint8 {
		if n == 1 {
			return goldap.LDAPResultAttributeOrValueExists
		}

		return goldap.LDAPResultInsufficientAccessRights
	})
	c := newServerClient(t, s)

	err := c.ModifyValues("cn=config,cn=zimbra", map[string][]string{"a": {"1", "2", "3"}}, nil, nil)

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights), strconv.Quote(err.Error()))
	assert.Len(t, s.seen(), 2, "the first single-value request failed, the others were not sent")
}

func TestModifyValues_RefusalOfTheWholeRequestIsReturnedWhenNotConverged(t *testing.T) {
	t.Parallel()

	s := newModifyServer(t, func(int, string) uint8 { return goldap.LDAPResultInsufficientAccessRights })
	c := newServerClient(t, s)

	err := c.ModifyValues("cn=config,cn=zimbra", nil, nil, map[string][]string{"a": {"1"}})

	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights))
	assert.Len(t, s.seen(), 1)
}
