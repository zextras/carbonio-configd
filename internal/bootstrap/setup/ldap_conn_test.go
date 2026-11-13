// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"maps"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lwStartTLSOID = "1.3.6.1.4.1.1466.20037"
	lwPasswdOID   = "1.3.6.1.4.1.4203.1.11.1"
)

// lwEntry is an entry the fake server returns from a search.
type lwEntry struct {
	DN    string
	Attrs map[string][]string
}

// lwOpts selects the transport of a lwServer.
type lwOpts struct {
	// startTLS makes the server accept the StartTLS extended operation.
	startTLS bool
	// implicitTLS wraps every connection in TLS (ldaps://).
	implicitTLS bool
	// unix listens on a unix socket (ldapi://) instead of TCP.
	unix bool
}

// lwServer is a small in-process LDAP server speaking just enough of the
// protocol for the wrappers under test: bind, add, modify, search, the
// password modify and StartTLS extended operations. It records what the
// client put on the wire, and answers with the result codes the test sets.
type lwServer struct {
	// URL is the URL of the listener.
	URL string
	// Addr is host:port of a TCP listener.
	Addr string
	// CertPEM is the (self-signed) certificate the server presents.
	CertPEM []byte

	// Hooks, set before the first request; nil answers success.
	BindCode   func(dn, password string) uint8
	AddCode    func(dn string) uint8
	DelCode    func(dn string) uint8
	ModifyCode func(dn string) uint8
	PasswdCode uint8
	Search     func(base string, scope int64, filter string) ([]lwEntry, uint8)

	mu      sync.Mutex
	log     []string
	tlsConn int
	tlsCert tls.Certificate
}

// lwCert returns a self-signed certificate valid for 127.0.0.1 and localhost,
// usable as its own CA, and its PEM.
func lwCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lw-ldap"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// lwStart starts a server and stops it with the test.
func lwStart(t *testing.T, o lwOpts) *lwServer {
	t.Helper()

	s := &lwServer{}
	s.tlsCert, s.CertPEM = lwCert(t)

	var (
		lis net.Listener
		err error
	)

	switch {
	case o.unix:
		dir, derr := os.MkdirTemp("", "lw")
		require.NoError(t, derr)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		sock := filepath.Join(dir, "s")
		lis, err = net.Listen("unix", sock)
		s.URL = "ldapi://" + strings.ReplaceAll(sock, "/", "%2F") + "/"
	default:
		lis, err = net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			s.Addr = lis.Addr().String()
			s.URL = "ldap://" + s.Addr
		}
	}

	require.NoError(t, err)

	if o.implicitTLS {
		s.URL = "ldaps://" + s.Addr
		lis = tls.NewListener(lis, &tls.Config{Certificates: []tls.Certificate{s.tlsCert}, MinVersion: tls.VersionTLS12})
	}

	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		for {
			conn, aerr := lis.Accept()
			if aerr != nil {
				return
			}

			go s.serve(conn, o.startTLS)
		}
	}()

	return s
}

// Port is the TCP port of the server.
func (s *lwServer) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr)
	n, _ := strconv.Atoi(p)

	return n
}

// Seen is every request, in order, as "op detail".
func (s *lwServer) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.log)
}

// TLSConns is how many connections were upgraded with StartTLS.
func (s *lwServer) TLSConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tlsConn
}

func (s *lwServer) record(line string) {
	s.mu.Lock()
	s.log = append(s.log, line)
	s.mu.Unlock()
}

func lwText(p *ber.Packet) string { return p.Data.String() }

func (s *lwServer) serve(conn net.Conn, startTLS bool) {
	defer func() { _ = conn.Close() }()

	for {
		packet, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}

		id, _ := packet.Children[0].Value.(int64)
		op := packet.Children[1]

		switch op.Tag {
		case goldap.ApplicationBindRequest:
			s.bind(conn, id, op)
		case goldap.ApplicationAddRequest:
			s.add(conn, id, op)
		case goldap.ApplicationDelRequest:
			s.del(conn, id, op)
		case goldap.ApplicationModifyRequest:
			s.modify(conn, id, op)
		case goldap.ApplicationSearchRequest:
			s.search(conn, id, op)
		case goldap.ApplicationExtendedRequest:
			if up := s.extended(conn, id, op, startTLS); up != nil {
				conn = up
			}
		default:
			return
		}
	}
}

