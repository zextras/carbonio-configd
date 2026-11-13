// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package ldapfake is a minimal in-process LDAP server for tests: it answers
// StartTLS, simple bind (always success) and search (an empty result, or the
// entries of Options.Entries for a base-object search), and can imitate a
// wedged server.
package ldapfake

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
)

// LDAP protocol operation tags the fake server understands (RFC 4511).
const (
	opBindRequest     ber.Tag = 0
	opUnbindRequest   ber.Tag = 2
	opSearchRequest   ber.Tag = 3
	opExtendedRequest ber.Tag = 23

	opBindResponse       ber.Tag = 1
	opSearchResultEntry  ber.Tag = 4
	opSearchResultDone   ber.Tag = 5
	opExtendedResponse   ber.Tag = 24
	resultSuccess        int64   = 0
	resultNoSuchObject   int64   = 32
	resultProtocolError  int64   = 2
	resultUnavailableErr int64   = 52
)

// Options selects the behaviour of a Server.
type Options struct {
	// tls is the server certificate configuration. With implicitTLS the
	// listener speaks TLS from the first byte (ldaps://); otherwise it is
	// offered through the StartTLS extended operation.
	TLS         *tls.Config
	ImplicitTLS bool
	// NoStartTLS makes the server refuse the StartTLS operation.
	NoStartTLS bool
	// Entries are the entries the server holds, by DN (compared without case
	// or spaces after the commas), each with its attributes. A base-object
	// search returns the entry at the base, or noSuchObject when there is none;
	// the filter and the requested attributes are ignored. With no Entries
	// every search is an empty success.
	Entries map[string]map[string][]string
	// HangOn is "starttls" or "bind": the server reads that request and never
	// answers, like a wedged slapd.
	HangOn string
}

// Server is a minimal in-process LDAP server: it answers StartTLS,
// simple bind (always success) and search (see Options.Entries).
type Server struct {
	ln   net.Listener
	opts Options

	entries map[string]map[string][]string

	startTLSOps atomic.Int32
	binds       atomic.Int32
	bindsInTLS  atomic.Int32

	wg sync.WaitGroup
}

// New starts a server on a loopback port and stops it when the test ends.
func New(t *testing.T, opts Options) *Server {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &Server{opts: opts}

	if opts.Entries != nil {
		s.entries = make(map[string]map[string][]string, len(opts.Entries))

		for dn, attrs := range opts.Entries {
			s.entries[normalizeDN(dn)] = attrs
		}
	}

	if opts.ImplicitTLS {
		ln = tls.NewListener(ln, opts.TLS)
	}

	s.ln = ln

	s.wg.Add(1)

	go s.serve()

	t.Cleanup(func() {
		_ = ln.Close()

		s.wg.Wait()
	})

	return s
}

// StartTLSOps is the number of StartTLS requests received.
func (s *Server) StartTLSOps() int { return int(s.startTLSOps.Load()) }

// Binds is the number of bind requests received.
func (s *Server) Binds() int { return int(s.binds.Load()) }

// BindsInTLS is the number of binds received over a TLS-protected connection.
func (s *Server) BindsInTLS() int { return int(s.bindsInTLS.Load()) }

// Addr returns the host:port the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Port returns the TCP port.
func (s *Server) Port() string {
	_, p, _ := net.SplitHostPort(s.Addr())

	return p
}

// URL returns scheme://host:port for the given host name.
func (s *Server) URL(scheme, host string) string {
	return scheme + "://" + net.JoinHostPort(host, s.Port())
}

func (s *Server) serve() {
	defer s.wg.Done()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}

		s.wg.Go(func() {
			defer func() { _ = conn.Close() }()

			s.handle(conn)
		})
	}
}

// session is the state of one client connection; StartTLS swaps conn and r
// for their TLS-wrapped versions.
type session struct {
	s     *Server
	conn  net.Conn
	r     *bufio.Reader
	isTLS bool
}

