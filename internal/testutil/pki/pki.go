// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package pki generates throwaway certificate authorities and server
// certificates for TLS tests.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// Server is a CA together with a server certificate it signed.
type Server struct {
	// CAPEM is the PEM-encoded CA certificate (what a client trusts).
	CAPEM []byte
	// CertPEM and KeyPEM are the PEM-encoded server certificate and key.
	CertPEM []byte
	KeyPEM  []byte
	// Certificate is the server certificate ready for tls.Config.Certificates.
	Certificate tls.Certificate
}

// NewServer creates a CA and a server certificate valid for the given DNS
// names and IP addresses. Each entry that parses as an IP becomes an IP SAN,
// every other entry a DNS SAN.
func NewServer(tb testing.TB, hosts ...string) Server {
	tb.Helper()

	caKey := newKey(tb)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "configd test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		tb.Fatalf("create CA certificate: %v", err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		tb.Fatalf("parse CA certificate: %v", err)
	}

	leafKey := newKey(tb)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "configd test server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leafTmpl.IPAddresses = append(leafTmpl.IPAddresses, ip)
		} else {
			leafTmpl.DNSNames = append(leafTmpl.DNSNames, h)
		}
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		tb.Fatalf("create server certificate: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		tb.Fatalf("marshal server key: %v", err)
	}

	s := Server{
		CAPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}

	s.Certificate, err = tls.X509KeyPair(s.CertPEM, s.KeyPEM)
	if err != nil {
		tb.Fatalf("load server key pair: %v", err)
	}

	return s
}

func newKey(tb testing.TB) *ecdsa.PrivateKey {
	tb.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}

	return k
}