func lwResult(id int64, app ber.Tag, code uint8, diag string) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "messageID"))

	resp := ber.Encode(ber.ClassApplication, ber.TypeConstructed, app, nil, "response")
	resp.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, uint64(code), "resultCode"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	resp.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, diag, "diagnosticMessage"))
	msg.AppendChild(resp)

	return msg.Bytes()
}

func lwDiag(code uint8) string {
	if code == goldap.LDAPResultSuccess {
		return ""
	}

	return "lw refused " + strconv.Itoa(int(code))
}

func (s *lwServer) bind(conn net.Conn, id int64, op *ber.Packet) {
	dn, pw := lwText(op.Children[1]), lwText(op.Children[2])
	s.record("bind " + dn)

	var code uint8
	if s.BindCode != nil {
		code = s.BindCode(dn, pw)
	}

	_, _ = conn.Write(lwResult(id, goldap.ApplicationBindResponse, code, lwDiag(code)))
}

func (s *lwServer) add(conn net.Conn, id int64, op *ber.Packet) {
	dn := lwText(op.Children[0])

	var attrs []string

	for _, a := range op.Children[1].Children {
		var vals []string
		for _, v := range a.Children[1].Children {
			vals = append(vals, lwText(v))
		}

		attrs = append(attrs, lwText(a.Children[0])+"="+strings.Join(vals, ","))
	}

	s.record("add " + dn + "|" + strings.Join(attrs, ";"))

	var code uint8
	if s.AddCode != nil {
		code = s.AddCode(dn)
	}

	_, _ = conn.Write(lwResult(id, goldap.ApplicationAddResponse, code, lwDiag(code)))
}

func (s *lwServer) del(conn net.Conn, id int64, op *ber.Packet) {
	dn := lwText(op)

	s.record("del " + dn)

	var code uint8
	if s.DelCode != nil {
		code = s.DelCode(dn)
	}

	_, _ = conn.Write(lwResult(id, goldap.ApplicationDelResponse, code, lwDiag(code)))
}

func (s *lwServer) modify(conn net.Conn, id int64, op *ber.Packet) {
	dn := lwText(op.Children[0])

	var changes []string

	for _, change := range op.Children[1].Children {
		kind := [...]string{"add", "delete", "replace"}[change.Children[0].Value.(int64)]

		var vals []string
		for _, v := range change.Children[1].Children[1].Children {
			vals = append(vals, lwText(v))
		}

		changes = append(changes, kind+" "+lwText(change.Children[1].Children[0])+"="+strings.Join(vals, ","))
	}

	s.record("modify " + dn + "|" + strings.Join(changes, ";"))

	var code uint8
	if s.ModifyCode != nil {
		code = s.ModifyCode(dn)
	}

	_, _ = conn.Write(lwResult(id, goldap.ApplicationModifyResponse, code, lwDiag(code)))
}

func (s *lwServer) search(conn net.Conn, id int64, op *ber.Packet) {
	base := lwText(op.Children[0])
	scope, _ := op.Children[1].Value.(int64)

	filter, err := goldap.DecompileFilter(op.Children[6])
	if err != nil {
		filter = "undecodable: " + err.Error()
	}

	var attrs []string
	for _, a := range op.Children[7].Children {
		attrs = append(attrs, lwText(a))
	}

	s.record(fmt.Sprintf("search %s|%d|%s|%s", base, scope, filter, strings.Join(attrs, ",")))

	var (
		entries []lwEntry
		code    uint8
	)

	if s.Search != nil {
		entries, code = s.Search(base, scope, filter)
	}

	for _, e := range entries {
		msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
		msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "messageID"))
		msg.AppendChild(lwEntryPacket(e))
		_, _ = conn.Write(msg.Bytes())
	}

	_, _ = conn.Write(lwResult(id, goldap.ApplicationSearchResultDone, code, lwDiag(code)))
}

func lwEntryPacket(e lwEntry) *ber.Packet {
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, goldap.ApplicationSearchResultEntry, nil, "entry")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.DN, "dn"))

	list := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")

	for _, name := range slices.Sorted(maps.Keys(e.Attrs)) {
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))

		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
		for _, v := range e.Attrs[name] {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "value"))
		}

		a.AppendChild(vals)
		list.AppendChild(a)
	}

	p.AppendChild(list)

	return p
}