func (s *Server) handle(conn net.Conn) {
	_, isTLS := conn.(*tls.Conn)
	ss := &session{s: s, conn: conn, r: bufio.NewReader(conn), isTLS: isTLS}

	for {
		p, err := ber.ReadPacket(ss.r)
		if err != nil || len(p.Children) < 2 {
			return
		}

		id, _ := p.Children[0].Value.(int64)

		if !ss.dispatch(id, p.Children[1]) {
			return
		}
	}
}

// dispatch answers one request and reports whether the connection stays open.
func (ss *session) dispatch(id int64, req *ber.Packet) bool {
	switch req.Tag {
	case opExtendedRequest:
		return ss.startTLS(id)
	case opBindRequest:
		ss.s.binds.Add(1)

		if ss.isTLS {
			ss.s.bindsInTLS.Add(1)
		}

		if ss.s.opts.HangOn == "bind" {
			return ss.hang()
		}

		return writePacket(ss.conn, result(id, opBindResponse, resultSuccess)) == nil
	case opSearchRequest:
		return ss.search(id, req)
	case opUnbindRequest:
		return false
	default:
		return writePacket(ss.conn, result(id, opExtendedResponse, resultUnavailableErr)) == nil
	}
}

// search answers a search request: the entry at the base DN when the server
// holds entries, else an empty result.
func (ss *session) search(id int64, req *ber.Packet) bool {
	if ss.s.entries == nil || len(req.Children) == 0 {
		return writePacket(ss.conn, result(id, opSearchResultDone, resultSuccess)) == nil
	}

	base, _ := req.Children[0].Value.(string)

	attrs, ok := ss.s.entries[normalizeDN(base)]
	if !ok {
		return writePacket(ss.conn, result(id, opSearchResultDone, resultNoSuchObject)) == nil
	}

	if writePacket(ss.conn, searchEntry(id, base, attrs)) != nil {
		return false
	}

	return writePacket(ss.conn, result(id, opSearchResultDone, resultSuccess)) == nil
}

// normalizeDN compares DNs without case and without spaces after commas.
func normalizeDN(dn string) string {
	return strings.ToLower(strings.ReplaceAll(dn, ", ", ","))
}

// searchEntry builds a SearchResultEntry message.
func searchEntry(id int64, dn string, attrs map[string][]string) *ber.Packet {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))

	entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, opSearchResultEntry, nil, "SearchResultEntry")
	entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, dn, "objectName"))

	list := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")

	for name, values := range attrs {
		attr := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))

		set := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range values {
			set.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "val"))
		}

		attr.AppendChild(set)
		list.AppendChild(attr)
	}

	entry.AppendChild(list)
	msg.AppendChild(entry)

	return msg
}

// hang swallows the connection without ever answering, until the client gives up.
func (ss *session) hang() bool {
	_, _ = io.Copy(io.Discard, ss.conn)

	return false
}

// startTLS handles the StartTLS extended operation.
func (ss *session) startTLS(id int64) bool {
	ss.s.startTLSOps.Add(1)

	if ss.s.opts.HangOn == "starttls" {
		return ss.hang()
	}

	if ss.s.opts.NoStartTLS || ss.s.opts.TLS == nil {
		return writePacket(ss.conn, result(id, opExtendedResponse, resultProtocolError)) == nil
	}

	if writePacket(ss.conn, result(id, opExtendedResponse, resultSuccess)) != nil {
		return false
	}

	tc := tls.Server(ss.conn, ss.s.opts.TLS)
	if tc.HandshakeContext(context.Background()) != nil {
		return false
	}

	ss.conn, ss.r, ss.isTLS = tc, bufio.NewReader(tc), true

	return true
}

// result builds an LDAPMessage carrying an LDAPResult with the given
// response tag and result code.
func result(id int64, tag ber.Tag, code int64) *ber.Packet {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))

	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "LDAPResult")
	op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(op)

	return msg
}

func writePacket(w io.Writer, p *ber.Packet) error {
	_, err := w.Write(p.Bytes())

	return err //nolint:wrapcheck // test helper
}