// extended answers the password modify and StartTLS operations; for StartTLS
// it returns the TLS connection that replaces conn.
func (s *lwServer) extended(conn net.Conn, id int64, op *ber.Packet, startTLS bool) net.Conn {
	oid := lwText(op.Children[0])

	switch oid {
	case lwPasswdOID:
		inner, _ := ber.DecodePacketErr(op.Children[1].Data.Bytes())

		var user, newPw string

		for _, c := range inner.Children {
			switch c.Tag {
			case 0:
				user = lwText(c)
			case 2:
				newPw = lwText(c)
			}
		}

		s.record("passwd " + user + "|" + newPw)
		_, _ = conn.Write(lwResult(id, goldap.ApplicationExtendedResponse, s.PasswdCode, lwDiag(s.PasswdCode)))
	case lwStartTLSOID:
		if !startTLS {
			s.record("starttls refused")
			_, _ = conn.Write(lwResult(id, goldap.ApplicationExtendedResponse, goldap.LDAPResultUnwillingToPerform, "no tls"))

			return nil
		}

		s.record("starttls")
		_, _ = conn.Write(lwResult(id, goldap.ApplicationExtendedResponse, goldap.LDAPResultSuccess, ""))

		s.mu.Lock()
		s.tlsConn++
		s.mu.Unlock()

		return tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.tlsCert}, MinVersion: tls.VersionTLS12})
	default:
		_, _ = conn.Write(lwResult(id, goldap.ApplicationExtendedResponse, goldap.LDAPResultProtocolError, "unknown oid"))
	}

	return nil
}

// lwDial opens a plain native connection to the server.
func lwDial(t *testing.T, s *lwServer) ldapConn {
	t.Helper()

	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return c
}

func TestLdapURLHost(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"ldap://ldap1.example.com:389":             "ldap1.example.com",
		"ldaps://ldap1.example.com":                "ldap1.example.com",
		"ldap://[::1]:389":                         "::1",
		"ldap://127.0.0.1:636":                     "127.0.0.1",
		"ldap://user:pw@host.example.com:389/":     "host.example.com",
		"ldapi://%2Frun%2Fcarbonio%2Frun%2Fldapi/": "",
		"ldap://bad host:389":                      "",
		"://":                                      "",
		"":                                         "",
		"ldap://host.example.com:389 ldap://other:389": "",
	} {
		assert.Equal(t, want, ldapURLHost(raw), raw)
	}
}

func TestNativeConn_BindNeverSendsAnEmptyPassword(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	c := lwDial(t, s)

	err := c.Bind("uid=zimbra,cn=admins,cn=zimbra", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty password")
	assert.Empty(t, s.Seen(), "an unauthenticated bind must never reach the server")
}

func TestNativeConn_Bind(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.BindCode = func(_, pw string) uint8 {
		if pw != "good" {
			return goldap.LDAPResultInvalidCredentials
		}

		return goldap.LDAPResultSuccess
	}
	c := lwDial(t, s)

	require.NoError(t, c.Bind("uid=a", "good"))

	err := c.Bind("uid=a", "wrong")
	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInvalidCredentials), "%v", err)
	assert.Equal(t, []string{"bind uid=a", "bind uid=a"}, s.Seen())
}

func TestNativeConn_AddKeepsAttributeOrderAndValues(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	c := lwDial(t, s)

	require.NoError(t, c.Add("uid=zmamavis,cn=appaccts,cn=zimbra", []ldapLDIFAttr{
		{Name: "objectClass", Values: []string{"organizationalPerson", "zimbraAccount"}},
		{Name: "uid", Values: []string{"zmamavis"}},
		{Name: "userPassword", Values: []string{"{SSHA}x"}},
	}))
	assert.Equal(t, []string{
		"add uid=zmamavis,cn=appaccts,cn=zimbra|objectClass=organizationalPerson,zimbraAccount;uid=zmamavis;userPassword={SSHA}x",
	}, s.Seen())
}

func TestNativeConn_AddReportsAnExistingEntryAsSuch(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.AddCode = func(dn string) uint8 {
		switch dn {
		case "cn=exists":
			return goldap.LDAPResultEntryAlreadyExists
		case "cn=denied":
			return goldap.LDAPResultInsufficientAccessRights
		}

		return goldap.LDAPResultSuccess
	}
	c := lwDial(t, s)

	err := c.Add("cn=exists", []ldapLDIFAttr{{Name: "cn", Values: []string{"exists"}}})
	require.Error(t, err)
	assert.True(t, ldapIsAlreadyExists(err), "%v", err)

	err = c.Add("cn=denied", []ldapLDIFAttr{{Name: "cn", Values: []string{"denied"}}})
	require.Error(t, err)
	assert.False(t, ldapIsAlreadyExists(err), "another refusal must not count as already present: %v", err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights))

	require.NoError(t, c.Add("cn=new", []ldapLDIFAttr{{Name: "cn", Values: []string{"new"}}}))
}

func TestNativeConn_ReplaceSendsOneReplaceWithEveryValue(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.ModifyCode = func(dn string) uint8 {
		if dn == "cn=missing" {
			return goldap.LDAPResultNoSuchObject
		}

		return goldap.LDAPResultSuccess
	}
	c := lwDial(t, s)

	require.NoError(t, c.Replace("olcDatabase={0}config,cn=config", "olcRootPW", []string{"{SSHA}abc"}))
	require.NoError(t, c.Replace("cn=x", "mail", []string{"a@x", "b@x"}))

	err := c.Replace("cn=missing", "mail", []string{"a@x"})
	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject), "%v", err)

	assert.Equal(t, []string{
		"modify olcDatabase={0}config,cn=config|replace olcRootPW={SSHA}abc",
		"modify cn=x|replace mail=a@x,b@x",
		"modify cn=missing|replace mail=a@x",
	}, s.Seen())
}

func TestNativeConn_SetPasswordIsThePasswordModifyOperation(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	c := lwDial(t, s)

	require.NoError(t, c.SetPassword("uid=zmreplica,cn=admins,cn=zimbra", "New-Secret-9"))
	assert.Equal(t, []string{"passwd uid=zmreplica,cn=admins,cn=zimbra|New-Secret-9"}, s.Seen())

	s.PasswdCode = goldap.LDAPResultInsufficientAccessRights

	err := c.SetPassword("uid=zmreplica,cn=admins,cn=zimbra", "Other-1")
	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultInsufficientAccessRights), "%v", err)
}

func TestNativeConn_SearchScopeFilterAttrsAndEntries(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(base string, _ int64, _ string) ([]lwEntry, uint8) {
		if base == "cn=nowhere" {
			return nil, goldap.LDAPResultNoSuchObject
		}

		return []lwEntry{
			{DN: "uid=a,cn=zimbra", Attrs: map[string][]string{"uid": {"a"}, "mail": {"a@x", "b@x"}}},
			{DN: "uid=b,cn=zimbra", Attrs: map[string][]string{"uid": {"b"}}},
		}, goldap.LDAPResultSuccess
	}
	c := lwDial(t, s)

	got, err := c.Search("cn=zimbra", "(objectClass=*)", true, []string{"uid", "mail"})
	require.NoError(t, err)
	assert.Equal(t, []ldapEntry{
		{DN: "uid=a,cn=zimbra", Attrs: map[string][]string{"uid": {"a"}, "mail": {"a@x", "b@x"}}},
		{DN: "uid=b,cn=zimbra", Attrs: map[string][]string{"uid": {"b"}}},
	}, got)

	_, err = c.Search("cn=accesslog", "(cn=accesslog)", false, []string{"cn"})
	require.NoError(t, err)

	empty, err := c.Search("cn=zimbra", "(uid=none)", false, nil)
	require.NoError(t, err)
	assert.Len(t, empty, 2, "the fake answers every search; the point is nil attrs are accepted")

	_, err = c.Search("cn=nowhere", "(objectClass=*)", false, []string{"cn"})
	require.Error(t, err)
	assert.True(t, goldap.IsErrorWithCode(err, goldap.LDAPResultNoSuchObject), "%v", err)

	seen := s.Seen()
	require.Len(t, seen, 4)
	assert.Equal(t, "search cn=zimbra|2|(objectClass=*)|uid,mail", seen[0], "subtree scope is 2")
	assert.Equal(t, "search cn=accesslog|0|(cn=accesslog)|cn", seen[1], "base scope is 0")
}

func TestNativeConn_CloseEndsTheConnection(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}})
	require.NoError(t, err)
	require.NoError(t, c.Bind("uid=a", "pw"))

	c.Close()

	assert.Error(t, c.Bind("uid=a", "pw"), "a closed connection cannot be used")
	assert.Equal(t, []string{"bind uid=a"}, s.Seen())
}

func TestNativeDial_PlainDoesNotNegotiateTLS(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{startTLS: true})
	c := lwDial(t, s)

	require.NoError(t, c.Bind("uid=a", "pw"))
	assert.Zero(t, s.TLSConns())
	assert.Equal(t, []string{"bind uid=a"}, s.Seen(), "no StartTLS for ldapTLSNone")
}

func TestNativeDial_FailsOverToTheFirstReachableURL(t *testing.T) {
	t.Parallel()

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	deadURL := "ldap://" + dead.Addr().String()
	require.NoError(t, dead.Close())

	s := lwStart(t, lwOpts{})
	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{deadURL, s.URL}})
	require.NoError(t, err)

	defer c.Close()

	require.NoError(t, c.Bind("uid=a", "pw"))
	assert.Equal(t, []string{"bind uid=a"}, s.Seen())
}

func TestNativeDial_AllURLsDownIsAnErrorNamingThem(t *testing.T) {
	t.Parallel()

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	deadURL := "ldap://" + dead.Addr().String()
	require.NoError(t, dead.Close())

	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{deadURL}, TLS: ldapTLSInsecure})
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), deadURL)
}

func TestNativeDial_InsecureStartTLSDoesNotVerifyTheServer(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{startTLS: true})
	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSInsecure})
	require.NoError(t, err, "the self-signed certificate is accepted: verify => 'none'")

	defer c.Close()

	require.NoError(t, c.Bind("uid=a", "pw"))
	assert.Equal(t, []string{"starttls", "bind uid=a"}, s.Seen(), "StartTLS comes before the bind: no clear-text password")
	assert.Equal(t, 1, s.TLSConns())
}

func TestNativeDial_StartTLSRefusedIsAnErrorNotAClearTextConnection(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	e, _, _ := ldapTestEnv(t)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSInsecure})
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "StartTLS to "+s.URL)
	assert.Equal(t, []string{"starttls refused"}, s.Seen())
}

func TestNativeDial_VerifiedStartTLSChecksTheCarbonioCA(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{startTLS: true})
	e, fakes, _ := ldapTestEnv(t)

	_, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSVerified})
	require.Error(t, err, "an untrusted certificate must fail a verified connection")
	assert.Contains(t, err.Error(), "StartTLS to "+s.URL)
	assert.Contains(t, err.Error(), "certificate")

	fakes.FS.Put("/opt/zextras/conf/ca/ca.pem", s.CertPEM, 0o644)

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSVerified})
	require.NoError(t, err, "the Carbonio CA trusts the server certificate")

	defer c.Close()

	require.NoError(t, c.Bind("uid=a", "pw"))
}

func TestNativeDial_VerifiedNeedsTheLocalConfig(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{startTLS: true})
	e, _, _ := ldapTestEnv(t)
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte("<localconfig><key"), 0o600))

	c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSVerified})
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Empty(t, s.Seen(), "nothing is sent when the CA cannot be located")
}

func TestNativeDial_LDAPSAndLDAPISkipStartTLS(t *testing.T) {
	t.Parallel()

	for name, o := range map[string]lwOpts{
		"ldaps": {implicitTLS: true},
		"ldapi": {unix: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := lwStart(t, o)
			e, _, _ := ldapTestEnv(t)

			c, err := ldapNativeDial(e, ldapDial{URLs: []string{s.URL}, TLS: ldapTLSInsecure})
			require.NoError(t, err, "the URL is already secure (or local): StartTLS would be an error")

			defer c.Close()

			require.NoError(t, c.Bind("uid=a", "pw"))
			assert.Equal(t, []string{"bind uid=a"}, s.Seen())
		})
	}
}
